// Package repository is the mlops module's data-access layer. It owns every SQL
// statement against training_feature_row, ml_model_version, ml_canary_account,
// and ml_prediction_log, and maps rows to and from the module's domain types. It
// satisfies the service.Repository contract; the service depends only on that
// interface.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// featureRowColumns is the training_feature_row projection the export shares.
const featureRowColumns = "audit_job_id::text, influencer_id::text, platform, features_jsonb, " +
	"fraud_label, fraud_label_source, reach_label, reach_label_source, quality_ok, quality_reasons, " +
	"model_version_at_capture, verification_tier, captured_at"

// modelVersionColumns is the ml_model_version projection reads share.
const modelVersionColumns = "id::text, model_name, version, role, s3_key, manifest_jsonb, metrics_jsonb, " +
	"validation_report_jsonb, data_floor_counts, feature_snapshot_hash, feature_snapshot_watermark, " +
	"feature_row_count, created_at, promoted_at, archived_at"

// canaryColumns is the ml_canary_account projection reads share.
const canaryColumns = "id::text, model_name, label, features_jsonb, expected_label, " +
	"expected_reach_min, expected_reach_max, source, active, created_at"

// rowScanner is the read surface shared by pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// PostgresRepository is the pgx-backed service.Repository.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

var _ service.Repository = (*PostgresRepository)(nil)

// New builds a PostgresRepository over pool.
func New(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// --- feature store -------------------------------------------------------

// UpsertFeatureRow writes one feature-store row keyed on the audit job,
// overwriting the descriptive columns on a re-capture. fraud_label is never
// touched here (SetFraudLabel backfills it); reach_label is preserved when a
// re-capture carries none, so a later capture cannot wipe a real reach figure.
func (r *PostgresRepository) UpsertFeatureRow(ctx context.Context, row model.FeatureRow) error {
	const q = `INSERT INTO training_feature_row
		(audit_job_id, influencer_id, platform, features_jsonb, reach_label, reach_label_source,
		 quality_ok, quality_reasons, model_version_at_capture, verification_tier, captured_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (audit_job_id) DO UPDATE SET
			influencer_id            = EXCLUDED.influencer_id,
			platform                 = EXCLUDED.platform,
			features_jsonb           = EXCLUDED.features_jsonb,
			reach_label              = COALESCE(EXCLUDED.reach_label, training_feature_row.reach_label),
			reach_label_source       = COALESCE(EXCLUDED.reach_label_source, training_feature_row.reach_label_source),
			quality_ok               = EXCLUDED.quality_ok,
			quality_reasons          = EXCLUDED.quality_reasons,
			model_version_at_capture = EXCLUDED.model_version_at_capture,
			verification_tier        = EXCLUDED.verification_tier,
			captured_at              = EXCLUDED.captured_at`

	reasons := row.QualityReasons
	if reasons == nil {
		reasons = []string{}
	}
	_, err := r.pool.Exec(ctx, q,
		row.AuditJobID, row.InfluencerID, row.Platform, string(row.Features),
		row.ReachLabel, row.ReachLabelSource, row.QualityOK, reasons,
		row.ModelVersionAtCapture, row.VerificationTier, row.CapturedAt)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "mlops.feature_upsert_failed", "could not write the feature row")
	}
	return nil
}

// SetFraudLabel backfills the supervised fraud target on a captured row. When no
// row exists (the audit predates the feature store) it affects no rows and
// returns nil, so a dispute decision on an old audit is a harmless no-op.
func (r *PostgresRepository) SetFraudLabel(ctx context.Context, auditJobID uuid.UUID, label bool, source string) error {
	const q = "UPDATE training_feature_row SET fraud_label = $2, fraud_label_source = $3 WHERE audit_job_id = $1"
	if _, err := r.pool.Exec(ctx, q, auditJobID, label, source); err != nil {
		return errs.Wrap(err, errs.KindInternal, "mlops.label_backfill_failed", "could not backfill the fraud label")
	}
	return nil
}

