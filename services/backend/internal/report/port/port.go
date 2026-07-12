// Package port declares the consumer-side interfaces the report module depends
// on, plus the narrow types they exchange. The report module assembles a
// finished audit's deliverable (a JSON view and a PDF) out of data owned by
// three other modules — the audit orchestrator (the job and who owns it), the
// scoring engine (the composite score), and the llm layer (the narrative) — and
// renders the PDF through a fourth (the platform PDF renderer).
//
// Every collaborator is reached only through an interface defined here, so the
// report module imports no other business module: the composition root builds a
// thin adapter from each real implementation onto the matching port.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AuditView is the audit job the report is built for. The AuditReader returns it
// already scoped to the authenticated caller, so a caller can never read a
// report for an audit that is not theirs.
type AuditView struct {
	ID           uuid.UUID
	InfluencerID uuid.UUID
	Status       string
	// Platforms names the platforms that contributed data to the audit.
	Platforms   []string
	RequestedAt time.Time
	FinishedAt  *time.Time
}

// AuditReader loads one of the caller's audits. The real implementation is the
// audit module, which scopes the read to the authenticated caller and returns a
// not-found error for an audit the caller does not own.
type AuditReader interface {
	AuditView(ctx context.Context, auditID string) (AuditView, error)
}

// CallerID resolves the authenticated caller. The report module needs the
// caller's identity — not just a caller-scoped read — to answer a question
// AuditReader cannot: is the person publishing or sharing this report the CREATOR
// WHO OWNS the connected account it was built from? The user who requested an
// audit and the creator who owns the audited account are different people (a
// brand may audit a creator it does not own), and only the latter may direct
// Instagram Graph data anywhere. The real implementation adapts auth.UserID.
type CallerID interface {
	CallerID(ctx context.Context) (uuid.UUID, error)
}

// ConnectedOwner reports which user owns the connected (OAuth-authenticated)
// account an influencer's data came from, if any. OwnerUserID is nil when no
// creator has connected the account — a public/CSV-sourced profile nobody has
// claimed.
//
// This is the linchpin of the Meta Platform Terms §3.c "the User expressly
// directs" requirement: a report built from a creator's Graph Insights may only
// be published or shared on that creator's own direction, so the service compares
// the caller against this owner. The real implementation is the influencer module
// (influencer.user_id), adapted by the composition root.
type ConnectedOwner struct {
	OwnerUserID *uuid.UUID
}

// OwnerReader resolves the connected-account owner for an influencer.
type OwnerReader interface {
	ConnectedOwnerOf(ctx context.Context, influencerID uuid.UUID) (ConnectedOwner, error)
}

// Subscore is one dimension of the composite score.
type Subscore struct {
	Name       string
	Value      float64
	Confidence float64
}

// ScoreView is the composite score the report presents. Present is false when
// the audit produced no score (a fully failed audit), in which case the report
// discloses the absence rather than inventing a number.
type ScoreView struct {
	Present        bool
	Overall        float64
	Authenticity   float64
	Niche          string
	Tier           string
	BenchmarkLabel string
	// VerificationTier is the score's trust tier ("verified"/"estimated"/
	// "unverified"), surfaced on the report and the public badge.
	VerificationTier string
	Subscores        []Subscore
}

// ScoreReader loads the latest persisted score for an influencer. The real
// implementation is the scoring module.
type ScoreReader interface {
	ScoreOf(ctx context.Context, influencerID uuid.UUID) (ScoreView, error)
}

// WeaknessFix pairs an observed weakness with a concrete fix.
type WeaknessFix struct {
	Weakness string
	Fix      string
}

// Narrative is the llm-generated advisory content. Present is false when no
// narrative was stored for the audit (the ml/llm step was skipped or failed);
// the report then shows the score alone and says the narrative is pending.
type Narrative struct {
	Present          bool
	Summary          string
	WeaknessFixPairs []WeaknessFix
	GrowthTips       []string
	BrandFit         string
}

// NarrativeReader loads the stored narrative for an audit job. The real
// implementation is the llm module reading llm_generation.content_jsonb.
type NarrativeReader interface {
	NarrativeOf(ctx context.Context, auditJobID uuid.UUID) (Narrative, error)
}

// FraudView is the per-audit fraud / coordination estimate the report presents
// as a headline. Found is false when no fraud pass was recorded for the audit at
// all; Present is false when a pass ran but produced no signal. The report shows
// the headline only when both are true, and never treats a zero as a clean
// result it cannot vouch for. CliqueCount is the primary coordination figure.
type FraudView struct {
	Found                    bool
	Present                  bool
	FakeFollowerRate         float64
	BotCommentRate           float64
	EngagementAnomaly        float64
	CliqueCount              int
	CliqueMembershipFraction float64
	Confidence               float64
	ModelVersion             string
}

// FraudReader loads the stored fraud estimate for an audit job. The real
// implementation is the audit module reading its fraud_result table; a job with
// no stored row is disclosed as Found=false rather than as an error.
type FraudReader interface {
	FraudOf(ctx context.Context, auditJobID uuid.UUID) (FraudView, error)
}

// PDFRenderer turns a rendered HTML document into PDF bytes. The real
// implementation is the platform PDF (Gotenberg) client.
type PDFRenderer interface {
	RenderHTML(ctx context.Context, html []byte) ([]byte, error)
}

// Storage persists a rendered artifact to object storage and mints a
// time-limited shareable URL for it. The real implementation is the platform S3
// client; the report module reaches it only through this port, so it depends on
// no storage SDK. Put is keyed by an opaque object key the report module owns;
// ShareURL returns a presigned link that grants read access for ttl without the
// caller holding any credential.
type Storage interface {
	Put(ctx context.Context, key, contentType string, data []byte) error
	ShareURL(key string, ttl time.Duration) (string, error)
}
