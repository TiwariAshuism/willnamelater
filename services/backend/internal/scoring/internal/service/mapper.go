package service

import (
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/engine"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
)

// toScoreRow maps a computed score plus the audit's snapshots onto the
// persistence row. The typed columns hold the four subscores the schema has a
// slot for; reach is stored in the audience_quality column (its closest slot)
// and consistency lives only in the breakdown, which additionally carries every
// subscore's confidence, the resolved cell, and the observed engagement rate
// that corpus recomputation later aggregates.
func toScoreRow(score contract.Score, snapshots []connector.Snapshot) model.ScoreRow {
	authenticity := score.Authenticity.Value
	engagement := score.EngagementQuality.Value
	reach := score.Reach.Value
	content := score.ContentQuality.Value

	row := model.ScoreRow{
		AuditJobID:            score.AuditJobID,
		Overall:               score.Overall,
		Authenticity:          &authenticity,
		Engagement:            &engagement,
		AudienceQuality:       &reach,
		ContentQuality:        &content,
		WeightsVersion:        score.WeightsVersion,
		BenchmarkVersion:      score.BenchmarkVersion,
		ContributingPlatforms: platformStrings(score.ContributingPlatforms),
		Breakdown: model.Breakdown{
			Niche:             score.Niche,
			Tier:              score.Tier,
			OverallConfidence: score.OverallConfidence,
			BenchmarkLabel:    score.BenchmarkLabel,
			Subscores:         subscoreMap(score),
		},
	}
	if score.InfluencerID != uuid.Nil {
		id := score.InfluencerID
		row.InfluencerID = &id
	}
	if er, ok := engine.ObservedEngagementRate(snapshots); ok {
		row.Breakdown.ObservedEngagementRate = &er
	}
	return row
}

// toScoreResponse maps a persisted row onto the read DTO. Subscores and
// confidence come from the breakdown, since the typed columns carry only the
// point values.
func toScoreResponse(row model.ScoreRow) model.ScoreResponse {
	resp := model.ScoreResponse{
		AuditJobID:            row.AuditJobID.String(),
		Niche:                 row.Breakdown.Niche,
		Tier:                  row.Breakdown.Tier,
		Overall:               row.Overall,
		Confidence:            row.Breakdown.OverallConfidence,
		Subscores:             row.Breakdown.Subscores,
		WeightsVersion:        row.WeightsVersion,
		BenchmarkVersion:      row.BenchmarkVersion,
		BenchmarkLabel:        row.Breakdown.BenchmarkLabel,
		ContributingPlatforms: row.ContributingPlatforms,
		CreatedAt:             row.CreatedAt,
	}
	if row.InfluencerID != nil {
		resp.InfluencerID = row.InfluencerID.String()
	}
	if resp.Subscores == nil {
		resp.Subscores = map[string]contract.Subscore{}
	}
	if resp.ContributingPlatforms == nil {
		resp.ContributingPlatforms = []string{}
	}
	return resp
}

// subscoreMap keys the five subscores by their stable component names for the
// breakdown JSON.
func subscoreMap(score contract.Score) map[string]contract.Subscore {
	return map[string]contract.Subscore{
		contract.ComponentReach:             score.Reach,
		contract.ComponentEngagementQuality: score.EngagementQuality,
		contract.ComponentAuthenticity:      score.Authenticity,
		contract.ComponentConsistency:       score.Consistency,
		contract.ComponentContentQuality:    score.ContentQuality,
	}
}

// platformStrings converts the typed platforms to the string slice the
// platform[] column binds to.
func platformStrings(platforms []connector.Platform) []string {
	out := make([]string, len(platforms))
	for i, p := range platforms {
		out[i] = string(p)
	}
	return out
}
