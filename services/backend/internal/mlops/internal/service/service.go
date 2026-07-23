// Package service implements the mlops module's business logic: the data-quality
// filter and feature-store write per completed audit, the feature-row export the
// trainer reads, the challenger register / promote / rollback state machine over
// the model registry (with an S3 artifact write and a server-side re-check of the
// recorded gate report), the canary set, and the shadow prediction-log ingest.
//
// Every collaborator is reached through a port declared in internal/mlops/port
// (the admin guard, the ml service-token auth, and the object store), so this
// package imports no other business module. The repository is the sole
// data-access dependency and is declared here as a consumer-side interface the
// repository package satisfies.
package service

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
	"github.com/getnyx/influaudit/backend/internal/mlops/port"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

const (
	// defaultExportLimit caps the feature-row export when the caller names none.
	defaultExportLimit = 5000
	// maxExportLimit is the hard ceiling on an export page, so a single call
	// cannot pull the whole store.
	maxExportLimit = 50000
	// manifestObjectName / manifest content type and the model content type name
	// the two S3 objects the register endpoint writes under the version prefix.
	manifestObjectName = "manifest.json"
	manifestMIME       = "application/json"
	modelMIME          = "application/octet-stream"
)

// Repository is the mlops module's data-access contract. It is declared by the
// service (its consumer) and satisfied by the repository package.
type Repository interface {
	// UpsertFeatureRow writes (or overwrites) one feature-store row keyed on the
	// audit job. It never touches fraud_label, which SetFraudLabel backfills.
	UpsertFeatureRow(ctx context.Context, row model.FeatureRow) error
	// SetFraudLabel backfills the supervised fraud target on a captured row. It is
	// a no-op (no error) when no row exists for the audit (the audit predates the
	// feature store).
	SetFraudLabel(ctx context.Context, auditJobID uuid.UUID, label bool, source, evidence string) error
	// ListFeatureRows returns feature rows oldest-first for the trainer's export.
	ListFeatureRows(ctx context.Context, filter model.FeatureRowFilter) ([]model.FeatureRow, error)
	// GetFeatureRow loads one captured row by its audit job. found is false, no
	// error, when the audit has no row. It is the canary endpoint's source of the
	// frozen feature vector — the client never supplies one.
	GetFeatureRow(ctx context.Context, auditJobID uuid.UUID) (row model.FeatureRow, found bool, err error)

	// RegisterChallenger records a challenger for its model, demoting any existing
	// challenger of that model to 'rejected' first. Idempotent on (model, version).
	RegisterChallenger(ctx context.Context, mv model.Version) (model.Version, error)
	// GetModelVersion loads one registered version. found is false, no error, when
	// it does not exist.
	GetModelVersion(ctx context.Context, modelName, version string) (mv model.Version, found bool, err error)
	// HasChampion reports whether the model already has a serving champion. A
	// promotion into an empty registry is a FIRST champion and is held to a stricter
	// rule (it must have canaries to be recoverable from).
	HasChampion(ctx context.Context, modelName string) (bool, error)
	// PromoteVersion flips roles in a single transaction: target -> champion,
	// previous champion -> archived, any other challenger -> archived.
	PromoteVersion(ctx context.Context, modelName, version string) (model.PromotionResult, error)

	// ListCanaries returns a model's canaries, optionally only the active ones.
	ListCanaries(ctx context.Context, modelName string, activeOnly bool) ([]model.Canary, error)
	// CreateCanary inserts one ground-truth canary anchored to a real audit.
	CreateCanary(ctx context.Context, c model.Canary) (model.Canary, error)

	// InsertPrediction appends one shadow score to the prediction log.
	InsertPrediction(ctx context.Context, p model.PredictionLog) error
}

// Service is the wired mlops service. It satisfies the generated MLOpsService and
// additionally exposes the two in-process operations the composition root adapts
// onto audit / admin ports (RecordFeatureRow, SetFraudLabel).
type Service struct {
	repo    Repository
	guard   port.AdminGuard
	svcAuth port.ServiceAuth
	store   port.ArtifactStore
	now     func() time.Time
}

var _ MLOpsService = (*Service)(nil)

