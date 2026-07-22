// Package port declares the consumer-side interfaces the audit orchestrator
// depends on, plus the narrow data types those interfaces exchange. Every
// collaborator the orchestrator uses — billing quota, metrics ingest, the
// scoring engine, the ml fraud client, the llm reporter, the connector
// registry, and the oauth-backed connection lookup — is reached only through an
// interface defined here.
//
// These interfaces are deliberately defined by the consumer, not re-exported
// from the providers. That is what keeps the audit module from importing any
// other business module: the composition root builds a thin adapter from each
// real implementation onto the matching port here. The types are kept free of
// any provider's own request/response shapes for the same reason.
package port

import (
	"context"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
)

// ReservationID is an opaque quota-reservation token. Its content is the
// billing module's concern; the orchestrator only carries it from a Reserve to
// the matching Commit or Release.
type ReservationID string

// Quota gates each audit against the caller's metered plan. Reserve is called
// before a job is enqueued and immediately consumes one unit, so an over-quota
// caller never has work scheduled. Commit finalises a reservation on success or
// partial success; Release returns the unit on total failure. The real
// implementation is the billing module's quota service, adapted by app.
type Quota interface {
	Reserve(ctx context.Context, userID uuid.UUID, unit string) (ReservationID, error)
	Commit(ctx context.Context, id ReservationID) error
	Release(ctx context.Context, id ReservationID) error
}

// Ingester persists one connector snapshot for an influencer and audit job. The
// real implementation is metrics.Module.Ingest, which pseudonymizes commenter
// identities on the way in.
type Ingester interface {
	Ingest(ctx context.Context, influencerID, auditJobID uuid.UUID, snap connector.Snapshot) error
}

// FraudSummary is the small, ml-agnostic fraud result the orchestrator gets
// from the fraud client. The ml module's wire request and response never appear
// here; app's adapter builds the request from the snapshots and collapses the
// response onto this shape.
// Every measurement below is a POINTER, and nil means ABSENT — "we could not
// look" — never zero. Zero is a measurement ("we looked; it is clean"), and
// conflating the two is how an account nobody examined gets presented to a brand
// as authentic. A nil signal is excluded from the composite and its weight is
// renormalized away; it is never clamped to 0 and never rendered as "0%".
type FraudSummary struct {
	// Present is false when no fraud signal could be produced at all (for example
	// the ml service was unavailable), in which case every field below is nil.
	Present bool

	// RiskScore is the ml service's composite per-account fraud estimate, 0-100,
	// higher = more likely inauthentic. It is NOT a fake-follower percentage and
	// must never be labelled as one: it is a weighted blend of growth-spike,
	// engagement-deviation, like/comment-ratio and UnDBot signals, renormalized
	// over whichever of those could actually be observed.
	RiskScore *float64

	// EngagementAnomaly is how far observed engagement sits from a SOURCED
	// benchmark. It is nil whenever no benchmark was supplied — which, until the
	// scoring layer passes one down, is always. It is nil, not 0: an unmeasured
	// anomaly is not an absent anomaly.
	EngagementAnomaly *float64

	// CliqueCount is the primary coordination signal: the number of maximal
	// co-commenter cliques of the model's minimum size. CliqueMembershipFraction
	// is the secondary signal, the share of analyzed commenters sitting inside one.
	//
	// Both are nil when the snapshots carried NO comments — the overwhelmingly
	// common case for Instagram and CSV audits, which pull no comment events at
	// all. A zero here would assert "we analyzed the commenters and found no
	// coordination", which we did not do.
	CliqueCount              *int
	CliqueMembershipFraction *float64

	// Confidence is scaled by how much of the signal vector was actually observed,
	// so a score resting on one signal is not presented as firmly as one resting on
	// four.
	Confidence   float64
	ModelVersion string

	// RefinedScore is the fraud champion's estimate over the full assembled vector
	// (0-100, higher = more inauthentic), set by the adapter only when a champion
	// is promoted and serving. Nil in cold start. It is threaded into scoring's
	// FraudInput; it is not persisted on the fraud_result row.
	RefinedScore *float64
}

// FraudClient scores coordinated-inauthenticity signals over the snapshots an
// audit collected. It takes connector snapshots and returns a summary, keeping
// the ml module's own types out of the audit module. The real implementation is
// the ml client, adapted by app.
type FraudClient interface {
	ScoreFraud(ctx context.Context, auditJobID uuid.UUID, snapshots []connector.Snapshot) (FraudSummary, error)
}

// FraudInput is the fraud contribution the scoring engine consumes. It is
// shaped identically to scoring.FraudInput; app adapts a FraudSummary onto the
// scoring module's own type.
// Nil means the signal was not observed; the scoring engine excludes it and
// renormalizes, never treating it as a clean zero.
type FraudInput struct {
	Present bool
	// RiskScore is the ml composite per-account risk estimate (0-100). NOT a
	// fake-follower rate.
	RiskScore *float64
	// CliqueMembershipFraction is the independent coordination measurement, blended
	// with RiskScore. Nil when no comments were analyzable.
	CliqueMembershipFraction *float64
	Confidence               float64
	ModelVersion             string
	// RefinedScore, when non-nil, is the champion's score over the full assembled
	// vector; scoring uses it as the fraud aggregate. Nil in cold start.
	RefinedScore *float64
}

