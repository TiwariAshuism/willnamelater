// Package app is the composition root. It is the only package permitted to
// import every business module, which is what keeps the module graph acyclic:
// modules never import one another, they declare the narrow interfaces they
// need from a collaborator and app supplies the implementation.
//
// Nothing here contains business logic. Its whole job is to construct the
// dependency graph in the right order, hand it to a server or worker, and tear
// it down cleanly.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/audit"
	"github.com/getnyx/influaudit/backend/internal/auth"
	"github.com/getnyx/influaudit/backend/internal/billing"
	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/connector/youtube"
	"github.com/getnyx/influaudit/backend/internal/influencer"
	"github.com/getnyx/influaudit/backend/internal/llm"
	"github.com/getnyx/influaudit/backend/internal/metrics"
	"github.com/getnyx/influaudit/backend/internal/ml"
	"github.com/getnyx/influaudit/backend/internal/oauth"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/redis"
	"github.com/getnyx/influaudit/backend/internal/platform/telemetry"
	"github.com/getnyx/influaudit/backend/internal/scoring"
)

// serviceName identifies this process in traces. Version is stamped at build
// time via -ldflags; the zero value is honest about an untagged local build.
const serviceName = "influaudit-backend"

// Version is overridden at link time: -ldflags "-X ...app.Version=v1.2.3".
var Version = "dev"

// App is the constructed dependency graph. Both cmd/api and cmd/worker build
// the same graph; they differ only in what they do with it, so a task handler
// and an HTTP handler can never diverge in how they reach the database.
type App struct {
	Config    *config.Config
	Pool      *pgxpool.Pool
	Redis     *goredis.Client
	Cipher    *crypto.Cipher
	Connector *connector.Registry
	Telemetry *telemetry.Provider

	// asynqClient enqueues background tasks (today, audit:run). Both cmd/api and
	// cmd/worker construct it: the API enqueues, the worker consumes. It is lazy —
	// it dials Redis on first use — so constructing it costs nothing at boot and
	// the route tests can build the module graph over an unreachable address.
	asynqClient *asynq.Client

	// Modules are the wired business modules. They are the only things Router
	// and RegisterTasks mount; nothing else in app knows they exist.
	Modules Modules

	// closers run in reverse construction order on Close.
	closers []func(context.Context) error
}

// Modules holds every wired business module. Cross-module needs are satisfied
// here, through ports, so no module imports another.
type Modules struct {
	Auth       *auth.Module
	OAuth      *oauth.Module
	Influencer *influencer.Module
	Billing    *billing.Module
	Metrics    *metrics.Module
	Scoring    *scoring.Module
	Audit      *audit.Module
}

// Build constructs every dependency. On any failure it tears down whatever was
// already constructed before returning, so a partially-built graph never leaks
// a connection pool or an exporter goroutine.
func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	a := &App{Config: cfg}

	tp, err := telemetry.Setup(ctx, telemetry.Config{
		Endpoint:       cfg.OTel.ExporterEndpoint,
		Insecure:       cfg.Environment != config.EnvProd,
		ServiceName:    serviceName,
		ServiceVersion: Version,
		SampleRatio:    sampleRatio(cfg.Environment),
	})
	if err != nil {
		return nil, a.abort(ctx, fmt.Errorf("telemetry: %w", err))
	}
	a.Telemetry = tp
	a.closers = append(a.closers, tp.Shutdown)

	pool, err := db.New(ctx, cfg.Postgres.DSN.Reveal(), db.PoolConfig{})
	if err != nil {
		return nil, a.abort(ctx, err)
	}
	a.Pool = pool
	a.closers = append(a.closers, func(context.Context) error { pool.Close(); return nil })

	rdb, err := redis.New(ctx, redis.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password.Reveal(),
		DB:       cfg.Redis.DB,
	})
	if err != nil {
		return nil, a.abort(ctx, err)
	}
	a.Redis = rdb
	a.closers = append(a.closers, func(context.Context) error { return rdb.Close() })

	// The cipher is optional outside prod: config.Validate already enforces that
	// a master key is present in prod, and MasterKey returns nil when unset.
	if key := cfg.MasterKey(); len(key) > 0 {
		cipher, err := crypto.NewCipher(key)
		if err != nil {
			return nil, a.abort(ctx, fmt.Errorf("crypto: %w", err))
		}
		a.Cipher = cipher
	} else if cfg.Environment == config.EnvProd {
		// Defence in depth: Validate should have caught this already.
		return nil, a.abort(ctx, errs.New(errs.KindInvalid, "app.master_key_required",
			"a master encryption key is required in production"))
	}

	// The connector config is loaded once: the registry builds live connectors
	// from it, and the oauth module reads each platform's scopes out of it, so
	// consent URLs can never drift from what the connectors actually request.
	connectors, err := connector.Load(cfg.Connectors.ConfigPath, cfg.Connectors.SchemaPath)
	if err != nil {
		return nil, a.abort(ctx, err)
	}

	registry, err := buildConnectorRegistry(connectors)
	if err != nil {
		return nil, a.abort(ctx, err)
	}
	a.Connector = registry

	if err := a.buildModules(connectors); err != nil {
		return nil, a.abort(ctx, err)
	}
	// buildModules constructed the asynq client; register its shutdown so Close
	// drains the enqueue connection pool in reverse order like every other closer.
	if a.asynqClient != nil {
		a.closers = append(a.closers, func(context.Context) error { return a.asynqClient.Close() })
	}

	// Seed the cold-start scoring weights and bootstrap benchmarks. It is
	// idempotent, so a restart re-seeds nothing, and it is bounded so a slow
	// database cannot wedge the boot. It runs here, after module construction,
	// rather than inside buildModules, because building the modules is a pure
	// wiring step with no I/O — which is what lets the route tests construct the
	// module graph over nil datastores.
	seedCtx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()
	if err := a.Modules.Scoring.EnsureBootstrap(seedCtx); err != nil {
		return nil, a.abort(ctx, fmt.Errorf("seed scoring: %w", err))
	}

	return a, nil
}