// New builds the mlops service over its repository and collaborator ports. The
// clock defaults to time.Now and is overridable by tests via the returned value.
func New(repo Repository, guard port.AdminGuard, svcAuth port.ServiceAuth, store port.ArtifactStore) *Service {
	return &Service{repo: repo, guard: guard, svcAuth: svcAuth, store: store, now: time.Now}
}

// RecordFeatureRow captures one completed audit as a feature-store row: it
// computes the frozen feature vector and the data-quality verdict, then upserts
// the row keyed on the audit job. A capture with no usable snapshot is a no-op.
// The caller (the audit orchestrator, via an app port adapter) treats a returned
// error as non-fatal — a feature-store write must never fail an audit.
func (s *Service) RecordFeatureRow(ctx context.Context, capture contract.FeatureCapture) error {
	primary, ok := primarySnapshot(capture.Snapshots)
	if !ok {
		return nil
	}

	capturedAt := capture.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = s.now().UTC()
	}

	vec := computeFeatureVector(capture, primary, capturedAt)
	reasons := evaluateQuality(capture, vec, primary)

	features, err := vec.Marshal()
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "mlops.feature_encode", "could not encode the feature vector")
	}

	row := model.FeatureRow{
		AuditJobID:            capture.AuditJobID,
		InfluencerID:          capture.InfluencerID,
		Platform:              string(primary.Platform),
		Features:              features,
		ReachOrganic:          capture.ReachOrganic,
		SnapshotSources:       snapshotSources(capture.Snapshots),
		QualityOK:             len(reasons) == 0,
		QualityReasons:        reasons,
		ModelVersionAtCapture: capture.Fraud.ModelVersion,
		VerificationTier:      capture.VerificationTier,
		CapturedAt:            capturedAt,
	}

	// The reach label is DERIVED from the snapshots the audit actually collected,
	// never taken from the caller (capture.ReachLabel is ignored): the column is
	// only worth storing if it is evidence, and a stamped-on constant is not. It is
	// stored only when the figure came from a live Instagram Graph pull AND the
	// capture states the reach is organic — an Insights reach that includes
	// ad-delivered reach would teach the model that ad spend is organic virality.
	if reach, source, ok := deriveReachLabel(capture.Snapshots); ok && isOrganic(capture.ReachOrganic) {
		src := string(source)
		row.ReachLabel = &reach
		row.ReachLabelSource = &src
	}
	return s.repo.UpsertFeatureRow(ctx, row)
}

// SetFraudLabel backfills the supervised fraud target on a captured feature row
// when a dispute is decided. It is a no-op when no row exists for the audit. The
// composition root adapts it onto an admin TrainingLabelSink port; the call is
// non-fatal to the dispute resolution.
func (s *Service) SetFraudLabel(ctx context.Context, auditJobID uuid.UUID, label bool, source contract.FraudLabelSource, evidence contract.FraudLabelEvidence) error {
	if !source.Valid() {
		return errs.New(errs.KindInvalid, "mlops.invalid_label_source", "fraud label source is not recognised")
	}
	// The evidence is REQUIRED, and an unrecognised one is rejected rather than
	// defaulted. A label whose basis we cannot name is a label we cannot train on,
	// and silently defaulting it to "observable" is precisely the laundering the
	// enum exists to prevent. (An honest adjudicator who saw nothing beyond the
	// heuristic records EvidenceHeuristicOnly, which is valid — and untrainable.)
	if !evidence.Valid() {
		return errs.New(errs.KindInvalid, "mlops.invalid_label_evidence",
			"fraud label evidence is not recognised")
	}
	return s.repo.SetFraudLabel(ctx, auditJobID, label, string(source), string(evidence))
}

// ExportFeatureRows returns the feature rows the trainer reads, oldest-first for a
// stable temporal split. quality=ok (the default) restricts to TRAINING-ELIGIBLE
// rows — which is not the same as quality_ok: a human-labelled row is ground truth
// and is never censored by the fraud-score-derived quality reason (see
// model.TrainingEligible). The limit is clamped to the export ceiling. It is
// admin-only.
func (s *Service) ExportFeatureRows(ctx context.Context, req model.FeatureRowQuery) (model.FeatureRowExportResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.FeatureRowExportResponse{}, err
	}

	trainableOnly := req.Quality != "all"
	filter := model.FeatureRowFilter{
		Since:         req.Since,
		TrainableOnly: trainableOnly,
		Limit:         clampLimit(req.Limit),
	}
	rows, err := s.repo.ListFeatureRows(ctx, filter)
	if err != nil {
		return model.FeatureRowExportResponse{}, err
	}

	items := make([]model.FeatureRowItem, 0, len(rows))
	for _, r := range rows {
		// The repository already applies the rule in SQL; re-applying it here keeps the
		// guarantee expressed in Go, where it is under test, and fails closed if the two
		// ever drift.
		if trainableOnly && !model.TrainingEligible(r.QualityReasons, r.FraudLabel, r.FraudLabelEvidence) {
			continue
		}
		items = append(items, model.ToFeatureRowItem(r))
	}
	return model.FeatureRowExportResponse{Count: len(items), Rows: items}, nil
}

