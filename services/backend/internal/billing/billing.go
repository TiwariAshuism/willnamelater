// Package billing is the public entry point of the billing module: the one
// package outside internal/billing/internal that the composition root imports.
// It wires the repository, payment provider, service, and handler together and
// exposes route registration, the quota API other modules gate work against, and
// the seam through which the composition root attaches the authenticated caller.
//
// Everything behind it lives under internal/billing/internal, which Go forbids
// any sibling module from importing, so a collaborator can only reach billing
// through this surface.
package billing

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/appctx"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/provider"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the wired billing module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
	quota   service.QuotaService
}

// New builds the billing module over the shared connection pool and the Razorpay
// credentials. The single Repository value backs both the billing API and the
// quota API, and satisfies the plan-reader contract the subscribe path needs.
//
// The webhook-signing secret is a credential distinct from the API key secret:
// Razorpay generates it per webhook endpoint in the dashboard. The payment
// provider defaults to the live Razorpay base URL and an HTTP client with a
// bounded timeout.
func New(pool *db.Pool, rzp config.RazorpayConfig) (*Module, error) {
	repo := repository.New(pool)

	rp := provider.NewRazorpay(provider.RazorpayConfig{
		KeyID:         rzp.KeyID,
		KeySecret:     rzp.KeySecret.Reveal(),
		WebhookSecret: rzp.WebhookSecret.Reveal(),
	}, nil, nil)

	svc := service.NewBillingService(repo, repo, rp)

	return &Module{
		handler: handler.New(svc),
		quota:   service.NewQuotaService(repo, nil),
	}, nil
}

// RegisterRoutes mounts the billing endpoints. It takes two groups because the
// module's routes have two different authentication models, and collapsing them
// onto one group would silently get one of them wrong:
//
//   - protected carries the auth middleware. plans, subscription, and subscribe
//     all act on behalf of a signed-in caller.
//   - public carries no auth middleware. The webhook is called by Razorpay, which
//     holds no session; it authenticates every call by its HMAC signature
//     instead. Putting it behind the auth middleware would reject every genuine
//     webhook.
func (m *Module) RegisterRoutes(protected, public *gin.RouterGroup) {
	m.handler.RegisterProtectedRoutes(protected)
	m.handler.RegisterPublicRoutes(public)
}

// Quota returns the metered quota service. The audit module gates each audit
// against it through a consumer-side port; the composition root supplies this
// implementation, so the audit module never imports billing.
func (m *Module) Quota() service.QuotaService {
	return m.quota
}

// WithCaller returns a copy of ctx carrying the authenticated caller id for the
// billing service and repository to read.
//
// It is the composition root's seam into the module. Billing keeps its context
// key private so no other package can forge an identity, and exposes only this
// typed setter. The auth middleware establishes who is calling on the protected
// billing routes; app translates that identity into the billing context with
// WithCaller so GetSubscription and Subscribe can resolve the caller without
// billing importing the auth module.
func WithCaller(ctx context.Context, userID uuid.UUID) context.Context {
	return appctx.WithUserID(ctx, userID)
}