// bootstrapTimeout bounds the one-time scoring seed so a slow database cannot
// wedge process startup.
const bootstrapTimeout = 30 * time.Second

// buildModules constructs every business module and satisfies their cross-module
// ports. This is the only place a module learns about another one's existence,
// and it does so through a narrow interface rather than an import.
func (a *App) buildModules(connectors *connector.Config) error {
	// oauth seals every issued token and metrics derives the commenter
	// pseudonymization salt, so both need the cipher.
	//
	// A nil *crypto.Cipher would satisfy their `sealer == nil` guards — a typed
	// nil inside a non-nil interface is not nil — and then panic on first use,
	// long after boot, on the first OAuth connect or the first audit ingest.
	// Refuse here instead, where an operator can act on it.
	if a.Cipher == nil {
		return errs.New(errs.KindInvalid, "app.master_key_required",
			"a master encryption key is required: oauth token sealing and commenter "+
				"pseudonymization both depend on it")
	}

	authMod, err := auth.New(a.Pool, a.Config.JWT)
	if err != nil {
		return err
	}

	billingMod, err := billing.New(a.Pool, a.Config.Razorpay)
	if err != nil {
		return err
	}

	// oauth declares an Identity port rather than importing auth. auth.UserID
	// reads the identity its middleware established; the adapter closes over
	// nothing and carries no state.
	oauthMod, err := oauth.New(
		a.Pool,
		a.Redis,
		a.Cipher,
		connectors,
		oauth.Config{RedirectBaseURL: a.Config.HTTP.PublicBaseURL},
		identityFromAuth{},
		os.Getenv,
	)
	if err != nil {
		return err
	}

	influencerMod := influencer.New(a.Pool)

	// scoring keys its benchmarks on (niche, tier). Tier it derives from live
	// follower counts, but niche is a content category only the influencer module
	// knows, so scoring reaches it through a Profiles port. influencer.NicheOf
	// satisfies that port directly, so no adapter is needed.
	scoringMod := scoring.New(a.Pool, influencerMod)

	metricsMod := metrics.New(a.Pool, a.Cipher)

	// The ML client and the llm module are pure constructors — no dial at build —
	// so they are safe to wire here alongside the modules the route tests build
	// over nil datastores. The llm module records each generation's cost; the ml
	// client scores fraud and coordination signals.
	mlClient := ml.New(a.Config.ML.BaseURL, httpDoerForML)
	llmMod := llm.NewModule(a.Config.Anthropic.APIKey, a.Pool)

	// The asynq client is lazy: it dials Redis on first enqueue, not here, so the
	// audit module can be constructed even when Redis is unreachable.
	a.asynqClient = asynq.NewClient(asynq.RedisClientOpt{
		Addr:     a.Config.Redis.Addr,
		Password: a.Config.Redis.Password.Reveal(),
		DB:       a.Config.Redis.DB,
	})

	// The audit orchestrator imports no other business module: every collaborator
	// reaches it through a consumer-side port declared in internal/audit/port,
	// satisfied by an adapter in audit_wiring.go. Two providers satisfy their port
	// directly and need no adapter: metrics.Module is a port.Ingester, and the
	// connector registry is a port.Connectors.
	auditMod := audit.New(
		a.Pool,
		a.asynqClient,
		auditQuota{b: billingMod},
		metricsMod,
		auditScorer{s: scoringMod},
		auditFraud{c: mlClient},
		auditReporter{llm: llmMod, scoring: scoringMod},
		a.Connector,
		auditConnections{influencer: influencerMod, oauth: oauthMod},
		auditCaller{},
	)

	a.Modules = Modules{
		Auth:       authMod,
		OAuth:      oauthMod,
		Influencer: influencerMod,
		Billing:    billingMod,
		Metrics:    metricsMod,
		Scoring:    scoringMod,
		Audit:      auditMod,
	}
	return nil
}

