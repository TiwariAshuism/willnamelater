package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// harness bundles a Service with its fakes so a test can reach in and assert.
type harness struct {
	svc         *Service
	repo        *fakeRepo
	quota       *fakeQuota
	enqueuer    *fakeEnqueuer
	ingester    *fakeIngester
	scorer      *fakeScorer
	fraud       *fakeFraud
	reporter    *fakeReporter
	connectors  fakeConnectors
	connections *fakeConnections
	caller      uuid.UUID
}

func newHarness(conns []port.Connection, registered map[connector.Platform]connector.Connector) *harness {
	repo := newFakeRepo()
	quota := &fakeQuota{}
	enqueuer := &fakeEnqueuer{}
	ingester := &fakeIngester{}
	scorer := &fakeScorer{}
	fraud := &fakeFraud{}
	reporter := &fakeReporter{}
	connectors := fakeConnectors{byPlatform: registered}
	connections := &fakeConnections{conns: conns}
	caller := uuid.New()

	svc := New(repo, enqueuer, quota, ingester, scorer, fraud, reporter, connectors, connections, fakeCaller{id: caller}, nil)

	return &harness{
		svc:         svc,
		repo:        repo,
		quota:       quota,
		enqueuer:    enqueuer,
		ingester:    ingester,
		scorer:      scorer,
		fraud:       fraud,
		reporter:    reporter,
		connectors:  connectors,
		connections: connections,
		caller:      caller,
	}
}

func TestSubmitAudit_CreatesAndEnqueues(t *testing.T) {
	h := newHarness(nil, nil)
	req := model.SubmitAuditRequest{InfluencerID: uuid.New().String(), IdempotencyKey: "k1"}

	resp, err := h.svc.SubmitAudit(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitAudit: %v", err)
	}
	if resp.Status != string(model.StatusQueued) {
		t.Fatalf("status = %q, want queued", resp.Status)
	}
	if h.repo.createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", h.repo.createCalls)
	}
	if len(h.enqueuer.tasks) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(h.enqueuer.tasks))
	}
	if h.enqueuer.tasks[0].Type() != TaskAuditRun {
		t.Fatalf("task type = %q, want %q", h.enqueuer.tasks[0].Type(), TaskAuditRun)
	}
	if h.quota.reserveN != 1 || h.quota.commitN != 0 || h.quota.releaseN != 0 {
		t.Fatalf("quota reserve/commit/release = %d/%d/%d, want 1/0/0", h.quota.reserveN, h.quota.commitN, h.quota.releaseN)
	}
}

func TestSubmitAudit_IdempotentReplayDoesNotDoubleReserve(t *testing.T) {
	h := newHarness(nil, nil)
	req := model.SubmitAuditRequest{InfluencerID: uuid.New().String(), IdempotencyKey: "same-key"}

	first, err := h.svc.SubmitAudit(context.Background(), req)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	second, err := h.svc.SubmitAudit(context.Background(), req)
	if err != nil {
		t.Fatalf("second submit: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("replay returned a different job: %s vs %s", first.ID, second.ID)
	}
	// Exactly one job and one enqueue survive the replay.
	if len(h.enqueuer.tasks) != 1 {
		t.Fatalf("enqueued = %d, want 1 (no re-enqueue on replay)", len(h.enqueuer.tasks))
	}
	// Reserve was attempted twice, but the replay released its unit, so the net
	// consumption is one: the caller is never double-charged.
	if net := h.quota.reserveN - h.quota.releaseN; net != 1 {
		t.Fatalf("net reservations = %d, want 1", net)
	}
	if h.quota.commitN != 0 {
		t.Fatalf("commitN = %d, want 0 (commit happens in the worker)", h.quota.commitN)
	}
}

func TestSubmitAudit_QuotaExceededCreatesNoJob(t *testing.T) {
	h := newHarness(nil, nil)
	h.quota.reserveErr = errs.New(errs.KindQuotaExceeded, "billing.quota_exceeded", "plan quota exceeded")

	req := model.SubmitAuditRequest{InfluencerID: uuid.New().String(), IdempotencyKey: "k1"}
	_, err := h.svc.SubmitAudit(context.Background(), req)
	if errs.KindOf(err) != errs.KindQuotaExceeded {
		t.Fatalf("kind = %v, want KindQuotaExceeded", errs.KindOf(err))
	}
	if h.repo.createCalls != 0 {
		t.Fatalf("createCalls = %d, want 0", h.repo.createCalls)
	}
	if len(h.enqueuer.tasks) != 0 {
		t.Fatalf("enqueued = %d, want 0", len(h.enqueuer.tasks))
	}
}

func TestSubmitAudit_EnqueueFailureRollsBack(t *testing.T) {
	h := newHarness(nil, nil)
	h.enqueuer.err = errors.New("redis down")

	req := model.SubmitAuditRequest{InfluencerID: uuid.New().String(), IdempotencyKey: "k1"}
	_, err := h.svc.SubmitAudit(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error when enqueue fails")
	}
	// The created job is undone and the reserved unit is returned.
	if h.repo.deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want 1", h.repo.deleteCalls)
	}
	if h.quota.releaseN != 1 {
		t.Fatalf("releaseN = %d, want 1", h.quota.releaseN)
	}
}

