package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/getnyx/influaudit/backend/internal/admin/internal/model"
	"github.com/getnyx/influaudit/backend/internal/admin/port"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// --- fakes ---------------------------------------------------------------

type fakeRepo struct {
	created                                    model.CreateDisputeParams
	resolved                                   model.ResolveDisputeParams
	open                                       []model.Dispute
	decided                                    []model.Dispute
	dispute                                    model.Dispute
	createErr, listErr, resolveErr, decidedErr error
}

func (r *fakeRepo) CreateDispute(_ context.Context, p model.CreateDisputeParams) (model.Dispute, error) {
	r.created = p
	if r.createErr != nil {
		return model.Dispute{}, r.createErr
	}
	return model.Dispute{ID: uuid.New(), AuditJobID: p.AuditJobID, RaisedBy: p.RaisedBy, Reason: p.Reason, Status: model.StatusOpen}, nil
}

func (r *fakeRepo) ListOpenDisputes(context.Context) ([]model.Dispute, error) {
	return r.open, r.listErr
}

func (r *fakeRepo) ListDecidedDisputes(context.Context) ([]model.Dispute, error) {
	return r.decided, r.decidedErr
}

func (r *fakeRepo) ResolveDispute(_ context.Context, p model.ResolveDisputeParams) (model.Dispute, error) {
	r.resolved = p
	if r.resolveErr != nil {
		return model.Dispute{}, r.resolveErr
	}
	d := r.dispute
	d.ID = p.ID
	d.Status = p.Status
	d.Resolution = p.Resolution
	d.ResolvedBy = p.ResolvedBy
	return d, nil
}

type fakeCaller struct {
	id  uuid.UUID
	err error
}

func (c fakeCaller) CallerID(context.Context) (uuid.UUID, error) { return c.id, c.err }

type fakeGuard struct {
	id  uuid.UUID
	err error
}

func (g fakeGuard) RequireAdmin(context.Context) (uuid.UUID, error) { return g.id, g.err }

type fakeFraud struct {
	view  port.FraudView
	found bool
	err   error
}

func (f fakeFraud) FraudResultOf(context.Context, uuid.UUID) (port.FraudView, bool, error) {
	return f.view, f.found, f.err
}

type fakeCost struct {
	summary port.CostSummary
	err     error
}

func (c fakeCost) CostSummary(context.Context) (port.CostSummary, error) { return c.summary, c.err }

type fakeQueues struct {
	names   []string
	infos   map[string]*asynq.QueueInfo
	listErr error
	infoErr error
}

func (q fakeQueues) Queues() ([]string, error) { return q.names, q.listErr }

func (q fakeQueues) GetQueueInfo(name string) (*asynq.QueueInfo, error) {
	if q.infoErr != nil {
		return nil, q.infoErr
	}
	return q.infos[name], nil
}

