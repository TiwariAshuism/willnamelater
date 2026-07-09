package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func TestRequestID(t *testing.T) {
	const canonical = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	tests := []struct {
		name     string
		inbound  string
		setInbnd bool
		// wantEcho, when non-empty, is the exact request ID we expect back.
		// When empty, any freshly minted UUID that differs from inbound passes.
		wantEcho string
	}{
		{name: "canonical uuid is honored", inbound: canonical, setInbnd: true, wantEcho: canonical},
		{name: "uppercase uuid is canonicalized", inbound: "6BA7B810-9DAD-11D1-80B4-00C04FD430C8", setInbnd: true, wantEcho: canonical},
		{name: "urn form is canonicalized", inbound: "urn:uuid:" + canonical, setInbnd: true, wantEcho: canonical},
		{name: "non-uuid is replaced", inbound: "not-a-uuid", setInbnd: true},
		{name: "header injection attempt is replaced", inbound: "id\r\nX-Injected: evil", setInbnd: true},
		{name: "empty inbound is replaced", inbound: "", setInbnd: true},
		{name: "missing header is minted", setInbnd: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var fromCtx string
			router := gin.New()
			router.Use(RequestID())
			router.GET("/", func(c *gin.Context) {
				fromCtx = RequestIDFromContext(c.Request.Context())
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.setInbnd {
				// Set via the map directly so a CRLF value is not rejected by
				// the header setter before our middleware can observe it.
				req.Header[HeaderRequestID] = []string{tc.inbound}
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			got := rec.Header().Get(HeaderRequestID)
			if got == "" {
				t.Fatal("response is missing the request-id header")
			}
			if got != fromCtx {
				t.Errorf("context id %q does not match response header %q", fromCtx, got)
			}
			if _, err := uuid.Parse(got); err != nil {
				t.Errorf("emitted request id %q is not a valid uuid: %v", got, err)
			}

			if tc.wantEcho != "" {
				if got != tc.wantEcho {
					t.Errorf("request id = %q, want %q", got, tc.wantEcho)
				}
				return
			}
			// Replacement cases: the raw inbound value must never survive.
			if tc.setInbnd && got == tc.inbound {
				t.Errorf("invalid inbound value %q was echoed back instead of replaced", tc.inbound)
			}
		})
	}
}
