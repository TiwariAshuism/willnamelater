package redis

import (
	"crypto/tls"
	"testing"
)

// TestTLSConfigFor covers the settings asynq must be handed to match the client
// this package builds. A mismatch between the two is silent — both processes
// come up, the API enqueues onto one endpoint and the worker consumes from
// another, and no task ever runs — so the derivation is asserted directly.
func TestTLSConfigFor(t *testing.T) {
	tests := []struct {
		name           string
		cfg            Config
		wantNil        bool
		wantServerName string
	}{
		{
			name:    "tls off yields no config",
			cfg:     Config{Addr: "localhost:6379"},
			wantNil: true,
		},
		{
			name:           "server name derives from a host:port address",
			cfg:            Config{Addr: "influaudit.redis.cache.windows.net:6380", TLS: true},
			wantServerName: "influaudit.redis.cache.windows.net",
		},
		{
			// SplitHostPort fails on a bare host. The address is then already the
			// server name, so the error is the answer rather than a fault.
			name:           "server name falls back to a bare address with no port",
			cfg:            Config{Addr: "cache.internal", TLS: true},
			wantServerName: "cache.internal",
		},
		{
			// Set when reaching Redis through a tunnel or by IP, where the
			// certificate names a host the address does not.
			name:           "explicit server name overrides the address",
			cfg:            Config{Addr: "10.0.0.5:6380", TLS: true, TLSServerName: "cache.internal"},
			wantServerName: "cache.internal",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TLSConfigFor(tc.cfg)

			if tc.wantNil {
				if got != nil {
					t.Fatalf("TLSConfigFor = %+v, want nil when TLS is off", got)
				}
				return
			}

			if got == nil {
				t.Fatal("TLSConfigFor = nil, want a config when TLS is on")
			}
			if got.ServerName != tc.wantServerName {
				t.Errorf("ServerName = %q, want %q", got.ServerName, tc.wantServerName)
			}
			if got.MinVersion != tls.VersionTLS12 {
				t.Errorf("MinVersion = %#x, want TLS 1.2 (%#x)", got.MinVersion, tls.VersionTLS12)
			}
			// An unverified connection to a managed cache is a credential handed to
			// whoever answered.
			if got.InsecureSkipVerify {
				t.Error("InsecureSkipVerify is set; certificate verification must stay on")
			}
		})
	}
}
