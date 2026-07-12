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
	// byIDErr / markErr fail the review read and the score-disclosure write.
	byIDErr, markErr error
	// scoreShown counts the calls to MarkScoreShown: disclosing the heuristic's
	// score to an adjudicator must be a recorded act, so the tests assert on
	// whether the write happened at all, not just on what came back.
	scoreShown int
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

func (r *fakeRepo) DisputeByID(_ context.Context, id uuid.UUID) (model.Dispute, error) {
	if r.byIDErr != nil {
		return model.Dispute{}, r.byIDErr
	}
	d := r.dispute
	d.ID = id
	return d, nil
}

func (r *fakeRepo) MarkScoreShown(_ context.Context, id uuid.UUID) (model.Dispute, error) {
	r.scoreShown++
	if r.markErr != nil {
		return model.Dispute{}, r.markErr
	}
	d := r.dispute
	d.ID = id
	d.ScoreShownToAdmin = true
	return d, nil
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
	d.LabelEvidence = p.LabelEvidence
	return d, nil
}

// fakeLabels is the ml training-label sink. It records what the dispute loop
// actually handed mlops — the bool AND the evidence, since the bool alone is an
// echo of the heuristic and mlops cannot gate a fold on it.
type fakeLabels struct {
	called     int
	fraudulent bool
	evidence   model.LabelEvidence
	err        error
}

