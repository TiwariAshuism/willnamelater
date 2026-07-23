package service

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
)

// realSealer wires the production envelope cipher into the service, so these
// tests exercise the actual cryptography rather than a fake that could hide a
// mistake in how the service uses it.
type realSealer struct{ c *crypto.Cipher }

func (r realSealer) Seal(plaintext, aad []byte) (crypto.Sealed, error) {
	return r.c.Seal(plaintext, aad)
}

func (r realSealer) Open(sealed crypto.Sealed, aad []byte) ([]byte, error) {
	return r.c.Open(sealed, aad)
}

func newRealCipher(t *testing.T, fill byte) *crypto.Cipher {
	t.Helper()
	key := bytes.Repeat([]byte{fill}, crypto.KeySize)
	c, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

// The whole point of the oauth module: what reaches the database must not
// contain the token. If this ever fails, a database disclosure is a full account
// takeover of every connected creator's channel.
func TestSealTokenNeverPersistsPlaintext(t *testing.T) {
	const (
		accessToken  = "ya29.a0AfH6SMBx-real-looking-access-token"
		refreshToken = "1//0gRefresh-Token-Value-That-Must-Never-Land-In-Postgres"
	)

	cipher := newRealCipher(t, 0x01)
	svc := &Service{sealer: realSealer{cipher}}
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	tok, err := svc.sealToken(userID, connector.PlatformYouTube, ExchangeResult{
		AccessToken:       accessToken,
		RefreshToken:      refreshToken,
		Expiry:            time.Now().Add(time.Hour),
		Scopes:            []string{"scope.a"},
		ProviderAccountID: "UC_channel",
	}, nil)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	// Every byte slice that maps onto a database column.
	columns := map[string][]byte{
		"access_token_enc":  tok.AccessTokenEnc,
		"refresh_token_enc": tok.RefreshTokenEnc,
		"dek_wrapped":       tok.DEKWrapped,
	}

	for name, blob := range columns {
		if len(blob) == 0 {
			t.Errorf("%s is empty; nothing was sealed", name)
			continue
		}
		if bytes.Contains(blob, []byte(accessToken)) {
			t.Errorf("%s contains the plaintext access token", name)
		}
		if bytes.Contains(blob, []byte(refreshToken)) {
			t.Errorf("%s contains the plaintext refresh token", name)
		}
	}
}

// The AAD binds a ciphertext to its owner. Copying one user's encrypted token
// row into another user's record must fail to open, not silently grant access.
func TestSealedTokenIsBoundToItsOwner(t *testing.T) {
	cipher := newRealCipher(t, 0x01)
	svc := &Service{sealer: realSealer{cipher}}

	alice := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	bob := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")

	tok, err := svc.sealToken(alice, connector.PlatformYouTube, ExchangeResult{
		AccessToken: "alice-access-token",
	}, nil)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	sealed := crypto.Sealed{Ciphertext: tok.AccessTokenEnc, WrappedDEK: tok.DEKWrapped}

	// Alice's own AAD opens it.
	plaintext, err := cipher.Open(sealed, []byte("oauth_token:"+alice.String()))
	if err != nil {
		t.Fatalf("owner could not open their own token: %v", err)
	}
	if string(plaintext) != "alice-access-token" {
		t.Errorf("round trip = %q", plaintext)
	}

	// Bob's AAD must not.
	if _, err := cipher.Open(sealed, []byte("oauth_token:"+bob.String())); !errors.Is(err, crypto.ErrCiphertext) {
		t.Errorf("a token moved to another user's row opened: err = %v, want ErrCiphertext", err)
	}
}

// A different master key must not open the record, which is what makes key
// rotation and key theft separable concerns.
func TestSealedTokenRejectsForeignMasterKey(t *testing.T) {
	svc := &Service{sealer: realSealer{newRealCipher(t, 0x01)}}
	attacker := newRealCipher(t, 0x02)

	userID := uuid.New()
	tok, err := svc.sealToken(userID, connector.PlatformYouTube, ExchangeResult{AccessToken: "secret"}, nil)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	sealed := crypto.Sealed{Ciphertext: tok.AccessTokenEnc, WrappedDEK: tok.DEKWrapped}
	if _, err := attacker.Open(sealed, []byte("oauth_token:"+userID.String())); !errors.Is(err, crypto.ErrCiphertext) {
		t.Errorf("foreign master key opened the token: err = %v", err)
	}
}

// The refresh token gets its own DEK and is stored as a self-describing blob.
// It must survive the round trip through EncodeSealed/DecodeSealed.
func TestRefreshTokenBlobRoundTrips(t *testing.T) {
	const refreshToken = "1//0gRefresh"

	cipher := newRealCipher(t, 0x01)
	svc := &Service{sealer: realSealer{cipher}}
	userID := uuid.New()

	tok, err := svc.sealToken(userID, connector.PlatformYouTube, ExchangeResult{
		AccessToken:  "access",
		RefreshToken: refreshToken,
	}, nil)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	sealed, err := model.DecodeSealed(tok.RefreshTokenEnc)
	if err != nil {
		t.Fatalf("DecodeSealed: %v", err)
	}

	got, err := cipher.Open(sealed, []byte("oauth_token:"+userID.String()))
	if err != nil {
		t.Fatalf("Open refresh token: %v", err)
	}
	if string(got) != refreshToken {
		t.Errorf("refresh token = %q, want %q", got, refreshToken)
	}
}

// A provider that issues no refresh token must leave the column nil rather than
// storing a sealed empty string, which would be indistinguishable from a real
// (empty) token at read time.
func TestNoRefreshTokenLeavesColumnNil(t *testing.T) {
	svc := &Service{sealer: realSealer{newRealCipher(t, 0x01)}}

	tok, err := svc.sealToken(uuid.New(), connector.PlatformYouTube, ExchangeResult{
		AccessToken: "access-only",
	}, nil)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	if tok.RefreshTokenEnc != nil {
		t.Errorf("RefreshTokenEnc = %v, want nil when no refresh token was issued", tok.RefreshTokenEnc)
	}
}

// Sealing is non-deterministic: a fresh DEK and nonce per call. Two seals of the
// same token must differ, or an observer could correlate rows.
func TestSealingIsNonDeterministic(t *testing.T) {
	svc := &Service{sealer: realSealer{newRealCipher(t, 0x01)}}
	userID := uuid.New()
	res := ExchangeResult{AccessToken: "same-token"}

	first, err := svc.sealToken(userID, connector.PlatformYouTube, res, nil)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}
	second, err := svc.sealToken(userID, connector.PlatformYouTube, res, nil)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	if bytes.Equal(first.AccessTokenEnc, second.AccessTokenEnc) {
		t.Error("identical ciphertexts for the same token: a DEK or nonce is being reused")
	}
}

// When the provider omits scopes, the configured scopes are recorded, so a
// connection never claims a broader grant than it holds.
func TestScopesFallBackToConfiguredScopes(t *testing.T) {
	svc := &Service{sealer: realSealer{newRealCipher(t, 0x01)}}
	fallback := []string{"youtube.readonly", "yt-analytics.readonly"}

	tok, err := svc.sealToken(uuid.New(), connector.PlatformYouTube, ExchangeResult{
		AccessToken: "access",
	}, fallback)
	if err != nil {
		t.Fatalf("sealToken: %v", err)
	}

	if len(tok.Scopes) != len(fallback) {
		t.Fatalf("Scopes = %v, want the configured fallback %v", tok.Scopes, fallback)
	}
}
