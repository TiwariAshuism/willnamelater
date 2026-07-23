// Package model holds the scoring module's data-transfer objects (the HTTP
// request/response shapes) and the persistence row the service and repository
// exchange. It depends only on the scoring contract leaf, never on the engine or
// the transport layers, so the shapes stay free of computation and I/O concerns.
package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

// ScoreRow is one row of the score table, in both directions: the service builds
// one to persist and the repository scans one to read. The nullable factor
// columns are pointers so a column the audit could not populate stays SQL NULL
// rather than collapsing to zero. Breakdown carries the full per-factor detail
// (supports, the resolved niche/tier, the authenticity sub-signal, and the
// observed engagement rate) that the typed columns cannot hold.
//
// The four typed columns are the CreatorTrust 4-factor composite. The retired
// influence columns (authenticity, engagement, content_quality) survive on the
// table for historical rows but are no longer written; see migration 000030.
type ScoreRow struct {
	AuditJobID             uuid.UUID
	InfluencerID           *uuid.UUID
	Overall                float64
	EngagementAuthenticity *float64
	AudienceQuality        *float64
	Consistency            *float64
	BrandFit               *float64
	WeightsVersion         int
	BenchmarkVersion       int
	ContributingPlatforms  []string
	VerificationTier       string
	Breakdown              Breakdown
	CreatedAt              time.Time
}

// Breakdown is the structured JSON persisted in score.breakdown. It records the
// scoring inputs a plain numeric column cannot: which (niche, tier) cell was
// used, every subscore's confidence, the benchmark provenance, and the observed
// engagement rate that corpus recomputation aggregates into real percentiles.
type Breakdown struct {
	Niche                  string                       `json:"niche"`
	Tier                   string                       `json:"tier"`
	OverallConfidence      float64                      `json:"overall_confidence"`
	BenchmarkLabel         string                       `json:"benchmark_label"`
	ObservedEngagementRate *float64                     `json:"observed_engagement_rate,omitempty"`
	Subscores              map[string]contract.Subscore `json:"subscores"`
	// AuthenticitySignal is the fraud/bot authenticity sub-signal, carried out of
	// the Engagement Authenticity factor for the constructive authenticity headline
	// (PRD §8.2). It is NOT a composite component; it is nil, or has Support 0, when
	// no fraud pass produced a usable signal, and the headline is then withheld.
	AuthenticitySignal *contract.Subscore `json:"authenticity_signal,omitempty"`
}

// Marshal renders the breakdown as JSON for the jsonb column.
func (b Breakdown) Marshal() ([]byte, error) { return json.Marshal(b) }

// ScoreResponse is the GET /influencers/:id/score body: the latest score with
// its full subscore breakdown and reproducibility stamps.
//
// Factors and EngagementRateBand are the deterministic PRESENTATION layer (Wave
// 5): a per-factor card (availability tier, tier-relative status band, plain
// improvement line) plus the observed engagement rate's tier band. They are pure
// lookup + banding over the same breakdown Subscores, Tier and
// ObservedEngagementRate — no new data, no LLM — so a result page can render
// per-metric cards without re-deriving anything. The bands are directional v1.
type ScoreResponse struct {
	AuditJobID            string                       `json:"audit_job_id"`
	InfluencerID          string                       `json:"influencer_id,omitempty"`
	Niche                 string                       `json:"niche"`
	Tier                  string                       `json:"tier"`
	Overall               float64                      `json:"overall"`
	Confidence            float64                      `json:"confidence"`
	Subscores             map[string]contract.Subscore `json:"subscores"`
	AuthenticitySignal    *contract.Subscore           `json:"authenticity_signal,omitempty"`
	Factors               []FactorPresentation         `json:"factors"`
	EngagementRateBand    string                       `json:"engagement_rate_band,omitempty"`
	WeightsVersion        int                          `json:"weights_version"`
	BenchmarkVersion      int                          `json:"benchmark_version"`
	BenchmarkLabel        string                       `json:"benchmark_label"`
	ContributingPlatforms []string                     `json:"contributing_platforms"`
	VerificationTier      string                       `json:"verification_tier"`
	CreatedAt             time.Time                    `json:"created_at"`
}

// FactorPresentation is one per-metric card in the result page's presentation
// layer. It is DERIVED, not measured: Availability names where the factor's data
// comes from, Band places its value in a directional v1 tier-relative band, and
// ImprovementLine is the plain-language "diagnosis not a course" for that
// (factor, band). A factor that was NOT measured (its Subscore.Support == 0)
// carries an honest Availability ("not_measured" or, for Audience Quality,
// "not_available_at_size"), Band "not_assessed", and an empty ImprovementLine —
// never a 0, a band, or a guessed line.
type FactorPresentation struct {
	Key             string `json:"key"`
	Label           string `json:"label"`
	Availability    string `json:"availability"`
	Band            string `json:"band"`
	ImprovementLine string `json:"improvement_line,omitempty"`
	// AuthenticityNote is the MODELED authenticity sub-note carried on the
	// otherwise-VERIFIED Engagement Authenticity factor; empty on every other card.
	AuthenticityNote string `json:"authenticity_note,omitempty"`
}

// ScoreHistoryResponse is the GET /influencers/:id/score/history body: the
// influencer's score over time, newest first.
type ScoreHistoryResponse struct {
	InfluencerID string       `json:"influencer_id"`
	Points       []ScorePoint `json:"points"`
}

// ScorePoint is one entry in the history series.
type ScorePoint struct {
	AuditJobID string    `json:"audit_job_id"`
	Overall    float64   `json:"overall"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}
