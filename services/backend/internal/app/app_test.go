package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// repoConfigPaths locates the real connectors.yaml and its schema, so these
// tests exercise the config we actually ship rather than an inlined copy that
// could drift away from it.
func repoConfigPaths(t *testing.T) config.ConnectorsConfig {
	t.Helper()
	base := filepath.Join("..", "..", "..", "..", "packages", "config")
	return config.ConnectorsConfig{
		ConfigPath: filepath.Join(base, "connectors.yaml"),
		SchemaPath: filepath.Join(base, "connectors.schema.json"),
	}
}

// An enabled platform with no registered builder must fail the boot. Skipping it
// would silently produce audits covering fewer platforms than the operator
// configured.
func TestBuildConnectorRegistryRejectsEnabledPlatformWithoutBuilder(t *testing.T) {
	cfg := &config.Config{Connectors: repoConfigPaths(t)}

	// Credentials must be present, or we would fail on the wrong error and the
	// test would pass for a reason it does not intend.
	t.Setenv("YT_OAUTH_CLIENT_ID", "id")
	t.Setenv("YT_OAUTH_CLIENT_SECRET", "secret")
	t.Setenv("YT_API_KEY", "key")
	t.Setenv("META_APP_ID", "id")
	t.Setenv("META_APP_SECRET", "secret")

	_, err := buildConnectorRegistry(cfg)
	if err == nil {
		t.Fatal("expected boot to fail for an enabled platform with no builder")
	}

	var domain *errs.Error
	if !errors.As(err, &domain) || domain.Code != "app.connector_unimplemented" {
		t.Fatalf("error = %v, want errs.Error with code app.connector_unimplemented", err)
	}
}

// A platform whose builder exists must still fail the boot when a credential the
// config names is absent from the environment. Discovering this mid-audit would
// cost real third-party quota.
func TestBuildConnectorRegistryRequiresNamedCredentials(t *testing.T) {
	// Register a builder for every enabled platform so the only possible failure
	// is the missing credential.
	restore := withBuilders(t, map[connector.Platform]connectorBuilder{
		connector.PlatformYouTube:   stubBuilder,
		connector.PlatformInstagram: stubBuilder,
	})
	defer restore()

	cfg := &config.Config{Connectors: repoConfigPaths(t)}

	// Deliberately leave YT_API_KEY unset. t.Setenv guarantees restoration.
	t.Setenv("YT_OAUTH_CLIENT_ID", "id")
	t.Setenv("YT_OAUTH_CLIENT_SECRET", "secret")
	t.Setenv("YT_API_KEY", "")

	_, err := buildConnectorRegistry(cfg)
	if err == nil {
		t.Fatal("expected boot to fail when a named credential is unset")
	}

	var domain *errs.Error
	if !errors.As(err, &domain) || domain.Code != "app.missing_credential" {
		t.Fatalf("error = %v, want errs.Error with code app.missing_credential", err)
	}
}

func TestResolveCredentials(t *testing.T) {
	tests := []struct {
		name    string
		auth    connector.Auth
		env     map[string]string
		want    credentials
		wantErr bool
	}{
		{
			name: "all named vars present",
			auth: connector.Auth{APIKeyEnv: "A_KEY", ClientIDEnv: "A_ID", ClientSecretEnv: "A_SECRET"},
			env:  map[string]string{"A_KEY": "k", "A_ID": "i", "A_SECRET": "s"},
			want: credentials{APIKey: "k", ClientID: "i", ClientSecret: "s"},
		},
		{
			name: "unnamed vars are not required",
			auth: connector.Auth{ClientIDEnv: "B_ID", ClientSecretEnv: "B_SECRET"},
			env:  map[string]string{"B_ID": "i", "B_SECRET": "s"},
			want: credentials{ClientID: "i", ClientSecret: "s"},
		},
		{
			name:    "named but unset is an error",
			auth:    connector.Auth{ClientIDEnv: "C_ID"},
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name:    "named but empty is an error",
			auth:    connector.Auth{ClientIDEnv: "D_ID"},
			env:     map[string]string{"D_ID": ""},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := resolveCredentials(connector.PlatformConfig{
				Platform: connector.PlatformYouTube,
				Auth:     tt.auth,
			})

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCredentials: %v", err)
			}
			if got != tt.want {
				t.Errorf("credentials = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// resolveCredentials must never place a credential value into the error it
// returns; boot errors are logged, and a logged secret is a leaked secret.
func TestResolveCredentialsErrorNamesVariableNotValue(t *testing.T) {
	t.Setenv("LEAK_ID", "")

	_, err := resolveCredentials(connector.PlatformConfig{
		Platform: connector.PlatformYouTube,
		Auth:     connector.Auth{ClientIDEnv: "LEAK_ID", ClientSecretEnv: "LEAK_SECRET"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	// The message must reference the variable NAME so an operator can fix it.
	if got := err.Error(); !strings.Contains(got, "LEAK_ID") {
		t.Errorf("error %q does not name the missing variable", got)
	}
}

func TestSampleRatio(t *testing.T) {
	tests := []struct {
		env  config.Environment
		want float64
	}{
		{config.EnvProd, 0.1},
		{config.EnvStaging, 1.0},
		{config.EnvDev, 1.0},
	}
	for _, tt := range tests {
		if got := sampleRatio(tt.env); got != tt.want {
			t.Errorf("sampleRatio(%q) = %v, want %v", tt.env, got, tt.want)
		}
	}
}

// withBuilders swaps the package-level builder table for the duration of a test
// and returns a restore func. Tests must not run in parallel while it is held.
func withBuilders(t *testing.T, m map[connector.Platform]connectorBuilder) func() {
	t.Helper()
	saved := connectorBuilders
	connectorBuilders = m
	return func() { connectorBuilders = saved }
}

func stubBuilder(connector.PlatformConfig, credentials) (connector.Connector, error) {
	return nil, errors.New("builder must not be reached in this test")
}