// RegisterModel records a challenger: it decodes the model file, PUTs it and the
// manifest to S3 under the version prefix, and inserts the registry row as
// role='challenger' (demoting any prior challenger of the model). It is idempotent
// on (model_name, version) and admin-only. The endpoint only records — every gate
// is re-checked at promote.
func (s *Service) RegisterModel(ctx context.Context, req model.RegisterModelRequest) (model.RegisterModelResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.RegisterModelResponse{}, err
	}
	if !model.ValidModelName(req.ModelName) {
		return model.RegisterModelResponse{}, errInvalidModelName()
	}

	modelBytes, err := base64.StdEncoding.DecodeString(req.ModelFileB64)
	if err != nil {
		return model.RegisterModelResponse{}, errs.New(errs.KindInvalid, "mlops.model_file_invalid", "model_file_b64 is not valid base64")
	}

	prefix := artifactPrefix(req.ModelName, req.Version)
	if _, err := s.store.PutObject(ctx, prefix+req.ModelFileName, modelMIME, modelBytes); err != nil {
		return model.RegisterModelResponse{}, err
	}
	if _, err := s.store.PutObject(ctx, prefix+manifestObjectName, manifestMIME, req.Manifest); err != nil {
		return model.RegisterModelResponse{}, err
	}

	mv, err := s.repo.RegisterChallenger(ctx, model.Version{
		ModelName:                req.ModelName,
		Version:                  req.Version,
		Role:                     model.RoleChallenger,
		S3Key:                    prefix,
		Manifest:                 req.Manifest,
		Metrics:                  orEmptyJSON(req.Metrics),
		ValidationReport:         req.ValidationReport,
		DataFloorCounts:          req.DataFloorCounts,
		FeatureSnapshotHash:      req.FeatureSnapshot.ContentHash,
		FeatureSnapshotWatermark: req.FeatureSnapshot.MaxCapturedAt,
		FeatureRowCount:          req.FeatureSnapshot.RowCount,
	})
	if err != nil {
		return model.RegisterModelResponse{}, err
	}

	return model.RegisterModelResponse{
		ID:        mv.ID.String(),
		ModelName: mv.ModelName,
		Version:   mv.Version,
		Role:      mv.Role,
		S3Key:     mv.S3Key,
		CreatedAt: mv.CreatedAt,
	}, nil
}

