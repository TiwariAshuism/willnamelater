// Package config loads and validates the application's runtime configuration.
//
// Configuration is layered: hard-coded defaults are overlaid by an optional
// YAML file, which is in turn overlaid by environment variables. Environment
// variables always win so that a deployment can override any file-based value
// without editing files baked into an image.
//
// Secrets never travel through the config in plaintext-printable form. Every
// field that holds a credential has type Secret, whose fmt and JSON
// representations are redacted, so an accidental log of the whole Config cannot
// leak a key.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	kenv "github.com/knadh/koanf/providers/env/v2"
	kfile "github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// envPrefix scopes which environment variables this application consumes.
// nestDelim maps a flat env var name onto the nested config tree, e.g.
// INFLUAUDIT_HTTP__ADDR addresses http.addr. A double underscore is used
// because single underscores are legitimate parts of leaf field names
// (read_timeout, base_url).
const (
	envPrefix = "INFLUAUDIT_"
	nestDelim = "__"
	keyDelim  = "."
)

// redacted is the placeholder rendered wherever a Secret would otherwise be
// printed. It is intentionally not the empty string so that a redacted value is
// visually distinct from an unset one.
const redacted = "[REDACTED]"

// Secret is a credential-bearing string. Its String and MarshalJSON
// implementations return a fixed placeholder so that the underlying value
// cannot escape through fmt verbs, structured logs, or JSON encoding. Read the
// real value explicitly with Reveal at the single point of use.
type Secret string

// String satisfies fmt.Stringer for both %v and %s, including when the Secret
// is a field of a larger struct that is being formatted.
func (Secret) String() string { return redacted }

// MarshalJSON ensures json.Marshal of any struct embedding a Secret emits the
// placeholder rather than the credential.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the underlying credential. It is the only way to obtain the
// plaintext and exists to make every such access greppable.
func (s Secret) Reveal() string { return string(s) }

// Environment names a deployment tier. Secrets are mandatory only in prod;
// developer machines may run with them absent.
type Environment string

// Recognized deployment environments. Secrets are mandatory in EnvProd and
// EnvStaging; EnvDev tolerates their absence.
const (
	EnvDev     Environment = "dev"
	EnvStaging Environment = "staging"
	EnvProd    Environment = "prod"
)

// Config is the fully resolved application configuration.
type Config struct {
	Environment Environment     `koanf:"environment"`
	HTTP        HTTPConfig      `koanf:"http"`
	Postgres    PostgresConfig  `koanf:"postgres"`
	Redis       RedisConfig     `koanf:"redis"`
	Crypto      CryptoConfig    `koanf:"crypto"`
	Anthropic   AnthropicConfig `koanf:"anthropic"`
	Gotenberg   GotenbergConfig `koanf:"gotenberg"`
	ML          MLConfig        `koanf:"ml"`
	Storage     StorageConfig   `koanf:"storage"`
	JWT         JWTConfig       `koanf:"jwt"`
	Razorpay    RazorpayConfig  `koanf:"razorpay"`
	OTel        OTelConfig      `koanf:"otel"`

	// masterKey is the decoded, length-checked master encryption key. It is
	// derived from Crypto.MasterKey during Validate and is deliberately
	// unexported so it is never serialized or printed.
	masterKey []byte
}

// HTTPConfig holds the public HTTP server settings.
type HTTPConfig struct {
	Addr         string        `koanf:"addr"`
	ReadTimeout  time.Duration `koanf:"read_timeout"`
	WriteTimeout time.Duration `koanf:"write_timeout"`
}

// PostgresConfig holds the primary datastore connection string. The DSN embeds
// a password, so it is a Secret in whole.
type PostgresConfig struct {
	DSN Secret `koanf:"dsn"`
}

// RedisConfig holds the cache and asynq broker connection.
type RedisConfig struct {
	Addr     string `koanf:"addr"`
	Password Secret `koanf:"password"`
	DB       int    `koanf:"db"`
}

// CryptoConfig carries the base64-encoded 32-byte master key used to seal
// secrets at rest. Use Config.MasterKey for the decoded bytes.
type CryptoConfig struct {
	MasterKey Secret `koanf:"master_key"`
}

// AnthropicConfig holds the LLM provider credential.
type AnthropicConfig struct {
	APIKey Secret `koanf:"api_key"`
}

// GotenbergConfig holds the PDF-rendering service location.
type GotenbergConfig struct {
	URL string `koanf:"url"`
}

// MLConfig holds the internal machine-learning service base URL.
type MLConfig struct {
	BaseURL string `koanf:"base_url"`
}

// StorageConfig holds S3-compatible object storage settings.
type StorageConfig struct {
	Endpoint  string `koanf:"endpoint"`
	Bucket    string `koanf:"bucket"`
	AccessKey Secret `koanf:"access_key"`
	SecretKey Secret `koanf:"secret_key"`
}

// JWTConfig holds the RS256 signing key, supplied either as a filesystem path
// to a PEM file or inline as a PEM string. Exactly one is required in prod.
type JWTConfig struct {
	PrivateKeyPath string `koanf:"private_key_path"`
	PrivateKeyPEM  Secret `koanf:"private_key_pem"`
}

// RazorpayConfig holds the payments gateway credentials. The key id is a
// non-secret identifier; the key secret is a Secret.
type RazorpayConfig struct {
	KeyID     string `koanf:"key_id"`
	KeySecret Secret `koanf:"key_secret"`
}

// OTelConfig holds the OpenTelemetry OTLP exporter endpoint.
type OTelConfig struct {
	ExporterEndpoint string `koanf:"exporter_endpoint"`
}

