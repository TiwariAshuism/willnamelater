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
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/redis"
	"github.com/getnyx/influaudit/backend/internal/platform/telemetry"
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

	// closers run in reverse construction order on Close.
	closers []func(context.Context) error
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

	registry, err := buildConnectorRegistry(cfg)
	if err != nil {
		return nil, a.abort(ctx, err)
	}
	a.Connector = registry

	return a, nil
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
// It is deliberately empty until a platform lands: an enabled connector with no
// builder is a configuration error, caught at boot.
var connectorBuilders = map[connector.Platform]connectorBuilder{}

// buildConnectorRegistry loads the declarative connector config, resolves each
// enabled platform's credentials from the environment variable names the config
// declares, and registers a live connector for each.
//
// An enabled platform with no registered builder fails the boot. Skipping it
// would mean an audit silently reports on fewer platforms than the operator
// configured, which is exactly the kind of quiet degradation this product exists
// to detect in other people's numbers.
func buildConnectorRegistry(cfg *config.Config) (*connector.Registry, error) {
	cc, err := connector.Load(cfg.Connectors.ConfigPath, cfg.Connectors.SchemaPath)
	if err != nil {
		return nil, err
	}

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
