// Package db owns the PostgreSQL connection pool and the primitives built on
// top of it: pool construction, health checking, transaction scoping, and
// schema migration. Modules depend on the *pgxpool.Pool it hands back rather
// than opening their own connections, so pool sizing and lifecycle live in one
// place.
package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Pool is the shared connection pool type. Aliasing pgxpool.Pool lets callers
// depend on this package rather than pgx directly for the common case while
// still allowing pgx-native use where needed.
type Pool = pgxpool.Pool

// Default pool limits. They are named here rather than inlined so the values a
// PoolConfig falls back to are visible and reviewable in one place; callers
// override any of them through PoolConfig.
const (
	DefaultMaxConns          = int32(16)
	DefaultMinConns          = int32(2)
	DefaultMaxConnLifetime   = time.Hour
	DefaultMaxConnIdleTime   = 30 * time.Minute
	DefaultHealthCheckPeriod = time.Minute
	DefaultConnectTimeout    = 10 * time.Second
)

// PoolConfig carries the tunable pool limits and timeouts. A zero value is
// valid: every field left at its zero value is replaced by the corresponding
// Default* constant, so callers set only what they need to change.
type PoolConfig struct {
	// MaxConns is the upper bound on connections held by the pool.
	MaxConns int32
	// MinConns is the number of connections the pool keeps warm.
	MinConns int32
	// MaxConnLifetime caps how long a connection may live before it is retired,
	// bounding the blast radius of a stale server-side session.
	MaxConnLifetime time.Duration
	// MaxConnIdleTime retires connections idle longer than this.
	MaxConnIdleTime time.Duration
	// HealthCheckPeriod is how often idle connections are probed.
	HealthCheckPeriod time.Duration
	// ConnectTimeout bounds a single connection attempt (dial + auth).
	ConnectTimeout time.Duration
}

// withDefaults returns cfg with every zero field replaced by its Default*.
func (c PoolConfig) withDefaults() PoolConfig {
	if c.MaxConns <= 0 {
		c.MaxConns = DefaultMaxConns
	}
	if c.MinConns <= 0 {
		c.MinConns = DefaultMinConns
	}
	if c.MaxConnLifetime <= 0 {
		c.MaxConnLifetime = DefaultMaxConnLifetime
	}
	if c.MaxConnIdleTime <= 0 {
		c.MaxConnIdleTime = DefaultMaxConnIdleTime
	}
	if c.HealthCheckPeriod <= 0 {
		c.HealthCheckPeriod = DefaultHealthCheckPeriod
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = DefaultConnectTimeout
	}
	return c
}

// New parses dsn, applies cfg, opens the pool, and verifies connectivity with a
// ping before returning. A returned pool is ready to use; on any failure New
// returns a wrapped error and leaks no pool.
func New(ctx context.Context, dsn string, cfg PoolConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// A malformed DSN is a configuration mistake, not an outage.
		return nil, errs.Wrap(err, errs.KindInvalid, "db.dsn_invalid", "database connection string is invalid")
	}

	cfg = cfg.withDefaults()
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "db.connect", "could not connect to database")
	}

	if err := Check(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

// Check reports whether the database behind pool is reachable. It is the health
// probe used by New at startup and is safe to call from a liveness endpoint.
func Check(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pool.Ping(ctx); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db.unhealthy", "database is unreachable")
	}
	return nil
}