// MasterKey returns the decoded 32-byte master encryption key, or nil when no
// key was configured (permitted outside prod). The returned slice is suitable
// for crypto.NewCipher.
func (c *Config) MasterKey() []byte { return c.masterKey }

// defaults returns a Config seeded with the values used when neither the YAML
// file nor the environment specifies them. Only fields with a sensible,
// non-secret default are set; everything else stays at its zero value and is
// enforced by Validate where required.
func defaults() Config {
	return Config{
		Environment: EnvDev,
		HTTP: HTTPConfig{
			Addr:         ":8080",
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
		Redis: RedisConfig{
			Addr: "127.0.0.1:6379",
			DB:   0,
		},
	}
}

// Load resolves configuration from defaults, then the optional YAML file at
// yamlPath, then process environment variables, and validates the result. A
// yamlPath that is empty or points at a missing file is skipped; the file being
// optional is a supported mode, not an error.
func Load(yamlPath string) (*Config, error) {
	return load(yamlPath, os.Environ)
}

// load is Load with an injectable environment source, so tests can exercise
// precedence and validation without mutating the real process environment.
func load(yamlPath string, environ func() []string) (*Config, error) {
	k := koanf.New(keyDelim)

	if yamlPath != "" {
		switch _, statErr := os.Stat(yamlPath); {
		case statErr == nil:
			if err := k.Load(kfile.Provider(yamlPath), yaml.Parser()); err != nil {
				return nil, errs.Wrap(err, errs.KindInvalid, "config.yaml_parse",
					"configuration file could not be read or parsed")
			}
		case errors.Is(statErr, os.ErrNotExist):
			// Optional file: fall through to defaults and environment.
		default:
			return nil, errs.Wrap(statErr, errs.KindInternal, "config.yaml_stat",
				"configuration file could not be accessed")
		}
	}

	envProvider := kenv.Provider(keyDelim, kenv.Opt{
		Prefix:        envPrefix,
		EnvironFunc:   environ,
		TransformFunc: transformEnv,
	})
	if err := k.Load(envProvider, nil); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "config.env_load",
			"environment configuration could not be read")
	}

	cfg := defaults()
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "config.decode",
			"configuration values could not be decoded")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// transformEnv maps a prefixed, double-underscore-nested environment variable
// name onto the dotted, lower-cased key path the config tree expects, e.g.
// INFLUAUDIT_HTTP__READ_TIMEOUT -> http.read_timeout.
func transformEnv(key, value string) (string, any) {
	key = strings.TrimPrefix(key, envPrefix)
	key = strings.ReplaceAll(key, nestDelim, keyDelim)
	return strings.ToLower(key), value
}

// Validate checks every field and returns a single error enumerating all
// problems, not just the first. On success it also populates the decoded master
// key. Structural fields are always required; credentials and external service
// endpoints are required only in prod, where the process cannot legitimately
// run without them.
func (c *Config) Validate() error {
	var problems []string
	add := func(msg string) { problems = append(problems, msg) }

	switch c.Environment {
	case EnvDev, EnvStaging, EnvProd:
	default:
		add(fmt.Sprintf("environment: must be one of %q, %q, %q", EnvDev, EnvStaging, EnvProd))
	}

	if c.HTTP.Addr == "" {
		add("http.addr: required")
	}
	if c.HTTP.ReadTimeout <= 0 {
		add("http.read_timeout: must be positive")
	}
	if c.HTTP.WriteTimeout <= 0 {
		add("http.write_timeout: must be positive")
	}

	prod := c.Environment == EnvProd
	requireInProd := func(field, value string) {
		if prod && value == "" {
			add(field + ": required in prod")
		}
	}
	requireSecretInProd := func(field string, value Secret) {
		if prod && value == "" {
			add(field + ": required in prod")
		}
	}

	requireSecretInProd("postgres.dsn", c.Postgres.DSN)
	requireSecretInProd("anthropic.api_key", c.Anthropic.APIKey)
	requireSecretInProd("storage.access_key", c.Storage.AccessKey)
	requireSecretInProd("storage.secret_key", c.Storage.SecretKey)
	requireSecretInProd("razorpay.key_secret", c.Razorpay.KeySecret)

	requireInProd("gotenberg.url", c.Gotenberg.URL)
	requireInProd("ml.base_url", c.ML.BaseURL)
	requireInProd("storage.endpoint", c.Storage.Endpoint)
	requireInProd("storage.bucket", c.Storage.Bucket)
	requireInProd("razorpay.key_id", c.Razorpay.KeyID)
	requireInProd("otel.exporter_endpoint", c.OTel.ExporterEndpoint)

	if prod && c.JWT.PrivateKeyPath == "" && c.JWT.PrivateKeyPEM == "" {
		add("jwt: private_key_path or private_key_pem required in prod")
	}

	c.validateMasterKey(prod, add)

	if len(problems) > 0 {
		return errs.New(errs.KindInvalid, "config.invalid",
			"invalid configuration: "+strings.Join(problems, "; "))
	}
	return nil
}

// validateMasterKey decodes and length-checks the master key. A supplied key is
// always validated regardless of environment; an absent key is an error only in
// prod. On success the decoded bytes are stored for MasterKey.
func (c *Config) validateMasterKey(prod bool, add func(string)) {
	raw := c.Crypto.MasterKey.Reveal()
	if raw == "" {
		if prod {
			add("crypto.master_key: required in prod")
		}
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		add("crypto.master_key: must be valid base64")
		return
	}
	if len(decoded) != crypto.KeySize {
		add(fmt.Sprintf("crypto.master_key: must decode to %d bytes, got %d", crypto.KeySize, len(decoded)))
		return
	}
	c.masterKey = decoded
}