// PromoteModel promotes a challenger — or rolls back to an archived former
// champion — after re-checking the recorded gate report server-side. Promoting a
// rejected version, or one whose report does not show the required gates passing,
// is a conflict; nothing is promoted on unproven data. It is admin-only.
func (s *Service) PromoteModel(ctx context.Context, version string, req model.PromoteModelRequest) (model.PromoteModelResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.PromoteModelResponse{}, err
	}
	if !model.ValidModelName(req.ModelName) {
		return model.PromoteModelResponse{}, errInvalidModelName()
	}

	mv, found, err := s.repo.GetModelVersion(ctx, req.ModelName, version)
	if err != nil {
		return model.PromoteModelResponse{}, err
	}
	if !found {
		return model.PromoteModelResponse{}, errs.New(errs.KindNotFound, "mlops.version_not_found", "model version does not exist")
	}

	switch mv.Role {
	case model.RoleChampion:
		// Already serving: a retried promotion is idempotent, no role flip.
		return championResponse(mv), nil
	case model.RoleRejected:
		return model.PromoteModelResponse{}, errs.New(errs.KindConflict, "mlops.version_rejected", "a rejected version cannot be promoted")
	}

	// A rollback waives gates only for a version that actually served as champion
	// before (promoted_at set). An archived version that never earned that — a
	// challenger superseded when a different version won — must re-validate its
	// stored gate report like any first promotion, so gates cannot be waived for a
	// model that never passed them.
	isRollback := mv.Role == model.RoleArchived && mv.PromotedAt != nil
	if !isRollback {
		canaries, err := s.repo.ListCanaries(ctx, req.ModelName, true)
		if err != nil {
			return model.PromoteModelResponse{}, err
		}
		hasChampion, err := s.repo.HasChampion(ctx, req.ModelName)
		if err != nil {
			return model.PromoteModelResponse{}, err
		}
		// THE FIRST CHAMPION IS THE ONE PROMOTION NOTHING CAN UNDO. There is no
		// previous champion to roll back to, and from then on every rollback
		// short-circuits the gates — so a first champion crowned on an empty canary set
		// makes every later mistake unrecoverable. It costs nothing today (the data
		// floor cannot clear anyway), and it is refused.
		if !hasChampion && len(canaries) == 0 {
			return model.PromoteModelResponse{}, errs.New(errs.KindConflict, "mlops.first_champion_requires_canaries",
				"a model's first champion cannot be promoted with an empty canary set")
		}
		if err := validatePromotable(mv, len(canaries)); err != nil {
			return model.PromoteModelResponse{}, err
		}
	}

	result, err := s.repo.PromoteVersion(ctx, req.ModelName, version)
	if err != nil {
		return model.PromoteModelResponse{}, err
	}
	return model.PromoteModelResponse{
		ModelName:               req.ModelName,
		ChampionVersion:         result.ChampionVersion,
		PreviousChampionVersion: result.PreviousChampionVersion,
		Manifest:                result.Manifest,
		S3Key:                   result.S3Key,
		PromotedAt:              result.PromotedAt,
	}, nil
}

// ListCanaries returns a model's canary set. It is admin-only.
func (s *Service) ListCanaries(ctx context.Context, req model.CanaryQuery) (model.CanaryListResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.CanaryListResponse{}, err
	}
	if !model.ValidModelName(req.ModelName) {
		return model.CanaryListResponse{}, errInvalidModelName()
	}

	activeOnly := req.HasActive && req.Active
	canaries, err := s.repo.ListCanaries(ctx, req.ModelName, activeOnly)
	if err != nil {
		return model.CanaryListResponse{}, err
	}

	items := make([]model.CanaryItem, 0, len(canaries))
	for _, c := range canaries {
		items = append(items, model.ToCanaryItem(c))
	}
	return model.CanaryListResponse{Count: len(items), Canaries: items}, nil
}

// CreateCanary inserts one ground-truth canary anchored to a REAL audit. It is
// admin-only.
//
// The canary set is the only artifact whose job is to catch a model that has
// learned to fabricate, so nothing about it may be hand-typed by the operator who
// also runs the model: the client names an audit, and the SERVER copies that
// audit's frozen feature vector out of the feature store. The audit's data path is
// checked too — an audit built on a creator-uploaded CSV is refused, because with
// Instagram gated on app review the CSV is the only Instagram path, and a
// hand-written export would otherwise launder straight through the audit pipeline
// into the canary set.
func (s *Service) CreateCanary(ctx context.Context, req model.CreateCanaryRequest) (model.CanaryResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.CanaryResponse{}, err
	}
	if !model.ValidModelName(req.ModelName) {
		return model.CanaryResponse{}, errInvalidModelName()
	}
	auditJobID, err := uuid.Parse(req.AuditJobID)
	if err != nil {
		return model.CanaryResponse{}, errs.New(errs.KindInvalid, "mlops.invalid_audit_id", "audit_job_id is not a valid uuid")
	}
	if err := validateCanaryGroundTruth(req); err != nil {
		return model.CanaryResponse{}, err
	}

	row, found, err := s.repo.GetFeatureRow(ctx, auditJobID)
	if err != nil {
		return model.CanaryResponse{}, err
	}
	if !found {
		return model.CanaryResponse{}, errs.New(errs.KindNotFound, "mlops.canary_audit_not_found",
			"that audit has no captured feature row, so there is no frozen vector to make a canary from")
	}
	if err := validateCanaryProvenance(req, row); err != nil {
		return model.CanaryResponse{}, err
	}

	c, err := s.repo.CreateCanary(ctx, model.Canary{
		ModelName:  req.ModelName,
		AuditJobID: auditJobID,
		Label:      req.Label,
		// The vector the SERVER froze at audit time, not one the caller sent.
		Features:         row.Features,
		ExpectedLabel:    req.ExpectedLabel,
		ExpectedReachMin: req.ExpectedReachMin,
		ExpectedReachMax: req.ExpectedReachMax,
		ProvenanceKind:   req.ProvenanceKind,
		Active:           true,
	})
	if err != nil {
		return model.CanaryResponse{}, err
	}
	return model.CanaryResponse{Canary: model.ToCanaryItem(c)}, nil
}

