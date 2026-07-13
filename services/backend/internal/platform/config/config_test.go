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
		"ml.service_token",
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

// prodEnv returns an environment that satisfies every prod requirement, so a
// test can introduce exactly one defect and attribute the resulting error to it.
// It is deliberately built from valid-but-not-well-known values: the dev
// credentials committed to this repository are themselves a prod defect, which
// is what TestValidateRejectsDevCredentialsInProd asserts.
func prodEnv(overrides ...string) []string {
	env := []string{
		"INFLUAUDIT_ENVIRONMENT=prod",
		"INFLUAUDIT_HTTP__PUBLIC_BASE_URL=https://api.influaudit.com",
		"INFLUAUDIT_POSTGRES__DSN=postgres://u:p@db.example:5432/influaudit?sslmode=require",
		"INFLUAUDIT_REDIS__ADDR=cache.example:6380",
		"INFLUAUDIT_REDIS__TLS=true",
		"INFLUAUDIT_CRYPTO__MASTER_KEY=" + base64.StdEncoding.EncodeToString([]byte("a-real-32-byte-production-key!!!")),
		"INFLUAUDIT_ANTHROPIC__API_KEY=sk-ant-real",
		"INFLUAUDIT_GOTENBERG__URL=http://gotenberg:3000",
		"INFLUAUDIT_ML__BASE_URL=http://ml:8000",
		"INFLUAUDIT_ML__SERVICE_TOKEN=a-real-service-token",
		"INFLUAUDIT_STORAGE__ENDPOINT=https://acct.r2.cloudflarestorage.com",
		"INFLUAUDIT_STORAGE__REGION=auto",
		"INFLUAUDIT_STORAGE__BUCKET=influaudit",
		"INFLUAUDIT_STORAGE__ACCESS_KEY=a-real-access-key",
		"INFLUAUDIT_STORAGE__SECRET_KEY=a-real-secret-key",
		"INFLUAUDIT_JWT__PRIVATE_KEY_PEM=-----BEGIN PRIVATE KEY-----x-----END PRIVATE KEY-----",
		"INFLUAUDIT_RAZORPAY__KEY_ID=rzp_live_x",
		"INFLUAUDIT_RAZORPAY__KEY_SECRET=a-real-razorpay-secret",
		"INFLUAUDIT_OTEL__EXPORTER_ENDPOINT=otel-collector:4317",
		"INFLUAUDIT_EMAIL__HOST=smtp.postmarkapp.com",
		"INFLUAUDIT_EMAIL__PORT=587",
		"INFLUAUDIT_EMAIL__FROM=reports@influaudit.com",
	}
	return append(env, overrides...)
}

// TestProdEnvIsItselfValid guards the fixture: if prodEnv stops being a clean
// prod configuration, every test below would pass for the wrong reason.
func TestProdEnvIsItselfValid(t *testing.T) {
	if _, err := load("", func() []string { return prodEnv() }); err != nil {
		t.Fatalf("prodEnv must be a valid prod config, got: %v", err)
	}
}

