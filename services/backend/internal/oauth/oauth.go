// Package oauth is the public entry point of the oauth module: the one package
// outside internal/oauth/internal that the composition root imports. It wires the
// Redis state store, the pgx token store, the envelope sealer, the provider HTTP
// client, and the service together, then exposes route registration.
//
// Everything behind it lives under internal/oauth/internal, which Go forbids any
// sibling module from importing, so a collaborator can only reach oauth through
// this surface.
//
// Identity is a port, not a dependency on the auth module. oauth must not import
// internal/auth; the composition root supplies a service.Identity that bridges to
// the auth middleware, keeping this module free of any cross-module import.
package oauth

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/provider"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/redis"
)

// Config is the module's public configuration. It mirrors the service's own
// config, which lives under internal/ and is therefore unreachable from the
// composition root — the same visibility wall that keeps other modules out.
type Config struct {
	// RedirectBaseURL is the public origin the provider redirects back to, with
	// no trailing slash. It must match the app's registered redirect URIs.
	RedirectBaseURL string
	// StateTTL bounds how long an in-flight authorization may take. Zero uses
	// the service default.
	StateTTL time.Duration
}

// Identity is the port through which this module learns who is calling. It is
// deliberately not a dependency on the auth module: the composition root adapts
// auth's context accessor onto this interface, so oauth never imports auth.
type Identity interface {
	UserID(ctx context.Context) (uuid.UUID, error)
}

// SecretLookup resolves an environment variable by NAME. Connector configuration
// references OAuth client credentials by name and never by value, so the module
// resolves them through this indirection. Production passes os.Getenv.
type SecretLookup func(name string) string

// LiveConnection is a decrypted platform connection for the audit path: the
// access token is in the clear, in memory, ready to hand to a connector. It is
// never persisted or logged.
type LiveConnection struct {
	Platform          string
	ProviderAccountID string
	Token             connector.OAuthToken
}

// Module is the wired oauth module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
	svc     *service.Service
}

// New wires the module. pool backs the token store, rdb the single-use state
// store, and cipher seals every issued token before it is persisted (it directly
// satisfies the service's Sealer port). It fails fast when the service's Config
// is invalid or a required collaborator is nil, so a misconfigured deployment
// cannot start.
func New(
	pool *db.Pool,
	rdb *redis.Client,
	cipher *crypto.Cipher,
	connectors *connector.Config,
	cfg Config,
	identity Identity,
	secrets SecretLookup,
) (*Module, error) {
	svc, err := service.New(
		service.Config{RedirectBaseURL: cfg.RedirectBaseURL, StateTTL: cfg.StateTTL},
		connectors,
		identity,
		repository.NewStateStore(rdb),
		repository.NewTokenStore(pool),
		cipher,
		provider.New(nil),
		service.SecretLookup(secrets),
	)
	if err != nil {
		return nil, err
	}

	return &Module{handler: handler.New(svc), svc: svc}, nil
}

// RegisterRoutes mounts the oauth endpoints under rg (typically the /v1 group).
// Every route is protected: the composition root applies the auth middleware to
// rg before calling this, since a connection is always owned by a caller.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	m.handler.RegisterRoutes(rg)
}

// ForgetProviderUser erases every connection belonging to a provider's app-scoped
// user, returning the platform user who owned them so the caller can cascade the
// erasure to the data derived from those tokens.
//
// It backs Meta's deauthorize and data-deletion callbacks and therefore takes no
// caller identity: those callbacks carry no session, and their signed_request is
// verified by the composition root BEFORE this is reached. found is false, with no
// error, when nobody matches — a callback for a user who never connected must
// succeed as a no-op rather than make Meta retry forever.
func (m *Module) ForgetProviderUser(ctx context.Context, provider, providerUserID string) (uuid.UUID, bool, error) {
	return m.svc.ForgetProviderUser(ctx, provider, providerUserID)
}

// LiveConnections returns the user's connected platforms with decrypted access
// tokens, for the audit orchestrator to hand to connectors. Only this module can
// do this: it holds the cipher and the owner-binding AAD. A row that fails to
// open is skipped, so one bad token never fails an audit.
func (m *Module) LiveConnections(ctx context.Context, userID uuid.UUID) ([]LiveConnection, error) {
	conns, err := m.svc.LiveConnections(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]LiveConnection, len(conns))
	for i, c := range conns {
		out[i] = LiveConnection{Platform: c.Platform, ProviderAccountID: c.ProviderAccountID, Token: c.Token}
	}
	return out, nil
}