// IngestPrediction appends one shadow score to the prediction log. It is gated by
// the ml service token, not the admin guard, because the caller is the ml server.
// It is append-only.
//
// audit_job_id is REQUIRED and an ingest without it is rejected. features_hash is
// a one-way sha256: it can prove two scores saw the same vector, but it can never
// lead back to the account. A prediction logged without its audit job can
// therefore never be joined to an outcome — the shadow window would be
// permanently unresolvable, and the shadow gate could never become a real
// label-joined arbiter.
func (s *Service) IngestPrediction(ctx context.Context, req model.PredictionLogRequest) (model.PredictionLogResponse, error) {
	if err := s.svcAuth.RequireService(ctx); err != nil {
		return model.PredictionLogResponse{}, err
	}
	if !model.ValidModelName(req.ModelName) {
		return model.PredictionLogResponse{}, errInvalidModelName()
	}
	auditJobID, err := uuid.Parse(req.AuditJobID)
	if err != nil {
		return model.PredictionLogResponse{}, errs.New(errs.KindInvalid, "mlops.invalid_audit_id",
			"audit_job_id is required and must be a valid uuid: a prediction that cannot be "+
				"joined back to its audit can never be resolved against an outcome")
	}

	entry := model.PredictionLog{
		ModelName:         req.ModelName,
		AuditJobID:        auditJobID,
		ChampionVersion:   req.ChampionVersion,
		ChampionScore:     req.ChampionScore,
		ChallengerVersion: req.ChallengerVersion,
		ChallengerScore:   req.ChallengerScore,
		FeaturesHash:      req.FeaturesHash,
	}
	if req.ScoredAt != nil {
		entry.ScoredAt = *req.ScoredAt
	} else {
		entry.ScoredAt = s.now().UTC()
	}

	if err := s.repo.InsertPrediction(ctx, entry); err != nil {
		return model.PredictionLogResponse{}, err
	}
	return model.PredictionLogResponse{Accepted: true}, nil
}

// validateCanaryGroundTruth enforces that a canary carries exactly the ground
// truth its model kind needs — a fraud canary an expected label, a reach canary a
// reach band — and that its provenance_kind is a member of the closed set AND
// actually supports the expectation it is attached to.
//
// The asymmetry is deliberate and is the whole point. A POSITIVE fraud canary must
// rest on something someone could observe: the platform acted, the creator
// admitted it, the vendor issued a receipt, or we bought the followers ourselves.
// A NEGATIVE fraud canary can rest on nothing of the sort, because no observation
// establishes the ABSENCE of a purchase — so it may only be 'presumed_clean', and
// there is no way to spell "verified clean" in this API.
func validateCanaryGroundTruth(req model.CreateCanaryRequest) error {
	if !model.ValidProvenanceKind(req.ProvenanceKind) {
		return errs.New(errs.KindInvalid, "mlops.canary_provenance_unknown",
			"provenance_kind must be one of: oauth_insights_measured, platform_enforcement_record, "+
				"creator_admission, vendor_receipt, operator_constructed_positive, presumed_clean")
	}

	hasReachBand := req.ExpectedReachMin != nil || req.ExpectedReachMax != nil
	switch req.ModelName {
	case model.ModelFraud:
		if req.ExpectedLabel == nil {
			return errs.New(errs.KindInvalid, "mlops.canary_missing_label", "a fraud canary requires expected_label")
		}
		if hasReachBand {
			return errs.New(errs.KindInvalid, "mlops.canary_reach_on_fraud", "a fraud canary must not carry a reach band")
		}
		if *req.ExpectedLabel && !model.ProvesFraudPositive(req.ProvenanceKind) {
			return errs.New(errs.KindInvalid, "mlops.canary_positive_without_evidence",
				"a fraudulent canary requires observable evidence: platform_enforcement_record, "+
					"creator_admission, vendor_receipt, or operator_constructed_positive")
		}
		if !*req.ExpectedLabel && req.ProvenanceKind != model.ProvenancePresumedClean {
			return errs.New(errs.KindInvalid, "mlops.canary_verified_negative",
				"no evidence proves the ABSENCE of fraud: a clean canary must be presumed_clean")
		}
	case model.ModelReach:
		if req.ExpectedReachMin == nil || req.ExpectedReachMax == nil {
			return errs.New(errs.KindInvalid, "mlops.canary_missing_reach", "a reach canary requires expected_reach_min and expected_reach_max")
		}
		if req.ExpectedLabel != nil {
			return errs.New(errs.KindInvalid, "mlops.canary_label_on_reach", "a reach canary must not carry an expected_label")
		}
		if req.ProvenanceKind != model.ProvenanceOAuthInsightsMeasured {
			return errs.New(errs.KindInvalid, "mlops.canary_reach_not_measured",
				"a reach canary's band must come from a measured OAuth Insights figure (oauth_insights_measured)")
		}
	}
	return nil
}

