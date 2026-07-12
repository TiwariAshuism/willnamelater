// Package contract holds the mlops module's cross-boundary value types: the
// feature-capture input the audit orchestrator hands in per completed audit, the
// fraud sub-vector it carries, and the dispute-label source the admin module
// backfills. It is a dependency-free leaf (it imports only the shared connector
// leaf and the standard library), so both the module-private layers
// (internal/mlops/internal/...) and outside callers (the composition root's port
// adapters) can share these types without importing each other.
//
// The mlops module facade (internal/mlops) re-exports these names as aliases, so
// an adapter that imports internal/mlops alone gets mlops.FeatureCapture,
// mlops.FraudSignal, and the label-source constants.
package contract

import (
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
)

// FraudSignal is the six-key fraud sub-vector plus the champion version that
// produced it, copied verbatim from the audit's fraud_result row. Present is
// false when a fraud pass ran but produced no signal (for example the ml service
// was unavailable); the quality filter rejects such a row from training because
// it cannot be quality-checked without the current model's read.
type FraudSignal struct {
	Present                  bool
	FakeFollowerRate         float64
	BotCommentRate           float64
	EngagementAnomaly        float64
	CliqueCount              int
	CliqueMembershipFraction float64
	Confidence               float64
	ModelVersion             string
}

// FeatureCapture is everything mlops needs to compute one frozen feature vector
// and its data-quality verdict for a completed audit. The composition root's
// audit-side port adapter builds it: the snapshots and fraud signal come from
// the audit run, and Niche / Tier / VerificationTier are resolved by the adapter
// over the scoring and influencer modules (mlops imports neither business
// module). ReachLabel is set only when a real Instagram Insights reach figure
// was pulled for the audit, else left nil — never fabricated.
type FeatureCapture struct {
	AuditJobID   uuid.UUID
	InfluencerID uuid.UUID
	// Snapshots are the usable per-platform snapshots the audit collected. The
	// primary platform of the vector is the one with the largest follower count.
	// An empty slice is a no-op: there is nothing to record.
	Snapshots []connector.Snapshot
	// Fraud is the current champion's fraud read for this audit (the six keys the
	// fraud model trains and serves on, so there is no train/serve skew).
	Fraud FraudSignal
	// Niche is the influencer's content niche (scoring's Profiles port), "" when
	// unknown. Tier is the follower-size bucket scoring already derives.
	Niche string
	Tier  string
	// VerificationTier is the trust tier the score carries
	// (contract.DeriveVerificationTier): "verified" | "estimated" | "unverified".
	VerificationTier string
	// ReachLabel is the real reach (median reached accounts) from an Instagram
	// Insights pull, or nil when no real figure was produced.
	ReachLabel *int64
	// CapturedAt is the audit completion time. A zero value is replaced with the
	// module clock's now at capture.
	CapturedAt time.Time
}

// FraudLabelSource records how a fraud_label was established. Both values come
// from a resolved dispute decision; the label is backfilled long after capture.
type FraudLabelSource string

const (
	// LabelSourceDisputeRejected marks a label set because a dispute was rejected
	// (the fraud flag stood): the account is confirmed fraudulent/coordinated, a
	// positive training example.
	LabelSourceDisputeRejected FraudLabelSource = "dispute_rejected"
	// LabelSourceDisputeUpheld marks a label set because a dispute was upheld (the
	// fraud flag was overturned): the account is confirmed legitimate, a negative
	// training example.
	LabelSourceDisputeUpheld FraudLabelSource = "dispute_upheld"
)

// ReachLabelSourceInsights is the only reach-label provenance: a real Instagram
// Insights pull. It is stamped on reach_label_source when ReachLabel is set.
const ReachLabelSourceInsights = "instagram_insights"

// Valid reports whether s is one of the two recognised label sources.
func (s FraudLabelSource) Valid() bool {
	return s == LabelSourceDisputeRejected || s == LabelSourceDisputeUpheld
}
