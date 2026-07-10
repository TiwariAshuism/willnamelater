package app

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	yaml "github.com/goccy/go-yaml"

	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
)

// This file exists because of a specific, embarrassing failure: the API once
// served a 19-endpoint OpenAPI document at /swagger while every one of those
// endpoints returned 404. Nothing mounted the module routers.
//
// No gate caught it. Unit tests passed (each module worked in isolation), the
// linter passed, the containers were healthy, and the spec was valid and
// deterministic. Only a live curl exposed it.
//
// TestEverySpecPathIsMounted is the permanent guard: the published contract and
// the routing table must agree, in both directions.

// specDocument is the subset of the OpenAPI document these tests read.
type specDocument struct {
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths map[string]map[string]any `yaml:"paths"`
}

func loadSpec(t *testing.T) specDocument {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..",
		"packages", "contracts", "openapi", "influaudit.yaml"))
	if err != nil {
		t.Fatalf("read spec (run: go run ./cmd/openapigen): %v", err)
	}

	var doc specDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("spec declares no paths")
	}
	return doc
}

// testRouter builds the real routing table. The modules are constructed over nil
// datastores on purpose: RegisterRoutes only records handler pointers, and no
// handler runs here. Substituting a fake router would test the fake.
func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment: config.EnvDev,
		HTTP:        config.HTTPConfig{PublicBaseURL: "http://localhost:8080"},
		JWT:         config.JWTConfig{PrivateKeyPEM: config.Secret(testSigningKey(t))},
		Connectors:  repoConfigPaths(t),
	}

	// buildModules refuses a nil cipher: oauth token sealing and commenter
	// pseudonymization both depend on it. A throwaway key is real cryptography,
	// not a stub.
	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x01}, crypto.KeySize))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	a := &App{Config: cfg, Cipher: cipher}

	connectors := loadRepoConnectors(t)
	if err := a.buildModules(connectors); err != nil {
		t.Fatalf("buildModules: %v", err)
	}

	// Router reads the spec from the repo root at runtime; from this package's
	// working directory that path does not resolve, and mountSwagger degrades
	// gracefully. That is fine: these tests are about module routes.
	return a.Router()
}

// testSigningKey generates a throwaway RSA key. Real key material must never be
// committed, and the auth module parses its signing key at construction, so the
// key has to be real — just not persistent.
func testSigningKey(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

// mountedRoutes returns the router's "METHOD path" set.
func mountedRoutes(r *gin.Engine) map[string]struct{} {
	out := make(map[string]struct{})
	for _, ri := range r.Routes() {
		out[ri.Method+" "+ri.Path] = struct{}{}
	}
	return out
}

// specRoutes returns the spec's "METHOD path" set, translated into gin's syntax:
// the server base path is prepended and {param} becomes :param.
func specRoutes(t *testing.T, doc specDocument) map[string]struct{} {
	t.Helper()

	base := ""
	if len(doc.Servers) > 0 {
		base = strings.TrimSuffix(doc.Servers[0].URL, "/")
	}
	if base != apiBasePath {
		t.Fatalf("spec server URL %q disagrees with the router's base path %q; "+
			"regenerate with: go run ./cmd/openapigen", base, apiBasePath)
	}

	verbs := map[string]string{
		"get": "GET", "post": "POST", "put": "PUT",
		"patch": "PATCH", "delete": "DELETE", "head": "HEAD", "options": "OPTIONS",
	}

	out := make(map[string]struct{})
	for path, item := range doc.Paths {
		ginPath := base + strings.NewReplacer("{", ":", "}", "").Replace(path)
		for verb := range item {
			method, ok := verbs[strings.ToLower(verb)]
			if !ok {
				continue // parameters, summary, and other non-operation keys
			}
			out[method+" "+ginPath] = struct{}{}
		}
	}
	return out
}

// Every endpoint the spec advertises must resolve to a mounted route. A spec
// entry with no route is a published lie: clients generated from it will 404.
func TestEverySpecPathIsMounted(t *testing.T) {
	doc := loadSpec(t)
	mounted := mountedRoutes(testRouter(t))

	var missing []string
	for route := range specRoutes(t, doc) {
		if _, ok := mounted[route]; !ok {
			missing = append(missing, route)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("the OpenAPI spec advertises %d endpoint(s) that are not mounted "+
			"and would return 404:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// And the converse: a mounted business route absent from the spec is an
// undocumented endpoint. Infrastructure routes are exempt by name, not by
// pattern, so a new one has to be added here deliberately.
func TestEveryMountedRouteIsInTheSpec(t *testing.T) {
	doc := loadSpec(t)
	spec := specRoutes(t, doc)

	infra := map[string]struct{}{
		"GET /healthz":      {},
		"GET /readyz":       {},
		"GET /openapi.yaml": {},
		"GET /swagger/*any": {},
		"GET /swagger":      {},
	}

	var undocumented []string
	for route := range mountedRoutes(testRouter(t)) {
		if _, ok := infra[route]; ok {
			continue
		}
		if _, ok := spec[route]; !ok {
			undocumented = append(undocumented, route)
		}
	}

	if len(undocumented) > 0 {
		sort.Strings(undocumented)
		t.Fatalf("mounted route(s) missing from the OpenAPI spec:\n  %s\n"+
			"regenerate with: go run ./cmd/openapigen",
			strings.Join(undocumented, "\n  "))
	}
}

// The router must mount a non-trivial number of module routes. Without this, a
// regression that mounts nothing would make both set-comparison tests above pass
// vacuously against an empty spec.
func TestRouterMountsModuleRoutes(t *testing.T) {
	const wantAtLeast = 15

	var moduleRoutes int
	for route := range mountedRoutes(testRouter(t)) {
		if strings.Contains(route, " "+apiBasePath+"/") {
			moduleRoutes++
		}
	}

	if moduleRoutes < wantAtLeast {
		t.Fatalf("only %d routes mounted under %s, want at least %d: the modules are not wired",
			moduleRoutes, apiBasePath, wantAtLeast)
	}
}

// The auth boundary is asserted behaviourally, not by inspecting gin's internals.
//
// An unauthenticated POST to /billing/subscribe must be rejected by the auth
// middleware with 401, before the handler runs. The same request to
// /billing/webhook must NOT be rejected with 401: Razorpay holds no session and
// proves itself with an HMAC over the raw body. If the webhook sat behind the
// middleware, every genuine webhook would be turned away.
//
// The webhook handler then fails for its own reasons (no signature), which is
// exactly the point: it got past the auth boundary and was judged on its
// signature instead.
func TestAuthBoundary(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		reason     string
	}{
		{
			name:       "protected route rejects an unauthenticated caller",
			path:       apiBasePath + "/billing/subscribe",
			wantStatus: http.StatusUnauthorized,
			reason:     "the auth middleware must reject it before the handler runs",
		},
		{
			name:       "webhook is reachable without a session",
			path:       apiBasePath + "/billing/webhook",
			wantStatus: http.StatusUnauthorized,
			reason:     "an unsigned webhook is unauthorized for its OWN reason, not the middleware's",
		},
	}

	r := testRouter(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader("{}"))
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s); body: %s",
					rec.Code, tt.wantStatus, tt.reason, rec.Body.String())
			}
		})
	}

	// Both return 401, so distinguish them by their error code: the middleware
	// and the webhook's signature check are different rejections.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, apiBasePath+"/billing/webhook", strings.NewReader("{}"))
	r.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "auth.") {
		t.Errorf("the webhook was rejected by the AUTH middleware, not by its signature check: %s",
			rec.Body.String())
	}
}
