// Package redis owns construction of the shared go-redis client. Modules and
// the asynq queue depend on the *redis.Client it returns rather than dialing
// their own, so connection settings and the startup health check live here.
package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Client is the shared Redis client type.
type Client = redis.Client

// Default client settings. Named here so the values a Config falls back to are
// reviewable in one place; callers override any of them through Config.
const (
	DefaultPoolSize     = 20
	DefaultDialTimeout  = 5 * time.Second
	DefaultReadTimeout  = 3 * time.Second
	DefaultWriteTimeout = 3 * time.Second
)

// Config carries the connection target and the tunable client limits. A zero
// value for any timeout or PoolSize falls back to the corresponding Default*.
type Config struct {
	// Addr is the host:port of the Redis server. Required.
	Addr string
	// Password authenticates the connection; empty means no auth.
	Password string
	// DB is the logical database index to select.
	DB int
	// PoolSize is the base number of socket connections.
	PoolSize int
	// DialTimeout bounds establishing a new connection.
	DialTimeout time.Duration
	// ReadTimeout bounds a single socket read.
	ReadTimeout time.Duration
	// WriteTimeout bounds a single socket write.
	WriteTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.PoolSize <= 0 {
		c.PoolSize = DefaultPoolSize
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = DefaultDialTimeout
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = DefaultReadTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}
	return c
}

// New builds a client from cfg and verifies connectivity with a ping before
// returning. On any failure it closes the client and returns a wrapped error,
// so a returned client is always usable.
func New(ctx context.Context, cfg Config) (*redis.Client, error) {
	cfg = cfg.withDefaults()

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	if err := Check(ctx, client); err != nil {
		_ = client.Close()
		return nil, err
	}

	return client, nil
}

// Check reports whether client can reach its Redis server. It is the health
// probe used by New at startup and is safe to call from a liveness endpoint.
func Check(ctx context.Context, client *redis.Client) error {
	if err := client.Ping(ctx).Err(); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "redis.unhealthy", "redis is unreachable")
	}
	return nil
}
