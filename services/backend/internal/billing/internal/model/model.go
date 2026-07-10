// Package model holds the billing module's request, response, and domain
// shapes. The *Response types are transport-facing (only what a client may
// see); the *Request types double as command objects whose exported JSON fields
// are client input and whose json:"-" fields are filled in by the service after
// it has resolved a plan or talked to the payment provider.
package model

import "time"

// PlanResponse is a purchasable plan as returned by GET /billing/plans.
type PlanResponse struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	PriceMicros int64  `json:"price_micros"`
	Currency    string `json:"currency"`
	AuditQuota  int    `json:"audit_quota"`
	BulkQuota   int    `json:"bulk_quota"`
	BulkEnabled bool   `json:"bulk_enabled"`
}

// SubscriptionResponse is the caller's current subscription as returned by
// GET /billing/subscription and POST /billing/subscribe. External provider
// identifiers are deliberately omitted.
type SubscriptionResponse struct {
	ID                 string    `json:"id"`
	PlanID             string    `json:"plan_id"`
	PlanCode           string    `json:"plan_code"`
	Status             string    `json:"status"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	CancelAtPeriodEnd  bool      `json:"cancel_at_period_end"`
}

// SubscribeRequest is the command behind POST /billing/subscribe. PlanCode is
// the only client-supplied field; the json:"-" fields are populated by the
// service from the resolved plan and the payment provider's response before the
// row is persisted, so the repository receives everything a subscription row
// needs without those internal values ever crossing the wire.
type SubscribeRequest struct {
	PlanCode string `json:"plan_code"`

	UserID                 string    `json:"-"`
	PlanID                 string    `json:"-"`
	Status                 string    `json:"-"`
	CurrentPeriodStart     time.Time `json:"-"`
	CurrentPeriodEnd       time.Time `json:"-"`
	ExternalCustomerID     string    `json:"-"`
	ExternalSubscriptionID string    `json:"-"`
}

// WebhookRequest carries a provider webhook to the service. RawBody is the exact
// bytes received so the HMAC signature can be recomputed over them; binding into
// a struct first would change the bytes and break verification. RawBody and
// Signature are filled by the handler from the request body and signature
// header; the EventStatus/EventSubscriptionID fields are filled by the service
// after the provider has verified and parsed the event, and are what the
// repository applies. None of the fields are JSON-bound.
type WebhookRequest struct {
	RawBody   []byte `json:"-"`
	Signature string `json:"-"`

	EventStatus         string `json:"-"`
	EventSubscriptionID string `json:"-"`
}

// Plan is a full plan row, used on the service's write paths where quota limits
// and the provider plan reference are needed. It is not returned to clients.
type Plan struct {
	ID          string
	Code        string
	Name        string
	PriceMicros int64
	Currency    string
	AuditQuota  int
	BulkQuota   int
	BulkEnabled bool
	Active      bool
}