func TestSubmitAudit_InvalidInfluencerID(t *testing.T) {
	h := newHarness(nil, nil)
	req := model.SubmitAuditRequest{InfluencerID: "not-a-uuid", IdempotencyKey: "k1"}

	_, err := h.svc.SubmitAudit(context.Background(), req)
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
	}
	if h.quota.reserveN != 0 {
		t.Fatalf("reserveN = %d, want 0 (validation precedes reserve)", h.quota.reserveN)
	}
}

func TestSubmitAudit_UnauthenticatedCaller(t *testing.T) {
	h := newHarness(nil, nil)
	h.svc.caller = fakeCaller{err: errs.New(errs.KindUnauthorized, "audit.unauthenticated", "no caller")}

	_, err := h.svc.SubmitAudit(context.Background(), model.SubmitAuditRequest{InfluencerID: uuid.New().String(), IdempotencyKey: "k1"})
	if errs.KindOf(err) != errs.KindUnauthorized {
		t.Fatalf("kind = %v, want KindUnauthorized", errs.KindOf(err))
	}
}

func TestGetAudit_OwnershipScoped(t *testing.T) {
	h := newHarness(nil, nil)
	other := uuid.New()
	job := model.Job{ID: uuid.New(), UserID: other, InfluencerID: uuid.New(), IdempotencyKey: "k", Status: model.StatusSucceeded}
	h.repo.seed(job)

	_, err := h.svc.GetAudit(context.Background(), job.ID.String())
	if errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("kind = %v, want KindNotFound for another user's job", errs.KindOf(err))
	}

	mine := model.Job{ID: uuid.New(), UserID: h.caller, InfluencerID: uuid.New(), IdempotencyKey: "k2", Status: model.StatusSucceeded}
	h.repo.seed(mine)
	resp, err := h.svc.GetAudit(context.Background(), mine.ID.String())
	if err != nil {
		t.Fatalf("GetAudit(mine): %v", err)
	}
	if resp.ID != mine.ID.String() {
		t.Fatalf("id = %q, want %q", resp.ID, mine.ID.String())
	}
}

func TestListAudits_OnlyCallers(t *testing.T) {
	h := newHarness(nil, nil)
	h.repo.seed(model.Job{ID: uuid.New(), UserID: h.caller, IdempotencyKey: "a", Status: model.StatusQueued})
	h.repo.seed(model.Job{ID: uuid.New(), UserID: uuid.New(), IdempotencyKey: "b", Status: model.StatusQueued})

	resp, err := h.svc.ListAudits(context.Background())
	if err != nil {
		t.Fatalf("ListAudits: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("len = %d, want 1 (only the caller's job)", len(resp))
	}
}
