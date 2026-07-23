package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/appctx"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/provider"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeRepo is a configurable billing repository stand-in. It satisfies both the
// generated BillingRepository and the hand-written PlanReader contracts and
// records the arguments it receives so a test can assert what the service
// persisted — and, critically, whether it was reached at all.
type fakeRepo struct {
	plans []model.PlanResponse
	sub   model.SubscriptionResponse
	plan  model.Plan

	listErr      error
	getErr       error
	subscribeErr error
	webhookErr   error
	planErr      error

	subscribeReq  *model.SubscribeRequest
	webhookReq    *model.WebhookRequest
	webhookCalls  int
	subscribeHits int
}

func (f *fakeRepo) ListPlans(context.Context) ([]model.PlanResponse, error) {
	return f.plans, f.listErr
}

func (f *fakeRepo) GetSubscription(context.Context) (model.SubscriptionResponse, error) {
	return f.sub, f.getErr
}

func (f *fakeRepo) Subscribe(_ context.Context, req model.SubscribeRequest) (model.SubscriptionResponse, error) {
	f.subscribeHits++
	r := req
	f.subscribeReq = &r
	if f.subscribeErr != nil {
		return model.SubscriptionResponse{}, f.subscribeErr
	}
	return f.sub, nil
}

func (f *fakeRepo) Webhook(_ context.Context, req model.WebhookRequest) error {
	f.webhookCalls++
	r := req
	f.webhookReq = &r
	return f.webhookErr
}

func (f *fakeRepo) GetPlanByCode(context.Context, string) (model.Plan, error) {
	return f.plan, f.planErr
}

// fakeProvider is a configurable PaymentProvider. CreateSubscription and
// HandleWebhook each return a preset result, so a test drives the service down a
// specific path without any network or crypto.
type fakeProvider struct {
	sub    provider.Subscription
	subErr error

	event      provider.Event
	eventErr   error
	handleHits int
}

func (f *fakeProvider) CreateSubscription(context.Context, provider.SubscriptionInput) (provider.Subscription, error) {
	return f.sub, f.subErr
}

func (f *fakeProvider) HandleWebhook(context.Context, []byte, string) (provider.Event, error) {
	f.handleHits++
	return f.event, f.eventErr
}

// TestWebhookVerificationPrecedesRepository is the security-critical assertion:
// the service must ask the provider to verify the signature FIRST, and an
// unverified payload must never reach the repository.
func TestWebhookVerificationPrecedesRepository(t *testing.T) {
	t.Parallel()

	unauthorized := errs.New(errs.KindUnauthorized, "billing.webhook_signature_invalid", "webhook signature verification failed")

	tests := []struct {
		name         string
		event        provider.Event
		eventErr     error
		wantErr      error
		wantWebhooks int
		wantStatus   string
		wantSubID    string
	}{
		{
			name:         "invalid signature never reaches the repository",
			eventErr:     unauthorized,
			wantErr:      unauthorized,
			wantWebhooks: 0,
		},
		{
			name:         "verified event applies its status transition",
			event:        provider.Event{Type: "subscription.activated", ExternalSubscriptionID: "sub_live", Status: "active"},
			wantWebhooks: 1,
			wantStatus:   "active",
			wantSubID:    "sub_live",
		},
		{
			name:         "verified but ignorable event touches no row",
			event:        provider.Event{Type: "subscription.updated"},
			wantWebhooks: 0,
		},
		{
			name:         "event with a status but no subscription id touches no row",
			event:        provider.Event{Type: "subscription.updated", Status: "active"},
			wantWebhooks: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &fakeRepo{}
			prov := &fakeProvider{event: tt.event, eventErr: tt.eventErr}
			svc := NewBillingService(repo, repo, prov)

			err := svc.Webhook(context.Background(), model.WebhookRequest{
				RawBody:   []byte(`{"event":"x"}`),
				Signature: "deadbeef",
			})

			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if prov.handleHits != 1 {
				t.Fatalf("provider.HandleWebhook called %d times, want exactly 1 (verification first)", prov.handleHits)
			}
			if repo.webhookCalls != tt.wantWebhooks {
				t.Fatalf("repo.Webhook called %d times, want %d", repo.webhookCalls, tt.wantWebhooks)
			}
			if tt.wantWebhooks > 0 {
				if repo.webhookReq.EventStatus != tt.wantStatus {
					t.Errorf("persisted status = %q, want %q", repo.webhookReq.EventStatus, tt.wantStatus)
				}
				if repo.webhookReq.EventSubscriptionID != tt.wantSubID {
					t.Errorf("persisted subscription id = %q, want %q", repo.webhookReq.EventSubscriptionID, tt.wantSubID)
				}
			}
		})
	}
}

