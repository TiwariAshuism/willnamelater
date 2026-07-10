// Package provider abstracts the external payment gateway behind an interface
// so the billing service depends on a contract rather than on Razorpay, and so
// tests exercise the flow with a fake instead of the network.
package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// SubscriptionInput is what the service asks the provider to create.
type SubscriptionInput struct {
	// UserID is the local user the subscription belongs to. It is passed to the
	// provider as external notes so a webhook can be correlated back if needed.
	UserID uuid.UUID
	// PlanExternalID is the provider's identifier for the plan. The billing
	// schema stores it in plan.code.
	PlanExternalID string
	// TotalCount is the number of billing cycles to authorize.
	TotalCount int
	// CustomerNotify asks the provider to email the customer about the mandate.
	CustomerNotify bool
}

// Subscription is the provider's view of a created subscription, normalized to
// the billing module's vocabulary.
type Subscription struct {
	ExternalCustomerID     string
	ExternalSubscriptionID string
	// Status is mapped onto the subscription.status vocabulary
	// (trialing|active|past_due|canceled|expired).
	Status             string
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
}

// Event is a verified, parsed webhook. A zero ExternalSubscriptionID or Status
// means the event is not one the billing module acts on and can be acknowledged
// without effect.
type Event struct {
	Type                   string
	ExternalSubscriptionID string
	// Status is the subscription status the event implies, mapped onto the
	// subscription.status vocabulary, or empty for events with no status effect.
	Status string
}

// PaymentProvider is the payment gateway contract the billing service depends
// on. HandleWebhook MUST reject a payload whose signature does not verify and
// MUST NOT return a usable Event in that case.
type PaymentProvider interface {
	CreateSubscription(ctx context.Context, in SubscriptionInput) (Subscription, error)
	HandleWebhook(ctx context.Context, raw []byte, signature string) (Event, error)
}

// httpDoer is the subset of *http.Client the Razorpay client needs. Tests
// supply a fake so no request leaves the process.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// compile-time assertion that the standard client satisfies httpDoer.
var _ httpDoer = (*http.Client)(nil)
