// Package model holds the mlops module's domain types (the feature-store row,
// the registry model version, the canary, and the prediction-log entry) and the
// request/response DTOs its HTTP surface exchanges. The domain types are what the
// repository reads and writes; the DTOs are what the handler binds and renders.
package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
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
	AuditJobID       uuid.UUID
	InfluencerID     uuid.UUID
	Platform         string
	Features         json.RawMessage
	FraudLabel       *bool
	FraudLabelSource *string
	// FraudLabelEvidence is what the adjudicator OBSERVED outside the heuristic's
	// own output. nil, or contract.EvidenceHeuristicOnly, means the label is a
	// heuristic echo: the row keeps its label for the customer-facing dispute
	// outcome but is NEVER training-eligible. See TrainingEligible.
	FraudLabelEvidence *string
	ReachLabel         *int64
	ReachLabelSource   *string
	// ReachOrganic states whether ReachLabel excludes ad-delivered reach. nil is
	// "unknown", which is not organic: a label is only ever stored alongside true.
	ReachOrganic *bool
	// SnapshotSources are the concrete data paths behind the row (connector
	// DataSource values). They are what lets the canary endpoint refuse an audit
	// built on a creator-uploaded CSV; an empty slice means the provenance is
	// unknown, which is refused too.
	SnapshotSources       []string
	QualityOK             bool
	QualityReasons        []string
	ModelVersionAtCapture string
	VerificationTier      string
	CapturedAt            time.Time
}

// ReasonFraudRiskHigh is the one quality reason derived from our OWN fraud
// heuristic's output rather than from an observation. It is named here, not only
// in the quality filter, because the export rule below and the
// training_feature_row.training_eligible generated column both have to know which
// reason is the circular one.
const ReasonFraudRiskHigh = "fraud_risk_estimate_high"

// TrainingEligible reports whether a stored row belongs in the DEFAULT training
// export. It is the Go mirror of the training_feature_row.training_eligible
// generated column (both name the reason code and the evidence rule once).
//
// TWO opposing failure modes meet here, and the rule has to thread between them.
//
// The first is CENSORSHIP. The fraud-risk reason excludes exactly the accounts
// that get disputed and labelled POSITIVE, and the export filters on quality by
// default — so applying it to labelled rows severs the positive class from the
// training set and y=1 accrues at ~0/week forever.
//
// The second is LAUNDERING, and it is the worse of the two. Waiving the exclusion
// for any row that merely HAS a fraud_label would hand-pick the rows most likely
// to be heuristic ECHOES and feed them to the trainer as ground truth. A dispute
// exists only because the heuristic flagged the account, and the adjudicator has
// always seen the heuristic's own score — so a bare label can mean nothing more
// than "a human agreed with the model". Train on that and the model learns to
// predict its own opinion. G0-G5 cannot see it: they check model-against-labels
// and assume the labels are real.
//
// So the waiver keys on OBSERVATION, not on the label: only a label carrying an
// observable evidence kind (a platform enforcement action, a creator admission, a
// receipt, brand conversion data, a manual follower-sample audit) may override the
// fraud-risk exclusion. A nil or heuristic-only evidence is not an observation and
// keeps the row out. An empty positive class is a shippable answer; a laundered
// one is not.
//
// Every OBSERVED reason (account too new, too few posts, follower spike, no
// estimate) still excludes the row unconditionally: those are facts about the
// data, not our model's opinion of the account.
func TrainingEligible(reasons []string, fraudLabel *bool, evidence *string) bool {
	labelIsObservation := fraudLabel != nil && evidence != nil &&
		contract.FraudLabelEvidence(*evidence).Observable()

	for _, r := range reasons {
		if labelIsObservation && r == ReasonFraudRiskHigh {
			continue
		}
		return false
	}
	return true
}

