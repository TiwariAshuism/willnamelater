// Package token issues and verifies the RS256 access-token JWTs the auth module
// hands to clients. The signing key is parsed once when the Issuer is
// constructed, never per request, so a hot path never touches PEM decoding.
package token

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/config"
)

// AccessTokenTTL bounds an access token's lifetime. Fifteen minutes keeps a
// leaked or logged token useful only briefly, while being long enough that a
// client is not refreshing on nearly every call.
const AccessTokenTTL = 15 * time.Minute

// signingMethod is the one algorithm this package signs and accepts. Pinning it
// closes the "alg" substitution attack where a token forged with "none" or an
// HMAC keyed on the public key would otherwise validate.
var signingMethod = jwt.SigningMethodRS256

// ErrNoSigningKey is returned when neither a key path nor an inline PEM was
// configured.
var ErrNoSigningKey = errors.New("token: no RS256 signing key configured")

// Claims is the access-token payload: the registered claims (sub, iat, exp, jti)
// plus the caller's role, which authorizes requests without a database lookup.
type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Issued is a freshly minted access token and the metadata a caller needs to
// build a response and to correlate the token in logs.
type Issued struct {
	Token     string
	JTI       string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Issuer signs and verifies access tokens under a single RSA key pair.
type Issuer struct {
	private *rsa.PrivateKey
	public  *rsa.PublicKey
	now     func() time.Time
}

// NewIssuer parses the RS256 private key from cfg — a filesystem path takes
// precedence over an inline PEM — and derives the public key for verification.
// It fails fast on a missing or malformed key so a broken deployment cannot
// start and then reject every login.
func NewIssuer(cfg config.JWTConfig) (*Issuer, error) {
	pemBytes, err := readKey(cfg)
	if err != nil {
		return nil, err
	}

	private, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("token: parse RSA private key: %w", err)
	}

	return &Issuer{private: private, public: &private.PublicKey, now: time.Now}, nil
}

// readKey returns the PEM bytes of the signing key, preferring the file path.
func readKey(cfg config.JWTConfig) ([]byte, error) {
	if cfg.PrivateKeyPath != "" {
		pemBytes, err := os.ReadFile(cfg.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("token: read signing key file: %w", err)
		}
		return pemBytes, nil
	}
	if pem := cfg.PrivateKeyPEM.Reveal(); pem != "" {
		return []byte(pem), nil
	}
	return nil, ErrNoSigningKey
}

// Issue mints an access token for the user with the given role. The subject is
// the user id, and a fresh jti gives every token a unique identifier for
// revocation lists and audit correlation.
func (i *Issuer) Issue(userID uuid.UUID, role string) (Issued, error) {
	now := i.now()
	exp := now.Add(AccessTokenTTL)
	jti := uuid.NewString()

	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        jti,
		},
	}

	signed, err := jwt.NewWithClaims(signingMethod, claims).SignedString(i.private)
	if err != nil {
		return Issued{}, fmt.Errorf("token: sign access token: %w", err)
	}

	return Issued{Token: signed, JTI: jti, IssuedAt: now, ExpiresAt: exp}, nil
}

// Verify parses and validates raw, returning its claims. It accepts only tokens
// signed with the configured RS256 key; expiry and signature are enforced by the
// parser. Any failure returns a non-nil error and empty claims.
func (i *Issuer) Verify(raw string) (Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		if t.Method != signingMethod {
			return nil, fmt.Errorf("token: unexpected signing method %q", t.Method.Alg())
		}
		return i.public, nil
	}, jwt.WithValidMethods([]string{signingMethod.Alg()}))
	if err != nil {
		return Claims{}, err
	}
	return claims, nil
}