// ListFeatureRows returns feature rows oldest-first for the trainer's export.
func (r *PostgresRepository) ListFeatureRows(ctx context.Context, filter model.FeatureRowFilter) ([]model.FeatureRow, error) {
	const q = "SELECT " + featureRowColumns + ` FROM training_feature_row
		WHERE ($1::timestamptz IS NULL OR captured_at >= $1)
		  AND (NOT $2::boolean OR quality_ok)
		ORDER BY captured_at ASC, audit_job_id ASC
		LIMIT $3`

	var since *time.Time
	if !filter.Since.IsZero() {
		since = &filter.Since
	}
	rows, err := r.pool.Query(ctx, q, since, filter.QualityOnly, filter.Limit)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "mlops.feature_list_failed", "could not list feature rows")
	}
	defer rows.Close()

	out := make([]model.FeatureRow, 0)
	for rows.Next() {
		row, scanErr := scanFeatureRow(rows)
		if scanErr != nil {
			return nil, errs.Wrap(scanErr, errs.KindInternal, "mlops.feature_list_failed", "could not list feature rows")
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "mlops.feature_list_failed", "could not list feature rows")
	}
	return out, nil
}

// --- model registry ------------------------------------------------------

// RegisterChallenger records a challenger for its model in a single transaction:
// any existing challenger of the model (a different version) is demoted to
// 'rejected' first, then the target is upserted as 'challenger'. Idempotent on
// (model_name, version): re-registering the same version updates its artifacts
// and evidence and keeps it the challenger.
func (r *PostgresRepository) RegisterChallenger(ctx context.Context, mv model.Version) (model.Version, error) {
	const demote = "UPDATE ml_model_version SET role = 'rejected' " +
		"WHERE model_name = $1 AND role = 'challenger' AND version <> $2"

	const upsert = `INSERT INTO ml_model_version
		(model_name, version, role, s3_key, manifest_jsonb, metrics_jsonb, validation_report_jsonb,
		 data_floor_counts, feature_snapshot_hash, feature_snapshot_watermark, feature_row_count)
		VALUES ($1, $2, 'challenger', $3, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb, $8, $9, $10)
		ON CONFLICT (model_name, version) DO UPDATE SET
			role                       = 'challenger',
			s3_key                     = EXCLUDED.s3_key,
			manifest_jsonb             = EXCLUDED.manifest_jsonb,
			metrics_jsonb              = EXCLUDED.metrics_jsonb,
			validation_report_jsonb    = EXCLUDED.validation_report_jsonb,
			data_floor_counts          = EXCLUDED.data_floor_counts,
			feature_snapshot_hash      = EXCLUDED.feature_snapshot_hash,
			feature_snapshot_watermark = EXCLUDED.feature_snapshot_watermark,
			feature_row_count          = EXCLUDED.feature_row_count
		RETURNING ` + modelVersionColumns

	var out model.Version
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, demote, mv.ModelName, mv.Version); err != nil {
			return errs.Wrap(err, errs.KindInternal, "mlops.register_failed", "could not demote the prior challenger")
		}
		v, scanErr := scanModelVersion(tx.QueryRow(ctx, upsert,
			mv.ModelName, mv.Version, mv.S3Key, string(mv.Manifest), string(mv.Metrics),
			string(mv.ValidationReport), string(mv.DataFloorCounts), mv.FeatureSnapshotHash,
			mv.FeatureSnapshotWatermark, mv.FeatureRowCount))
		if scanErr != nil {
			return errs.Wrap(scanErr, errs.KindInternal, "mlops.register_failed", "could not record the challenger")
		}
		out = v
		return nil
	})
	if err != nil {
		return model.Version{}, err
	}
	return out, nil
}

