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
// Each measurement is a pointer; nil means it was not observed and freezes into
// the feature vector as JSON null (native-missing to LightGBM), never as a zero
// that a model would read as a confident clean measurement.
type FraudSignal struct {
	Present bool
	// RiskScore is the composite per-account risk estimate (0-100). NOT a
	// fake-follower rate: the fake_follower_rate and bot_comment_rate keys are gone
	// because neither was ever measured (see FEATURE_ORDER v2).
	RiskScore                *float64
	EngagementAnomaly        *float64
	CliqueCount              *int
	CliqueMembershipFraction *float64
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
	// ReachLabel is IGNORED and kept only so the composition root keeps compiling.
	// It used to be the caller's word for the reach figure, and the service stamped
	// "instagram_insights" on whatever integer arrived — provenance nobody had
	// actually established. mlops now DERIVES the reach label from Snapshots itself
	// (deriveReachLabel), so no caller can hand it a number to bless.
	//
	// Deprecated: unused. Delete the field, and the call site in the app adapter,
	// once the composition root no longer sets it.
	ReachLabel *int64
	// ReachOrganic states whether the reach figure in Snapshots EXCLUDES
	// ad-delivered reach. Insights `reach` on a boosted post includes the reach the
	// account PAID for; training on it teaches the reach model that ad spend is
	// organic virality. nil means the split is unknown, and unknown is NOT organic:
	// mlops stores a reach label only when this is explicitly true. If the API
	// cannot expose the split for a media type, that media must be excluded
	// upstream — estimating the organic portion is not a third option.
	ReachOrganic *bool
	// PromotedMediaFraction is the fraction of the sampled media that were boosted /
	// promoted, when the connector can observe it (nil = not observed). A
	// promotion-heavy account's engagement features are ad-inflated, so the quality
	// filter excludes such a row from training.
	PromotedMediaFraction *float64
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

// Valid reports whether s is one of the two recognised label sources.
func (s FraudLabelSource) Valid() bool {
	return s == LabelSourceDisputeRejected || s == LabelSourceDisputeUpheld
}

// FraudLabelEvidence is WHAT THE ADJUDICATOR ACTUALLY OBSERVED, outside the
// heuristic's own output, when they decided a dispute.
//
// It exists because FraudLabelSource is NOT ground truth and cannot be made into
// it. A dispute exists only because the heuristic flagged the account;
// "dispute_rejected" therefore means no more than "an admin declined to overturn
// the flag", and the review screen has always shown that admin the heuristic's own
// risk score. A model fit on those labels learns to predict WHETHER A HUMAN AGREED
// WITH THE HEURISTIC — it can assert nothing the heuristic did not already assert.
// The G0-G5 gates cannot detect this: they check model-against-labels and assume
// the labels are real.
//
// So the label alone may not enter a training fold. Only a label carrying an
// OBSERVABLE evidence kind may. The observability test applies to humans exactly as
// it does to an LLM: nobody — admin or model — can observe a follower purchase by
// looking at a follower count.
type FraudLabelEvidence string

const (
	// EvidencePlatformEnforcement is set when the platform itself acted (takedown, ban, removal
	// of followers). An external authority observed the fraud.
	EvidencePlatformEnforcement FraudLabelEvidence = "platform_enforcement_action"
	// EvidenceCreatorAdmission is set when the creator admitted to buying engagement.
	EvidenceCreatorAdmission FraudLabelEvidence = "creator_admission"
	// EvidencePurchaseReceipt is set when a receipt or engagement-panel invoice was produced.
	EvidencePurchaseReceipt FraudLabelEvidence = "purchase_receipt_or_panel_invoice"
	// EvidenceBrandConversionData is set when a brand's own campaign conversion data contradicts
	// the claimed audience.
	EvidenceBrandConversionData FraudLabelEvidence = "brand_campaign_conversion_data"
	// EvidenceManualFollowerAudit is set when a human sampled the actual follower list and
	// examined the accounts — the one manual method that observes the thing itself.
	EvidenceManualFollowerAudit FraudLabelEvidence = "manual_follower_sample_audit"
	// EvidenceHeuristicOnly is set when the admin reviewed the flag and agreed, observing
	// nothing the heuristic had not already computed.
	//
	// This is the HONEST, first-class answer for the common case, and it is why the
	// enum exists. The dispute outcome is real and the customer is owed it, so the
	// row is kept — but it is a heuristic echo, and it may NEVER become y.
	EvidenceHeuristicOnly FraudLabelEvidence = "none_reviewed_heuristic_only"
)

// Valid reports whether e is a recognised evidence kind.
func (e FraudLabelEvidence) Valid() bool {
	switch e {
	case EvidencePlatformEnforcement, EvidenceCreatorAdmission, EvidencePurchaseReceipt,
		EvidenceBrandConversionData, EvidenceManualFollowerAudit, EvidenceHeuristicOnly:
		return true
	}
	return false
}

// Observable reports whether the evidence rests on something someone actually SAW,
// outside the heuristic's own output — and therefore whether a label carrying it
// may enter a training fold.
//
// EvidenceHeuristicOnly is the sole false case, and deliberately so: it is a real
// admin decision but contains no observation, so training on it would close the
// loop between the model and its own opinion. An unknown/empty evidence is also
// false — absence of a stated observation is not an observation.
func (e FraudLabelEvidence) Observable() bool {
	return e.Valid() && e != EvidenceHeuristicOnly
}

// ReachLabelSource records how a reach_label was MEASURED. It is derived from the
// snapshot that produced the figure (connector.Snapshot.Source) — never from the
// caller's assertion — because the column is only worth storing if it is evidence.
type ReachLabelSource string

// ReachSourceInstagramGraph is the only provenance a reach label may claim: a live
// Meta/Instagram Graph Insights pull. A creator-uploaded CSV export is a
// self-reported number nobody measured, and a provider read has no reach at all,
// so neither can produce a reach label.
const ReachSourceInstagramGraph ReachLabelSource = "instagram_graph_insights"

// Valid reports whether s is a recognised reach-label source, mirroring
// FraudLabelSource.Valid.
func (s ReachLabelSource) Valid() bool {
	return s == ReachSourceInstagramGraph
}

// ReachSourceFor maps a snapshot's data path onto the reach provenance it may
// claim. ok is false for every path that cannot MEASURE reach (CSV upload,
// provider, YouTube), and a snapshot from such a path never yields a reach label.
func ReachSourceFor(ds connector.DataSource) (ReachLabelSource, bool) {
	if ds == connector.SourceInstagramGraph {
		return ReachSourceInstagramGraph, true
	}
	return "", false
}
