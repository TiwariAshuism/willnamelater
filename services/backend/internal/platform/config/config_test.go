package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// b64Key returns the base64 encoding of n zero bytes, for exercising the
// master-key length check without embedding a real key.
func b64Key(n int) string {
	return base64.StdEncoding.EncodeToString(make([]byte, n))
}

// writeYAML writes contents to a temp file and returns its path.
func writeYAML(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return path
}

func TestLoadPrecedence(t *testing.T) {
	yaml := writeYAML(t, strings.Join([]string{
		"http:",
		"  addr: \":9090\"",
		"  write_timeout: 45s",
		"ml:",
		"  base_url: http://ml-from-yaml",
	}, "\n"))

	tests := []struct {
		name    string
		yaml    string
		environ func() []string
		check   func(t *testing.T, c *Config)
	}{
		{
			name:    "defaults only",
			yaml:    "",
			environ: func() []string { return nil },
			check: func(t *testing.T, c *Config) {
				if c.Environment != EnvDev {
					t.Errorf("environment = %q, want %q", c.Environment, EnvDev)
				}
				if c.HTTP.Addr != ":8080" {
					t.Errorf("http.addr = %q, want :8080", c.HTTP.Addr)
				}
				if c.HTTP.ReadTimeout != 15*time.Second {
					t.Errorf("http.read_timeout = %v, want 15s", c.HTTP.ReadTimeout)
				}
			},
		},
		{
			name:    "yaml overrides defaults",
			yaml:    yaml,
			environ: func() []string { return nil },
			check: func(t *testing.T, c *Config) {
				if c.HTTP.Addr != ":9090" {
					t.Errorf("http.addr = %q, want :9090 from yaml", c.HTTP.Addr)
				}
				if c.HTTP.WriteTimeout != 45*time.Second {
					t.Errorf("http.write_timeout = %v, want 45s from yaml", c.HTTP.WriteTimeout)
				}
				if c.ML.BaseURL != "http://ml-from-yaml" {
					t.Errorf("ml.base_url = %q, want yaml value", c.ML.BaseURL)
				}
				// Untouched default survives the yaml overlay.
				if c.HTTP.ReadTimeout != 15*time.Second {
					t.Errorf("http.read_timeout = %v, want default 15s", c.HTTP.ReadTimeout)
				}
			},
		},
		{
			name: "env overrides yaml and defaults",
			yaml: yaml,
			environ: func() []string {
				return []string{
					"INFLUAUDIT_HTTP__ADDR=:7070",
					"INFLUAUDIT_HTTP__READ_TIMEOUT=30s",
					"UNPREFIXED_HTTP__ADDR=:1111",
				}
			},
			check: func(t *testing.T, c *Config) {
				if c.HTTP.Addr != ":7070" {
					t.Errorf("http.addr = %q, want :7070 from env", c.HTTP.Addr)
				}
				if c.HTTP.ReadTimeout != 30*time.Second {
					t.Errorf("http.read_timeout = %v, want 30s from env", c.HTTP.ReadTimeout)
				}
				// yaml value survives where env is silent.
				if c.HTTP.WriteTimeout != 45*time.Second {
					t.Errorf("http.write_timeout = %v, want 45s from yaml", c.HTTP.WriteTimeout)
				}
				if c.ML.BaseURL != "http://ml-from-yaml" {
					t.Errorf("ml.base_url = %q, want yaml value", c.ML.BaseURL)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := load(tc.yaml, tc.environ)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			tc.check(t, cfg)
		})
	}
}

func TestValidateMissingRequiredAggregation(t *testing.T) {
	// prod with nothing supplied must fault every prod-required field at once.
	cfg, err := load("", func() []string {
		return []string{"INFLUAUDIT_ENVIRONMENT=prod"}
	})
	if err == nil {
		t.Fatalf("load succeeded; want aggregated validation error, cfg=%+v", cfg)
	}
	if got := errs.Status(err); got != 400 {
		t.Errorf("errs.Status = %d, want 400 (KindInvalid)", got)
	}

	wantFields := []string{
		"postgres.dsn",
		"anthropic.api_key",
		"storage.access_key",
		"storage.secret_key",
		"razorpay.key_secret",
		"gotenberg.url",
		"ml.base_url",
		"storage.endpoint",
		"storage.bucket",
		"razorpay.key_id",
		"otel.exporter_endpoint",
		"jwt",
		"crypto.master_key",
	}
	msg := err.Error()
	for _, f := range wantFields {
		if !strings.Contains(msg, f) {
			t.Errorf("aggregated error missing field %q; full error: %s", f, msg)
		}
	}
}

func TestValidateEnvironmentName(t *testing.T) {
	_, err := load("", func() []string {
		return []string{"INFLUAUDIT_ENVIRONMENT=production"}
	})
	if err == nil || !strings.Contains(err.Error(), "environment:") {
		t.Fatalf("want environment validation error, got %v", err)
	}
}

func TestValidateMasterKey(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string // substring; empty means expect success
	}{
		{name: "valid 32 bytes", value: b64Key(crypto.KeySize)},
		{name: "wrong length", value: b64Key(16), wantErr: "must decode to 32 bytes, got 16"},
		{name: "not base64", value: "!!!not-base64!!!", wantErr: "must be valid base64"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// dev environment so no other secret is required and the master key
			// is validated in isolation.
			cfg, err := load("", func() []string {
				return []string{
					"INFLUAUDIT_ENVIRONMENT=dev",
					"INFLUAUDIT_CRYPTO__MASTER_KEY=" + tc.value,
				}
			})

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("load: unexpected error: %v", err)
				}
				key := cfg.MasterKey()
				if len(key) != crypto.KeySize {
					t.Fatalf("MasterKey length = %d, want %d", len(key), crypto.KeySize)
				}
				// The decoded key must be usable by the crypto package.
				if _, err := crypto.NewCipher(key); err != nil {
					t.Fatalf("crypto.NewCipher with decoded key: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("load succeeded; want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestSecretNeverLeaks asserts that a Secret is redacted across every channel a
// credential could realistically escape through.
func TestSecretNeverLeaks(t *testing.T) {
	const plaintext = "super-secret-value-do-not-print"
	s := Secret(plaintext)

	cfg := Config{
		Environment: EnvProd,
		Postgres:    PostgresConfig{DSN: s},
		Anthropic:   AnthropicConfig{APIKey: s},
		Storage:     StorageConfig{AccessKey: s, SecretKey: s},
		JWT:         JWTConfig{PrivateKeyPEM: s},
		Razorpay:    RazorpayConfig{KeySecret: s},
		Crypto:      CryptoConfig{MasterKey: s},
		Redis:       RedisConfig{Password: s},
	}

	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)
	logger.Println(cfg)
	logger.Printf("%v %s", cfg, s)

	jsonBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	outputs := map[string]string{
		"fmt %v struct": fmt.Sprintf("%v", cfg),
		"fmt %v secret": fmt.Sprintf("%v", s),
		// Formatted with surrounding text so this reads as a real log line
		// rather than a bare Stringer call.
		"fmt %s secret":   fmt.Sprintf("secret=%s", s),
		"json.Marshal":    string(jsonBytes),
		"log output":      logBuf.String(),
		"Stringer direct": s.String(),
	}

	for channel, out := range outputs {
		if strings.Contains(out, plaintext) {
			t.Errorf("%s leaked the secret: %s", channel, out)
		}
		if !strings.Contains(out, redacted) {
			t.Errorf("%s did not contain the redaction marker %q: %s", channel, redacted, out)
		}
	}

	// Reveal is the sanctioned escape hatch and must return the real value.
	if s.Reveal() != plaintext {
		t.Errorf("Reveal = %q, want %q", s.Reveal(), plaintext)
	}
}