// admin is a fixed admin id every guard-passing test shares.
func adminID() uuid.UUID { return uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa") }
func userID() uuid.UUID  { return uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb") }
func jobID() uuid.UUID   { return uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc") }

func newService(repo Repository, caller port.CallerID, guard port.AdminGuard, fraud port.FraudReader, cost port.CostReader, queues port.QueueInspector) *Service {
	return New(repo, caller, guard, fraud, cost, queues)
}

// --- FileDispute ---------------------------------------------------------

func TestFileDisputeUsesCallerAsRaisedBy(t *testing.T) {
	repo := &fakeRepo{}
	svc := newService(repo, fakeCaller{id: userID()}, fakeGuard{}, fakeFraud{}, fakeCost{}, fakeQueues{})

	resp, err := svc.FileDispute(context.Background(), jobID().String(), model.FileDisputeRequest{Reason: "engagement looks organic"})
	if err != nil {
		t.Fatalf("FileDispute: %v", err)
	}
	if repo.created.RaisedBy != userID() || repo.created.AuditJobID != jobID() || repo.created.Reason == "" {
		t.Fatalf("dispute not filed with the caller/audit/reason: %+v", repo.created)
	}
	if resp.Status != string(model.StatusOpen) || resp.AuditJobID != jobID().String() {
		t.Fatalf("response not mapped: %+v", resp)
	}
}

func TestFileDisputePropagatesCallerError(t *testing.T) {
	svc := newService(&fakeRepo{}, fakeCaller{err: errs.New(errs.KindUnauthorized, "x", "y")}, fakeGuard{}, fakeFraud{}, fakeCost{}, fakeQueues{})
	if _, err := svc.FileDispute(context.Background(), jobID().String(), model.FileDisputeRequest{Reason: "r"}); errs.KindOf(err) != errs.KindUnauthorized {
		t.Fatalf("want unauthorized from caller, got %v", err)
	}
}

func TestFileDisputeRejectsBadAuditID(t *testing.T) {
	svc := newService(&fakeRepo{}, fakeCaller{id: userID()}, fakeGuard{}, fakeFraud{}, fakeCost{}, fakeQueues{})
	if _, err := svc.FileDispute(context.Background(), "not-a-uuid", model.FileDisputeRequest{Reason: "r"}); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for a bad audit id, got %v", err)
	}
}

// --- admin gating --------------------------------------------------------

func TestAdminOnlyRoutesRequireAdmin(t *testing.T) {
	forbidden := errs.New(errs.KindForbidden, "admin.forbidden", "not an admin")
	svc := newService(&fakeRepo{}, fakeCaller{id: userID()}, fakeGuard{err: forbidden}, fakeFraud{}, fakeCost{}, fakeQueues{})
	ctx := context.Background()

	if _, err := svc.ListDisputeQueue(ctx); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("ListDisputeQueue not guarded: %v", err)
	}
	if _, err := svc.ResolveDispute(ctx, jobID().String(), model.ResolveDisputeRequest{Decision: "upheld"}); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("ResolveDispute not guarded: %v", err)
	}
	if _, err := svc.CostDashboard(ctx); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("CostDashboard not guarded: %v", err)
	}
	if _, err := svc.QueueMonitor(ctx); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("QueueMonitor not guarded: %v", err)
	}
	if _, err := svc.ExportLabels(ctx); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("ExportLabels not guarded: %v", err)
	}
}

// --- ResolveDispute ------------------------------------------------------

func TestResolveDisputeMapsDecisionToStatus(t *testing.T) {
	repo := &fakeRepo{}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{}, fakeCost{}, fakeQueues{})

	if _, err := svc.ResolveDispute(context.Background(), jobID().String(), model.ResolveDisputeRequest{Decision: "rejected", Notes: "clear coordination"}); err != nil {
		t.Fatalf("ResolveDispute: %v", err)
	}
	if repo.resolved.Status != model.StatusRejected {
		t.Fatalf("rejected decision must map to StatusRejected, got %q", repo.resolved.Status)
	}
	if repo.resolved.ResolvedBy != adminID() || repo.resolved.Resolution != "clear coordination" {
		t.Fatalf("resolver/notes not recorded: %+v", repo.resolved)
	}
}

func TestResolveDisputeRejectsUnknownDecision(t *testing.T) {
	svc := newService(&fakeRepo{}, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{}, fakeCost{}, fakeQueues{})
	if _, err := svc.ResolveDispute(context.Background(), jobID().String(), model.ResolveDisputeRequest{Decision: "maybe"}); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for an unrecognised decision, got %v", err)
	}
}

// --- CostDashboard -------------------------------------------------------

func TestCostDashboardComputesUSDAndHitRate(t *testing.T) {
	cost := fakeCost{summary: port.CostSummary{
		TotalGenerations: 4, TotalInputTokens: 100, TotalOutputTokens: 40,
		TotalCostMicros: 2_500_000, CachedGenerations: 1,
		ByModel: []port.ModelCost{{Model: "claude-opus-4-8", Generations: 4, CostMicros: 2_500_000, CachedGenerations: 1}},
	}}
	svc := newService(&fakeRepo{}, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{}, cost, fakeQueues{})

	resp, err := svc.CostDashboard(context.Background())
	if err != nil {
		t.Fatalf("CostDashboard: %v", err)
	}
	if resp.TotalCostUSD != 2.5 {
		t.Errorf("USD = %v, want 2.5 (2_500_000 micros)", resp.TotalCostUSD)
	}
	if resp.CacheHitRate != 0.25 {
		t.Errorf("hit rate = %v, want 0.25 (1/4)", resp.CacheHitRate)
	}
	if len(resp.ByModel) != 1 || resp.ByModel[0].CostUSD != 2.5 {
		t.Errorf("per-model cost not mapped: %+v", resp.ByModel)
	}
}

