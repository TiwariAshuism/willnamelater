package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// DefaultBaseURL is the Razorpay API root used when RazorpayConfig.BaseURL is
// empty.
const DefaultBaseURL = "https://api.razorpay.com"

// defaultTimeout bounds a single call to the provider so a hung gateway cannot
// pin a request goroutine indefinitely.
const defaultTimeout = 15 * time.Second

// RazorpayConfig carries the credentials and endpoint for the Razorpay client.
// KeyID and KeySecret authenticate API calls; WebhookSecret verifies inbound
// webhook signatures and is a different secret from the API key.
type RazorpayConfig struct {
	KeyID         string
	KeySecret     string
	WebhookSecret string
	BaseURL       string
}

// Razorpay is the production PaymentProvider. All network access goes through
// the injected httpDoer, and the clock is injectable, so the type is fully
// testable without a live gateway.
type Razorpay struct {
	cfg  RazorpayConfig
	http httpDoer
	now  func() time.Time
}

var _ PaymentProvider = (*Razorpay)(nil)

// NewRazorpay builds a Razorpay client. A nil doer defaults to an http.Client
// with a bounded timeout; a nil now defaults to time.Now; an empty BaseURL
// defaults to DefaultBaseURL.
func NewRazorpay(cfg RazorpayConfig, doer httpDoer, now func() time.Time) *Razorpay {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if doer == nil {
		doer = &http.Client{Timeout: defaultTimeout}
	}
	if now == nil {
		now = time.Now
	}
	return &Razorpay{cfg: cfg, http: doer, now: now}
}

// createSubscriptionResponse is the subset of Razorpay's subscription resource
// the billing module consumes.
type createSubscriptionResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	CustomerID   string `json:"customer_id"`
	CurrentStart *int64 `json:"current_start"`
	CurrentEnd   *int64 `json:"current_end"`
}

// CreateSubscription creates a subscription at Razorpay and normalizes the
// result. Any transport or gateway failure is reported as unavailable; the
// underlying cause is wrapped for logs and never carried in the client message.
func (r *Razorpay) CreateSubscription(ctx context.Context, in SubscriptionInput) (Subscription, error) {
	body, err := json.Marshal(map[string]any{
		"plan_id":         in.PlanExternalID,
		"total_count":     in.TotalCount,
		"customer_notify": boolToInt(in.CustomerNotify),
		"notes":           map[string]string{"user_id": in.UserID.String()},
	})
	if err != nil {
		return Subscription{}, errs.Wrap(err, errs.KindInternal, "billing.provider_request", "could not build provider request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+"/v1/subscriptions", bytes.NewReader(body))
	if err != nil {
		return Subscription{}, errs.Wrap(err, errs.KindInternal, "billing.provider_request", "could not build provider request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(r.cfg.KeyID, r.cfg.KeySecret)

	resp, err := r.http.Do(req)
	if err != nil {
		return Subscription{}, errs.Wrap(err, errs.KindUnavailable, "billing.provider_unavailable", "payment provider is unavailable")
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return Subscription{}, errs.Wrap(err, errs.KindUnavailable, "billing.provider_unavailable", "payment provider is unavailable")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Subscription{}, errs.Wrap(
			fmt.Errorf("provider status %d: %s", resp.StatusCode, payload),
			errs.KindUnavailable, "billing.provider_error", "payment provider rejected the request",
		)
	}

	var parsed createSubscriptionResponse
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return Subscription{}, errs.Wrap(err, errs.KindInternal, "billing.provider_response", "could not parse provider response")
	}

	sub := Subscription{
		ExternalCustomerID:     parsed.CustomerID,
		ExternalSubscriptionID: parsed.ID,
		Status:                 mapSubscriptionStatus(parsed.Status),
	}
	if parsed.CurrentStart != nil {
		sub.CurrentPeriodStart = time.Unix(*parsed.CurrentStart, 0).UTC()
	}
	if parsed.CurrentEnd != nil {
		sub.CurrentPeriodEnd = time.Unix(*parsed.CurrentEnd, 0).UTC()
	}
	return sub, nil
}

// webhookEnvelope is the subset of a Razorpay webhook body the billing module
// reads: the event name and the subscription entity it concerns.
type webhookEnvelope struct {
	Event   string `json:"event"`
	Payload struct {
		Subscription struct {
			Entity struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"entity"`
		} `json:"subscription"`
	} `json:"payload"`
}

// HandleWebhook verifies the webhook signature and parses the event.
//
// Verification recomputes HMAC-SHA256 over the exact raw bytes using the
// webhook secret and compares it to the provided signature with
// crypto/hmac.Equal, which is constant time and so leaks nothing about how much
// of the signature matched. A payload whose signature does not verify is
// rejected as unauthorized and is never parsed or acted upon.
func (r *Razorpay) HandleWebhook(_ context.Context, raw []byte, signature string) (Event, error) {
	if !r.verify(raw, signature) {
		return Event{}, errs.New(errs.KindUnauthorized, "billing.webhook_signature_invalid", "webhook signature verification failed")
	}

	var env webhookEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Event{}, errs.Wrap(err, errs.KindInvalid, "billing.webhook_malformed", "webhook payload is malformed")
	}

	return Event{
		Type:                   env.Event,
		ExternalSubscriptionID: env.Payload.Subscription.Entity.ID,
		Status:                 statusForEvent(env.Event, env.Payload.Subscription.Entity.Status),
	}, nil
}

// verify reports whether signature is a valid HMAC-SHA256 of raw under the
// webhook secret. It decodes the hex signature and compares raw MAC bytes so
// the comparison is constant time; a malformed hex signature is simply invalid.
func (r *Razorpay) verify(raw []byte, signature string) bool {
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(r.cfg.WebhookSecret))
	mac.Write(raw)
	return hmac.Equal(mac.Sum(nil), provided)
}

// statusForEvent maps a Razorpay subscription webhook event to the billing
// subscription status it implies. Events that carry no status change (or that
// the module does not track) yield the empty string and are acknowledged
// without effect. entityStatus is used as a fallback for the generic
// subscription.updated event.
func statusForEvent(event, entityStatus string) string {
	switch event {
	case "subscription.activated", "subscription.charged", "subscription.resumed":
		return "active"
	case "subscription.pending", "subscription.halted":
		return "past_due"
	case "subscription.cancelled":
		return "canceled"
	case "subscription.completed":
		return "expired"
	case "subscription.updated":
		return mapSubscriptionStatus(entityStatus)
	default:
		return ""
	}
}

// mapSubscriptionStatus translates a Razorpay subscription status onto the
// subscription.status vocabulary. Unknown values map to empty so a caller can
// decide how to treat them rather than persisting an invalid status.
func mapSubscriptionStatus(status string) string {
	switch status {
	case "created", "authenticated", "pending":
		return "trialing"
	case "active":
		return "active"
	case "halted":
		return "past_due"
	case "cancelled":
		return "canceled"
	case "completed", "expired":
		return "expired"
	default:
		return ""
	}
}

// boolToInt renders a bool as the 0/1 integer Razorpay expects for
// customer_notify.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