func (l *fakeLabels) RecordDisputeLabel(_ context.Context, _ uuid.UUID, fraudulent bool, evidence model.LabelEvidence) error {
	l.called++
	l.fraudulent = fraudulent
	l.evidence = evidence
	return l.err
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

// f64p / intp are observed fraud measurements. Every fraud figure is a pointer:
// nil means the signal was never observed, and an exported training label must
// carry that absence as NULL rather than a fabricated zero.
func f64p(v float64) *float64 { return &v }
func intp(v int) *int         { return &v }

// admin is a fixed admin id every guard-passing test shares.
func adminID() uuid.UUID { return uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa") }
func userID() uuid.UUID  { return uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb") }
func jobID() uuid.UUID   { return uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc") }

func newService(repo Repository, caller port.CallerID, guard port.AdminGuard, fraud port.FraudReader, cost port.CostReader, queues port.QueueInspector) *Service {
	return New(repo, caller, guard, fraud, cost, queues, nil)
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
	if _, err := svc.ReviewDispute(ctx, jobID().String()); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("ReviewDispute not guarded: %v", err)
	}
	if _, err := svc.RevealHeuristicScore(ctx, jobID().String()); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("RevealHeuristicScore not guarded: %v", err)
	}
	if _, err := svc.ResolveDispute(ctx, jobID().String(), model.ResolveDisputeRequest{Decision: "upheld", LabelEvidence: string(model.EvidenceCreatorAdmission)}); errs.KindOf(err) != errs.KindForbidden {
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

	req := model.ResolveDisputeRequest{
		Decision:      "rejected",
		Notes:         "clear coordination",
		LabelEvidence: string(model.EvidencePurchaseReceipt),
	}
	if _, err := svc.ResolveDispute(context.Background(), jobID().String(), req); err != nil {
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
	req := model.ResolveDisputeRequest{Decision: "maybe", LabelEvidence: string(model.EvidenceCreatorAdmission)}
	if _, err := svc.ResolveDispute(context.Background(), jobID().String(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for an unrecognised decision, got %v", err)
	}
}

// --- ResolveDispute: the evidence is not optional ------------------------
//
// The decision alone is not a label. A dispute exists only because the heuristic
// flagged the account, so "rejected" means no more than "an admin declined to
// overturn the flag". The one thing that can make it a label is a statement of
// what the adjudicator OBSERVED outside the heuristic's own output — so a resolve
// that states nothing, or states something the closed set does not recognise, is
// refused outright rather than persisted with a NULL the export would have to
// guess about later.
func TestResolveDisputeRequiresRecognisedEvidence(t *testing.T) {
	tests := []struct {
		name     string
		evidence string
		want     errs.Kind
	}{
		{"omitted entirely: silence is not an observation", "", errs.KindInvalid},
		{"free text is not an evidence kind", "admin eyeballed it and agreed", errs.KindInvalid},
		{"a kind outside the closed set", "vibes", errs.KindInvalid},
		{"an mlops-side value we do not mirror", "model_said_so", errs.KindInvalid},
		{"platform enforcement: an external authority observed it", string(model.EvidencePlatformEnforcement), 0},
		{"creator admission", string(model.EvidenceCreatorAdmission), 0},
		{"purchase receipt", string(model.EvidencePurchaseReceipt), 0},
		{"brand conversion data", string(model.EvidenceBrandConversionData), 0},
		{"manual follower sample audit", string(model.EvidenceManualFollowerAudit), 0},
		{"heuristic-only: honest, accepted, and kept out of the export", string(model.EvidenceHeuristicOnly), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{}, fakeCost{}, fakeQueues{})
			req := model.ResolveDisputeRequest{Decision: "rejected", LabelEvidence: tt.evidence}

			resp, err := svc.ResolveDispute(context.Background(), jobID().String(), req)
			if tt.want != 0 {
				if errs.KindOf(err) != tt.want {
					t.Fatalf("want kind %v for evidence %q, got %v", tt.want, tt.evidence, err)
				}
				if repo.resolved.ID != uuid.Nil {
					t.Fatal("a resolve with no stated observation must never reach the repository")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveDispute: %v", err)
			}
			if got := repo.resolved.LabelEvidence; string(got) != tt.evidence {
				t.Fatalf("persisted label_evidence = %q, want %q", got, tt.evidence)
			}
			if resp.LabelEvidence != tt.evidence {
				t.Fatalf("response label_evidence = %q, want %q", resp.LabelEvidence, tt.evidence)
			}
		})
	}
}

// The bool that reaches mlops is worthless on its own — it says a human agreed
// with the heuristic. The evidence has to travel WITH it, because mlops, not this
// module, decides what may enter a training fold and it cannot make that call
// from the bool.
func TestResolveDisputeSendsEvidenceToTheTrainingSink(t *testing.T) {
	repo := &fakeRepo{}
	labels := &fakeLabels{}
	svc := New(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{}, fakeCost{}, fakeQueues{}, labels)

	req := model.ResolveDisputeRequest{Decision: "rejected", LabelEvidence: string(model.EvidenceManualFollowerAudit)}
	if _, err := svc.ResolveDispute(context.Background(), jobID().String(), req); err != nil {
		t.Fatalf("ResolveDispute: %v", err)
	}
	if labels.called != 1 || !labels.fraudulent {
		t.Fatalf("sink not called with the fraud label: called=%d fraudulent=%v", labels.called, labels.fraudulent)
	}
	if labels.evidence != model.EvidenceManualFollowerAudit {
		t.Fatalf("sink evidence = %q, want the manual follower audit that was actually observed", labels.evidence)
	}
}

// --- Evidence-blind adjudication -----------------------------------------
//
// An adjudicator who is shown the heuristic's own risk score and then asked
// whether the heuristic was right is not producing a label, they are ratifying
// one. So the review read hides the score by default, and the only way to see it
// is an act the row remembers.
func TestReviewDisputeHidesTheHeuristicScore(t *testing.T) {
	repo := &fakeRepo{dispute: model.Dispute{AuditJobID: jobID(), Status: model.StatusOpen}}
	fraud := fakeFraud{found: true, view: port.FraudView{Present: true, RiskScore: f64p(88), ModelVersion: "risk-v2"}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fraud, fakeCost{}, fakeQueues{})

	resp, err := svc.ReviewDispute(context.Background(), jobID().String())
	if err != nil {
		t.Fatalf("ReviewDispute: %v", err)
	}
	if resp.HeuristicScore != nil {
		t.Fatalf("the review read leaked the heuristic score without a reveal: %+v", resp.HeuristicScore)
	}
	if resp.Dispute.ScoreShownToAdmin {
		t.Error("a blind read must not claim the score was shown")
	}
	if repo.scoreShown != 0 {
		t.Error("a read must not record a disclosure that did not happen")
	}
}

func TestRevealHeuristicScoreDisclosesAndRecordsIt(t *testing.T) {
	repo := &fakeRepo{dispute: model.Dispute{AuditJobID: jobID(), Status: model.StatusOpen}}
	fraud := fakeFraud{found: true, view: port.FraudView{Present: true, RiskScore: f64p(88), CliqueCount: intp(4), ModelVersion: "risk-v2"}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fraud, fakeCost{}, fakeQueues{})

	resp, err := svc.RevealHeuristicScore(context.Background(), jobID().String())
	if err != nil {
		t.Fatalf("RevealHeuristicScore: %v", err)
	}
	if resp.HeuristicScore == nil || resp.HeuristicScore.RiskScore == nil || *resp.HeuristicScore.RiskScore != 88 {
		t.Fatalf("the reveal must actually disclose the score: %+v", resp.HeuristicScore)
	}
	if repo.scoreShown != 1 {
		t.Fatalf("the disclosure must be recorded server-side exactly once, got %d writes", repo.scoreShown)
	}
	if !resp.Dispute.ScoreShownToAdmin {
		t.Error("the response must show the disclosure that was just stamped on the row")
	}
}

// Once the score has been disclosed for a dispute, the read carries it: the row
// already records the contamination, and hiding the number afterwards would hide
// only the evidence of it, not the fact.
func TestReviewDisputeShowsTheScoreOnceRevealed(t *testing.T) {
	repo := &fakeRepo{dispute: model.Dispute{AuditJobID: jobID(), Status: model.StatusOpen, ScoreShownToAdmin: true}}
	fraud := fakeFraud{found: true, view: port.FraudView{Present: true, RiskScore: f64p(88)}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fraud, fakeCost{}, fakeQueues{})

	resp, err := svc.ReviewDispute(context.Background(), jobID().String())
	if err != nil {
		t.Fatalf("ReviewDispute: %v", err)
	}
	if resp.HeuristicScore == nil || !resp.Dispute.ScoreShownToAdmin {
		t.Fatalf("an already-revealed dispute must read back with its score: %+v", resp)
	}
}

// An audit that produced no fraud estimate has nothing to disclose. Stamping the
// row anyway would record a disclosure that never happened — a fabricated fact
// about how the decision was reached.
func TestRevealHeuristicScoreRecordsNothingWhenThereIsNoScore(t *testing.T) {
	repo := &fakeRepo{dispute: model.Dispute{AuditJobID: jobID(), Status: model.StatusOpen}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{found: false}, fakeCost{}, fakeQueues{})

	resp, err := svc.RevealHeuristicScore(context.Background(), jobID().String())
	if err != nil {
		t.Fatalf("RevealHeuristicScore: %v", err)
	}
	if resp.HeuristicScore != nil {
		t.Fatalf("no fraud row means no score, not a zero one: %+v", resp.HeuristicScore)
	}
	if repo.scoreShown != 0 {
		t.Error("nothing was disclosed, so nothing may be recorded as disclosed")
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
		{ID: uuid.New(), AuditJobID: jobID(), Status: model.StatusRejected, ResolvedAt: &resolvedAt,
			LabelEvidence: model.EvidencePlatformEnforcement},
	}}
	// EXPECTATION CHANGED: the fraud view carries RiskScore (the honest composite)
	// instead of FakeFollowerRate/BotCommentRate, and its measurements are pointers.
	fraud := fakeFraud{found: true, view: port.FraudView{Present: true, RiskScore: f64p(63.5),
		CliqueCount: intp(9), ModelVersion: "clique-v1"}}
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
	if l.LabelEvidence != string(model.EvidencePlatformEnforcement) {
		t.Errorf("label_evidence = %q, want the platform enforcement action the trainer filters folds on", l.LabelEvidence)
	}
	if !l.HasFeatures || l.Features.ModelVersion != "clique-v1" {
		t.Errorf("stored fraud estimate not attached as features: %+v", l.Features)
	}
	if l.Features.CliqueCount == nil || *l.Features.CliqueCount != 9 {
		t.Errorf("clique count not attached: %v", l.Features.CliqueCount)
	}
	if l.Features.RiskScore == nil || *l.Features.RiskScore != 63.5 {
		t.Errorf("risk score not attached: %v", l.Features.RiskScore)
	}
}

// The exported label is training input. A signal the audit never observed must
// leave the export as null, not 0: a zero-filled feature teaches the fraud model
// that "we didn't look" is a confident measurement, and these rows are the
// supervised set the champion is fit on.
func TestExportLabelsCarriesUnobservedFeaturesAsNull(t *testing.T) {
	repo := &fakeRepo{decided: []model.Dispute{
		{ID: uuid.New(), AuditJobID: jobID(), Status: model.StatusRejected,
			LabelEvidence: model.EvidenceCreatorAdmission},
	}}
	// A fraud pass that produced a risk score but analyzed no commenters.
	fraud := fakeFraud{found: true, view: port.FraudView{Present: true, RiskScore: f64p(41),
		Confidence: 0.25, ModelVersion: "risk-v2"}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fraud, fakeCost{}, fakeQueues{})

	resp, err := svc.ExportLabels(context.Background())
	if err != nil {
		t.Fatalf("ExportLabels: %v", err)
	}
	f := resp.Labels[0].Features
	if f.CliqueCount != nil {
		t.Errorf("clique_count = %d, want null: no commenter was ever analyzed", *f.CliqueCount)
	}
	if f.CliqueMembershipFraction != nil {
		t.Errorf("clique_membership_fraction = %v, want null", *f.CliqueMembershipFraction)
	}
	if f.EngagementAnomaly != nil {
		t.Errorf("engagement_anomaly = %v, want null: no benchmark was supplied", *f.EngagementAnomaly)
	}
	if f.RiskScore == nil || *f.RiskScore != 41 {
		t.Errorf("risk_score = %v, want the observed 41 carried through", f.RiskScore)
	}
}

// A decided dispute whose audit produced no fraud estimate is exported with a
// label and HasFeatures=false — never a fabricated all-zero feature vector.
func TestExportLabelsOmitsFeaturesWhenNoFraudRow(t *testing.T) {
	repo := &fakeRepo{decided: []model.Dispute{
		{ID: uuid.New(), AuditJobID: jobID(), Status: model.StatusResolved,
			LabelEvidence: model.EvidenceManualFollowerAudit},
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
	repo := &fakeRepo{decided: []model.Dispute{
		{ID: uuid.New(), AuditJobID: jobID(), Status: model.StatusRejected,
			LabelEvidence: model.EvidencePurchaseReceipt},
	}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{err: errs.New(errs.KindUnavailable, "x", "y")}, fakeCost{}, fakeQueues{})
	if _, err := svc.ExportLabels(context.Background()); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("want the fraud-read error surfaced, got %v", err)
	}
}

// --- ExportLabels: an echo is not a label --------------------------------
//
// THE DEFECT THIS CLOSES. A dispute decided on the heuristic alone tells the
// trainer only that a human agreed with the heuristic, and the heuristic's own
// score was on the screen while they did. Exporting it as y closes the loop: the
// model is fit to predict its own output, and every downstream gate passes,
// because they all check the model against the labels and take the labels as
// real. So the row is kept in the database — the dispute outcome is a real
// decision the customer is owed — and it never leaves as a training label.
func TestExportLabelsExcludesHeuristicOnlyDecisions(t *testing.T) {
	observed := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	echo := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	unstated := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	repo := &fakeRepo{decided: []model.Dispute{
		// Someone actually looked at the follower list. This is a label.
		{ID: observed, AuditJobID: jobID(), Status: model.StatusRejected,
			LabelEvidence: model.EvidenceManualFollowerAudit, ScoreShownToAdmin: true},
		// The admin agreed with the flag, having observed nothing the heuristic had
		// not already computed. A real decision; not a label.
		{ID: echo, AuditJobID: jobID(), Status: model.StatusRejected,
			LabelEvidence: model.EvidenceHeuristicOnly},
		// A legacy row from before the column existed: no evidence was ever recorded,
		// so no observation is known to have happened. Silence is not an observation.
		{ID: unstated, AuditJobID: jobID(), Status: model.StatusRejected},
	}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{found: false}, fakeCost{}, fakeQueues{})

	resp, err := svc.ExportLabels(context.Background())
	if err != nil {
		t.Fatalf("ExportLabels: %v", err)
	}
	if resp.Count != 1 || len(resp.Labels) != 1 {
		t.Fatalf("count = %d, want exactly the 1 observed label: %+v", resp.Count, resp.Labels)
	}
	if resp.Labels[0].DisputeID != observed.String() {
		t.Fatalf("exported dispute %s, want the manually-audited one (%s)", resp.Labels[0].DisputeID, observed)
	}
	if resp.Excluded != 2 {
		t.Errorf("excluded = %d, want 2 (the heuristic echo and the unstated one)", resp.Excluded)
	}
	for _, l := range resp.Labels {
		if l.LabelEvidence == string(model.EvidenceHeuristicOnly) || l.LabelEvidence == "" {
			t.Fatalf("a heuristic echo reached the training-label export: %+v", l)
		}
	}
	// The contamination flag rides along so the trainer can stratify on it: this
	// label rests on a real observation, but the adjudicator could see the score.
	if !resp.Labels[0].ScoreShownToAdmin {
		t.Error("score_shown_to_admin not carried into the export")
	}
}

// The export is a filter, not a delete: the excluded decisions are still in the
// database, and the dispute read still serves them. The customer is owed the
// outcome; only its standing as a training label is withdrawn.
func TestExportLabelsKeepsHeuristicOnlyDisputesReadable(t *testing.T) {
	repo := &fakeRepo{dispute: model.Dispute{
		AuditJobID: jobID(), Status: model.StatusRejected, LabelEvidence: model.EvidenceHeuristicOnly,
	}}
	svc := newService(repo, fakeCaller{}, fakeGuard{id: adminID()}, fakeFraud{found: false}, fakeCost{}, fakeQueues{})

	resp, err := svc.ReviewDispute(context.Background(), jobID().String())
	if err != nil {
		t.Fatalf("ReviewDispute: %v", err)
	}
	if resp.Dispute.LabelEvidence != string(model.EvidenceHeuristicOnly) {
		t.Fatalf("the excluded decision must still be readable, evidence and all: %+v", resp.Dispute)
	}
}
