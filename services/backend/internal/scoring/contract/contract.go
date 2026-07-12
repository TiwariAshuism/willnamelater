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
	"encoding/json"
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

// The Basis names WHAT PRODUCED a subscore's value, so a reader can tell an
// arithmetic proxy apart from a real percentile against a reference population
// and from a trained model's output. Without it every subscore looks alike on the
// wire, and a closed-form formula reads as if it were a measurement.
const (
	// BasisClosedForm: the value is arithmetic over the audit's own data (a log
	// scale over follower count, a ratio of comments to likes, a ladder against
	// fixed reference constants). It is a PROXY. Nothing about it has been
	// validated against an outcome.
	BasisClosedForm = "closed_form"
	// BasisCorpus: the value is the observed metric's percentile within a
	// reference population aggregated from real, distinct, live-API-sourced
	// audits.
	BasisCorpus = "corpus"
	// BasisModelPrefix prefixes a trained model's basis; ModelBasis builds it.
	BasisModelPrefix = "model:"
)

// ModelBasis renders the basis of a subscore produced by a trained model at a
// given version, e.g. "model:fraud-2026-06-01". An unversioned champion is
// labelled "model:unversioned" rather than being passed off as closed form: the
// reader still learns a model produced the number.
func ModelBasis(version string) string {
	if version == "" {
		return BasisModelPrefix + "unversioned"
	}
	return BasisModelPrefix + version
}

// The SupportKind names WHAT KIND OF CLAIM a subscore's Support number makes.
// The three are NOT interchangeable, and collapsing them into one word
// ("confidence") is how a data-coverage count comes to be read as a statement
// about accuracy:
const (
	// SupportCoverage: the share of the data the closed-form proxy wanted that we
	// actually had (e.g. posts observed / posts needed). It says NOTHING about
	// whether the proxy tracks anything real — ten posts make the coverage full
	// and leave the proxy exactly as unvalidated as it was at one.
	SupportCoverage = "coverage"
	// SupportPrior: a documented constant discount resting on NO observations at
	// all — a reference band, or a known-weak proxy deliberately held down. It is
	// a judgement we wrote into the code, not a measurement.
	SupportPrior = "prior"
	// SupportConfidence: a genuine statistical claim — the sample size of a
	// reference population actually observed, or a model's own calibrated
	// confidence. This is the ONLY kind that may be read as "how sure are we".
	SupportConfidence = "confidence"
	// SupportNone: the component was not measured. Support is 0 and the component
	// is dropped from the composite with its weight renormalized away.
	SupportNone = "none"
)

// Subscore is one component of the composite: its value on a 0..100 scale, the
// Basis that produced it, and a Support in [0,1] whose MEANING is given by
// SupportKind.
//
// Support (not "confidence") is the single number the composite shrinks the value
// toward neutral by, and a Support of 0 drops the component entirely. It replaces
// a field called Confidence that conflated three different things — most starkly
// the content subscore, whose "confidence" was literally postCount/10: a count of
// how much data we had, saturating at ten posts, asserting nothing whatsoever
// about whether the proxy predicts anything. Read SupportKind before reading
// Support.
//
// A Subscore decoded from a row persisted before this type existed carries its
// legacy confidence in Support with an EMPTY Basis and SupportKind — unrecorded,
// rather than back-filled with a label nobody actually stamped (see UnmarshalJSON).
type Subscore struct {
	Value       float64 `json:"value"`
	Basis       string  `json:"basis,omitempty"`
	Support     float64 `json:"support"`
	SupportKind string  `json:"support_kind,omitempty"`
}

// UnmarshalJSON decodes a subscore, accepting the legacy {"value","confidence"}
// shape written before basis and support kind existed. The legacy number moves
// into Support and the basis/kind stay EMPTY: the row genuinely does not record
// what produced the value or what the number meant, and guessing a label here
// would invent provenance that was never observed.
func (s *Subscore) UnmarshalJSON(b []byte) error {
	var raw struct {
		Value       float64  `json:"value"`
		Basis       string   `json:"basis"`
		Support     *float64 `json:"support"`
		SupportKind string   `json:"support_kind"`
		Confidence  *float64 `json:"confidence"` // legacy
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = Subscore{Value: raw.Value, Basis: raw.Basis, SupportKind: raw.SupportKind}
	switch {
	case raw.Support != nil:
		s.Support = *raw.Support
	case raw.Confidence != nil:
		s.Support = *raw.Confidence
	}
	return nil
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

	// OverallConfidence is the weight-blended SUPPORT across the subscores. It
	// mixes support kinds (coverage, prior, confidence), so it is a summary of how
	// much stands behind the composite, NOT a statistical confidence in it. The
	// per-subscore Basis/SupportKind are the honest reading; this number exists
	// because the score API has carried a single "confidence" field since v1.
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
