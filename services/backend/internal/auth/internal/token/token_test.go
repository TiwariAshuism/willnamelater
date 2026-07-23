package token

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/config"
)

// pemFor encodes key as a PKCS1 PEM block.
func pemFor(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func TestNewIssuerKeySources(t *testing.T) {
	t.Parallel()

	key := newKey(t)
	keyPEM := pemFor(t, key)

	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	tests := []struct {
		name    string
		cfg     config.JWTConfig
		wantErr bool
	}{
		{name: "inline pem", cfg: config.JWTConfig{PrivateKeyPEM: config.Secret(keyPEM)}},
		{name: "file path", cfg: config.JWTConfig{PrivateKeyPath: path}},
		{name: "path wins over inline", cfg: config.JWTConfig{PrivateKeyPath: path, PrivateKeyPEM: config.Secret("garbage")}},
		{name: "missing", cfg: config.JWTConfig{}, wantErr: true},
		{name: "malformed pem", cfg: config.JWTConfig{PrivateKeyPEM: config.Secret("not a pem")}, wantErr: true},
		{name: "missing file", cfg: config.JWTConfig{PrivateKeyPath: filepath.Join(dir, "absent.pem")}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewIssuer(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	issuer, err := NewIssuer(config.JWTConfig{PrivateKeyPEM: config.Secret(pemFor(t, newKey(t)))})
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	userID := uuid.New()
	issued, err := issuer.Issue(userID, "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.Token == "" || issued.JTI == "" {
		t.Fatal("Issue returned empty token or jti")
	}
	if got := issued.ExpiresAt.Sub(issued.IssuedAt); got != AccessTokenTTL {
		t.Fatalf("ttl = %v, want %v", got, AccessTokenTTL)
	}

	claims, err := issuer.Verify(issued.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != userID.String() {
		t.Fatalf("sub = %q, want %q", claims.Subject, userID.String())
	}
	if claims.Role != "admin" {
		t.Fatalf("role = %q, want admin", claims.Role)
	}
	if claims.ID != issued.JTI {
		t.Fatalf("jti = %q, want %q", claims.ID, issued.JTI)
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatal("iat/exp claims must be set")
	}
}

func TestVerifyRejectsForeignAndTamperedTokens(t *testing.T) {
	t.Parallel()

	signer, err := NewIssuer(config.JWTConfig{PrivateKeyPEM: config.Secret(pemFor(t, newKey(t)))})
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	verifier, err := NewIssuer(config.JWTConfig{PrivateKeyPEM: config.Secret(pemFor(t, newKey(t)))})
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	issued, err := signer.Issue(uuid.New(), "user")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// A token signed by a different key must not verify against another issuer.
	if _, err := verifier.Verify(issued.Token); err == nil {
		t.Fatal("Verify accepted a token signed by a foreign key")
	}

	// A token whose "alg" is downgraded to none must be rejected.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	unsigned, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := signer.Verify(unsigned); err == nil {
		t.Fatal("Verify accepted an alg=none token")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	issuer, err := NewIssuer(config.JWTConfig{PrivateKeyPEM: config.Secret(pemFor(t, newKey(t)))})
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	// Force issuance in the distant past so the token is already expired.
	issuer.now = func() time.Time { return time.Now().Add(-2 * AccessTokenTTL) }

	issued, err := issuer.Issue(uuid.New(), "user")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := issuer.Verify(issued.Token); err == nil {
		t.Fatal("Verify accepted an expired token")
	}
}
