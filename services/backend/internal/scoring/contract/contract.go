// Package contract holds the scoring module's cross-boundary value types: the
// fraud input the audit orchestrator hands in, the Score it gets back, and the
// Profiles port the module uses to resolve an influencer's niche.
//
// It is a dependency-free leaf so both the module-private layers
// (internal/scoring/internal/...) and outside callers (the audit orchestrator,
// the composition root) can share these types without importing each other. The
// module facade re-exports the names as aliases, so a caller that imports
// internal/scoring alone gets scoring.Score, scoring.FraudInput and
// scoring.Profiles.
package contract

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
)

// The Component names key the scoring_weights.weights JSON object and the score
// breakdown. They are stable strings: a persisted weight set or score row is
// read back by these keys, so they must never be renamed casually.
const (
	ComponentReach             = "reach"
	ComponentEngagementQuality = "engagement_quality"
	ComponentAuthenticity      = "authenticity"
	ComponentConsistency       = "consistency"
	ComponentContentQuality    = "content_quality"
)

// FraudInput is the ML fraud signal the orchestrator passes to Score. Its rates
// are fractions in [0,1] (not percentages); the orchestrator normalizes the ML
// service's response onto this shape before calling. Present is false when no
// fraud pass ran (e.g. every platform degraded), which makes the authenticity
// subscore neutral and zero-confidence rather than falsely clean.
// Each measurement is a POINTER: nil means the signal could not be observed, and
// it is then excluded from the aggregate with its weight renormalized away. Nil is
// never treated as 0 — a zero would assert a clean measurement we never took.
type FraudInput struct {
	// Present is false when no fraud pass produced anything at all.
	Present bool

	// RiskScore is the ml service's composite per-account fraud estimate (0-100,
	// higher = more likely inauthentic), itself already renormalized over the
	// signals it could observe (growth spike, engagement deviation, like/comment
	// ratio, UnDBot). It is NOT a fake-follower rate.
	RiskScore *float64

	// CliqueMembershipFraction is the share of analyzed commenters sitting inside a
	// coordinated co-commenter clique — an INDEPENDENT measurement from a different
	// model, which is why it is blended with RiskScore rather than folded into it.
	// Nil when no comments were available to analyze (most Instagram/CSV audits).
	CliqueMembershipFraction *float64

	Confidence   float64
	ModelVersion string

	// RefinedScore is the fraud champion's estimate over the FULL assembled
	// feature vector (0-100, higher = more inauthentic), set only once a champion
	// is promoted and serves. When non-nil it is the authenticity subscore's fraud
	// aggregate — the champion trained to predict the fraud label from these
	// signals, so its output supersedes the heuristic blend. Nil in cold start,
	// where the heuristic aggregate stands.
	RefinedScore *float64
}

// Verification tiers describe how much a score can be trusted based on where its
// data came from. They are stable, persisted strings and are surfaced on the
// public badge as a 🟢/🟡 signal.
const (
	// VerificationVerified: every contributing snapshot came from a live,
	// authenticated API pull (OAuth or an API key). The number rests on ground
	// truth the platform itself reported.
	VerificationVerified = "verified"
	// VerificationEstimated: at least one contributing snapshot came from an
	// upload or a public-data provider, so the composite is a considered estimate,
	// not measured ground truth.
	VerificationEstimated = "estimated"
	// VerificationUnverified: no platform produced usable data (a failed audit).
	VerificationUnverified = "unverified"
)

// DeriveVerificationTier classifies a score by the provenance of the snapshots
// that fed it. It is deliberately monotone and conservative: a single uploaded
// or provider-sourced platform downgrades the whole composite to "estimated",
// and only an all-live set earns "verified" — the score never over-claims. Any
// source that is not a recognised live path (including an unset one) is treated
// as non-live, so an unlabelled snapshot can never be silently certified.
func DeriveVerificationTier(snaps []connector.Snapshot) string {
	if len(snaps) == 0 {
		return VerificationUnverified
	}
	for _, s := range snaps {
		if s.Source != connector.SourceYouTubeAPI && s.Source != connector.SourceInstagramGraph {
			return VerificationEstimated
		}
	}
	return VerificationVerified
}

// Subscore is one component of the composite: its value on a 0..100 scale and
// the confidence in [0,1] that qualifies it. Confidence stays low while the
// evidence behind the value is thin — a bootstrap benchmark with few samples, a
// missing fraud pass, or too few posts to judge cadence.
type Subscore struct {
	Value      float64 `json:"value"`
	Confidence float64 `json:"confidence"`
}

// Score is the computed influence + authenticity result for one audit. Overall
// is the weighted composite of the five subscores on a 0..100 scale. The version
// stamps pin the exact weight set and benchmark generation used, so the score is
// reproducible even after newer weights or benchmarks become active.
type Score struct {
	AuditJobID   uuid.UUID
	InfluencerID uuid.UUID
	Niche        string
	Tier         string

	Overall           float64
	Reach             Subscore
	EngagementQuality Subscore
	Authenticity      Subscore
	Consistency       Subscore
	ContentQuality    Subscore

	// OverallConfidence is the weight-blended confidence across the subscores.
	OverallConfidence float64

	WeightsVersion   int
	BenchmarkVersion int
	// BenchmarkLabel is the human-facing provenance of the benchmark generation,
	// e.g. "industry-bootstrap v1" for cold-start reference bands or "corpus v3"
	// once real percentiles have replaced them.
	BenchmarkLabel string

	// ContributingPlatforms names the platforms that actually fed the number. A
	// partial audit records the reduced set so a consumer never reads the score
	// as if it covered every requested platform.
	ContributingPlatforms []connector.Platform

	// VerificationTier is the trust tier derived from the data sources that fed
	// the score (see DeriveVerificationTier): "verified", "estimated", or
	// "unverified".
	VerificationTier string

	CreatedAt time.Time
}

// Profiles resolves the content niche of an influencer. scoring keys its weights
// and benchmarks on (niche, tier); tier is derived from the live follower count,
// but niche is a content category that platform metrics do not carry, so scoring
// reaches the influencer module through this port. The composition root wires
// it; the influencer module already stores the niche.
type Profiles interface {
	// NicheOf returns the influencer's niche. An empty string (with a nil error)
	// is treated as "unknown" and falls back to the default benchmark cohort.
	NicheOf(ctx context.Context, influencerID uuid.UUID) (string, error)
}
