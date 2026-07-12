package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// --- fakes ---------------------------------------------------------------

// fakeRepo is a configurable service.Repository stand-in. It records the last
// write of each kind and returns canned reads, so the service's mapping and state
// transitions can be exercised without a database.
type fakeRepo struct {
	upserted    *model.FeatureRow
	labelCall   *labelCall
	rows        []model.FeatureRow
	featureRow  model.FeatureRow
	rowFound    bool
	registered  *model.Version
	getResult   model.Version
	getFound    bool
	hasChampion bool
	promoteArg  *promoteArg
	promoteRes  model.PromotionResult
	canaries    []model.Canary
	createdCan  *model.Canary
	prediction  *model.PredictionLog

	upsertErr, labelErr, listErr, rowErr, registerErr, getErr, championErr, promoteErr, canaryListErr, canaryCreateErr, predictErr error
}

type labelCall struct {
	auditJobID uuid.UUID
	label      bool
	source     string
	evidence   string
}

type promoteArg struct {
	modelName string
	version   string
}

func (r *fakeRepo) UpsertFeatureRow(_ context.Context, row model.FeatureRow) error {
	r.upserted = &row
	return r.upsertErr
}

func (r *fakeRepo) SetFraudLabel(_ context.Context, id uuid.UUID, label bool, source, evidence string) error {
	r.labelCall = &labelCall{auditJobID: id, label: label, source: source, evidence: evidence}
	return r.labelErr
}

func (r *fakeRepo) ListFeatureRows(_ context.Context, _ model.FeatureRowFilter) ([]model.FeatureRow, error) {
	return r.rows, r.listErr
}

func (r *fakeRepo) GetFeatureRow(_ context.Context, _ uuid.UUID) (model.FeatureRow, bool, error) {
	return r.featureRow, r.rowFound, r.rowErr
}

func (r *fakeRepo) HasChampion(_ context.Context, _ string) (bool, error) {
	return r.hasChampion, r.championErr
}

func (r *fakeRepo) RegisterChallenger(_ context.Context, mv model.Version) (model.Version, error) {
	r.registered = &mv
	if r.registerErr != nil {
		return model.Version{}, r.registerErr
	}
	mv.ID = uuid.New()
	mv.CreatedAt = time.Unix(1_700_000_000, 0).UTC()
	return mv, nil
}

func (r *fakeRepo) GetModelVersion(_ context.Context, _, _ string) (model.Version, bool, error) {
	return r.getResult, r.getFound, r.getErr
}

func (r *fakeRepo) PromoteVersion(_ context.Context, modelName, version string) (model.PromotionResult, error) {
	r.promoteArg = &promoteArg{modelName: modelName, version: version}
	return r.promoteRes, r.promoteErr
}

func (r *fakeRepo) ListCanaries(_ context.Context, _ string, _ bool) ([]model.Canary, error) {
	return r.canaries, r.canaryListErr
}

func (r *fakeRepo) CreateCanary(_ context.Context, c model.Canary) (model.Canary, error) {
	r.createdCan = &c
	if r.canaryCreateErr != nil {
		return model.Canary{}, r.canaryCreateErr
	}
	c.ID = uuid.New()
	return c, nil
}

func (r *fakeRepo) InsertPrediction(_ context.Context, p model.PredictionLog) error {
	r.prediction = &p
	return r.predictErr
}

// fakeGuard is a configurable AdminGuard.
type fakeGuard struct {
	id  uuid.UUID
	err error
}

func (g fakeGuard) RequireAdmin(context.Context) (uuid.UUID, error) { return g.id, g.err }

// fakeServiceAuth is a configurable ServiceAuth.
type fakeServiceAuth struct{ err error }

func (a fakeServiceAuth) RequireService(context.Context) error { return a.err }

// fakeStore records the objects a register PUT writes.
type fakeStore struct {
	puts []storePut
	err  error
}

type storePut struct {
	key         string
	contentType string
	data        []byte
}

func (s *fakeStore) PutObject(_ context.Context, key, contentType string, data []byte) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.puts = append(s.puts, storePut{key: key, contentType: contentType, data: data})
	return "etag", nil
}

