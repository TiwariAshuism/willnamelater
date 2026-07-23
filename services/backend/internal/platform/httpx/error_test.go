package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// captureLogs redirects the default slog logger to a buffer for the duration of
// the test and restores it afterward, so assertions can inspect server-side
// log output.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// renderThrough runs RenderError inside a real gin request so c.Request and the
// response writer are wired exactly as in production.
func renderThrough(err error) *httptest.ResponseRecorder {
	router := gin.New()
	router.GET("/", func(c *gin.Context) { RenderError(c, err) })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

func TestRenderErrorStatusAndBody(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid maps to 400 and exposes code and message",
			err:         errs.New(errs.KindInvalid, "audit.bad_handle", "handle is malformed"),
			wantStatus:  http.StatusBadRequest,
			wantCode:    "audit.bad_handle",
			wantMessage: "handle is malformed",
		},
		{
			name:        "not found maps to 404",
			err:         errs.New(errs.KindNotFound, "audit.not_found", "audit does not exist"),
			wantStatus:  http.StatusNotFound,
			wantCode:    "audit.not_found",
			wantMessage: "audit does not exist",
		},
		{
			name:        "quota exceeded maps to 402",
			err:         errs.New(errs.KindQuotaExceeded, "audit.quota_exceeded", "monthly quota reached"),
			wantStatus:  http.StatusPaymentRequired,
			wantCode:    "audit.quota_exceeded",
			wantMessage: "monthly quota reached",
		},
		{
			name:        "not implemented maps to 501",
			err:         errs.ErrNotImplemented,
			wantStatus:  http.StatusNotImplemented,
			wantCode:    "not_implemented",
			wantMessage: "this capability is not available yet",
		},
		{
			name:        "untyped error is reported as generic internal",
			err:         errors.New("connection reset by peer"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "internal",
			wantMessage: "internal server error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			rec := renderThrough(tc.err)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			var body errorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not the error envelope: %v (%s)", err, rec.Body.String())
			}
			if body.Error.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if body.Error.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Error.Message, tc.wantMessage)
			}
		})
	}
}

// TestRenderErrorNeverLeaksCause is the security-critical guarantee: a wrapped
// cause carrying a secret must reach the logs but never the client.
func TestRenderErrorNeverLeaksCause(t *testing.T) {
	const secret = "sk_live_51H8xSECRETtokenVALUE"

	cause := errors.New("upstream rejected token " + secret)
	err := errs.Wrap(cause, errs.KindUnavailable, "connector.upstream_down", "the provider is temporarily unavailable")

	logs := captureLogs(t)
	rec := renderThrough(err)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response body leaked the wrapped cause secret: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upstream rejected token") {
		t.Fatalf("response body leaked cause text: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), secret) {
		t.Error("expected the wrapped cause (with secret) to be logged for diagnosis, but it was absent")
	}
}