// CommentQualitySummary is the ml-agnostic comment-quality result the
// orchestrator gets from the comment classifier. It is a DISPLAY signal only: it
// never touches the score or the fraud vector (the ML service's firewall forbids
// it until its weight is fitted against real outcomes). Present is false when no
// comments were available to classify. LowQualityRatio is a POINTER — nil below
// the classifier's minimum sample, where a rate would assert a precision nobody
// has; it is never rendered as 0%.
type CommentQualitySummary struct {
	Present          bool
	AnalyzedCount    int
	LowQualityCount  int
	LowQualityRatio  *float64
	SufficientSample bool
	Counts           map[string]int
	RateKey          string
	ModelVersion     string
}

// CommentClassifier assigns rule-based quality buckets to the comments an audit
// sampled and returns a display-only summary. The real implementation is the ml
// client, adapted by app so no ml type leaks into the audit module. It may be nil
// (the audit runs identically without it); the call is best-effort, so an
// unavailable classifier never fails the audit — the summary is simply absent.
type CommentClassifier interface {
	ClassifyComments(ctx context.Context, snapshots []connector.Snapshot) (CommentQualitySummary, error)
}

// ScoreResult is the narrow score summary the orchestrator threads from the
// scorer into the report input. The full, persisted score row is the scoring
// module's concern (it is keyed on the audit job id the Scorer is given).
type ScoreResult struct {
	Overall               float64
	ContributingPlatforms []connector.Platform
}

// Scorer computes and persists the influence/authenticity score for an audit
// over the snapshots that succeeded. The real implementation is
// scoring.Module.Score, adapted by app.
type Scorer interface {
	Score(ctx context.Context, auditJobID, influencerID uuid.UUID, snapshots []connector.Snapshot, fraud FraudInput) (ScoreResult, error)
}

// ReportInput is everything the reporter needs to generate an audit's advisory
// narrative. It is assembled by the orchestrator from the score, the fraud
// summary, and the collected snapshots.
type ReportInput struct {
	AuditJobID   uuid.UUID
	InfluencerID uuid.UUID
	Score        ScoreResult
	Fraud        FraudSummary
	Snapshots    []connector.Snapshot
}

// ReportOutput references the generated report. Its content and persistence are
// the llm/report modules' concern; the orchestrator only needs the handle the
// adapter returns.
type ReportOutput struct {
	GenerationID uuid.UUID
}

// Usage records the token cost of one report generation. The orchestrator does
// not persist it (the llm module does); it is part of the port surface so app's
// adapter can return what it recorded.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CostMicros   int64
	Cached       bool
}

// Reporter turns an audit's result into an advisory narrative. The real
// implementation is the llm provider, adapted by app so that no llm type leaks
// into the audit module.
type Reporter interface {
	GenerateReport(ctx context.Context, in ReportInput) (ReportOutput, Usage, error)
}

// Connectors resolves the connector for a platform. The real implementation is
// the connector.Registry; the port narrows it to the single lookup the
// orchestrator needs.
type Connectors interface {
	Get(p connector.Platform) (connector.Connector, bool)
}

// Connection is one platform an influencer has connected, carrying the live,
// decrypted OAuth token for a single fetch. The token is held only in memory
// for the duration of the audit and is never logged or persisted by this
// module.
type Connection struct {
	Platform  connector.Platform
	Handle    string
	AccountID string
	Token     *connector.OAuthToken
}

// Connections lists the platforms an influencer has connected together with the
// live token per platform. The oauth module owns the underlying oauth_token
// table; app wires an oauth-backed adapter onto this port.
type Connections interface {
	ListConnections(ctx context.Context, influencerID uuid.UUID) ([]Connection, error)
}

// CallerID resolves the authenticated caller from the request context. The
// audit module never imports auth; app adapts auth's context accessor onto this
// port. It returns an error (an unauthorized domain error) when the request
// carried no authenticated identity.
type CallerID interface {
	CallerID(ctx context.Context) (uuid.UUID, error)
}

// FeatureRecord is one completed audit's contribution to the ml feature store:
// the usable snapshots it collected and the fraud signal it produced. The
// composition root adapts it onto the mlops feature recorder, which computes the
// frozen feature vector and resolves niche/tier/verification there (the audit
// module imports neither mlops nor scoring).
type FeatureRecord struct {
	AuditJobID   uuid.UUID
	InfluencerID uuid.UUID
	Snapshots    []connector.Snapshot
	Fraud        FraudSummary
}

// FeatureRecorder captures a completed audit as an ml training feature row (the
// data-flywheel intake). The real implementation is the mlops module, adapted by
// app. The call is best-effort: a failure is logged and never fails the audit,
// so the flywheel is a side benefit of running an audit, not a dependency of it.
type FeatureRecorder interface {
	RecordFeatures(ctx context.Context, rec FeatureRecord) error
}
