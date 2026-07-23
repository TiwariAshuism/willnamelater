package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/bulkaudit/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// The scaffold service satisfies the interface the handler depends on.
var _ service.BulkAuditService = service.New()

func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New()).Register(r)
	return r
}

func do(r *gin.Engine, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
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
		{"create", http.MethodPost, "/bulk-audits", `{"platform":"youtube","handles":["a","b"]}`},
		{"list", http.MethodGet, "/bulk-audits", ""},
		{"get", http.MethodGet, "/bulk-audits/abc", ""},
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
	rec := do(newRouter(), http.MethodPost, "/bulk-audits", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bulkaudit.request_invalid") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// TestCreateRejectsEmptyHandles guards the binding tags: a batch with no handles
// is a 400, never reaching the service.
func TestCreateRejectsEmptyHandles(t *testing.T) {
	rec := do(newRouter(), http.MethodPost, "/bulk-audits", `{"platform":"youtube","handles":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}
