package model

import (
	"encoding/json"
	"time"
)

// --- feature-row export (GET /admin/mlops/feature-rows) ------------------

// FeatureRowQuery is the parsed query string of the feature-row export. The
// handler binds ?since=<rfc3339>&quality=ok|all&limit=<n>; the service defaults
// (quality=ok, limit=5000) are applied when a field is unset.
type FeatureRowQuery struct {
	Since   time.Time
	Quality string // "ok" (default) | "all"
	Limit   int
}

// FeatureRowExportResponse is the export payload the trainer reads. Rows is
// ordered oldest-first for a stable temporal split.
type FeatureRowExportResponse struct {
	Count int              `json:"count"`
	Rows  []FeatureRowItem `json:"rows"`
}

// FeatureRowItem is one exported feature row. Features is the stored vector
// verbatim (JSON nulls preserved). The nil-able label fields are omitted when
// absent so the trainer never reads a fabricated value.
type FeatureRowItem struct {
	AuditJobID            string          `json:"audit_job_id"`
	InfluencerID          string          `json:"influencer_id"`
	Platform              string          `json:"platform"`
	Features              json.RawMessage `json:"features"`
	FraudLabel            *bool           `json:"fraud_label"`
	FraudLabelSource      string          `json:"fraud_label_source,omitempty"`
	ReachLabel            *int64          `json:"reach_label"`
	ReachLabelSource      string          `json:"reach_label_source,omitempty"`
	QualityOK             bool            `json:"quality_ok"`
	QualityReasons        []string        `json:"quality_reasons"`
	ModelVersionAtCapture string          `json:"model_version_at_capture"`
	VerificationTier      string          `json:"verification_tier"`
	CapturedAt            time.Time       `json:"captured_at"`
}

// --- register (POST /admin/mlops/models) ---------------------------------

// FeatureSnapshotRef pins the exact feature rows a challenger was trained on, for
// reproducibility: how many, the newest captured_at that was eligible, and a
// content hash over the ordered tuples.
type FeatureSnapshotRef struct {
	RowCount      int       `json:"row_count"`
	MaxCapturedAt time.Time `json:"max_captured_at"`
	ContentHash   string    `json:"content_hash"`
}

// RegisterModelRequest is the challenger the trainer registers. The backend
// decodes ModelFileB64, PUTs the model file and manifest to S3, and inserts the
// row as role='challenger'. Manifest, Metrics, ValidationReport, and
// DataFloorCounts are stored verbatim as jsonb; the promote endpoint later
// re-checks ValidationReport and DataFloorCounts.
type RegisterModelRequest struct {
	ModelName        string             `json:"model_name" binding:"required"`
	Version          string             `json:"version" binding:"required"`
	Manifest         json.RawMessage    `json:"manifest" binding:"required"`
	ModelFileName    string             `json:"model_file_name" binding:"required"`
	ModelFileB64     string             `json:"model_file_b64" binding:"required"`
	Metrics          json.RawMessage    `json:"metrics"`
	ValidationReport json.RawMessage    `json:"validation_report" binding:"required"`
	FeatureSnapshot  FeatureSnapshotRef `json:"feature_snapshot"`
	DataFloorCounts  json.RawMessage    `json:"data_floor_counts" binding:"required"`
}

// RegisterModelResponse confirms a recorded challenger.
type RegisterModelResponse struct {
	ID        string    `json:"id"`
	ModelName string    `json:"model_name"`
	Version   string    `json:"version"`
	Role      string    `json:"role"`
	S3Key     string    `json:"s3_key"`
	CreatedAt time.Time `json:"created_at"`
}

// --- promote / rollback (POST /admin/mlops/models/:version/promote) ------

// PromoteModelRequest promotes (or rolls back to) a version. OverrideShadow
// waives the shadow-gate requirement for an emergency promotion; a rollback (the
// target is an archived former champion) waives it unconditionally.
type PromoteModelRequest struct {
	ModelName      string `json:"model_name" binding:"required"`
	Reason         string `json:"reason"`
	OverrideShadow bool   `json:"override_shadow"`
}

// PromoteModelResponse returns the new champion and the version it displaced,
// plus the manifest + S3 prefix the CLI materialises into the serving artifact
// directory.
type PromoteModelResponse struct {
	ModelName               string          `json:"model_name"`
	ChampionVersion         string          `json:"champion_version"`
	PreviousChampionVersion string          `json:"previous_champion_version,omitempty"`
	Manifest                json.RawMessage `json:"manifest"`
	S3Key                   string          `json:"s3_key"`
	PromotedAt              time.Time       `json:"promoted_at"`
}

// --- canaries (GET/POST /admin/mlops/canaries) ---------------------------

// CanaryQuery is the parsed query string of the canary list. HasActive
// distinguishes an unset ?active from an explicit ?active=false.
type CanaryQuery struct {
	ModelName string
	Active    bool
	HasActive bool
}

// CanaryListResponse is the canary list payload.
type CanaryListResponse struct {
	Count    int          `json:"count"`
	Canaries []CanaryItem `json:"canaries"`
}

// CanaryItem is one canary in the list / create response.
type CanaryItem struct {
	ID               string          `json:"id"`
	ModelName        string          `json:"model_name"`
	Label            string          `json:"label"`
	Features         json.RawMessage `json:"features"`
	ExpectedLabel    *bool           `json:"expected_label,omitempty"`
	ExpectedReachMin *int64          `json:"expected_reach_min,omitempty"`
	ExpectedReachMax *int64          `json:"expected_reach_max,omitempty"`
	Source           string          `json:"source"`
	Active           bool            `json:"active"`
	CreatedAt        time.Time       `json:"created_at"`
}

// CanaryResponse is the single-canary create response.
type CanaryResponse struct {
	Canary CanaryItem `json:"canary"`
}

// CreateCanaryRequest inserts one manually-verified ground-truth canary. Features
// is the frozen feature vector for the account; ExpectedLabel is set for a fraud
// canary, the reach band for a reach canary.
type CreateCanaryRequest struct {
	ModelName        string          `json:"model_name" binding:"required"`
	Label            string          `json:"label" binding:"required"`
	Features         json.RawMessage `json:"features" binding:"required"`
	ExpectedLabel    *bool           `json:"expected_label"`
	ExpectedReachMin *int64          `json:"expected_reach_min"`
	ExpectedReachMax *int64          `json:"expected_reach_max"`
	Source           string          `json:"source" binding:"required"`
}

// --- prediction-log ingest (POST /ml/predictions) -----------------------

// PredictionLogRequest is one shadow score the ml server logs. AuditJobID and
// ChallengerVersion/ChallengerScore are nil-able; ScoredAt defaults to now when
// unset.
type PredictionLogRequest struct {
	ModelName         string     `json:"model_name" binding:"required"`
	AuditJobID        *string    `json:"audit_job_id"`
	ChampionVersion   string     `json:"champion_version" binding:"required"`
	ChampionScore     float64    `json:"champion_score"`
	ChallengerVersion *string    `json:"challenger_version"`
	ChallengerScore   *float64   `json:"challenger_score"`
	FeaturesHash      string     `json:"features_hash" binding:"required"`
	ScoredAt          *time.Time `json:"scored_at"`
}

// PredictionLogResponse acknowledges an accepted shadow score.
type PredictionLogResponse struct {
	Accepted bool `json:"accepted"`
}
