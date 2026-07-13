// Package redis owns construction of the shared go-redis client. Modules and
// the asynq queue depend on the *redis.Client it returns rather than dialing
// their own, so connection settings and the startup health check live here.
package redis

import (
	"context"
	"crypto/tls"
	"net"
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
	// TLS encrypts the connection. Every managed Redis (Memorystore, Azure Cache,
	// ElastiCache with in-transit encryption, Upstash) accepts TLS connections
	// only — Azure goes as far as disabling the plaintext port outright — so this
	// is on in every deployed environment and off only against the local compose
	// redis. config.Validate refuses to boot prod without it.
	TLS bool
	// TLSServerName overrides the name presented in SNI and checked against the
	// server certificate. Empty derives it from Addr, which is what every managed
	// service wants; set it only when connecting through a tunnel or an IP whose
	// certificate names a different host.
	TLSServerName string
}

// TLSConfigFor returns the TLS configuration implied by cfg, or nil when TLS is
// off.
//
// It is exported because asynq dials Redis itself rather than sharing the client
// this package builds (asynq.RedisClientOpt carries its own *tls.Config), and it
// must be handed the identical settings. A mismatch does not fail loudly: both
// processes start, the API enqueues onto one endpoint, the worker consumes from
// another, and no task ever runs. One function, one source of truth.
func TLSConfigFor(cfg Config) *tls.Config {
	if !cfg.TLS {
		return nil
	}
	name := cfg.TLSServerName
	if name == "" {
		// SplitHostPort fails on a bare host with no port. Addr is then already
		// the server name, so the error is the answer.
		if host, _, err := net.SplitHostPort(cfg.Addr); err == nil {
			name = host
		} else {
			name = cfg.Addr
		}
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: name,
	}
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
		TLSConfig:    TLSConfigFor(cfg),
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
