package connector_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// repoConfigPaths locates the real connectors.yaml and its schema relative to
// this test file, which lives at services/backend/internal/connector.
func repoConfigPaths(t *testing.T) (configPath, schemaPath string) {
	t.Helper()
	base := filepath.Join("..", "..", "..", "..", "packages", "config")
	configPath = filepath.Join(base, "connectors.yaml")
	schemaPath = filepath.Join(base, "connectors.schema.json")
	for _, p := range []string{configPath, schemaPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected fixture %s to exist: %v", p, err)
		}
	}
	return configPath, schemaPath
}

// TestLoadRealConfig loads the real connectors.yaml from disk and asserts it
// validates against the real schema and decodes to the expected shape. It does
// not inline a copy of the config, so drift in the committed file is caught.
func TestLoadRealConfig(t *testing.T) {
	cfg, err := connector.Load(repoConfigPaths(t))
	if err != nil {
		t.Fatalf("Load real config: %v", err)
	}

	if cfg.Version < 1 {
		t.Fatalf("version = %d, want >= 1", cfg.Version)
	}

	byPlatform := make(map[connector.Platform]connector.PlatformConfig)
	for _, cc := range cfg.Connectors {
		byPlatform[cc.Platform] = cc
	}

	// YouTube is the enabled MVP connector. Instagram is present but stays
	// disabled until Meta app review clears; the csvimport connector is the
	// interim Instagram data path.
	yt, ok := byPlatform[connector.PlatformYouTube]
	if !ok {
		t.Fatal("youtube connector missing")
	}
	if !yt.Enabled {
		t.Fatal("youtube must be enabled")
	}
	if yt.RateLimit.Model != connector.RateLimitQuotaUnits {
		t.Fatalf("youtube rate limit model = %q, want quota_units", yt.RateLimit.Model)
	}
	if yt.RateLimit.UnitsPerDay != 10000 || yt.RateLimit.DefaultCost != 1 || yt.RateLimit.SearchCost != 100 {
		t.Fatalf("youtube quota values unexpected: %+v", yt.RateLimit)
	}

	ig, ok := byPlatform[connector.PlatformInstagram]
	if !ok {
		t.Fatal("instagram connector missing")
	}
	// Instagram stays disabled until Meta app review clears — the live Graph
	// connector must not be reachable before then; csvimport serves Instagram in
	// the interim.
	if ig.Enabled {
		t.Fatal("instagram must stay disabled until Meta app review clears")
	}
	if !ig.RequiresAppReview {
		t.Fatal("instagram must carry requires_app_review: true")
	}
	if ig.RateLimit.Model != connector.RateLimitBucketedCalls {
		t.Fatalf("instagram rate limit model = %q, want bucketed_calls", ig.RateLimit.Model)
	}
	if w, err := ig.RateLimit.WindowDuration(); err != nil || w != time.Hour {
		t.Fatalf("instagram window = %v (err %v), want 1h", w, err)
	}

	// facebook, tiktok, x, linkedin are present but disabled at MVP.
	for _, p := range []connector.Platform{
		connector.PlatformFacebook, connector.PlatformTikTok,
		connector.PlatformX, connector.PlatformLinkedIn,
	} {
		cc, ok := byPlatform[p]
		if !ok {
			t.Fatalf("%q connector missing", p)
		}
		if cc.Enabled {
			t.Fatalf("%q must be disabled at MVP", p)
		}
	}

	// Only youtube is enabled at MVP; instagram is gated on Meta app review.
	if got := cfg.Enabled(); len(got) != 1 {
		t.Fatalf("Enabled() returned %d connectors, want 1 (youtube)", len(got))
	}
}

func TestLoadRejectsMissingSchema(t *testing.T) {
	configPath, _ := repoConfigPaths(t)
	_, err := connector.Load(configPath, filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("Load with missing schema: err = nil, want error")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

// TestLoadRejectsSchemaViolation feeds a config that breaks the schema (a
// negative quota) and asserts Load fails at boot rather than at first audit.
func TestLoadRejectsSchemaViolation(t *testing.T) {
	_, schemaPath := repoConfigPaths(t)
	bad := filepath.Join(t.TempDir(), "connectors.yaml")
	writeFile(t, bad, `
version: 1
connectors:
  - platform: youtube
    enabled: true
    display_name: YouTube
    base_url: https://example.com
    auth:
      type: api_key
      api_key_env: YT_API_KEY
    capabilities:
      - profile
    rate_limit:
      model: quota_units
      units_per_day: -5
      default_cost: 1
      search_cost: 100
`)
	_, err := connector.Load(bad, schemaPath)
	if err == nil {
		t.Fatal("Load with schema-violating config: err = nil, want error")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

// TestValidateRejectsEmbeddedSecret is the security-critical case: a credential
// field that holds a literal secret rather than an env-var NAME must be
// rejected so a leaked key can never be committed.
func TestValidateRejectsEmbeddedSecret(t *testing.T) {
	cfg := &connector.Config{
		Version: 1,
		Connectors: []connector.PlatformConfig{{
			Platform:     connector.PlatformYouTube,
			Enabled:      true,
			DisplayName:  "YouTube",
			BaseURL:      "https://example.com",
			Capabilities: []connector.Capability{connector.CapabilityProfile},
			Auth: connector.Auth{
				Type: connector.AuthAPIKey,
				// A real API key, not an env-var name.
				APIKeyEnv: "AIzaSyD-1a2b3c4d5e6f7g8h9i0jklmnop",
			},
			RateLimit: connector.RateLimit{
				Model: connector.RateLimitQuotaUnits, UnitsPerDay: 10000,
				DefaultCost: 1, SearchCost: 100,
			},
		}},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate with embedded secret: err = nil, want error")
	}
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

// TestValidateAcceptsEnvVarNames confirms the secret guard does not reject
// legitimate env-var references.
func TestValidateAcceptsEnvVarNames(t *testing.T) {
	cfg := &connector.Config{
		Version: 1,
		Connectors: []connector.PlatformConfig{{
			Platform:     connector.PlatformYouTube,
			Enabled:      true,
			DisplayName:  "YouTube",
			BaseURL:      "https://example.com",
			Capabilities: []connector.Capability{connector.CapabilityProfile},
			Auth: connector.Auth{
				Type:            connector.AuthOAuth2,
				ClientIDEnv:     "YT_OAUTH_CLIENT_ID",
				ClientSecretEnv: "YT_OAUTH_CLIENT_SECRET",
			},
			RateLimit: connector.RateLimit{
				Model: connector.RateLimitQuotaUnits, UnitsPerDay: 10000,
				DefaultCost: 1, SearchCost: 100,
			},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with valid env-var names: unexpected error: %v", err)
	}
}

func TestValidateRejectsDuplicatePlatform(t *testing.T) {
	dup := connector.PlatformConfig{
		Platform:     connector.PlatformYouTube,
		Enabled:      true,
		DisplayName:  "YouTube",
		BaseURL:      "https://example.com",
		Capabilities: []connector.Capability{connector.CapabilityProfile},
		Auth:         connector.Auth{Type: connector.AuthAPIKey, APIKeyEnv: "YT_API_KEY"},
		RateLimit: connector.RateLimit{
			Model: connector.RateLimitQuotaUnits, UnitsPerDay: 10000,
			DefaultCost: 1, SearchCost: 100,
		},
	}
	cfg := &connector.Config{Version: 1, Connectors: []connector.PlatformConfig{dup, dup}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate with duplicate platform: err = nil, want error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
