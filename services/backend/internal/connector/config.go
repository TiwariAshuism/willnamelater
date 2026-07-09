package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	yaml "github.com/goccy/go-yaml"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// AuthType names how a connector authenticates to its platform.
type AuthType string

// Supported authentication types. Anything else is rejected at load.
const (
	AuthOAuth2 AuthType = "oauth2"
	AuthAPIKey AuthType = "api_key"
)

// RateLimitModel names how a platform meters usage, which determines which
// RateLimit fields are meaningful.
type RateLimitModel string

const (
	// RateLimitQuotaUnits models a daily unit budget where each API call costs
	// a number of units (YouTube Data API: a 10000-unit/day budget, most reads
	// costing 1 unit and search costing 100).
	RateLimitQuotaUnits RateLimitModel = "quota_units"
	// RateLimitBucketedCalls models a fixed number of calls per rolling window
	// (Meta Graph API: a per-app/user bucket of calls per hour).
	RateLimitBucketedCalls RateLimitModel = "bucketed_calls"
)

// Auth is the declarative auth block for one connector. It never holds secret
// values: every credential is referenced by the NAME of the environment
// variable that supplies it at runtime, so the config file is safe to commit.
type Auth struct {
	Type AuthType `yaml:"type"`
	// APIKeyEnv names the env var holding a static API key (api_key auth).
	APIKeyEnv string `yaml:"api_key_env"`
	// ClientIDEnv / ClientSecretEnv name the env vars holding the OAuth client
	// credentials (oauth2 auth).
	ClientIDEnv     string `yaml:"client_id_env"`
	ClientSecretEnv string `yaml:"client_secret_env"`
	// Scopes are the OAuth scopes requested when a user connects an account.
	Scopes []string `yaml:"scopes"`
}

// RateLimit is the declarative rate-limit block. Which fields apply depends on
// Model; Validate enforces the per-model requirements.
type RateLimit struct {
	Model RateLimitModel `yaml:"model"`

	// quota_units fields.
	UnitsPerDay int `yaml:"units_per_day"`
	DefaultCost int `yaml:"default_cost"`
	SearchCost  int `yaml:"search_cost"`

	// bucketed_calls fields.
	CallsPerHour int    `yaml:"calls_per_hour"`
	Window       string `yaml:"window"`
}

// WindowDuration parses the bucketed-calls Window (e.g. "1h") into a Duration.
// It is only meaningful when Model is RateLimitBucketedCalls.
func (r RateLimit) WindowDuration() (time.Duration, error) {
	return time.ParseDuration(r.Window)
}

// PlatformConfig is one platform's declarative block in connectors.yaml.
type PlatformConfig struct {
	Platform          Platform     `yaml:"platform"`
	Enabled           bool         `yaml:"enabled"`
	DisplayName       string       `yaml:"display_name"`
	BaseURL           string       `yaml:"base_url"`
	Auth              Auth         `yaml:"auth"`
	Capabilities      []Capability `yaml:"capabilities"`
	RateLimit         RateLimit    `yaml:"rate_limit"`
	RequiresAppReview bool         `yaml:"requires_app_review"`
}

// Config is the whole connectors.yaml document.
type Config struct {
	Version    int              `yaml:"version"`
	Connectors []PlatformConfig `yaml:"connectors"`
}

// Enabled returns only the connectors flagged enabled, in file order. This is
// what the startup wiring iterates to build and register live connectors.
func (c *Config) Enabled() []PlatformConfig {
	out := make([]PlatformConfig, 0, len(c.Connectors))
	for _, cc := range c.Connectors {
		if cc.Enabled {
			out = append(out, cc)
		}
	}
	return out
}

// knownPlatforms and knownCapabilities are the closed sets the config is
// validated against, keeping the YAML enum and the Go constants in lockstep.
var (
	knownPlatforms = map[Platform]struct{}{
		PlatformYouTube: {}, PlatformInstagram: {}, PlatformFacebook: {},
		PlatformTikTok: {}, PlatformX: {}, PlatformLinkedIn: {},
	}
	knownCapabilities = map[Capability]struct{}{
		CapabilityProfile: {}, CapabilityMetrics: {},
		CapabilityRecentPosts: {}, CapabilityAudienceBreakdown: {},
	}
)

// envVarNamePattern matches a plausible environment-variable NAME: uppercase
// letters, digits and underscores, starting with a letter. Any credential
// reference that does not match is assumed to be an embedded literal secret
// (which almost always contains lowercase letters, digits and punctuation) and
// is rejected, so a leaked key can never be silently committed in this file.
var envVarNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Load reads connectors.yaml from configPath, validates it against the JSON
// Schema at schemaPath, decodes it, and runs semantic Validate. Any failure is
// returned as a KindInvalid error so a malformed config fails fast at boot
// rather than at the first audit.
func Load(configPath, schemaPath string) (*Config, error) {
	schema, err := compileSchema(schemaPath)
	if err != nil {
		return nil, err
	}

	// #nosec G304 -- configPath is an operator-supplied boot parameter, never
	// derived from request input. There is no user-controlled path here.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "connector.config_unreadable",
			"connector config could not be read")
	}

	if err := validateAgainstSchema(schema, raw); err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "connector.config_decode",
			"connector config could not be decoded into the expected shape")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// compileSchema loads the schema document itself (not via a filesystem URL, to
