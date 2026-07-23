// Package api is the apigen source for the billing module. The BillingAPI
// interface is the single declaration of the module's HTTP surface; apigen
// consumes it to generate the service and repository interfaces the
// hand-written implementations satisfy.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
)

// BillingAPI declares the billing module's HTTP endpoints. Every method takes
// context.Context first; the authenticated caller's identity travels on that
// context, so it never appears as a parameter.
type BillingAPI interface {
	// GET /billing/plans
	ListPlans(ctx context.Context) ([]model.PlanResponse, error)

	// GET /billing/subscription
	GetSubscription(ctx context.Context) (model.SubscriptionResponse, error)

	// POST /billing/subscribe
	Subscribe(ctx context.Context, req model.SubscribeRequest) (model.SubscriptionResponse, error)

	// POST /billing/webhook
	Webhook(ctx context.Context, req model.WebhookRequest) error
}
