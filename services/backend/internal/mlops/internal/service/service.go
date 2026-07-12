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
	SetFraudLabel(ctx context.Context, auditJobID uuid.UUID, label bool, source string) error
	// ListFeatureRows returns feature rows oldest-first for the trainer's export.
	ListFeatureRows(ctx context.Context, filter model.FeatureRowFilter) ([]model.FeatureRow, error)

	// RegisterChallenger records a challenger for its model, demoting any existing
	// challenger of that model to 'rejected' first. Idempotent on (model, version).
	RegisterChallenger(ctx context.Context, mv model.Version) (model.Version, error)
	// GetModelVersion loads one registered version. found is false, no error, when
	// it does not exist.
	GetModelVersion(ctx context.Context, modelName, version string) (mv model.Version, found bool, err error)
	// PromoteVersion flips roles in a single transaction: target -> champion,
	// previous champion -> archived, any other challenger -> archived.
	PromoteVersion(ctx context.Context, modelName, version string) (model.PromotionResult, error)

	// ListCanaries returns a model's canaries, optionally only the active ones.
	ListCanaries(ctx context.Context, modelName string, activeOnly bool) ([]model.Canary, error)
	// CreateCanary inserts one manually-verified ground-truth canary.
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
	reasons := evaluateQuality(capture.Fraud, vec, primary)

	features, err := vec.Marshal()
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "mlops.feature_encode", "could not encode the feature vector")
	}

	row := model.FeatureRow{
		AuditJobID:            capture.AuditJobID,
		InfluencerID:          capture.InfluencerID,
		Platform:              string(primary.Platform),
		Features:              features,
		ReachLabel:            capture.ReachLabel,
		QualityOK:             len(reasons) == 0,
		QualityReasons:        reasons,
		ModelVersionAtCapture: capture.Fraud.ModelVersion,
		VerificationTier:      capture.VerificationTier,
		CapturedAt:            capturedAt,
	}
	if capture.ReachLabel != nil {
		src := contract.ReachLabelSourceInsights
		row.ReachLabelSource = &src
	}
	return s.repo.UpsertFeatureRow(ctx, row)
}

// SetFraudLabel backfills the supervised fraud target on a captured feature row
// when a dispute is decided. It is a no-op when no row exists for the audit. The
// composition root adapts it onto an admin TrainingLabelSink port; the call is
// non-fatal to the dispute resolution.
func (s *Service) SetFraudLabel(ctx context.Context, auditJobID uuid.UUID, label bool, source contract.FraudLabelSource) error {
	if !source.Valid() {
		return errs.New(errs.KindInvalid, "mlops.invalid_label_source", "fraud label source is not recognised")
	}
	return s.repo.SetFraudLabel(ctx, auditJobID, label, string(source))
}

// ExportFeatureRows returns the feature rows the trainer reads, oldest-first for a
// stable temporal split. quality=ok (the default) restricts to clean rows; the
// limit is clamped to the export ceiling. It is admin-only.
func (s *Service) ExportFeatureRows(ctx context.Context, req model.FeatureRowQuery) (model.FeatureRowExportResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.FeatureRowExportResponse{}, err
	}

	filter := model.FeatureRowFilter{
		Since:       req.Since,
		QualityOnly: req.Quality != "all",
		Limit:       clampLimit(req.Limit),
	}
	rows, err := s.repo.ListFeatureRows(ctx, filter)
	if err != nil {
		return model.FeatureRowExportResponse{}, err
	}

	items := make([]model.FeatureRowItem, 0, len(rows))
	for _, r := range rows {
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

	isRollback := mv.Role == model.RoleArchived
	if err := validatePromotable(mv, isRollback, req.OverrideShadow); err != nil {
		return model.PromoteModelResponse{}, err
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

// CreateCanary inserts one manually-verified ground-truth canary from a real
// audited account. It is admin-only. A fraud canary must carry an expected label;
// a reach canary must carry a reach band — never both, never neither.
func (s *Service) CreateCanary(ctx context.Context, req model.CreateCanaryRequest) (model.CanaryResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.CanaryResponse{}, err
	}
	if !model.ValidModelName(req.ModelName) {
		return model.CanaryResponse{}, errInvalidModelName()
	}
	if err := validateCanaryGroundTruth(req); err != nil {
		return model.CanaryResponse{}, err
	}

	c, err := s.repo.CreateCanary(ctx, model.Canary{
		ModelName:        req.ModelName,
		Label:            req.Label,
		Features:         req.Features,
		ExpectedLabel:    req.ExpectedLabel,
		ExpectedReachMin: req.ExpectedReachMin,
		ExpectedReachMax: req.ExpectedReachMax,
		Source:           req.Source,
		Active:           true,
	})
	if err != nil {
		return model.CanaryResponse{}, err
	}
	return model.CanaryResponse{Canary: model.ToCanaryItem(c)}, nil
}

// IngestPrediction appends one shadow score to the prediction log. It is gated by
// the ml service token, not the admin guard, because the caller is the ml server.
// It is best-effort and append-only.
func (s *Service) IngestPrediction(ctx context.Context, req model.PredictionLogRequest) (model.PredictionLogResponse, error) {
	if err := s.svcAuth.RequireService(ctx); err != nil {
		return model.PredictionLogResponse{}, err
	}
	if !model.ValidModelName(req.ModelName) {
		return model.PredictionLogResponse{}, errInvalidModelName()
	}

	entry := model.PredictionLog{
		ModelName:         req.ModelName,
		ChampionVersion:   req.ChampionVersion,
		ChampionScore:     req.ChampionScore,
		ChallengerVersion: req.ChallengerVersion,
		ChallengerScore:   req.ChallengerScore,
		FeaturesHash:      req.FeaturesHash,
	}
	if req.AuditJobID != nil && *req.AuditJobID != "" {
		id, err := uuid.Parse(*req.AuditJobID)
		if err != nil {
			return model.PredictionLogResponse{}, errs.New(errs.KindInvalid, "mlops.invalid_audit_id", "audit_job_id is not a valid uuid")
		}
		entry.AuditJobID = &id
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
// truth its model kind needs: a fraud canary an expected label, a reach canary a
// reach band. This keeps the canary set honest — a canary with no verifiable
// expectation could never be a must-pass check.
func validateCanaryGroundTruth(req model.CreateCanaryRequest) error {
	hasReachBand := req.ExpectedReachMin != nil || req.ExpectedReachMax != nil
	switch req.ModelName {
	case model.ModelFraud:
		if req.ExpectedLabel == nil {
			return errs.New(errs.KindInvalid, "mlops.canary_missing_label", "a fraud canary requires expected_label")
		}
		if hasReachBand {
			return errs.New(errs.KindInvalid, "mlops.canary_reach_on_fraud", "a fraud canary must not carry a reach band")
		}
	case model.ModelReach:
		if req.ExpectedReachMin == nil || req.ExpectedReachMax == nil {
			return errs.New(errs.KindInvalid, "mlops.canary_missing_reach", "a reach canary requires expected_reach_min and expected_reach_max")
		}
		if req.ExpectedLabel != nil {
			return errs.New(errs.KindInvalid, "mlops.canary_label_on_reach", "a reach canary must not carry an expected_label")
		}
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