func adminID() uuid.UUID { return uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa") }

// adminSvc builds a service whose admin guard passes and whose service-token auth
// passes, over the given repo and store.
func adminSvc(repo Repository, store *fakeStore) *Service {
	return New(repo, fakeGuard{id: adminID()}, fakeServiceAuth{}, store)
}

// passingReport is a gate report whose required gates all pass, with the canary
// gate skipped (an honest cold start). The shapes are derived from the resolved
// contract's validation_report_jsonb, not from any real training run.
func passingReport() json.RawMessage {
	return json.RawMessage(`{"g1_held_out":{"pass":true},"g2_stratified":{"pass":true},` +
		`"g3_canary":{"pass":false,"skipped":true},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`)
}

// fraudFloor is a data-floor-counts blob that meets the fraud per-class floor.
func fraudFloor() json.RawMessage {
	return json.RawMessage(`{"positive":61,"negative":74,"positive_influencers":40,"negative_influencers":52,"floor":50}`)
}

// --- RecordFeatureRow ----------------------------------------------------

func TestRecordFeatureRowComputesVectorAndQuality(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})

	capturedAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	posts := make([]connector.Post, 6)
	for i := range posts {
		posts[i] = connector.Post{Likes: 100, Comments: 5, PublishedAt: capturedAt.AddDate(0, 0, -i*3)}
	}
	capture := contract.FeatureCapture{
		AuditJobID:   uuid.New(),
		InfluencerID: uuid.New(),
		Snapshots: []connector.Snapshot{{
			Platform:  connector.PlatformInstagram,
			Followers: 15200,
			Posts:     posts,
			Metrics:   []connector.MetricPoint{{Name: "followers", At: capturedAt.AddDate(-1, 0, 0), Value: 14000}},
		}},
		// EXPECTATION CHANGED: FakeFollowerRate is gone (it was the composite risk score
		// renamed). RiskScore is on a 0-100 scale, so 4.0 is the old 0.04 fraction —
		// still comfortably under the quality filter's maxFraudRisk.
		Fraud:            contract.FraudSignal{Present: true, RiskScore: risk(4), ModelVersion: "lgbm-abc"},
		Niche:            "fitness",
		Tier:             "mid",
		VerificationTier: "verified",
		CapturedAt:       capturedAt,
	}

	if err := svc.RecordFeatureRow(context.Background(), capture); err != nil {
		t.Fatalf("RecordFeatureRow: %v", err)
	}
	if repo.upserted == nil {
		t.Fatal("expected a feature row to be upserted")
	}
	got := repo.upserted
	if !got.QualityOK {
		t.Errorf("a clean capture must be quality_ok, reasons=%v", got.QualityReasons)
	}
	if got.Platform != "instagram" || got.ModelVersionAtCapture != "lgbm-abc" || got.VerificationTier != "verified" {
		t.Errorf("row metadata not mapped: %+v", got)
	}

	var vec model.FeatureVector
	if err := json.Unmarshal(got.Features, &vec); err != nil {
		t.Fatalf("features not valid json: %v", err)
	}
	if vec.FollowerCount != 15200 || vec.PostCount != 6 || vec.Niche != "fitness" || vec.Tier != "mid" {
		t.Errorf("descriptive features wrong: %+v", vec)
	}
	if vec.EngagementRate == nil || vec.PostingCadencePerWeek == nil || vec.AccountAgeDaysProxy == nil {
		t.Errorf("expected engagement/cadence/age computed, got %+v", vec)
	}
	// Foundation gaps must remain null, never zero-filled.
	if vec.FollowingCount != nil || vec.Verified != nil || vec.FollowerFollowingRatio != nil {
		t.Errorf("following/verified/ratio must be null (foundation gap), got %+v", vec)
	}
}

func TestRecordFeatureRowNoSnapshotsIsNoOp(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	if err := svc.RecordFeatureRow(context.Background(), contract.FeatureCapture{AuditJobID: uuid.New()}); err != nil {
		t.Fatalf("RecordFeatureRow: %v", err)
	}
	if repo.upserted != nil {
		t.Fatal("a capture with no usable snapshot must not write a row")
	}
}

// igSnapshot is an Instagram snapshot from the given data path carrying one reach
// metric point — the shape a real Insights pull produces.
func igSnapshot(source connector.DataSource, reach float64) connector.Snapshot {
	return connector.Snapshot{
		Platform:  connector.PlatformInstagram,
		Source:    source,
		Followers: 100,
		Metrics:   []connector.MetricPoint{{Name: "reach", Value: reach}},
	}
}

func organic(v bool) *bool { return &v }

// A reach label is only ever stored with MEASURED provenance: a live Instagram
// Graph pull, and the capture stating the figure is organic. The source is derived
// from the snapshot, never from the caller.
func TestRecordFeatureRowDerivesReachProvenanceFromSnapshot(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	capture := contract.FeatureCapture{
		AuditJobID:   uuid.New(),
		Snapshots:    []connector.Snapshot{igSnapshot(connector.SourceInstagramGraph, 15234)},
		Fraud:        contract.FraudSignal{Present: true},
		ReachOrganic: organic(true),
	}
	if err := svc.RecordFeatureRow(context.Background(), capture); err != nil {
		t.Fatalf("RecordFeatureRow: %v", err)
	}
	if repo.upserted.ReachLabel == nil || *repo.upserted.ReachLabel != 15234 {
		t.Fatalf("reach label not derived from the snapshot: %+v", repo.upserted.ReachLabel)
	}
	if repo.upserted.ReachLabelSource == nil || *repo.upserted.ReachLabelSource != string(contract.ReachSourceInstagramGraph) {
		t.Fatalf("reach provenance must be the measured source: %+v", repo.upserted.ReachLabelSource)
	}
	if repo.upserted.ReachOrganic == nil || !*repo.upserted.ReachOrganic {
		t.Fatalf("the organic flag must be persisted: %+v", repo.upserted.ReachOrganic)
	}
}

// A CSV export is a creator's self-reported number. It may not claim measured
// reach provenance, so no reach label is stored at all — and the caller's own
// ReachLabel is ignored, which is what makes the column evidence rather than a
// constant.
func TestRecordFeatureRowRefusesReachLabelFromCSVAndFromTheCaller(t *testing.T) {
	claimed := int64(999999)
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	capture := contract.FeatureCapture{
		AuditJobID:   uuid.New(),
		Snapshots:    []connector.Snapshot{igSnapshot(connector.SourceCSVUpload, 15234)},
		Fraud:        contract.FraudSignal{Present: true},
		ReachLabel:   &claimed, // the caller's word: must not be honoured
		ReachOrganic: organic(true),
	}
	if err := svc.RecordFeatureRow(context.Background(), capture); err != nil {
		t.Fatalf("RecordFeatureRow: %v", err)
	}
	if repo.upserted.ReachLabel != nil || repo.upserted.ReachLabelSource != nil {
		t.Fatalf("a CSV snapshot must yield no reach label: label=%v source=%v",
			repo.upserted.ReachLabel, repo.upserted.ReachLabelSource)
	}
	if len(repo.upserted.SnapshotSources) != 1 || repo.upserted.SnapshotSources[0] != string(connector.SourceCSVUpload) {
		t.Fatalf("the data path must be recorded on the row: %+v", repo.upserted.SnapshotSources)
	}
}