// validateCanaryProvenance checks the SOURCE AUDIT behind a canary, which is the
// half of the provenance the client cannot assert: what data path produced the
// numbers, and (for a reach canary) whether a measured reach figure actually
// exists on the row.
func validateCanaryProvenance(req model.CreateCanaryRequest, row model.FeatureRow) error {
	// A row captured before snapshot_sources existed carries no record of what it
	// was built from. Unknown provenance is not clean provenance.
	if len(row.SnapshotSources) == 0 {
		return errs.New(errs.KindConflict, "mlops.canary_source_unknown",
			"that audit does not record which data paths produced it, so it cannot back a canary")
	}
	for _, src := range row.SnapshotSources {
		if src == string(connector.SourceCSVUpload) {
			return errs.New(errs.KindConflict, "mlops.canary_source_csv",
				"that audit includes a creator-uploaded CSV snapshot: an uploaded export is a "+
					"self-reported number, and a canary may not be built from one")
		}
	}

	if req.ModelName != model.ModelReach {
		return nil
	}
	// A reach canary's band asserts a measured figure. The row must actually carry
	// one — from a live Instagram Graph pull, organic — and the band must contain it,
	// or the canary contradicts its own evidence.
	if row.ReachLabel == nil || row.ReachLabelSource == nil ||
		!contract.ReachLabelSource(*row.ReachLabelSource).Valid() {
		return errs.New(errs.KindConflict, "mlops.canary_reach_unmeasured",
			"that audit carries no measured organic reach figure, so it cannot back a reach canary")
	}
	if *row.ReachLabel < *req.ExpectedReachMin || *row.ReachLabel > *req.ExpectedReachMax {
		return errs.New(errs.KindInvalid, "mlops.canary_band_excludes_measurement",
			"the reach band does not contain the audit's own measured reach figure")
	}
	return nil
}

// championResponse builds a promote response for a version that is already the
// champion, so a retried promotion returns the current state without a role flip.
func championResponse(mv model.Version) model.PromoteModelResponse {
	resp := model.PromoteModelResponse{
		ModelName:       mv.ModelName,
		ChampionVersion: mv.Version,
		Manifest:        mv.Manifest,
		S3Key:           mv.S3Key,
	}
	if mv.PromotedAt != nil {
		resp.PromotedAt = *mv.PromotedAt
	}
	return resp
}

// artifactPrefix is the S3 key prefix for a model version's artifacts.
func artifactPrefix(modelName, version string) string {
	return "ml-models/" + modelName + "/" + version + "/"
}

// clampLimit applies the export default and ceiling.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultExportLimit
	}
	if limit > maxExportLimit {
		return maxExportLimit
	}
	return limit
}

// orEmptyJSON returns raw, or an empty JSON object when raw is nil, so an
// optional jsonb column is never written NULL (the schema is NOT NULL).
func orEmptyJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

// errInvalidModelName is the single invalid-input error for an unrecognised
// model name.
func errInvalidModelName() error {
	return errs.New(errs.KindInvalid, "mlops.invalid_model_name", "model_name must be 'fraud' or 'reach'")
}
