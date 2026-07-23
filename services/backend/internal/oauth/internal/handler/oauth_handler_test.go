package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeService is a configurable OAuthService stand-in for the transport tests.
// It records the provider it was called with so a test can assert the path
// parameter was threaded through, and returns the configured result or error.
type fakeService struct {
	authResp     model.AuthorizeResponse
	connResp     model.ConnectionResponse
	listResp     []model.ConnectionResponse
	err          error
	gotProvider  string
	callbackSeen bool
}

func (f *fakeService) Authorize(_ context.Context, provider string) (model.AuthorizeResponse, error) {
	f.gotProvider = provider
	return f.authResp, f.err
}

func (f *fakeService) Callback(_ context.Context, provider string) (model.ConnectionResponse, error) {
	f.gotProvider = provider
	f.callbackSeen = true
	return f.connResp, f.err
}

func (f *fakeService) Connections(context.Context) ([]model.ConnectionResponse, error) {
	return f.listResp, f.err
}

func (f *fakeService) Disconnect(_ context.Context, provider string) error {
	f.gotProvider = provider
	return f.err
}

func newRouter(svc *fakeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r)
	return r
}

func do(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) errs.Error {
	t.Helper()
	var env struct {
		Error errs.Error `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
	}
	return env.Error
}

// The static /oauth/connections route must coexist with the /oauth/:provider
// wildcard routes and win for the literal "connections" segment, rather than
// being captured as a provider named "connections".
func TestConnectionsRouteIsNotShadowedByProviderWildcard(t *testing.T) {
	t.Parallel()

	svc := &fakeService{listResp: []model.ConnectionResponse{{Provider: "google"}}}
	rec := do(newRouter(svc), http.MethodGet, "/oauth/connections")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if svc.gotProvider != "" {
		t.Fatalf("connections was routed as provider %q", svc.gotProvider)
	}
}

func TestHappyPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		svc        *fakeService
		wantStatus int
		wantProv   string
	}{
		{
			name:       "authorize",
			method:     http.MethodGet,
			path:       "/oauth/google/authorize",
			svc:        &fakeService{authResp: model.AuthorizeResponse{AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth?x=1", State: "s"}},
			wantStatus: http.StatusOK,
			wantProv:   "google",
		},
		{
			name:       "callback",
			method:     http.MethodGet,
			path:       "/oauth/meta/callback?code=abc&state=s",
			svc:        &fakeService{connResp: model.ConnectionResponse{Provider: "meta", Platform: "instagram"}},
			wantStatus: http.StatusOK,
			wantProv:   "meta",
		},
		{
			name:       "connections",
			method:     http.MethodGet,
			path:       "/oauth/connections",
			svc:        &fakeService{},
			wantStatus: http.StatusOK,
		},
		{
			name:       "disconnect",
			method:     http.MethodDelete,
			path:       "/oauth/google",
			svc:        &fakeService{},
			wantStatus: http.StatusNoContent,
			wantProv:   "google",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(newRouter(tt.svc), tt.method, tt.path)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantProv != "" && tt.svc.gotProvider != tt.wantProv {
				t.Fatalf("provider = %q, want %q", tt.svc.gotProvider, tt.wantProv)
			}
		})
	}
}

func TestErrorKindMapsToStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unknown provider is not found",
			err:        errs.New(errs.KindNotFound, "oauth.unknown_provider", "unknown provider"),
			wantStatus: http.StatusNotFound,
			wantCode:   "oauth.unknown_provider",
		},
		{
			name:       "invalid state is unauthorized",
			err:        errs.New(errs.KindUnauthorized, "oauth.state_invalid", "authorization state is invalid or expired"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "oauth.state_invalid",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(newRouter(&fakeService{err: tt.err}), http.MethodGet, "/oauth/google/authorize")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := decodeEnvelope(t, rec).Code; got != tt.wantCode {
				t.Fatalf("code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}

// The callback receives the provider's untrusted error_description in the query
// string. Whatever the provider sent, it must never be reflected into the
// response body — reflecting it would turn the callback into an XSS/redirect
// injection surface.
func TestCallbackNeverReflectsUntrustedErrorDescription(t *testing.T) {
	t.Parallel()

	const injected = "<script>alert(1)</script>"
	// The real service maps a denied consent to a safe, fixed message; the fake
	// reproduces that contract so the assertion is about the transport.
	svc := &fakeService{err: errs.New(errs.KindInvalid, "oauth.authorization_denied", "authorization was not granted")}

	path := "/oauth/google/callback?error=access_denied&error_description=" + url.QueryEscape(injected)
	rec := do(newRouter(svc), http.MethodGet, path)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !svc.callbackSeen {
		t.Fatal("callback handler did not invoke the service")
	}
	if body := rec.Body.String(); strings.Contains(body, injected) || strings.Contains(body, "script") {
		t.Fatalf("response reflected the provider's untrusted error_description: %s", body)
	}
	if msg := decodeEnvelope(t, rec).Message; msg != "authorization was not granted" {
		t.Fatalf("message = %q, want the safe domain message", msg)
	}
}

// A service error that wraps a cause carrying a secret must render as its safe
// Message and stable Code only; neither the cause nor the secret may leak.
func TestWrappedCauseNeverLeaks(t *testing.T) {
	t.Parallel()

	const secret = "postgres://admin:sup3r-s3cret@10.0.0.5:5432/influ"
	cause := errors.New("dial " + secret + ": connection refused")
	wrapped := errs.Wrap(cause, errs.KindUnavailable, "oauth.token_list_failed", "could not list connections")

	rec := do(newRouter(&fakeService{err: wrapped}), http.MethodGet, "/oauth/connections")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	body := rec.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("response body leaked the secret: %s", body)
	}
	if strings.Contains(body, "connection refused") {
		t.Fatalf("response body leaked the wrapped cause: %s", body)
	}
	if msg := decodeEnvelope(t, rec).Message; msg != "could not list connections" {
		t.Fatalf("message = %q, want the safe domain message", msg)
	}
}
