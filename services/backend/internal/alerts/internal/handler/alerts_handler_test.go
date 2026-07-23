package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/alerts/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// notImplementedService is the real scaffold service. Testing the handler over it
// confirms the routes bind and the shared error vocabulary renders 501, rather
// than asserting on a fake.
func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New()).Register(r)
	return r
}

// The scaffold service satisfies the interface the handler depends on.
var _ service.AlertsService = service.New()

func do(r *gin.Engine, method, target, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(context.Background(), method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRoutesBindAndAnswerNotImplemented(t *testing.T) {
	r := newRouter()
	cases := []struct {
		name, method, target, body string
	}{
		{"list", http.MethodGet, "/alerts", ""},
		{"create", http.MethodPost, "/alerts", `{"influencer_id":"i","metric":"engagement_rate","comparator":"below","threshold":1.5}`},
		{"delete", http.MethodDelete, "/alerts/abc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(r, tc.method, tc.target, tc.body)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), errs.ErrNotImplemented.Code) {
				t.Fatalf("body missing the not_implemented code: %s", rec.Body.String())
			}
		})
	}
}

func TestCreateRejectsMalformedBodyBeforeService(t *testing.T) {
	rec := do(newRouter(), http.MethodPost, "/alerts", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alerts.request_invalid") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// TestCreateRejectsMissingRequiredFields guards the binding tags on the request
// DTO: a body missing a required field is a 400, never reaching the service.
func TestCreateRejectsMissingRequiredFields(t *testing.T) {
	// A syntactically valid body that omits every required field.
	rec := do(newRouter(), http.MethodPost, "/alerts", `{"threshold":1.0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}
