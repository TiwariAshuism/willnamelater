package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
)

// Identity resolves the authenticated user from the request context. The oauth
// module does not own authentication; app supplies an implementation that
// bridges to the auth middleware, keeping this module free of any dependency on
// the auth module's internals.
type Identity interface {
	UserID(ctx context.Context) (uuid.UUID, error)
}

// StateStore persists the short-lived CSRF/PKCE state for an in-flight
// authorization. Save writes it with a TTL; Consume fetches and deletes it in a
// single atomic step so a state can be used at most once. A missing or expired
// state yields ok=false, which the service treats as a rejected callback.
type StateStore interface {
	Save(ctx context.Context, state string, data model.StateData, ttl time.Duration) error
	Consume(ctx context.Context, state string) (data model.StateData, ok bool, err error)
}

// TokenStore persists sealed OAuth tokens and answers the connection queries.
// It never sees plaintext: every secret in EncryptedToken is already ciphertext.
type TokenStore interface {
	Upsert(ctx context.Context, tok model.EncryptedToken) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]model.Connection, error)
	DeleteByUserPlatform(ctx context.Context, userID uuid.UUID, platform string) (deleted int64, err error)
}

// Sealer envelope-encrypts a secret under an owner-binding AAD. *crypto.Cipher
// satisfies it; it is an interface so the service layer can be tested without
// reaching for the concrete cipher construction path.
type Sealer interface {
	Seal(plaintext, aad []byte) (crypto.Sealed, error)
}

// SecretLookup returns the value of a named environment variable. Connector
// configuration references OAuth client credentials by env-var NAME (never by
// value), so the service resolves them through this indirection. Production
// passes os.Getenv; tests pass a map-backed lookup.
type SecretLookup func(name string) string

// ExchangeRequest is the fully resolved input to a provider token exchange. The
// service assembles it from provider metadata, connector configuration, and the
// stored PKCE verifier, so the ProviderClient stays free of any config lookup.
type ExchangeRequest struct {
	// Provider is the public provider name; the client selects its account-id
	// response parser from it.
	Provider       string
	TokenURL       string
	AccountInfoURL string
	ClientID       string
	ClientSecret   string
	RedirectURI    string
	Code           string
	CodeVerifier   string
}

// ExchangeResult is the normalized outcome of a successful token exchange.
type ExchangeResult struct {
	AccessToken       string
	RefreshToken      string
	Expiry            time.Time
	Scopes            []string
	ProviderAccountID string
}

// ProviderClient performs the authorization-code exchange against a provider's
// token endpoint and resolves the provider's stable account identifier. It is
// the module's only outbound-network dependency in the callback path, so it is
// an interface: production uses the HTTP implementation, tests use a fake and
// never touch the network.
type ProviderClient interface {
	Exchange(ctx context.Context, req ExchangeRequest) (ExchangeResult, error)
}