// FeatureRowFilter bounds the feature-row export. Since lower-bounds captured_at
// when non-zero; TrainableOnly restricts to training-eligible rows (the training
// default — see TrainingEligible: NOT the same as quality_ok, because a labelled
// row is never censored by the fraud-score-derived reason); Limit caps the result
// set.
type FeatureRowFilter struct {
	Since         time.Time
	TrainableOnly bool
	Limit         int
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

// Canary provenance kinds key ml_canary_account.provenance_kind (a CHECK-
// constrained enum, not prose). They are the closed set of bases on which a
// canary's ground truth may rest.
//
// Note what is absent: "an admin looked at it and it seemed fine". Nobody — not
// an LLM, not an admin — can OBSERVE the absence of a follower purchase, so a
// clean canary is ProvenancePresumedClean: a stated basis, never a verified
// negative.
const (
	// ProvenanceOAuthInsightsMeasured is a figure the platform itself measured and
	// returned over OAuth. It is the only basis for a reach canary's band.
	ProvenanceOAuthInsightsMeasured = "oauth_insights_measured"
	// ProvenancePlatformEnforcement is the platform having acted on the account
	// (takedown, follower purge, flagged notice).
	ProvenancePlatformEnforcement = "platform_enforcement_record"
	// ProvenanceCreatorAdmission is the creator admitting the purchase.
	ProvenanceCreatorAdmission = "creator_admission"
	// ProvenanceVendorReceipt is a receipt from the follower/engagement vendor.
	ProvenanceVendorReceipt = "vendor_receipt"
	// ProvenanceOperatorConstructedPositive is an account WE bought followers for,
	// so the positive label is known by construction.
	ProvenanceOperatorConstructedPositive = "operator_constructed_positive"
	// ProvenancePresumedClean is the honest basis of a negative canary: no evidence
	// of fraud was found, which is not the same as evidence of its absence.
	ProvenancePresumedClean = "presumed_clean"
)

// ValidProvenanceKind reports whether kind is a recognised canary provenance.
func ValidProvenanceKind(kind string) bool {
	switch kind {
	case ProvenanceOAuthInsightsMeasured, ProvenancePlatformEnforcement,
		ProvenanceCreatorAdmission, ProvenanceVendorReceipt,
		ProvenanceOperatorConstructedPositive, ProvenancePresumedClean:
		return true
	}
	return false
}

// ProvesFraudPositive reports whether kind is evidence someone could actually
// OBSERVE that an account bought its audience. Only these may back a positive
// fraud canary; nothing may back a "verified" negative.
func ProvesFraudPositive(kind string) bool {
	switch kind {
	case ProvenancePlatformEnforcement, ProvenanceCreatorAdmission,
		ProvenanceVendorReceipt, ProvenanceOperatorConstructedPositive:
		return true
	}
	return false
}

// Canary is one row of ml_canary_account: a ground-truth account every challenger
// must score correctly. It is ANCHORED to the audit it came from: Features is the
// frozen vector the server copied out of that audit's training_feature_row, never
// a vector a client sent. ExpectedLabel is set for fraud canaries, the reach band
// for reach canaries, and ProvenanceKind states the basis of the expectation.
type Canary struct {
	ID               uuid.UUID
	ModelName        string
	AuditJobID       uuid.UUID
	Label            string
	Features         json.RawMessage
	ExpectedLabel    *bool
	ExpectedReachMin *int64
	ExpectedReachMax *int64
	ProvenanceKind   string
	Active           bool
	CreatedAt        time.Time
}

// PredictionLog is one row of ml_prediction_log: a single shadow score pairing
// the champion's output with the challenger's on the same live features. It is
// append-only and drives the G5 shadow gate's distribution comparison.
//
// AuditJobID is REQUIRED. features_hash is a one-way sha256, so the audit job is
// the only way a logged prediction can ever be joined back to an outcome; a row
// without it is unresolvable forever, and the shadow gate could never grow into a
// label-joined arbiter.
type PredictionLog struct {
	ModelName         string
	AuditJobID        uuid.UUID
	ChampionVersion   string
	ChampionScore     float64
	ChallengerVersion *string
	ChallengerScore   *float64
	FeaturesHash      string
	ScoredAt          time.Time
}