// Insights reach on a boosted post includes AD-DELIVERED reach. Unknown organic
// split is NOT organic: no label is stored, because there is no honest way to
// estimate the organic portion back out of it.
func TestRecordFeatureRowWithholdsReachWhenOrganicSplitUnknown(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	capture := contract.FeatureCapture{
		AuditJobID: uuid.New(),
		Snapshots:  []connector.Snapshot{igSnapshot(connector.SourceInstagramGraph, 15234)},
		Fraud:      contract.FraudSignal{Present: true},
		// ReachOrganic nil: the connector could not expose the split.
	}
	if err := svc.RecordFeatureRow(context.Background(), capture); err != nil {
		t.Fatalf("RecordFeatureRow: %v", err)
	}
	if repo.upserted.ReachLabel != nil || repo.upserted.ReachLabelSource != nil {
		t.Fatalf("an unknown organic split must yield no reach label: %+v", repo.upserted)
	}
}

// --- SetFraudLabel -------------------------------------------------------

func TestSetFraudLabelPassesThroughToRepo(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	id := uuid.New()
	if err := svc.SetFraudLabel(context.Background(), id, true, contract.LabelSourceDisputeRejected, contract.EvidenceManualFollowerAudit); err != nil {
		t.Fatalf("SetFraudLabel: %v", err)
	}
	if repo.labelCall == nil || repo.labelCall.auditJobID != id || !repo.labelCall.label ||
		repo.labelCall.source != string(contract.LabelSourceDisputeRejected) {
		t.Fatalf("label not backfilled as expected: %+v", repo.labelCall)
	}
}

func TestSetFraudLabelRejectsUnknownSource(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	if err := svc.SetFraudLabel(context.Background(), uuid.New(), true, contract.FraudLabelSource("guess"), contract.EvidenceManualFollowerAudit); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for an unknown label source, got %v", err)
	}
}

// --- ExportFeatureRows ---------------------------------------------------

func TestExportFeatureRowsGuarded(t *testing.T) {
	forbidden := errs.New(errs.KindForbidden, "x", "y")
	svc := New(&fakeRepo{}, fakeGuard{err: forbidden}, fakeServiceAuth{}, &fakeStore{})
	if _, err := svc.ExportFeatureRows(context.Background(), model.FeatureRowQuery{}); errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("export must be admin-guarded, got %v", err)
	}
}

func TestExportFeatureRowsDefaultsQualityAndLimit(t *testing.T) {
	repo := &fakeRepo{rows: []model.FeatureRow{{AuditJobID: uuid.New(), InfluencerID: uuid.New(), Features: json.RawMessage(`{}`)}}}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.ExportFeatureRows(context.Background(), model.FeatureRowQuery{})
	if err != nil {
		t.Fatalf("ExportFeatureRows: %v", err)
	}
	if resp.Count != 1 || len(resp.Rows) != 1 {
		t.Fatalf("export count wrong: %+v", resp)
	}
}

