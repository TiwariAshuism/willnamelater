package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/campaign/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// The scaffold service satisfies the interface the handler depends on.
var _ service.CampaignService = service.New()

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
		{"create", http.MethodPost, "/campaigns", `{"name":"Summer Launch","influencer_ids":["a","b"]}`},
		{"list", http.MethodGet, "/campaigns", ""},
		{"get", http.MethodGet, "/campaigns/abc", ""},
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
	rec := do(newRouter(), http.MethodPost, "/campaigns", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "campaign.request_invalid") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// TestCreateRejectsMissingName guards the binding tags: a campaign with no name
// is a 400, never reaching the service.
func TestCreateRejectsMissingName(t *testing.T) {
	rec := do(newRouter(), http.MethodPost, "/campaigns", `{"influencer_ids":["a"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}
