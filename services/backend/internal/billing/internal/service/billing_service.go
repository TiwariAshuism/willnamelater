package service

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/appctx"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/provider"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/repository"
)

// defaultBillingCycles is the number of billing cycles a new subscription
// authorizes at the payment provider. Razorpay requires total_count when a
// subscription is created; twelve is the mandate horizon a monthly plan
// re-authorizes on renewal.
const defaultBillingCycles = 12

// notifyCustomer asks the provider to email the customer about the payment
// mandate when the subscription is created.
const notifyCustomer = true

// billingService implements BillingService over the billing repository and the
// payment provider. The plan/subscription reads and writes go to the
// repository; the subscribe path additionally resolves a plan code to a full
// row and creates the external subscription at the provider; the webhook path
// verifies the payload with the provider before any database write.
type billingService struct {
	repo  repository.BillingRepository
	plans repository.PlanReader
	rzp   provider.PaymentProvider
}

var _ BillingService = (*billingService)(nil)

// NewBillingService builds the billing service. repo backs the four API
// operations; plans resolves the plan code supplied on subscribe to the full
// plan row (its id and provider reference); rzp creates external subscriptions
// and verifies inbound webhooks.
func NewBillingService(repo repository.BillingRepository, plans repository.PlanReader, rzp provider.PaymentProvider) BillingService {
	return &billingService{repo: repo, plans: plans, rzp: rzp}
}

// ListPlans returns the active plans. It delegates to the repository.
func (s *billingService) ListPlans(ctx context.Context) ([]model.PlanResponse, error) {
	return s.repo.ListPlans(ctx)
}

// GetSubscription returns the caller's live subscription. The caller identity
// travels on ctx; the repository reads it.
func (s *billingService) GetSubscription(ctx context.Context) (model.SubscriptionResponse, error) {
	return s.repo.GetSubscription(ctx)
}

// Subscribe creates a subscription for the caller. It resolves the requested
// plan, asks the payment provider to create the external subscription, and
// persists the row with the provider's identifiers and status. The external
// subscription id is what a later webhook is correlated against, so it must be
// stored: without it the subscription could never transition on a provider
// event.
func (s *billingService) Subscribe(ctx context.Context, req model.SubscribeRequest) (model.SubscriptionResponse, error) {
	userID, err := appctx.UserID(ctx)
	if err != nil {
		return model.SubscriptionResponse{}, err
	}

	plan, err := s.plans.GetPlanByCode(ctx, req.PlanCode)
	if err != nil {
		return model.SubscriptionResponse{}, err
	}

	sub, err := s.rzp.CreateSubscription(ctx, provider.SubscriptionInput{
		UserID:         userID,
		PlanExternalID: plan.Code,
		TotalCount:     defaultBillingCycles,
		CustomerNotify: notifyCustomer,
	})
	if err != nil {
		return model.SubscriptionResponse{}, err
	}

	req.UserID = userID.String()
	req.PlanID = plan.ID
	req.Status = sub.Status
	req.CurrentPeriodStart = sub.CurrentPeriodStart
	req.CurrentPeriodEnd = sub.CurrentPeriodEnd
	req.ExternalCustomerID = sub.ExternalCustomerID
	req.ExternalSubscriptionID = sub.ExternalSubscriptionID

	return s.repo.Subscribe(ctx, req)
}

// Webhook applies a verified provider event. It verifies the signature with the
// provider FIRST and returns that error unchanged on failure — it is already a
// KindUnauthorized (forged) or KindInvalid (malformed) domain error — so an
// unverified payload never reaches the database. An event the module does not
// act on (no subscription reference or no status effect) is acknowledged
// without a write, keeping delivery idempotent for events we ignore.
func (s *billingService) Webhook(ctx context.Context, req model.WebhookRequest) error {
	event, err := s.rzp.HandleWebhook(ctx, req.RawBody, req.Signature)
	if err != nil {
		return err
	}

	if event.ExternalSubscriptionID == "" || event.Status == "" {
		return nil
	}

	req.EventStatus = event.Status
	req.EventSubscriptionID = event.ExternalSubscriptionID
	return s.repo.Webhook(ctx, req)
}
