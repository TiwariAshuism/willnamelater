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
// one to persist and the repository scans one to read. The nullable subscore
// columns are pointers so a column the audit could not populate stays SQL NULL
// rather than collapsing to zero. Breakdown carries the full per-subscore detail
// (confidences, the resolved niche/tier, and the observed engagement rate) that
// the typed columns cannot hold.
type ScoreRow struct {
	AuditJobID            uuid.UUID
	InfluencerID          *uuid.UUID
	Overall               float64
	Authenticity          *float64
	Engagement            *float64
	AudienceQuality       *float64
	ContentQuality        *float64
	WeightsVersion        int
	BenchmarkVersion      int
	ContributingPlatforms []string
	VerificationTier      string
	Breakdown             Breakdown
	CreatedAt             time.Time
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
}

// Marshal renders the breakdown as JSON for the jsonb column.
func (b Breakdown) Marshal() ([]byte, error) { return json.Marshal(b) }

// ScoreResponse is the GET /influencers/:id/score body: the latest score with
// its full subscore breakdown and reproducibility stamps.
type ScoreResponse struct {
	AuditJobID            string                       `json:"audit_job_id"`
	InfluencerID          string                       `json:"influencer_id,omitempty"`
	Niche                 string                       `json:"niche"`
	Tier                  string                       `json:"tier"`
	Overall               float64                      `json:"overall"`
	Confidence            float64                      `json:"confidence"`
	Subscores             map[string]contract.Subscore `json:"subscores"`
	WeightsVersion        int                          `json:"weights_version"`
	BenchmarkVersion      int                          `json:"benchmark_version"`
	BenchmarkLabel        string                       `json:"benchmark_label"`
	ContributingPlatforms []string                     `json:"contributing_platforms"`
	VerificationTier      string                       `json:"verification_tier"`
	CreatedAt             time.Time                    `json:"created_at"`
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