// stay portable across OSes) and compiles it under a stable in-memory id.
func compileSchema(schemaPath string) (*jsonschema.Schema, error) {
	// #nosec G304 -- schemaPath is an operator-supplied boot parameter, never
	// derived from request input.
	f, err := os.Open(schemaPath)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "connector.schema_unreadable",
			"connector schema could not be opened")
	}
	// Read-only handle: a close failure cannot lose data and must not mask the
	// schema error we may already be returning.
	defer func() { _ = f.Close() }()

	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "connector.schema_invalid",
			"connector schema is not valid JSON")
	}

	const id = "connectors.schema.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "connector.schema_invalid",
			"connector schema could not be registered")
	}
	schema, err := c.Compile(id)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "connector.schema_invalid",
			"connector schema failed to compile")
	}
	return schema, nil
}

// validateAgainstSchema round-trips the parsed YAML through JSON so numbers and
// keys reach the validator in the exact form (json.Number, map[string]any) the
// jsonschema library expects, then validates the whole document.
func validateAgainstSchema(schema *jsonschema.Schema, rawYAML []byte) error {
	var parsed any
	if err := yaml.Unmarshal(rawYAML, &parsed); err != nil {
		return errs.Wrap(err, errs.KindInvalid, "connector.config_invalid",
			"connector config is not valid YAML")
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return errs.Wrap(err, errs.KindInvalid, "connector.config_invalid",
			"connector config could not be normalized for validation")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return errs.Wrap(err, errs.KindInvalid, "connector.config_invalid",
			"connector config could not be normalized for validation")
	}
	if err := schema.Validate(doc); err != nil {
		return errs.Wrap(err, errs.KindInvalid, "connector.config_invalid",
			"connector config failed schema validation")
	}
	return nil
}

// Validate applies the semantic rules the JSON Schema cannot express: closed
// enum membership against the Go constants, no duplicate platforms, per-auth
// and per-rate-limit-model field requirements, a parseable rate-limit window,
// and — as defense in depth over the schema — the guarantee that no credential
// field embeds a literal secret.
func (c *Config) Validate() error {
	if c.Version < 1 {
		return errs.New(errs.KindInvalid, "connector.config_invalid",
			"config version must be >= 1")
	}
	if len(c.Connectors) == 0 {
		return errs.New(errs.KindInvalid, "connector.config_invalid",
			"config must declare at least one connector")
	}

	seen := make(map[Platform]struct{}, len(c.Connectors))
	for i := range c.Connectors {
		cc := &c.Connectors[i]
		if _, dup := seen[cc.Platform]; dup {
			return errs.New(errs.KindInvalid, "connector.config_invalid",
				fmt.Sprintf("duplicate connector for platform %q", cc.Platform))
		}
		seen[cc.Platform] = struct{}{}

		if err := cc.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (cc *PlatformConfig) validate() error {
	fail := func(msg string) error {
		return errs.New(errs.KindInvalid, "connector.config_invalid",
			fmt.Sprintf("connector %q: %s", cc.Platform, msg))
	}

	if _, ok := knownPlatforms[cc.Platform]; !ok {
		return fail("unknown platform")
	}
	if cc.DisplayName == "" {
		return fail("display_name is required")
	}
	if cc.BaseURL == "" {
		return fail("base_url is required")
	}
	if len(cc.Capabilities) == 0 {
		return fail("at least one capability is required")
	}
	for _, capability := range cc.Capabilities {
		if _, ok := knownCapabilities[capability]; !ok {
			return fail(fmt.Sprintf("unknown capability %q", capability))
		}
	}

	if err := cc.validateAuth(fail); err != nil {
		return err
	}
	return cc.validateRateLimit(fail)
}

func (cc *PlatformConfig) validateAuth(fail func(string) error) error {
	// Reject any credential reference that does not look like an env-var name;
	// a value that fails this check is an embedded literal secret.
	for field, val := range map[string]string{
		"api_key_env":       cc.Auth.APIKeyEnv,
		"client_id_env":     cc.Auth.ClientIDEnv,
		"client_secret_env": cc.Auth.ClientSecretEnv,
	} {
		if val != "" && !envVarNamePattern.MatchString(val) {
			return fail(fmt.Sprintf("%s must reference an env var by name, not embed a secret value", field))
		}
	}

	switch cc.Auth.Type {
	case AuthOAuth2:
		if cc.Auth.ClientIDEnv == "" || cc.Auth.ClientSecretEnv == "" {
			return fail("oauth2 auth requires client_id_env and client_secret_env")
		}
	case AuthAPIKey:
		if cc.Auth.APIKeyEnv == "" {
			return fail("api_key auth requires api_key_env")
		}
	default:
		return fail(fmt.Sprintf("unknown auth type %q", cc.Auth.Type))
	}
	return nil
}

func (cc *PlatformConfig) validateRateLimit(fail func(string) error) error {
	switch cc.RateLimit.Model {
	case RateLimitQuotaUnits:
		if cc.RateLimit.UnitsPerDay < 1 || cc.RateLimit.DefaultCost < 1 || cc.RateLimit.SearchCost < 1 {
			return fail("quota_units rate limit requires positive units_per_day, default_cost and search_cost")
		}
	case RateLimitBucketedCalls:
		if cc.RateLimit.CallsPerHour < 1 {
			return fail("bucketed_calls rate limit requires a positive calls_per_hour")
		}
		if _, err := cc.RateLimit.WindowDuration(); err != nil {
			return fail("bucketed_calls rate limit requires a valid window duration (e.g. \"1h\")")
		}
	default:
		return fail(fmt.Sprintf("unknown rate limit model %q", cc.RateLimit.Model))
	}
	return nil
}
