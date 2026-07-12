// Package port declares the consumer-side interfaces the admin module depends
// on, plus the narrow types they exchange. The admin module owns the dispute
// table, but its dashboards and its training-label export read data owned by
// other parts of the system: the llm layer's per-generation cost accounting, the
// audit orchestrator's per-audit fraud estimate, and the asynq queue state.
//
// Every one of those is reached only through an interface defined here, so the
// admin module imports no other business module. The composition root builds a
// thin adapter from each real implementation onto the matching port. Identity is
// the same story: the module never imports auth, and resolves the caller and the
// admin guard through the ports below.
package port

import (
	"context"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// CallerID resolves the authenticated caller from the request context. Filing a
// dispute is open to any signed-in user, and the caller becomes the dispute's
// raised_by. The admin module never imports auth; app adapts auth's context
// accessor onto this port. It returns an unauthorized domain error when the
// request carried no authenticated identity.
type CallerID interface {
	CallerID(ctx context.Context) (uuid.UUID, error)
}

// AdminGuard authorises an admin-only action. It returns the caller's id when
// they are an authenticated admin, an unauthorized domain error when no identity
// is present, and a forbidden domain error when the caller is authenticated but
// not an admin. The returned id becomes a resolved dispute's resolved_by. app
// satisfies it over auth's identity (the users.role = 'admin' bit).
type AdminGuard interface {
	RequireAdmin(ctx context.Context) (uuid.UUID, error)
}

// FraudView is the per-audit fraud / coordination estimate the admin module
// reads to attach features to a labelled dispute. It mirrors audit.FraudView.
// Present is false when a fraud pass ran but produced no signal; the found
// return of FraudResultOf is false when no fraud row exists for the audit at all.
type FraudView struct {
	Present bool
	// RiskScore is the composite per-account risk estimate (0-100). NOT a
	// fake-follower rate. Pointers are nil when the signal was not observed —
	// never a fabricated zero.
	RiskScore                *float64
	EngagementAnomaly        *float64
	CliqueCount              *int
	CliqueMembershipFraction *float64
	Confidence               float64
	ModelVersion             string
}

// FraudReader loads the stored fraud estimate for an audit job. found is false,
// with no error, when no fraud row was written for it. The real implementation
// is audit.Module.FraudResultOf, adapted by app; the admin module never imports
// the audit module.
type FraudReader interface {
	FraudResultOf(ctx context.Context, auditJobID uuid.UUID) (view FraudView, found bool, err error)
}

// ModelCost is one model's aggregate slice of the LLM cost accounting.
type ModelCost struct {
	Model             string
	Generations       int
	InputTokens       int64
	OutputTokens      int64
	CostMicros        int64
	CachedGenerations int
}

// CostSummary is the aggregate of every llm_generation row, in total and broken
// down by model. It is computed by the owner of the llm_generation table, so the
// admin module reads a finished aggregate rather than the raw rows. CostMicros is
// millionths of a US dollar.
type CostSummary struct {
	TotalGenerations  int
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCostMicros   int64
	CachedGenerations int
	ByModel           []ModelCost
}

// CostReader returns the aggregate LLM generation cost. The real implementation
// is the llm module reading its own llm_generation table; app adapts it onto
// this port so the admin module never imports llm.
type CostReader interface {
	CostSummary(ctx context.Context) (CostSummary, error)
}

// QueueInspector is the read surface of the asynq queue state the job monitor
// needs. Its method set is a subset of *asynq.Inspector, which satisfies it
// directly, so app injects the real inspector with no adapter. The admin module
// does not dial Redis; the inspector is constructed and injected by the
// composition root.
type QueueInspector interface {
	Queues() ([]string, error)
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)
}