// --- QueueMonitor --------------------------------------------------------

func TestQueueMonitorProjectsQueues(t *testing.T) {
	queues := fakeQueues{
		names: []string{"default"},
		infos: map[string]*asynq.QueueInfo{
			"default": {Queue: "default", Size: 3, Pending: 2, Active: 1, Failed: 0, Latency: 1500 * time.Millisecond},
		},
	}
	svc := newService(&fakeRepo{}, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{}, fakeCost{}, queues)

	resp, err := svc.QueueMonitor(context.Background())
	if err != nil {
		t.Fatalf("QueueMonitor: %v", err)
	}
	if len(resp.Queues) != 1 || resp.Queues[0].Queue != "default" || resp.Queues[0].Size != 3 || resp.Queues[0].LatencyMs != 1500 {
		t.Fatalf("queue not projected: %+v", resp.Queues)
	}
}

func TestQueueMonitorMapsBackendOutageToUnavailable(t *testing.T) {
	svc := newService(&fakeRepo{}, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{}, fakeCost{}, fakeQueues{listErr: errors.New("redis down")})
	if _, err := svc.QueueMonitor(context.Background()); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("want unavailable when the queue backend is unreachable, got %v", err)
	}
}

// --- ExportLabels --------------------------------------------------------

func TestExportLabelsLabelsAndAttachesFeatures(t *testing.T) {
	resolvedAt := time.Unix(1_700_000_000, 0).UTC()
	repo := &fakeRepo{decided: []model.Dispute{
		{ID: uuid.New(), AuditJobID: jobID(), Status: model.StatusRejected, ResolvedAt: &resolvedAt},
	}}
	fraud := fakeFraud{found: true, view: port.FraudView{Present: true, CliqueCount: 9, ModelVersion: "clique-v1"}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fraud, fakeCost{}, fakeQueues{})

	resp, err := svc.ExportLabels(context.Background())
	if err != nil {
		t.Fatalf("ExportLabels: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("count = %d, want 1", resp.Count)
	}
	l := resp.Labels[0]
	if !l.Label {
		t.Error("a rejected dispute must label the account fraudulent (true)")
	}
	if !l.HasFeatures || l.Features.CliqueCount != 9 || l.Features.ModelVersion != "clique-v1" {
		t.Errorf("stored fraud estimate not attached as features: %+v", l.Features)
	}
}

// A decided dispute whose audit produced no fraud estimate is exported with a
// label and HasFeatures=false — never a fabricated all-zero feature vector.
func TestExportLabelsOmitsFeaturesWhenNoFraudRow(t *testing.T) {
	repo := &fakeRepo{decided: []model.Dispute{
		{ID: uuid.New(), AuditJobID: jobID(), Status: model.StatusResolved},
	}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{found: false}, fakeCost{}, fakeQueues{})

	resp, err := svc.ExportLabels(context.Background())
	if err != nil {
		t.Fatalf("ExportLabels: %v", err)
	}
	l := resp.Labels[0]
	if l.Label {
		t.Error("an upheld (resolved) dispute labels the account legitimate (false)")
	}
	if l.HasFeatures {
		t.Error("no fraud row means no features, not a zero vector")
	}
}

func TestExportLabelsPropagatesFraudReadError(t *testing.T) {
	repo := &fakeRepo{decided: []model.Dispute{{ID: uuid.New(), AuditJobID: jobID(), Status: model.StatusRejected}}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{err: errs.New(errs.KindUnavailable, "x", "y")}, fakeCost{}, fakeQueues{})
	if _, err := svc.ExportLabels(context.Background()); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("want the fraud-read error surfaced, got %v", err)
	}
}