// THE POSITIVE CLASS MUST SURVIVE THE QUALITY GATE. A disputed account is a
// high-risk account by construction, so a fraud-score-derived quality reason on a
// HUMAN-LABELED row would censor exactly the rows that carry y=1 — forever. The
// row below (risk 80/100, quality_ok=false, fraud_label set) MUST appear in the
// default export.
func TestExportFeatureRowsKeepsLabelledHighRiskRows(t *testing.T) {
	labelled := true
	// EXPECTATION TIGHTENED: the waiver keys on the label's EVIDENCE, not on the
	// mere presence of a label. This row carries a manual follower-sample audit — a
	// human actually looked at the follower list — so it is a real observation and
	// the fraud-risk censorship is waived.
	observed := string(contract.EvidenceManualFollowerAudit)
	repo := &fakeRepo{rows: []model.FeatureRow{{
		AuditJobID: uuid.New(), InfluencerID: uuid.New(), Features: json.RawMessage(`{"risk_score":80}`),
		FraudLabel: &labelled, FraudLabelEvidence: &observed,
		QualityOK: false, QualityReasons: []string{model.ReasonFraudRiskHigh},
	}}}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.ExportFeatureRows(context.Background(), model.FeatureRowQuery{})
	if err != nil {
		t.Fatalf("ExportFeatureRows: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("an evidence-backed labelled row must never be censored by our own fraud estimate: %+v", resp)
	}
	if !resp.Rows[0].TrainingEligible {
		t.Fatal("the exported row must report itself training-eligible")
	}
}

// The OTHER half of the rule, and the more important one: a label with NO
// observation behind it is a heuristic ECHO and must never reach the trainer.
//
// A dispute exists only because the heuristic flagged the account, and the
// adjudicator sees the heuristic's own score. "The admin agreed with the flag"
// therefore contains no information the heuristic did not already assert. Waiving
// the fraud-risk exclusion for such a row would hand-pick precisely the circular
// labels and feed them to the model as ground truth — and G0-G5 could not see it,
// because they check the model against the labels and assume the labels are real.
//
// An empty positive class is a shippable answer. A laundered one is not.
func TestExportFeatureRowsExcludesHeuristicEchoLabels(t *testing.T) {
	labelled := true
	echo := string(contract.EvidenceHeuristicOnly)
	repo := &fakeRepo{rows: []model.FeatureRow{{
		AuditJobID: uuid.New(), InfluencerID: uuid.New(), Features: json.RawMessage(`{"risk_score":80}`),
		FraudLabel: &labelled, FraudLabelEvidence: &echo,
		QualityOK: false, QualityReasons: []string{model.ReasonFraudRiskHigh},
	}}}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.ExportFeatureRows(context.Background(), model.FeatureRowQuery{})
	if err != nil {
		t.Fatalf("ExportFeatureRows: %v", err)
	}
	if resp.Count != 0 {
		t.Fatalf("a heuristic-echo label must never enter the training export: %+v", resp)
	}
}

// A label with NO evidence recorded at all is treated exactly like an echo. The
// absence of a stated observation is not an observation, and defaulting it to
// "trainable" would reopen the laundering path through the back door.
func TestExportFeatureRowsExcludesLabelWithNoEvidence(t *testing.T) {
	labelled := true
	repo := &fakeRepo{rows: []model.FeatureRow{{
		AuditJobID: uuid.New(), InfluencerID: uuid.New(), Features: json.RawMessage(`{"risk_score":80}`),
		FraudLabel: &labelled, FraudLabelEvidence: nil,
		QualityOK: false, QualityReasons: []string{model.ReasonFraudRiskHigh},
	}}}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.ExportFeatureRows(context.Background(), model.FeatureRowQuery{})
	if err != nil {
		t.Fatalf("ExportFeatureRows: %v", err)
	}
	if resp.Count != 0 {
		t.Fatalf("a label with no recorded observation must not be trainable: %+v", resp)
	}
}

// The scoping is narrow: only the fraud-score-derived reason is waived for a
// labelled row. An OBSERVED data-quality fact (too few posts) still excludes it,
// and an unlabelled high-risk row is still excluded (the anti-gaming filter is
// intact).
func TestExportFeatureRowsStillDropsObservedQualityFailures(t *testing.T) {
	labelled := true
	repo := &fakeRepo{rows: []model.FeatureRow{
		{
			AuditJobID: uuid.New(), InfluencerID: uuid.New(), Features: json.RawMessage(`{}`),
			FraudLabel: &labelled, QualityReasons: []string{model.ReasonFraudRiskHigh, "insufficient_posts"},
		},
		{
			AuditJobID: uuid.New(), InfluencerID: uuid.New(), Features: json.RawMessage(`{}`),
			QualityReasons: []string{model.ReasonFraudRiskHigh}, // unlabelled: the gate still applies
		},
	}}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.ExportFeatureRows(context.Background(), model.FeatureRowQuery{})
	if err != nil {
		t.Fatalf("ExportFeatureRows: %v", err)
	}
	if resp.Count != 0 {
		t.Fatalf("observed quality failures and unlabelled high-risk rows must stay out: %+v", resp)
	}
}

// --- RegisterModel -------------------------------------------------------

func TestRegisterModelWritesArtifactsAndRecordsChallenger(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStore{}
	svc := adminSvc(repo, store)

	modelBytes := []byte("lightgbm-model-bytes")
	req := model.RegisterModelRequest{
		ModelName:        model.ModelFraud,
		Version:          "lgbm-ab12cd34ef56",
		Manifest:         json.RawMessage(`{"version":"lgbm-ab12cd34ef56"}`),
		ModelFileName:    "model.txt",
		ModelFileB64:     base64.StdEncoding.EncodeToString(modelBytes),
		ValidationReport: passingReport(),
		DataFloorCounts:  fraudFloor(),
		FeatureSnapshot:  model.FeatureSnapshotRef{RowCount: 135, ContentHash: "sha256:deadbeef"},
	}
	resp, err := svc.RegisterModel(context.Background(), req)
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	if resp.Role != model.RoleChallenger || resp.S3Key != "ml-models/fraud/lgbm-ab12cd34ef56/" {
		t.Fatalf("register response wrong: %+v", resp)
	}
	if len(store.puts) != 2 {
		t.Fatalf("expected 2 S3 objects (model + manifest), got %d", len(store.puts))
	}
	if store.puts[0].key != "ml-models/fraud/lgbm-ab12cd34ef56/model.txt" || string(store.puts[0].data) != string(modelBytes) {
		t.Errorf("model object wrong: %+v", store.puts[0])
	}
	if store.puts[1].key != "ml-models/fraud/lgbm-ab12cd34ef56/manifest.json" {
		t.Errorf("manifest object wrong: %+v", store.puts[1])
	}
	if repo.registered == nil || repo.registered.FeatureRowCount != 135 || repo.registered.FeatureSnapshotHash != "sha256:deadbeef" {
		t.Errorf("challenger not recorded with snapshot ref: %+v", repo.registered)
	}
}

func TestRegisterModelRejectsBadModelName(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	req := model.RegisterModelRequest{ModelName: "spam", Version: "v", ModelFileB64: "", ValidationReport: passingReport(), DataFloorCounts: fraudFloor()}
	if _, err := svc.RegisterModel(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for a bad model name, got %v", err)
	}
}

func TestRegisterModelRejectsBadBase64(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	req := model.RegisterModelRequest{ModelName: model.ModelFraud, Version: "v", ModelFileB64: "!!!not-base64!!!", ValidationReport: passingReport(), DataFloorCounts: fraudFloor()}
	if _, err := svc.RegisterModel(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for bad base64, got %v", err)
	}
}

// --- PromoteModel: state transitions -------------------------------------

func TestPromoteChallengerFlipsRolesWhenGatesPass(t *testing.T) {
	repo := &fakeRepo{
		getFound:    true,
		hasChampion: true, // a succession, not a first champion
		getResult: model.Version{
			ModelName: model.ModelFraud, Version: "lgbm-new", Role: model.RoleChallenger,
			ValidationReport: passingReport(), DataFloorCounts: fraudFloor(),
		},
		promoteRes: model.PromotionResult{
			ChampionVersion: "lgbm-new", PreviousChampionVersion: "lgbm-old",
			Manifest: json.RawMessage(`{"version":"lgbm-new"}`), S3Key: "ml-models/fraud/lgbm-new/",
			PromotedAt: time.Unix(1_700_000_500, 0).UTC(),
		},
	}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.PromoteModel(context.Background(), "lgbm-new", model.PromoteModelRequest{ModelName: model.ModelFraud})
	if err != nil {
		t.Fatalf("PromoteModel: %v", err)
	}
	if repo.promoteArg == nil || repo.promoteArg.version != "lgbm-new" {
		t.Fatal("expected the repository promotion to run for the target version")
	}
	if resp.ChampionVersion != "lgbm-new" || resp.PreviousChampionVersion != "lgbm-old" {
		t.Fatalf("promote response wrong: %+v", resp)
	}
}

func TestPromoteRejectsFailingGate(t *testing.T) {
	failing := json.RawMessage(`{"g1_held_out":{"pass":false},"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true}}`)
	repo := &fakeRepo{getFound: true, hasChampion: true, getResult: model.Version{
		ModelName: model.ModelFraud, Version: "lgbm-new", Role: model.RoleChallenger,
		ValidationReport: failing, DataFloorCounts: fraudFloor(),
	}}
	svc := adminSvc(repo, &fakeStore{})
	if _, err := svc.PromoteModel(context.Background(), "lgbm-new", model.PromoteModelRequest{ModelName: model.ModelFraud}); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("want conflict when a required gate fails, got %v", err)
	}
	if repo.promoteArg != nil {
		t.Fatal("a failing gate must not trigger a role flip")
	}
}

