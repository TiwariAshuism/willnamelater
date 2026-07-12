// Package model holds the mlops module's domain types (the feature-store row,
// the registry model version, the canary, and the prediction-log entry) and the
// request/response DTOs its HTTP surface exchanges. The domain types are what the
// repository reads and writes; the DTOs are what the handler binds and renders.
package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Model names key ml_model_version.model_name and ml_canary_account.model_name.
// They are the two independent retraining targets: the fraud model trains on the
// six fraud keys, the reach model on the full descriptive vector.
const (
	ModelFraud = "fraud"
	ModelReach = "reach"
)

// ValidModelName reports whether name is one of the two recognised model names.
func ValidModelName(name string) bool {
	return name == ModelFraud || name == ModelReach
}

// Registry roles key ml_model_version.role. At most one champion and one
// challenger exist per model at a time (enforced by partial unique indexes).
const (
	// RoleChampion is the serving model whose artifact the ml service reads.
	RoleChampion = "champion"
	// RoleChallenger is a newly registered model in offline validation / shadow.
	RoleChallenger = "challenger"
	// RoleArchived is a former champion (or superseded challenger) retained for
	// rollback and audit. Promoting an archived version is a rollback.
	RoleArchived = "archived"
	// RoleRejected is a challenger a gate failed. It is never promotable.
	RoleRejected = "rejected"
)

// FeatureRow is one row of the training_feature_row feature store. Features is
// the frozen feature vector as stored jsonb (an absent optional feature is JSON
// null, never zero-filled). FraudLabel and ReachLabel are nil until backfilled /
// captured; a row may carry neither.
type FeatureRow struct {
	AuditJobID            uuid.UUID
	InfluencerID          uuid.UUID
	Platform              string
	Features              json.RawMessage
	FraudLabel            *bool
	FraudLabelSource      *string
	ReachLabel            *int64
	ReachLabelSource      *string
	QualityOK             bool
	QualityReasons        []string
	ModelVersionAtCapture string
	VerificationTier      string
	CapturedAt            time.Time
}

// FeatureRowFilter bounds the feature-row export. Since lower-bounds captured_at
// when non-zero; QualityOnly restricts to quality_ok rows (the training default);
// Limit caps the result set.
type FeatureRowFilter struct {
	Since       time.Time
	QualityOnly bool
	Limit       int
}

// Version is one row of ml_model_version: a registered model's role, its S3
// artifact prefix, and the JSON evidence (manifest, metrics, gate report, data
// floor) that the register endpoint recorded and the promote endpoint re-checks.
type Version struct {
	ID                       uuid.UUID
	ModelName                string
	Version                  string
	Role                     string
	S3Key                    string
	Manifest                 json.RawMessage
	Metrics                  json.RawMessage
	ValidationReport         json.RawMessage
	DataFloorCounts          json.RawMessage
	FeatureSnapshotHash      string
	FeatureSnapshotWatermark time.Time
	FeatureRowCount          int
	CreatedAt                time.Time
	PromotedAt               *time.Time
	ArchivedAt               *time.Time
}

// PromotionResult is what a promote transaction produced: the new champion, the
// version it displaced (empty for a model's first champion), and the champion's
// manifest + S3 prefix for the CLI to materialise into the serving artifact dir.
type PromotionResult struct {
	ChampionVersion         string
	PreviousChampionVersion string
	Manifest                json.RawMessage
	S3Key                   string
	PromotedAt              time.Time
}

// Canary is one row of ml_canary_account: a manually-verified ground-truth
// account every challenger must score correctly. ExpectedLabel is set for fraud
// canaries, the reach band for reach canaries.
type Canary struct {
	ID               uuid.UUID
	ModelName        string
	Label            string
	Features         json.RawMessage
	ExpectedLabel    *bool
	ExpectedReachMin *int64
	ExpectedReachMax *int64
	Source           string
	Active           bool
	CreatedAt        time.Time
}

// PredictionLog is one row of ml_prediction_log: a single shadow score pairing
// the champion's output with the challenger's on the same live features. It is
// append-only and drives the G5 shadow gate's distribution comparison.
type PredictionLog struct {
	ModelName         string
	AuditJobID        *uuid.UUID
	ChampionVersion   string
	ChampionScore     float64
	ChallengerVersion *string
	ChallengerScore   *float64
	FeaturesHash      string
	ScoredAt          time.Time
}