// GetModelVersion loads one registered version. found is false, no error, when it
// does not exist.
func (r *PostgresRepository) GetModelVersion(ctx context.Context, modelName, version string) (model.Version, bool, error) {
	const q = "SELECT " + modelVersionColumns + " FROM ml_model_version WHERE model_name = $1 AND version = $2"
	mv, err := scanModelVersion(r.pool.QueryRow(ctx, q, modelName, version))
	if err != nil {
		if notFound(err) {
			return model.Version{}, false, nil
		}
		return model.Version{}, false, errs.Wrap(err, errs.KindInternal, "mlops.version_read_failed", "could not read the model version")
	}
	return mv, true, nil
}

// PromoteVersion flips registry roles in a single transaction: the previous
// champion is archived, any other challenger of the model is archived, and the
// target becomes champion. It returns the new champion's manifest and S3 prefix
// for the CLI to materialise, plus the displaced champion's version (empty for a
// model's first champion). The caller has already validated the target's gates.
func (r *PostgresRepository) PromoteVersion(ctx context.Context, modelName, version string) (model.PromotionResult, error) {
	const archivePrevChampion = "UPDATE ml_model_version SET role = 'archived', archived_at = now() " +
		"WHERE model_name = $1 AND role = 'champion' AND version <> $2 RETURNING version"

	const archiveOtherChallengers = "UPDATE ml_model_version SET role = 'archived', archived_at = now() " +
		"WHERE model_name = $1 AND role = 'challenger' AND version <> $2"

	const promote = "UPDATE ml_model_version SET role = 'champion', promoted_at = now(), archived_at = NULL " +
		"WHERE model_name = $1 AND version = $2 RETURNING manifest_jsonb, s3_key, promoted_at"

	var result model.PromotionResult
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var prev *string
		err := tx.QueryRow(ctx, archivePrevChampion, modelName, version).Scan(&prev)
		if err != nil && !notFound(err) {
			return errs.Wrap(err, errs.KindInternal, "mlops.promote_failed", "could not archive the previous champion")
		}
		if prev != nil {
			result.PreviousChampionVersion = *prev
		}

		if _, err := tx.Exec(ctx, archiveOtherChallengers, modelName, version); err != nil {
			return errs.Wrap(err, errs.KindInternal, "mlops.promote_failed", "could not archive the remaining challenger")
		}

		var (
			manifest   []byte
			s3Key      string
			promotedAt time.Time
		)
		if err := tx.QueryRow(ctx, promote, modelName, version).Scan(&manifest, &s3Key, &promotedAt); err != nil {
			if notFound(err) {
				return errs.New(errs.KindNotFound, "mlops.version_not_found", "model version does not exist")
			}
			return errs.Wrap(err, errs.KindInternal, "mlops.promote_failed", "could not promote the target version")
		}
		result.ChampionVersion = version
		result.Manifest = manifest
		result.S3Key = s3Key
		result.PromotedAt = promotedAt
		return nil
	})
	if err != nil {
		return model.PromotionResult{}, err
	}
	return result, nil
}

// --- canaries ------------------------------------------------------------

// ListCanaries returns a model's canaries newest-first, optionally only the
// active ones.
func (r *PostgresRepository) ListCanaries(ctx context.Context, modelName string, activeOnly bool) ([]model.Canary, error) {
	const q = "SELECT " + canaryColumns + ` FROM ml_canary_account
		WHERE model_name = $1 AND (NOT $2::boolean OR active)
		ORDER BY created_at DESC, id DESC`

	rows, err := r.pool.Query(ctx, q, modelName, activeOnly)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "mlops.canary_list_failed", "could not list canaries")
	}
	defer rows.Close()

	out := make([]model.Canary, 0)
	for rows.Next() {
		c, scanErr := scanCanary(rows)
		if scanErr != nil {
			return nil, errs.Wrap(scanErr, errs.KindInternal, "mlops.canary_list_failed", "could not list canaries")
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "mlops.canary_list_failed", "could not list canaries")
	}
	return out, nil
}