func TestPromoteRejectsBelowDataFloor(t *testing.T) {
	belowFloor := json.RawMessage(`{"positive":10,"negative":74,"floor":50}`)
	repo := &fakeRepo{getFound: true, hasChampion: true, getResult: model.Version{
		ModelName: model.ModelFraud, Version: "lgbm-new", Role: model.RoleChallenger,
		ValidationReport: passingReport(), DataFloorCounts: belowFloor,
	}}
	svc := adminSvc(repo, &fakeStore{})
	if _, err := svc.PromoteModel(context.Background(), "lgbm-new", model.PromoteModelRequest{ModelName: model.ModelFraud}); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("want conflict below the data floor, got %v", err)
	}
	if repo.promoteArg != nil {
		t.Fatal("a below-floor challenger must never be promoted")
	}
}

func TestPromoteRejectedVersionIsConflict(t *testing.T) {
	repo := &fakeRepo{getFound: true, getResult: model.Version{
		ModelName: model.ModelFraud, Version: "lgbm-bad", Role: model.RoleRejected,
	}}
	svc := adminSvc(repo, &fakeStore{})
	if _, err := svc.PromoteModel(context.Background(), "lgbm-bad", model.PromoteModelRequest{ModelName: model.ModelFraud}); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("want conflict promoting a rejected version, got %v", err)
	}
}

func TestPromoteMissingVersionIsNotFound(t *testing.T) {
	svc := adminSvc(&fakeRepo{getFound: false}, &fakeStore{})
	if _, err := svc.PromoteModel(context.Background(), "nope", model.PromoteModelRequest{ModelName: model.ModelFraud}); errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("want not-found for a missing version, got %v", err)
	}
}

// Rollback: an archived former champion promotes without any gate re-check,
// because it already earned its gates when it was champion.
func TestPromoteRollbackWaivesGates(t *testing.T) {
	promotedAt := time.Unix(1_700_000_000, 0).UTC()
	repo := &fakeRepo{
		getFound: true,
		getResult: model.Version{
			ModelName: model.ModelFraud, Version: "lgbm-old", Role: model.RoleArchived,
			// A former champion (promoted_at set): a rollback must still succeed even
			// with an empty report — it earned its gates when it first served.
			PromotedAt:       &promotedAt,
			ValidationReport: json.RawMessage(`{}`), DataFloorCounts: json.RawMessage(`{}`),
		},
		promoteRes: model.PromotionResult{ChampionVersion: "lgbm-old", PreviousChampionVersion: "lgbm-current"},
	}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.PromoteModel(context.Background(), "lgbm-old", model.PromoteModelRequest{ModelName: model.ModelFraud})
	if err != nil {
		t.Fatalf("rollback promote: %v", err)
	}
	if resp.ChampionVersion != "lgbm-old" || repo.promoteArg == nil {
		t.Fatalf("rollback did not promote the archived version: %+v", resp)
	}
}

