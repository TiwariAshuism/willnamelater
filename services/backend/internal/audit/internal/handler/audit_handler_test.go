package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeService is a configurable AuditService stand-in for the transport tests.
type fakeService struct {
	resp    model.AuditResponse
	list    []model.AuditResponse
	err     error
	listErr error
}

func (f *fakeService) SubmitAudit(context.Context, model.SubmitAuditRequest) (model.AuditResponse, error) {
	return f.resp, f.err
}

func (f *fakeService) GetAudit(context.Context, string) (model.AuditResponse, error) {
	return f.resp, f.err
}

func (f *fakeService) ListAudits(context.Context) ([]model.AuditResponse, error) {
	return f.list, f.listErr
}

func newRouter(svc *fakeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).Register(r)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestSubmit_WrappedCauseNeverLeaks is the security-critical transport test: a
// service error whose wrapped cause carries a secret must render only the domain
// code and message, never the cause.
func TestSubmit_WrappedCauseNeverLeaks(t *testing.T) {
	const secret = "postgres://user:sup3rs3cret@db:5432/influaudit"
	svc := &fakeService{
		err: errs.Wrap(errors.New(secret), errs.KindInternal, "audit.create_failed", "could not create audit job"),
	}
	r := newRouter(svc)

	rec := do(r, http.MethodPost, "/audits", `{"influencer_id":"`+"11111111-1111-1111-1111-111111111111"+`","idempotency_key":"k"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response body leaked the wrapped cause: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sup3rs3cret") {
		t.Fatalf("response body leaked a secret fragment: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "could not create audit job") {
		t.Fatalf("response body missing the safe message: %s", rec.Body.String())
	}
}

func TestSubmit_MalformedBodyIsRejected(t *testing.T) {
	r := newRouter(&fakeService{})
	rec := do(r, http.MethodPost, "/audits", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "audit.request_invalid") {
		t.Fatalf("body = %s, want the malformed-body code", rec.Body.String())
	}
}

func TestSubmit_SuccessReturns202(t *testing.T) {
	svc := &fakeService{resp: model.AuditResponse{ID: "abc", Status: string(model.StatusQueued)}}
	r := newRouter(svc)
	rec := do(r, http.MethodPost, "/audits", `{"influencer_id":"11111111-1111-1111-1111-111111111111","idempotency_key":"k"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}

func TestGet_QuotaExceededMapsTo402(t *testing.T) {
	svc := &fakeService{err: errs.New(errs.KindQuotaExceeded, "billing.quota_exceeded", "plan quota exceeded")}
	r := newRouter(svc)
	rec := do(r, http.MethodGet, "/audits/abc", "")
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
}