// LabelEvidence is WHAT THE ADJUDICATOR ACTUALLY OBSERVED, outside the
// heuristic's own output, when they decided a dispute. It is declared here, on
// the admin module's public seam, because it crosses the boundary twice: it is
// persisted on dispute.label_evidence (migration 000027), and it travels to the
// ml training loop through TrainingLabelSink below.
//
// It exists because the decision alone is NOT ground truth and cannot be made
// into it. A dispute exists only because the heuristic flagged the account, so
// "rejected" means no more than "an admin declined to overturn the flag" — and
// the review screen has historically shown that admin the heuristic's own risk
// score. A model fit on those labels learns to predict whether a human agreed
// with the heuristic; it can assert nothing the heuristic did not already assert.
//
// The observability test applies to humans exactly as it does to a model: nobody
// can observe a follower purchase by looking at a follower count. So only a label
// carrying an OBSERVABLE evidence kind may enter a training fold.
//
// The values mirror mlops's contract.FraudLabelEvidence one for one (and the
// CHECK constraint on dispute.label_evidence). They are mirrored rather than
// imported because a business module imports no other business module; the
// composition root maps between the two string types.
type LabelEvidence string

const (
	// EvidencePlatformEnforcement records that the platform itself acted (takedown,
	// ban, removal of followers). An external authority observed the fraud.
	EvidencePlatformEnforcement LabelEvidence = "platform_enforcement_action"
	// EvidenceCreatorAdmission records that the creator admitted to buying
	// engagement.
	EvidenceCreatorAdmission LabelEvidence = "creator_admission"
	// EvidencePurchaseReceipt records that a receipt or engagement-panel invoice was
	// produced.
	EvidencePurchaseReceipt LabelEvidence = "purchase_receipt_or_panel_invoice"
	// EvidenceBrandConversionData records that a brand's own campaign conversion
	// data contradicts the claimed audience.
	EvidenceBrandConversionData LabelEvidence = "brand_campaign_conversion_data"
	// EvidenceManualFollowerAudit records that a human sampled the actual follower
	// list and examined the accounts — the one manual method that observes the thing
	// itself.
	EvidenceManualFollowerAudit LabelEvidence = "manual_follower_sample_audit"
	// EvidenceHeuristicOnly records that the admin reviewed the flag and agreed,
	// observing nothing the heuristic had not already computed.
	//
	// This is the HONEST, first-class answer for the common case, and it is why the
	// enum exists. The dispute outcome is real and the customer is owed it, so the
	// row is kept — but it is a heuristic echo, and it may NEVER become y. The
	// training-label export drops it.
	EvidenceHeuristicOnly LabelEvidence = "none_reviewed_heuristic_only"
)

// Valid reports whether e is a recognised evidence kind. The empty string is not
// one: a decided dispute must state what was observed, even if the answer is
// EvidenceHeuristicOnly ("nothing the heuristic had not already computed").
func (e LabelEvidence) Valid() bool {
	switch e {
	case EvidencePlatformEnforcement, EvidenceCreatorAdmission, EvidencePurchaseReceipt,
		EvidenceBrandConversionData, EvidenceManualFollowerAudit, EvidenceHeuristicOnly:
		return true
	}
	return false
}

// Observable reports whether the evidence rests on something someone actually
// SAW, outside the heuristic's own output — and therefore whether the label may
// leave the training-label export at all.
//
// EvidenceHeuristicOnly is the sole false case among the valid kinds, and
// deliberately so: it is a real admin decision but contains no observation, so
// training on it would close the loop between the model and its own opinion. An
// unknown or empty evidence is also false — absence of a stated observation is
// not an observation.
func (e LabelEvidence) Observable() bool {
	return e.Valid() && e != EvidenceHeuristicOnly
}

// TrainingLabelSink backfills a supervised fraud label onto the ml feature-store
// row when a dispute is decided: fraudulent=true when the flag stood (dispute
// rejected), false when it was overturned (upheld).
//
// evidence travels WITH the bool and is not optional. The bool on its own is an
// echo of the heuristic — mlops needs the evidence kind to decide whether the
// label may enter a fold at all, and must reject EvidenceHeuristicOnly exactly as
// the HTTP export does.
//
// The real implementation is the mlops module, adapted by app; the admin module
// never imports mlops. The call is best-effort — a failure is logged and never
// fails the dispute resolution, since the label is a training side-effect, not
// the decision itself.
type TrainingLabelSink interface {
	RecordDisputeLabel(ctx context.Context, auditJobID uuid.UUID, fraudulent bool, evidence LabelEvidence) error
}