// A never-promoted archived version (a challenger superseded when a different
// version won, promoted_at nil) is not a rollback: it must re-validate its stored
// gate report in full, so an empty/failing report is a conflict. Gates can never
// be waived for a model that never earned them (H6).
func TestPromoteNeverServedArchivedReValidates(t *testing.T) {
	repo := &fakeRepo{
		getFound:    true,
		hasChampion: true,
		getResult: model.Version{
			ModelName: model.ModelFraud, Version: "lgbm-loser", Role: model.RoleArchived,
			// No PromotedAt: never served. An empty report cannot pass the gates.
			ValidationReport: json.RawMessage(`{}`), DataFloorCounts: json.RawMessage(`{}`),
		},
	}
	svc := adminSvc(repo, &fakeStore{})
	_, err := svc.PromoteModel(context.Background(), "lgbm-loser", model.PromoteModelRequest{ModelName: model.ModelFraud})
	if err == nil {
		t.Fatal("expected a never-served archived version to re-validate and be rejected")
	}
	if repo.promoteArg != nil {
		t.Fatalf("a failing re-validation must not promote: %+v", repo.promoteArg)
	}
}

// Promoting the current champion again is idempotent: no role flip, current state
// returned.
func TestPromoteChampionIsIdempotent(t *testing.T) {
	promotedAt := time.Unix(1_700_000_000, 0).UTC()
	repo := &fakeRepo{getFound: true, getResult: model.Version{
		ModelName: model.ModelFraud, Version: "lgbm-live", Role: model.RoleChampion,
		Manifest: json.RawMessage(`{"version":"lgbm-live"}`), S3Key: "ml-models/fraud/lgbm-live/", PromotedAt: &promotedAt,
	}}
	svc := adminSvc(repo, &fakeStore{})
	resp, err := svc.PromoteModel(context.Background(), "lgbm-live", model.PromoteModelRequest{ModelName: model.ModelFraud})
	if err != nil {
		t.Fatalf("idempotent promote: %v", err)
	}
	if resp.ChampionVersion != "lgbm-live" || repo.promoteArg != nil {
		t.Fatalf("re-promoting the champion must not flip roles: %+v", resp)
	}
}

// THE FIRST CHAMPION IS UNRECOVERABLE. There is no previous champion to roll back
// to, and every later rollback short-circuits the gates — so crowning a first
// champion with no canary set means no mistake after it can ever be caught by the
// one artifact built to catch mistakes. It is refused server-side, in Go.
func TestPromoteFirstChampionWithNoCanariesIsConflict(t *testing.T) {
	repo := &fakeRepo{
		getFound:    true,
		hasChampion: false, // no champion yet: this promotion crowns the first one
		canaries:    nil,   // and there is nothing to catch it
		getResult: model.Version{
			ModelName: model.ModelFraud, Version: "lgbm-first", Role: model.RoleChallenger,
			ValidationReport: passingReport(), DataFloorCounts: fraudFloor(),
		},
	}
	svc := adminSvc(repo, &fakeStore{})
	_, err := svc.PromoteModel(context.Background(), "lgbm-first", model.PromoteModelRequest{ModelName: model.ModelFraud})
	if errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("want conflict for a first champion with an empty canary set, got %v", err)
	}
	if repo.promoteArg != nil {
		t.Fatal("a first champion must not be crowned on an empty canary set")
	}
}

// With canaries on file the first champion is promotable — but its report must now
// show G3 actually PASSING, not skipped (the server knows the set is not empty).
func TestPromoteFirstChampionWithCanariesRequiresPassingCanaryGate(t *testing.T) {
	canaryOnFile := []model.Canary{{ID: uuid.New(), ModelName: model.ModelFraud, ProvenanceKind: model.ProvenanceVendorReceipt}}
	base := model.Version{
		ModelName: model.ModelFraud, Version: "lgbm-first", Role: model.RoleChallenger,
		ValidationReport: passingReport(), // g3 skipped
		DataFloorCounts:  fraudFloor(),
	}

	skipped := &fakeRepo{getFound: true, getResult: base, canaries: canaryOnFile}
	svc := adminSvc(skipped, &fakeStore{})
	if _, err := svc.PromoteModel(context.Background(), "lgbm-first", model.PromoteModelRequest{ModelName: model.ModelFraud}); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a skipped canary gate with canaries on file must be a conflict, got %v", err)
	}

	passing := base
	passing.ValidationReport = json.RawMessage(`{"g1_held_out":{"pass":true},"g2_stratified":{"pass":true},` +
		`"g3_canary":{"pass":true},"g4_vs_champion":{"pass":true},"g6_beats_heuristic":{"pass":true}}`)
	repo := &fakeRepo{
		getFound: true, getResult: passing, canaries: canaryOnFile,
		promoteRes: model.PromotionResult{ChampionVersion: "lgbm-first"},
	}
	svc = adminSvc(repo, &fakeStore{})
	if _, err := svc.PromoteModel(context.Background(), "lgbm-first", model.PromoteModelRequest{ModelName: model.ModelFraud}); err != nil {
		t.Fatalf("a first champion with a passing canary gate must promote: %v", err)
	}
	if repo.promoteArg == nil {
		t.Fatal("expected the promotion to run")
	}
}

// --- Canaries ------------------------------------------------------------

// auditRow is a captured feature row from a live Instagram Graph audit: the source
// the canary endpoint copies its frozen vector from.
func auditRow() model.FeatureRow {
	return model.FeatureRow{
		AuditJobID:      uuid.New(),
		Features:        json.RawMessage(`{"risk_score":61.2,"follower_count":15200}`),
		SnapshotSources: []string{string(connector.SourceInstagramGraph)},
	}
}

// canaryReq is a well-formed positive fraud canary over the given audit.
func canaryReq(auditJobID uuid.UUID) model.CreateCanaryRequest {
	label := true
	return model.CreateCanaryRequest{
		ModelName:      model.ModelFraud,
		AuditJobID:     auditJobID.String(),
		Label:          "known bought-follower account",
		ProvenanceKind: model.ProvenanceVendorReceipt,
		ExpectedLabel:  &label,
	}
}