// TestValidateRejectsSilentProdMisconfiguration covers the configurations that
// are structurally valid — present, correctly typed, correctly shaped — and yet
// wrong for prod. Each one previously booted clean and failed later at runtime,
// which is the failure mode these checks exist to convert into a boot error.
func TestValidateRejectsSilentProdMisconfiguration(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		wantErr string
	}{
		{
			// The default is http://localhost:8080, so an emptiness check can never
			// catch a deployment that simply never set it.
			name:    "public base url left at its localhost default",
			env:     prodEnv("INFLUAUDIT_HTTP__PUBLIC_BASE_URL=http://localhost:8080"),
			wantErr: "http.public_base_url",
		},
		{
			name:    "public base url is not https",
			env:     prodEnv("INFLUAUDIT_HTTP__PUBLIC_BASE_URL=http://api.influaudit.com"),
			wantErr: "http.public_base_url: must be an https:// origin",
		},
		{
			name:    "postgres dsn disables tls",
			env:     prodEnv("INFLUAUDIT_POSTGRES__DSN=postgres://u:p@db.example:5432/x?sslmode=disable"),
			wantErr: "postgres.dsn: sslmode=disable",
		},
		{
			// Every managed Redis is TLS-only; without this the process starts and
			// then cannot reach its queue or its cache at all.
			name:    "redis tls disabled",
			env:     prodEnv("INFLUAUDIT_REDIS__TLS=false"),
			wantErr: "redis.tls: must be enabled in prod",
		},
		{
			name:    "ml service token absent",
			env:     prodEnv("INFLUAUDIT_ML__SERVICE_TOKEN="),
			wantErr: "ml.service_token: required in prod",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load("", func() []string { return tc.env })
			if err == nil {
				t.Fatalf("load succeeded; want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestValidateRejectsDevCredentialsInProd is the important one. Every value here
// is committed to this repository in plain sight — and every one of them is valid
// base64, the right length, and non-empty, so it passes every structural check.
// Only a check by value keeps it out of production.
func TestValidateRejectsDevCredentialsInProd(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		wantErr string
	}{
		{
			name:    "the compose default master key",
			env:     prodEnv("INFLUAUDIT_CRYPTO__MASTER_KEY=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="),
			wantErr: "crypto.master_key: is a well-known development key",
		},
		{
			name:    "the .env.example placeholder master key",
			env:     prodEnv("INFLUAUDIT_CRYPTO__MASTER_KEY=" + b64Key(crypto.KeySize)),
			wantErr: "crypto.master_key: is a well-known development key",
		},
		{
			name:    "the compose default ml service token",
			env:     prodEnv("INFLUAUDIT_ML__SERVICE_TOKEN=dev-ml-service-token"),
			wantErr: "ml.service_token: is the well-known development token",
		},
		{
			name:    "the LocalStack access key",
			env:     prodEnv("INFLUAUDIT_STORAGE__ACCESS_KEY=test"),
			wantErr: "storage.access_key: is the well-known LocalStack development credential",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load("", func() []string { return tc.env })
			if err == nil {
				t.Fatalf("load succeeded with a committed dev credential in prod; want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDevToleratesDevCredentials is the other half of the contract: the values
// rejected above are exactly the ones that must keep the local stack booting out
// of the box.
func TestDevToleratesDevCredentials(t *testing.T) {
	cfg, err := load("", func() []string {
		return []string{
			"INFLUAUDIT_ENVIRONMENT=dev",
			"INFLUAUDIT_CRYPTO__MASTER_KEY=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			"INFLUAUDIT_ML__SERVICE_TOKEN=dev-ml-service-token",
			"INFLUAUDIT_STORAGE__ACCESS_KEY=test",
		}
	})
	if err != nil {
		t.Fatalf("dev must tolerate the committed dev credentials, got: %v", err)
	}
	if len(cfg.MasterKey()) != crypto.KeySize {
		t.Errorf("master key not decoded: got %d bytes", len(cfg.MasterKey()))
	}
}

// TestStoragePathStyleDefaultsOn guards the default that LocalStack, MinIO and
// Cloudflare R2 all depend on. A plain bool would default to false and silently
// switch every deployment to virtual-host addressing.
func TestStoragePathStyleDefaultsOn(t *testing.T) {
	cfg, err := load("", func() []string { return nil })
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Storage.PathStyle {
		t.Error("storage.path_style = false by default; want true")
	}

	off, err := load("", func() []string { return []string{"INFLUAUDIT_STORAGE__PATH_STYLE=false"} })
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if off.Storage.PathStyle {
		t.Error("storage.path_style stayed true; env must be able to turn it off")
	}
}

// TestValidateRequiresAMailRelayInProd: a deployment that cannot send mail cannot
// tell a creator their report is ready, which is the one moment the product owes
// them a message. And a half-configured relay is worse than none — it constructs
// cleanly and fails on the first send, long after boot.
func TestValidateRequiresAMailRelayInProd(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		wantErr string
	}{
		{
			name:    "no relay at all",
			env:     prodEnv("INFLUAUDIT_EMAIL__HOST=", "INFLUAUDIT_EMAIL__FROM="),
			wantErr: "email.host: required in prod",
		},
		{
			name:    "host without a from address",
			env:     prodEnv("INFLUAUDIT_EMAIL__FROM="),
			wantErr: "email.from",
		},
		{
			// Authenticating to a relay in the clear hands the password to anyone on
			// the path. "none" exists for a local capture server and nothing else.
			name:    "plaintext relay",
			env:     prodEnv("INFLUAUDIT_EMAIL__TLS=none"),
			wantErr: `email.tls: "none" is not permitted in prod`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load("", func() []string { return tc.env })
			if err == nil {
				t.Fatalf("load succeeded; want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// A half-configured relay must fail at boot in EVERY environment, not just prod:
// the failure it prevents (construct clean, die on first send) is not prod-specific.
func TestValidateRejectsHalfConfiguredRelayInDev(t *testing.T) {
	_, err := load("", func() []string {
		return []string{
			"INFLUAUDIT_ENVIRONMENT=dev",
			"INFLUAUDIT_EMAIL__HOST=smtp.example.com",
			// no port, no from
		}
	})
	if err == nil {
		t.Fatal("load succeeded with a half-configured relay; want a validation error")
	}
	for _, want := range []string{"email.port", "email.from"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}
