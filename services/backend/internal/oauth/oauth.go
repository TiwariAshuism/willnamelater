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
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/provider"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/redis"
)

// errMisconfigured is returned when the composition root omits a signup
// collaborator. The adapters below would otherwise wrap a nil provisioner in a
// non-nil adapter, defeating the service's own nil check, so the raw values are
// verified here before wrapping.
var errMisconfigured = errs.New(errs.KindInternal, "oauth.misconfigured",
	"oauth module is missing a required dependency")

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

// The following are the CONSUMER-SIDE PORTS the OAuth-as-signup flow needs. They
// are declared here, in the module's public surface, precisely so the
// composition root can implement them by adapting the real auth and influencer
// modules WITHOUT this module importing either — the same discipline as Identity.

// UserProvisioner finds or creates a user by email and returns their id. The
// composition root adapts the auth module onto it. It must be idempotent on
// email so signing up with an address that already has an account logs into that
// same account rather than duplicating it.
type UserProvisioner interface {
	FindOrCreateUserByEmail(ctx context.Context, email string) (uuid.UUID, error)
}

// InfluencerSignup is the narrow input to the influencer provisioner: the owning
// user, the connected Instagram account id, its handle, and the provider's
// app-scoped user id.
type InfluencerSignup struct {
	OwnerUserID        uuid.UUID
	InstagramAccountID string
	Handle             string
	ProviderUserID     string
}

// InfluencerProvisioner upserts the influencer owned by a user for a connected
// Instagram account and returns the influencer id. The composition root adapts
// the influencer module onto it.
type InfluencerProvisioner interface {
	UpsertInstagramInfluencer(ctx context.Context, in InfluencerSignup) (uuid.UUID, error)
}

// Session is the token bundle a completed signup returns so the web can log the
// new account in. It mirrors the auth module's login response and carries no
// decrypted OAuth token.
type Session struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	UserID       uuid.UUID `json:"user_id"`
	Email        string    `json:"email"`
}

// SessionIssuer mints a session for a user id, exactly as login does. The
// composition root adapts the auth module onto it.
type SessionIssuer interface {
	IssueSession(ctx context.Context, userID uuid.UUID) (Session, error)
}

// AuditStarter auto-submits an audit for a just-provisioned account so a new
// creator lands on a score without a manual step. It is OPTIONAL: pass nil to
// disable the auto-audit (signup still succeeds; the creator runs it from the
// dashboard). The composition root adapts the audit module onto it — its
// SubmitAuditForOwner matches this shape — so oauth still imports neither audit
// nor any decrypted-token path. Its method set is identical to the service's own
// AuditStarter port, so the value is threaded through directly.
type AuditStarter interface {
	StartAudit(ctx context.Context, ownerUserID, influencerID uuid.UUID) error
}

// influencerProvisionerAdapter bridges the public InfluencerProvisioner to the
// service-internal port, converting the narrow input across the package boundary
// (the two structs are field-identical; the internal one exists only because Go
// forbids the composition root from naming the service package's types).
type influencerProvisionerAdapter struct{ inner InfluencerProvisioner }

func (a influencerProvisionerAdapter) UpsertInstagramInfluencer(ctx context.Context, in service.InfluencerSignup) (uuid.UUID, error) {
	return a.inner.UpsertInstagramInfluencer(ctx, InfluencerSignup(in))
}

// sessionIssuerAdapter bridges the public SessionIssuer to the service-internal
// port, converting the field-identical session type across the boundary.
type sessionIssuerAdapter struct{ inner SessionIssuer }

func (a sessionIssuerAdapter) IssueSession(ctx context.Context, userID uuid.UUID) (model.AuthSession, error) {
	s, err := a.inner.IssueSession(ctx, userID)
	if err != nil {
		return model.AuthSession{}, err
	}
	return model.AuthSession(s), nil
}

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
	signup  *handler.SignupHandler
	svc     *service.Service
}

// New wires the module. pool backs the token store, rdb the single-use state
// store, and cipher seals every issued token before it is persisted (it directly
// satisfies the service's Sealer port). It fails fast when the service's Config
// is invalid or a required collaborator is nil, so a misconfigured deployment
// cannot start.
//
// users, influencers, and sessions back the OAuth-as-signup flow. The
// composition root supplies them by adapting the auth and influencer modules, so
// this module still imports neither.
//
// auditStarter is OPTIONAL: when non-nil, a completed signup auto-submits an
// audit through it so the creator gets a score without a manual step; nil
// disables that and signup still succeeds. It is not part of the required-
// collaborator check for the same reason.
func New(
	pool *db.Pool,
	rdb *redis.Client,
	cipher *crypto.Cipher,
	connectors *connector.Config,
	cfg Config,
	identity Identity,
	secrets SecretLookup,
	users UserProvisioner,
	influencers InfluencerProvisioner,
	sessions SessionIssuer,
	auditStarter AuditStarter,
) (*Module, error) {
	if users == nil || influencers == nil || sessions == nil {
		return nil, errMisconfigured
	}

	svc, err := service.New(
		service.Config{RedirectBaseURL: cfg.RedirectBaseURL, StateTTL: cfg.StateTTL},
		connectors,
		identity,
		repository.NewStateStore(rdb),
		repository.NewTokenStore(pool),
		cipher,
		provider.New(nil),
		service.SecretLookup(secrets),
		users,
		influencerProvisionerAdapter{inner: influencers},
		sessionIssuerAdapter{inner: sessions},
		auditStarter,
	)
	if err != nil {
		return nil, err
	}

	return &Module{
		handler: handler.New(svc),
		signup:  handler.NewSignup(svc),
		svc:     svc,
	}, nil
}

// RegisterRoutes mounts the oauth endpoints under rg (typically the /v1 group).
// Every route is protected: the composition root applies the auth middleware to
// rg before calling this, since a connection is always owned by a caller.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	m.handler.RegisterRoutes(rg)
}

// RegisterPublicRoutes mounts the PUBLIC OAuth-as-signup endpoints under rg. The
// composition root passes the UNAUTHENTICATED group here: these routes create the
// session rather than requiring one. They must NOT be mounted behind the auth
// middleware.
//
//	POST /oauth/meta/signup/start     -> begin an anonymous Meta authorization
//	GET  /oauth/meta/signup/callback  -> complete it, creating the account
//	POST /oauth/meta/signup/callback
func (m *Module) RegisterPublicRoutes(rg *gin.RouterGroup) {
	m.signup.RegisterPublicRoutes(rg)
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