// CreateCanary inserts one manually-verified ground-truth canary.
func (r *PostgresRepository) CreateCanary(ctx context.Context, c model.Canary) (model.Canary, error) {
	const q = `INSERT INTO ml_canary_account
		(model_name, label, features_jsonb, expected_label, expected_reach_min, expected_reach_max, source, active)
		VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7, $8)
		RETURNING ` + canaryColumns

	out, err := scanCanary(r.pool.QueryRow(ctx, q,
		c.ModelName, c.Label, string(c.Features), c.ExpectedLabel,
		c.ExpectedReachMin, c.ExpectedReachMax, c.Source, c.Active))
	if err != nil {
		return model.Canary{}, errs.Wrap(err, errs.KindInternal, "mlops.canary_create_failed", "could not create the canary")
	}
	return out, nil
}

// --- prediction log ------------------------------------------------------

// InsertPrediction appends one shadow score to the prediction log.
func (r *PostgresRepository) InsertPrediction(ctx context.Context, p model.PredictionLog) error {
	const q = `INSERT INTO ml_prediction_log
		(model_name, audit_job_id, champion_version, champion_score, challenger_version, challenger_score, features_hash, scored_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	if _, err := r.pool.Exec(ctx, q,
		p.ModelName, p.AuditJobID, p.ChampionVersion, p.ChampionScore,
		p.ChallengerVersion, p.ChallengerScore, p.FeaturesHash, p.ScoredAt); err != nil {
		return errs.Wrap(err, errs.KindInternal, "mlops.prediction_insert_failed", "could not append the shadow prediction")
	}
	return nil
}

// --- scanners ------------------------------------------------------------

// scanFeatureRow reads one training_feature_row into a domain FeatureRow.
func scanFeatureRow(row rowScanner) (model.FeatureRow, error) {
	var (
		r          model.FeatureRow
		auditJobID string
		influencer string
		features   []byte
	)
	if err := row.Scan(&auditJobID, &influencer, &r.Platform, &features,
		&r.FraudLabel, &r.FraudLabelSource, &r.ReachLabel, &r.ReachLabelSource,
		&r.QualityOK, &r.QualityReasons, &r.ModelVersionAtCapture, &r.VerificationTier, &r.CapturedAt); err != nil {
		return model.FeatureRow{}, err
	}
	id, err := uuid.Parse(auditJobID)
	if err != nil {
		return model.FeatureRow{}, err
	}
	inf, err := uuid.Parse(influencer)
	if err != nil {
		return model.FeatureRow{}, err
	}
	r.AuditJobID = id
	r.InfluencerID = inf
	r.Features = features
	return r, nil
}

// scanModelVersion reads one ml_model_version into a domain ModelVersion.
func scanModelVersion(row rowScanner) (model.Version, error) {
	var (
		mv               model.Version
		id               string
		manifest         []byte
		metrics          []byte
		validationReport []byte
		dataFloor        []byte
	)
	if err := row.Scan(&id, &mv.ModelName, &mv.Version, &mv.Role, &mv.S3Key,
		&manifest, &metrics, &validationReport, &dataFloor,
		&mv.FeatureSnapshotHash, &mv.FeatureSnapshotWatermark, &mv.FeatureRowCount,
		&mv.CreatedAt, &mv.PromotedAt, &mv.ArchivedAt); err != nil {
		return model.Version{}, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return model.Version{}, err
	}
	mv.ID = parsed
	mv.Manifest = manifest
	mv.Metrics = metrics
	mv.ValidationReport = validationReport
	mv.DataFloorCounts = dataFloor
	return mv, nil
}

// scanCanary reads one ml_canary_account into a domain Canary.
func scanCanary(row rowScanner) (model.Canary, error) {
	var (
		c        model.Canary
		id       string
		features []byte
	)
	if err := row.Scan(&id, &c.ModelName, &c.Label, &features, &c.ExpectedLabel,
		&c.ExpectedReachMin, &c.ExpectedReachMax, &c.Source, &c.Active, &c.CreatedAt); err != nil {
		return model.Canary{}, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return model.Canary{}, err
	}
	c.ID = parsed
	c.Features = features
	return c, nil
}
