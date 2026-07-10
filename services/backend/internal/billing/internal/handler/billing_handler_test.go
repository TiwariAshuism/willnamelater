package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeService is a configurable BillingService stand-in for the transport tests.
// Each endpoint returns its preset outcome, and webhookCalls records whether the
// webhook ever reached the service, so a test can assert the handler rejected a
// request before delegating.
type fakeService struct {
	plans    []model.PlanResponse
	sub      model.SubscriptionResponse
	listErr  error
	getErr   error
	subErr   error
	webhkErr error

	webhookCalls int
}

func (f *fakeService) ListPlans(context.Context) ([]model.PlanResponse, error) {
	return f.plans, f.listErr
}

func (f *fakeService) GetSubscription(context.Context) (model.SubscriptionResponse, error) {
	return f.sub, f.getErr
}

func (f *fakeService) Subscribe(context.Context, model.SubscribeRequest) (model.SubscriptionResponse, error) {
	return f.sub, f.subErr
}

func (f *fakeService) Webhook(context.Context, model.WebhookRequest) error {
	f.webhookCalls++
	return f.webhkErr
}

// newRouter mounts both route sets on the same engine. The auth boundary between
// them is the composition root's concern, not the handler's, so exercising them
// together here is correct: these tests assert transport behaviour, not who is
// allowed to call what.
func newRouter(svc *fakeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc)
	h.RegisterProtectedRoutes(r)
	h.RegisterPublicRoutes(r)
	return r
}

func do(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response envelope: %v (body=%s)", err, rec.Body.String())
	}
}

// TestWebhookRejectionsNeverReachService covers the two ways the handler must
// refuse a webhook before delegating: a request carrying no signature at all,
// and one whose body exceeds the fixed limit for the unauthenticated endpoint.
// In both cases the service — which is what performs HMAC verification — must
// never be called.
func TestWebhookRejectionsNeverReachService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		headers    map[string]string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing signature is unauthorized",
			body:       `{"event":"subscription.activated"}`,
			headers:    nil,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "billing.webhook_unsigned",
		},
		{
			name:       "oversized body is rejected",
			body:       strings.Repeat("a", (64<<10)+1),
			headers:    map[string]string{"X-Razorpay-Signature": "deadbeef"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "billing.webhook_too_large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakeService{}
			rec := do(newRouter(svc), http.MethodPost, "/billing/webhook", tt.body, tt.headers)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if svc.webhookCalls != 0 {
				t.Fatalf("service was called %d times, want 0 (request must be refused before delegation)", svc.webhookCalls)
			}
			var env struct {
				Error errs.Error `json:"error"`
			}
			decodeEnvelope(t, rec, &env)
			if env.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", env.Error.Code, tt.wantCode)
			}
		})
	}
}

// TestWebhookBadSignatureIsUnauthorized covers a signed webhook whose signature
// the service rejects: the handler delegates, and the service's KindUnauthorized
// error renders as 401.
func TestWebhookBadSignatureIsUnauthorized(t *testing.T) {
	t.Parallel()

	svc := &fakeService{webhkErr: errs.New(errs.KindUnauthorized, "billing.webhook_signature_invalid", "webhook signature verification failed")}
	rec := do(newRouter(svc), http.MethodPost, "/billing/webhook",
		`{"event":"subscription.activated"}`,
		map[string]string{"X-Razorpay-Signature": "not-a-valid-signature"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if svc.webhookCalls != 1 {
		t.Fatalf("service called %d times, want 1", svc.webhookCalls)
	}
}

// TestWebhookSuccess is the happy path: a verified webhook returns 200 with no
// body echoed back.
func TestWebhookSuccess(t *testing.T) {
	t.Parallel()

	svc := &fakeService{}
	rec := do(newRouter(svc), http.MethodPost, "/billing/webhook",
		`{"event":"subscription.activated"}`,
		map[string]string{"X-Razorpay-Signature": "valid"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "" {
		t.Fatalf("webhook echoed a body: %q", body)
	}
}

// TestQuotaExceededRendersAsPaymentRequired asserts the KindQuotaExceeded domain
// error maps to 402 through the shared error renderer.
func TestQuotaExceededRendersAsPaymentRequired(t *testing.T) {
	t.Parallel()

	svc := &fakeService{getErr: errs.New(errs.KindQuotaExceeded, "billing.quota_exceeded", "plan quota exceeded for this period")}
	rec := do(newRouter(svc), http.MethodGet, "/billing/subscription", "", nil)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPaymentRequired)
	}
	var env struct {
		Error errs.Error `json:"error"`
	}
	decodeEnvelope(t, rec, &env)
	if env.Error.Code != "billing.quota_exceeded" {
		t.Fatalf("code = %q, want billing.quota_exceeded", env.Error.Code)
	}
}

// TestWrappedCauseNeverLeaks is the security assertion required by the task: a
// service error wrapping a cause that carries a secret must render as its safe
// Message and stable Code only. Neither the cause nor the secret may appear in
// the response body.
func TestWrappedCauseNeverLeaks(t *testing.T) {
	t.Parallel()

	const secret = "rzp_live_9f8s7d6f5g4h3j2k"
	cause := errors.New("razorpay auth failed for key " + secret)
	wrapped := errs.Wrap(cause, errs.KindInternal, "billing.plans_query", "could not read plans")

	rec := do(newRouter(&fakeService{listErr: wrapped}), http.MethodGet, "/billing/plans", "", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("response body leaked the secret: %s", body)
	}
	if strings.Contains(body, "razorpay auth failed") {
		t.Fatalf("response body leaked the wrapped cause: %s", body)
	}

	var env struct {
		Error errs.Error `json:"error"`
	}
	decodeEnvelope(t, rec, &env)
	if env.Error.Message != "could not read plans" {
		t.Fatalf("message = %q, want the safe domain message", env.Error.Message)
	}
}

// TestMalformedSubscribeBodyIsBadRequest asserts the subscribe binder failure
// renders as a 400 with the module's stable code, discarding gin's own message.
func TestMalformedSubscribeBodyIsBadRequest(t *testing.T) {
	t.Parallel()

	rec := do(newRouter(&fakeService{}), http.MethodPost, "/billing/subscribe", `{not json`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var env struct {
		Error errs.Error `json:"error"`
	}
	decodeEnvelope(t, rec, &env)
	if env.Error.Code != "billing.request_invalid" {
		t.Fatalf("code = %q, want billing.request_invalid", env.Error.Code)
	}
}