// The canary's feature vector comes from the SERVER's frozen row, not from the
// client — there is no features field on the request at all, so the operator who
// runs the model cannot hand-type the vector the model is then tested against.
func TestCreateCanaryCopiesTheFrozenVectorFromTheAudit(t *testing.T) {
	row := auditRow()
	repo := &fakeRepo{featureRow: row, rowFound: true}
	svc := adminSvc(repo, &fakeStore{})

	resp, err := svc.CreateCanary(context.Background(), canaryReq(row.AuditJobID))
	if err != nil {
		t.Fatalf("CreateCanary: %v", err)
	}
	if repo.createdCan == nil || !repo.createdCan.Active {
		t.Fatalf("canary not stored as active: %+v", repo.createdCan)
	}
	if string(repo.createdCan.Features) != string(row.Features) {
		t.Fatalf("the canary's vector must be the audit's frozen vector, got %s", repo.createdCan.Features)
	}
	if repo.createdCan.AuditJobID != row.AuditJobID {
		t.Fatalf("the canary must be anchored to its audit: %+v", repo.createdCan.AuditJobID)
	}
	if resp.Canary.ProvenanceKind != model.ProvenanceVendorReceipt {
		t.Fatalf("canary response wrong: %+v", resp)
	}
}

func TestCreateCanaryUnknownAuditIsNotFound(t *testing.T) {
	svc := adminSvc(&fakeRepo{rowFound: false}, &fakeStore{})
	if _, err := svc.CreateCanary(context.Background(), canaryReq(uuid.New())); errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("an audit with no captured row cannot back a canary, got %v", err)
	}
}

// A hand-written CSV export laundered through the audit pipeline must not come out
// the other side as ground truth. With Instagram gated on app review, the CSV is
// the ONLY Instagram path — so this is the live laundering route, not a theoretical
// one.
func TestCreateCanaryRefusesCSVSourcedAudit(t *testing.T) {
	row := auditRow()
	row.SnapshotSources = []string{string(connector.SourceCSVUpload)}
	repo := &fakeRepo{featureRow: row, rowFound: true}
	svc := adminSvc(repo, &fakeStore{})

	if _, err := svc.CreateCanary(context.Background(), canaryReq(row.AuditJobID)); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a CSV-sourced audit must not back a canary, got %v", err)
	}
	if repo.createdCan != nil {
		t.Fatal("nothing may be inserted from a CSV-sourced audit")
	}
}

// A row captured before the data path was recorded proves nothing about where its
// numbers came from. Unknown provenance is refused: absence is not evidence.
func TestCreateCanaryRefusesAuditWithUnknownProvenance(t *testing.T) {
	row := auditRow()
	row.SnapshotSources = nil
	repo := &fakeRepo{featureRow: row, rowFound: true}
	svc := adminSvc(repo, &fakeStore{})

	if _, err := svc.CreateCanary(context.Background(), canaryReq(row.AuditJobID)); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("an audit with no recorded data path must not back a canary, got %v", err)
	}
}

// NO EVIDENCE PROVES ABSENCE. A clean canary may only rest on 'presumed_clean';
// there is no way to spell "an admin checked and it was fine" in this API.
func TestCreateCleanCanaryMustBePresumedClean(t *testing.T) {
	row := auditRow()
	repo := &fakeRepo{featureRow: row, rowFound: true}
	svc := adminSvc(repo, &fakeStore{})

	clean := false
	req := canaryReq(row.AuditJobID)
	req.ExpectedLabel = &clean
	req.ProvenanceKind = model.ProvenancePlatformEnforcement // "verified" negative: not a thing
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("a clean canary cannot claim positive evidence, got %v", err)
	}

	req.ProvenanceKind = model.ProvenancePresumedClean
	if _, err := svc.CreateCanary(context.Background(), req); err != nil {
		t.Fatalf("a presumed_clean negative canary must be accepted: %v", err)
	}
}

// A positive fraud canary must rest on something someone could OBSERVE. An
// operator's opinion, dressed as 'presumed_clean' or anything else without
// evidence, is not a positive label.
func TestCreatePositiveCanaryRequiresObservableEvidence(t *testing.T) {
	row := auditRow()
	repo := &fakeRepo{featureRow: row, rowFound: true}
	svc := adminSvc(repo, &fakeStore{})

	req := canaryReq(row.AuditJobID)
	req.ProvenanceKind = model.ProvenancePresumedClean
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("a fraudulent canary needs observable evidence, got %v", err)
	}

	req.ProvenanceKind = "an admin looked at it and it seemed fine"
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("provenance is a closed enum, not prose, got %v", err)
	}
}

func TestCreateFraudCanaryRequiresExpectedLabel(t *testing.T) {
	row := auditRow()
	svc := adminSvc(&fakeRepo{featureRow: row, rowFound: true}, &fakeStore{})
	req := canaryReq(row.AuditJobID)
	req.ExpectedLabel = nil
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("a fraud canary without an expected label must be invalid, got %v", err)
	}
}