// identityFromAuth adapts the auth module's context accessor onto the Identity
// port the oauth service declares. It exists so oauth never imports auth.
type identityFromAuth struct{}

func (identityFromAuth) UserID(ctx context.Context) (uuid.UUID, error) {
	id, ok := auth.UserID(ctx)
	if !ok {
		return uuid.Nil, errs.New(errs.KindUnauthorized, "app.unauthenticated",
			"this endpoint requires authentication")
	}
	return id, nil
}

// Close releases every constructed dependency in reverse order. It joins all
// shutdown errors rather than returning the first, so one stuck exporter cannot
// hide a failed pool drain.
func (a *App) Close(ctx context.Context) error {
	var joined error
	for i := len(a.closers) - 1; i >= 0; i-- {
		if err := a.closers[i](ctx); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// abort tears down a partially-built graph and returns the triggering error,
// with any teardown failure joined on so it is logged rather than swallowed.
func (a *App) abort(ctx context.Context, cause error) error {
	if err := a.Close(ctx); err != nil {
		return errors.Join(cause, fmt.Errorf("during teardown: %w", err))
	}
	return cause
}

// connectorBuilder constructs a live connector from its declarative config and
// the credentials resolved from the environment.
type connectorBuilder func(pc connector.PlatformConfig, creds credentials) (connector.Connector, error)

// credentials holds the values resolved from the environment-variable NAMES a
// platform's auth block declares. The config file itself never carries secrets.
type credentials struct {
	APIKey       string
	ClientID     string
	ClientSecret string
}

// connectorBuilders is the single extension point for platforms. Adding one is:
// implement connector.Connector, add a block to connectors.yaml, add an entry
// here. Nothing else in the codebase changes.
//
// An enabled connector with no builder is a configuration error, caught at boot.
var connectorBuilders = map[connector.Platform]connectorBuilder{
	connector.PlatformYouTube: buildYouTube,
}

// connectorHTTPTimeout bounds a single call to a platform API. The audit
// orchestrator also imposes a per-platform deadline; this is the inner bound, so
// one wedged request cannot consume the whole platform's budget.
const connectorHTTPTimeout = 20 * time.Second

// buildYouTube constructs the YouTube connector. Its API key authenticates
// public reads, which is what makes it the only platform that needs no app
// review and therefore the one that carries a live audit today.
func buildYouTube(pc connector.PlatformConfig, creds credentials) (connector.Connector, error) {
	return youtube.New(youtube.Config{
		BaseURL: pc.BaseURL,
		APIKey:  creds.APIKey,
		HTTP:    &http.Client{Timeout: connectorHTTPTimeout},
	})
}

// buildConnectorRegistry loads the declarative connector config, resolves each
// enabled platform's credentials from the environment variable names the config
// declares, and registers a live connector for each.
//
// An enabled platform with no registered builder fails the boot. Skipping it
// would mean an audit silently reports on fewer platforms than the operator
// configured, which is exactly the kind of quiet degradation this product exists
// to detect in other people's numbers.
func buildConnectorRegistry(cc *connector.Config) (*connector.Registry, error) {
	registry := connector.NewRegistry()
	for _, pc := range cc.Enabled() {
		build, ok := connectorBuilders[pc.Platform]
		if !ok {
			return nil, errs.New(errs.KindInvalid, "app.connector_unimplemented",
				fmt.Sprintf("connector %q is enabled in config but no implementation is registered", pc.Platform))
		}

		creds, err := resolveCredentials(pc)
		if err != nil {
			return nil, err
		}

		c, err := build(pc, creds)
		if err != nil {
			return nil, fmt.Errorf("build connector %q: %w", pc.Platform, err)
		}
		if err := registry.Register(c); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// resolveCredentials reads every environment variable the platform's auth block
// names. A missing credential fails at boot rather than mid-audit against a
// live quota, where the failure would cost real API units to discover.
func resolveCredentials(pc connector.PlatformConfig) (credentials, error) {
	var creds credentials
	for _, ref := range []struct {
		name string
		dst  *string
	}{
		{pc.Auth.APIKeyEnv, &creds.APIKey},
		{pc.Auth.ClientIDEnv, &creds.ClientID},
		{pc.Auth.ClientSecretEnv, &creds.ClientSecret},
	} {
		if ref.name == "" {
			continue
		}
		value, ok := os.LookupEnv(ref.name)
		if !ok || value == "" {
			return credentials{}, errs.New(errs.KindInvalid, "app.missing_credential",
				fmt.Sprintf("connector %q requires environment variable %s", pc.Platform, ref.name))
		}
		*ref.dst = value
	}
	return creds, nil
}

// sampleRatio traces everything outside prod, where volume is negligible and
// completeness is what you want while debugging, and samples down in prod.
func sampleRatio(env config.Environment) float64 {
	if env == config.EnvProd {
		return 0.1
	}
	return 1.0
}