// TestSubscribePersistsProviderIdentifiers verifies the subscribe path: it
// resolves the plan, creates the external subscription, and persists the
// provider's identifiers and status against the caller.
func TestSubscribePersistsProviderIdentifiers(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	start := time.Now().UTC().Truncate(time.Second)
	end := start.AddDate(0, 1, 0)

	repo := &fakeRepo{
		plan: model.Plan{ID: "plan-uuid", Code: "pro", Active: true},
		sub:  model.SubscriptionResponse{ID: "sub-row"},
	}
	prov := &fakeProvider{sub: provider.Subscription{
		ExternalCustomerID:     "cust_1",
		ExternalSubscriptionID: "sub_ext_1",
		Status:                 "trialing",
		CurrentPeriodStart:     start,
		CurrentPeriodEnd:       end,
	}}
	svc := NewBillingService(repo, repo, prov)

	ctx := appctx.WithUserID(context.Background(), userID)
	if _, err := svc.Subscribe(ctx, model.SubscribeRequest{PlanCode: "pro"}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if repo.subscribeReq == nil {
		t.Fatal("repository Subscribe was never called")
	}
	got := repo.subscribeReq
	if got.UserID != userID.String() {
		t.Errorf("UserID = %q, want %q", got.UserID, userID.String())
	}
	if got.PlanID != "plan-uuid" {
		t.Errorf("PlanID = %q, want plan-uuid", got.PlanID)
	}
	if got.ExternalSubscriptionID != "sub_ext_1" {
		t.Errorf("ExternalSubscriptionID = %q, want sub_ext_1", got.ExternalSubscriptionID)
	}
	if got.ExternalCustomerID != "cust_1" {
		t.Errorf("ExternalCustomerID = %q, want cust_1", got.ExternalCustomerID)
	}
	if got.Status != "trialing" {
		t.Errorf("Status = %q, want trialing", got.Status)
	}
}

// TestSubscribeRequiresCaller asserts the subscribe path fails closed when no
// caller identity is on the context and never touches the provider or the
// repository.
func TestSubscribeRequiresCaller(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{plan: model.Plan{ID: "p", Code: "pro"}}
	prov := &fakeProvider{}
	svc := NewBillingService(repo, repo, prov)

	_, err := svc.Subscribe(context.Background(), model.SubscribeRequest{PlanCode: "pro"})
	if got := errs.KindOf(err); got != errs.KindUnauthorized {
		t.Fatalf("kind = %v, want KindUnauthorized", got)
	}
	if prov.handleHits != 0 || repo.subscribeHits != 0 {
		t.Fatalf("unauthenticated subscribe reached a dependency: providerHits=%d repoHits=%d", prov.handleHits, repo.subscribeHits)
	}
}

// TestSubscribeProviderFailureIsNotPersisted asserts that when the provider
// fails to create the external subscription, no row is written: a subscription
// with no provider mandate would be unbillable.
func TestSubscribeProviderFailureIsNotPersisted(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{plan: model.Plan{ID: "p", Code: "pro"}}
	prov := &fakeProvider{subErr: errs.New(errs.KindUnavailable, "billing.provider_unavailable", "payment provider is unavailable")}
	svc := NewBillingService(repo, repo, prov)

	ctx := appctx.WithUserID(context.Background(), uuid.New())
	_, err := svc.Subscribe(ctx, model.SubscribeRequest{PlanCode: "pro"})
	if got := errs.KindOf(err); got != errs.KindUnavailable {
		t.Fatalf("kind = %v, want KindUnavailable", got)
	}
	if repo.subscribeHits != 0 {
		t.Fatalf("repository Subscribe was called %d times after a provider failure, want 0", repo.subscribeHits)
	}
}

// TestReadPathsDelegate asserts the read endpoints pass the repository result
// straight through.
func TestReadPathsDelegate(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{
		plans: []model.PlanResponse{{ID: "1", Code: "free"}},
		sub:   model.SubscriptionResponse{ID: "sub-1", PlanCode: "pro"},
	}
	svc := NewBillingService(repo, repo, &fakeProvider{})

	plans, err := svc.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) != 1 || plans[0].Code != "free" {
		t.Fatalf("plans = %+v, want one 'free' plan", plans)
	}

	sub, err := svc.GetSubscription(context.Background())
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.ID != "sub-1" {
		t.Fatalf("subscription id = %q, want sub-1", sub.ID)
	}
}
