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
	upserted   *model.FeatureRow
	labelCall  *labelCall
	rows       []model.FeatureRow
	registered *model.Version
	getResult  model.Version
	getFound   bool
	promoteArg *promoteArg
	promoteRes model.PromotionResult
	canaries   []model.Canary
	createdCan *model.Canary
	prediction *model.PredictionLog

	upsertErr, labelErr, listErr, registerErr, getErr, promoteErr, canaryListErr, canaryCreateErr, predictErr error
}

type labelCall struct {
	auditJobID uuid.UUID
	label      bool
	source     string
}

type promoteArg struct {
	modelName string
	version   string
}

func (r *fakeRepo) UpsertFeatureRow(_ context.Context, row model.FeatureRow) error {
	r.upserted = &row
	return r.upsertErr
}

func (r *fakeRepo) SetFraudLabel(_ context.Context, id uuid.UUID, label bool, source string) error {
	r.labelCall = &labelCall{auditJobID: id, label: label, source: source}
	return r.labelErr
}

func (r *fakeRepo) ListFeatureRows(_ context.Context, _ model.FeatureRowFilter) ([]model.FeatureRow, error) {
	return r.rows, r.listErr
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
		`"g3_canary":{"pass":false,"skipped":true},"g4_vs_champion":{"pass":true}}`)
}

// fraudFloor is a data-floor-counts blob that meets the fraud per-class floor.
func fraudFloor() json.RawMessage {
	return json.RawMessage(`{"positive":61,"negative":74,"floor":50}`)
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
		Fraud:            contract.FraudSignal{Present: true, FakeFollowerRate: 0.04, ModelVersion: "lgbm-abc"},
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

func TestRecordFeatureRowStoresReachLabelSourceOnlyWhenPresent(t *testing.T) {
	reach := int64(15234)
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	capture := contract.FeatureCapture{
		AuditJobID: uuid.New(),
		Snapshots:  []connector.Snapshot{{Platform: connector.PlatformInstagram, Followers: 100}},
		Fraud:      contract.FraudSignal{Present: true},
		ReachLabel: &reach,
	}
	if err := svc.RecordFeatureRow(context.Background(), capture); err != nil {
		t.Fatalf("RecordFeatureRow: %v", err)
	}
	if repo.upserted.ReachLabel == nil || *repo.upserted.ReachLabel != reach {
		t.Fatalf("reach label not stored: %+v", repo.upserted.ReachLabel)
	}
	if repo.upserted.ReachLabelSource == nil || *repo.upserted.ReachLabelSource != contract.ReachLabelSourceInsights {
		t.Fatalf("reach label source must be set when reach is present: %+v", repo.upserted.ReachLabelSource)
	}
}

// --- SetFraudLabel -------------------------------------------------------

func TestSetFraudLabelPassesThroughToRepo(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	id := uuid.New()
	if err := svc.SetFraudLabel(context.Background(), id, true, contract.LabelSourceDisputeRejected); err != nil {
		t.Fatalf("SetFraudLabel: %v", err)
	}
	if repo.labelCall == nil || repo.labelCall.auditJobID != id || !repo.labelCall.label ||
		repo.labelCall.source != string(contract.LabelSourceDisputeRejected) {
		t.Fatalf("label not backfilled as expected: %+v", repo.labelCall)
	}
}

func TestSetFraudLabelRejectsUnknownSource(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	if err := svc.SetFraudLabel(context.Background(), uuid.New(), true, contract.FraudLabelSource("guess")); errs.KindOf(err) != errs.KindInvalid {
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
		getFound: true,
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
	repo := &fakeRepo{getFound: true, getResult: model.Version{
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
	repo := &fakeRepo{getFound: true, getResult: model.Version{
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
		getFound: true,
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

// --- Canaries ------------------------------------------------------------

func TestCreateFraudCanaryRequiresExpectedLabel(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	req := model.CreateCanaryRequest{ModelName: model.ModelFraud, Label: "l", Features: json.RawMessage(`{}`), Source: "s"}
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("a fraud canary without an expected label must be invalid, got %v", err)
	}
}

func TestCreateReachCanaryRequiresBand(t *testing.T) {
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	req := model.CreateCanaryRequest{ModelName: model.ModelReach, Label: "l", Features: json.RawMessage(`{}`), Source: "s"}
	if _, err := svc.CreateCanary(context.Background(), req); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("a reach canary without a band must be invalid, got %v", err)
	}
}

func TestCreateFraudCanaryStored(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	label := true
	req := model.CreateCanaryRequest{
		ModelName: model.ModelFraud, Label: "known bought-follower account",
		Features: json.RawMessage(`{"fake_follower_rate":0.6}`), ExpectedLabel: &label, Source: "manual audit",
	}
	resp, err := svc.CreateCanary(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateCanary: %v", err)
	}
	if repo.createdCan == nil || !repo.createdCan.Active || repo.createdCan.ExpectedLabel == nil || !*repo.createdCan.ExpectedLabel {
		t.Fatalf("canary not stored as active with its label: %+v", repo.createdCan)
	}
	if resp.Canary.ModelName != model.ModelFraud {
		t.Fatalf("canary response wrong: %+v", resp)
	}
}

// --- IngestPrediction ----------------------------------------------------

func TestIngestPredictionRequiresServiceToken(t *testing.T) {
	svc := New(&fakeRepo{}, fakeGuard{id: adminID()}, fakeServiceAuth{err: errs.New(errs.KindUnauthorized, "x", "y")}, &fakeStore{})
	req := model.PredictionLogRequest{ModelName: model.ModelFraud, ChampionVersion: "v", FeaturesHash: "h"}
	if _, err := svc.IngestPrediction(context.Background(), req); errs.KindOf(err) != errs.KindUnauthorized {
		t.Fatalf("ingest must require the service token, got %v", err)
	}
}

func TestIngestPredictionAppends(t *testing.T) {
	repo := &fakeRepo{}
	svc := adminSvc(repo, &fakeStore{})
	challenger := "lgbm-new"
	challengerScore := 58.1
	req := model.PredictionLogRequest{
		ModelName: model.ModelFraud, ChampionVersion: "lgbm-old", ChampionScore: 62.4,
		ChallengerVersion: &challenger, ChallengerScore: &challengerScore, FeaturesHash: "sha256:abc",
	}
	resp, err := svc.IngestPrediction(context.Background(), req)
	if err != nil {
		t.Fatalf("IngestPrediction: %v", err)
	}
	if !resp.Accepted || repo.prediction == nil {
		t.Fatalf("prediction not appended: %+v", repo.prediction)
	}
	if repo.prediction.ChampionScore != 62.4 || repo.prediction.ChallengerVersion == nil || *repo.prediction.ChallengerVersion != challenger {
		t.Fatalf("prediction mapped wrong: %+v", repo.prediction)
	}
	if repo.prediction.ScoredAt.IsZero() {
		t.Fatal("scored_at must default to now when unset")
	}
}

func TestIngestPredictionRejectsBadAuditID(t *testing.T) {
	bad := "not-a-uuid"
	svc := adminSvc(&fakeRepo{}, &fakeStore{})
	req := model.PredictionLogRequest{ModelName: model.ModelFraud, ChampionVersion: "v", FeaturesHash: "h", AuditJobID: &bad}
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