// A reach canary's band must come from a MEASURED figure, and the audit must
// actually carry one — a reach expectation with no measurement behind it is a
// number someone chose.
func TestCreateReachCanaryRequiresMeasuredReachOnTheAudit(t *testing.T) {
	row := auditRow()
	repo := &fakeRepo{featureRow: row, rowFound: true}
	svc := adminSvc(repo, &fakeStore{})

	min64, max64 := int64(10000), int64(20000)
	req := model.CreateCanaryRequest{
		ModelName: model.ModelReach, AuditJobID: row.AuditJobID.String(), Label: "steady reach account",
		ProvenanceKind: model.ProvenanceOAuthInsightsMeasured, ExpectedReachMin: &min64, ExpectedReachMax: &max64,
	}
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("a reach canary needs a measured reach on its audit, got %v", err)
	}

	// With a measured organic figure on the row, the band must contain it.
	reach, source := int64(15234), string(contract.ReachSourceInstagramGraph)
	row.ReachLabel, row.ReachLabelSource, row.ReachOrganic = &reach, &source, organic(true)
	repo.featureRow = row
	if _, err := svc.CreateCanary(context.Background(), req); err != nil {
		t.Fatalf("a measured, organic reach audit must back a reach canary: %v", err)
	}

	outside := int64(100)
	req.ExpectedReachMax = &outside
	req.ExpectedReachMin = &outside
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("a band that excludes the audit's own measurement must be invalid, got %v", err)
	}
}

func TestCreateReachCanaryRequiresBand(t *testing.T) {
	row := auditRow()
	svc := adminSvc(&fakeRepo{featureRow: row, rowFound: true}, &fakeStore{})
	req := model.CreateCanaryRequest{
		ModelName: model.ModelReach, AuditJobID: row.AuditJobID.String(), Label: "l",
		ProvenanceKind: model.ProvenanceOAuthInsightsMeasured,
	}
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("a reach canary without a band must be invalid, got %v", err)
	}
}

// --- IngestPrediction ----------------------------------------------------

func TestIngestPredictionRequiresServiceToken(t *testing.T) {
	svc := New(&fakeRepo{}, fakeGuard{id: adminID()}, fakeServiceAuth{err: errs.New(errs.KindUnauthorized, "x", "y")}, &fakeStore{})
	req := model.PredictionLogRequest{ModelName: model.ModelFraud, ChampionVersion: "v", FeaturesHash: "h", AuditJobID: uuid.NewString()}
	if _, err := svc.IngestPrediction(context.Background(), req); errs.KindOf(err) != errs.KindUnauthorized {
		t.Fatalf("ingest must require the service token, got %v", err)
	}
}

func TestIngestPredictionAppends(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	challenger := "lgbm-new"
	challengerScore := 58.1
	auditJobID := uuid.New()
	req := model.PredictionLogRequest{
		ModelName: model.ModelFraud, AuditJobID: auditJobID.String(),
		ChampionVersion: "lgbm-old", ChampionScore: 62.4,
		ChallengerVersion: &challenger, ChallengerScore: &challengerScore, FeaturesHash: "sha256:abc",
	}
	resp, err := svc.IngestPrediction(context.Background(), req)
	if err != nil {
		t.Fatalf("IngestPrediction: %v", err)
	}
	if !resp.Accepted || repo.prediction == nil {
		t.Fatalf("prediction not appended: %+v", repo.prediction)
	}
	if repo.prediction.AuditJobID != auditJobID {
		t.Fatalf("the correlation id must be persisted: %+v", repo.prediction.AuditJobID)
	}
	if repo.prediction.ChampionScore != 62.4 || repo.prediction.ChallengerVersion == nil || *repo.prediction.ChallengerVersion != challenger {
		t.Fatalf("prediction mapped wrong: %+v", repo.prediction)
	}
	if repo.prediction.ScoredAt.IsZero() {
		t.Fatal("scored_at must default to now when unset")
	}
}

// features_hash is a one-way sha256: without the audit job a logged prediction can
// NEVER be joined back to an outcome. A shadow row that can never be resolved is
// not evidence, and the ingest refuses to write one.
func TestIngestPredictionRejectsMissingAuditID(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	req := model.PredictionLogRequest{ModelName: model.ModelFraud, ChampionVersion: "v", FeaturesHash: "h"}
	if _, err := svc.IngestPrediction(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for a prediction with no audit job, got %v", err)
	}
	if repo.prediction != nil {
		t.Fatal("an unresolvable prediction must never be appended")
	}
}

func TestIngestPredictionRejectsBadAuditID(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	req := model.PredictionLogRequest{ModelName: model.ModelFraud, ChampionVersion: "v", FeaturesHash: "h", AuditJobID: "not-a-uuid"}
	if _, err := svc.IngestPrediction(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for a bad audit id, got %v", err)
	}
}

// --- error propagation ---------------------------------------------------

func TestRegisterPropagatesStoreError(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{err: errs.New(errs.KindUnavailable, "storage.put", "down")})
	req := model.RegisterModelRequest{
		ModelName: model.ModelFraud, Version: "v", Manifest: json.RawMessage(`{}`),
		ModelFileName: "model.txt", ModelFileB64: base64.StdEncoding.EncodeToString([]byte("x")),
		ValidationReport: passingReport(), DataFloorCounts: fraudFloor(),
	}
	if _, err := svc.RegisterModel(context.Background(), req); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("want the store error surfaced, got %v", err)
	}
}

func TestRecordFeatureRowPropagatesRepoError(t *testing.T) {
	repo := &fakeRepo{upsertErr: errors.New("db down")}
	svc := adminSvc(repo, &fakeStore{})
	capture := contract.FeatureCapture{
		AuditJobID: uuid.New(),
		Snapshots:  []connector.Snapshot{{Platform: connector.PlatformInstagram, Followers: 1}},
		Fraud:      contract.FraudSignal{Present: true},
	}
	if err := svc.RecordFeatureRow(context.Background(), capture); err == nil {
		t.Fatal("expected the repository error to surface (the caller decides it is non-fatal)")
	}
}
