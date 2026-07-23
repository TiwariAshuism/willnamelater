package service

import (
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/engine"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/presentation"
)

// presentationFactorOrder is the stable order the per-factor cards render in — the
// composite weight order (PRD §6). Iterating a fixed slice (not the Subscores map)
// keeps the response deterministic and gives every factor a card even when its
// subscore is absent from the breakdown (treated as unmeasured).
var presentationFactorOrder = []string{
	contract.ComponentEngagementAuthenticity,
	contract.ComponentAudienceQuality,
	contract.ComponentConsistencyReliability,
	contract.ComponentBrandFitClarity,
}

// toScoreRow maps a computed score plus the audit's snapshots onto the
// persistence row. The four typed columns hold the four hireability factors; the
// authenticity sub-signal and every factor's support live in the breakdown, which
// additionally carries the resolved cell and the observed engagement rate that
// corpus recomputation later aggregates.
func toScoreRow(score contract.Score, snapshots []connector.Snapshot) model.ScoreRow {
	engagementAuthenticity := score.EngagementAuthenticity.Value
	audienceQuality := score.AudienceQuality.Value
	consistency := score.ConsistencyReliability.Value
	brandFit := score.BrandFitClarity.Value
	authenticitySignal := score.FraudAuthenticity

	row := model.ScoreRow{
		AuditJobID:             score.AuditJobID,
		Overall:                score.Overall,
		EngagementAuthenticity: &engagementAuthenticity,
		AudienceQuality:        &audienceQuality,
		Consistency:            &consistency,
		BrandFit:               &brandFit,
		WeightsVersion:         score.WeightsVersion,
		BenchmarkVersion:       score.BenchmarkVersion,
		ContributingPlatforms:  platformStrings(score.ContributingPlatforms),
		VerificationTier:       score.VerificationTier,
		Breakdown: model.Breakdown{
			Niche:              score.Niche,
			Tier:               score.Tier,
			OverallConfidence:  score.OverallConfidence,
			BenchmarkLabel:     score.BenchmarkLabel,
			Subscores:          subscoreMap(score),
			AuthenticitySignal: &authenticitySignal,
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
		AuthenticitySignal:    row.Breakdown.AuthenticitySignal,
		WeightsVersion:        row.WeightsVersion,
		BenchmarkVersion:      row.BenchmarkVersion,
		BenchmarkLabel:        row.Breakdown.BenchmarkLabel,
		ContributingPlatforms: row.ContributingPlatforms,
		VerificationTier:      row.VerificationTier,
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
	resp.Factors = factorPresentations(row.Breakdown.Subscores, row.Breakdown.Tier)
	if row.Breakdown.ObservedEngagementRate != nil {
		resp.EngagementRateBand = presentation.EngagementRateBand(*row.Breakdown.ObservedEngagementRate, row.Breakdown.Tier)
	}
	return resp
}

// factorPresentations builds the per-factor cards for the result page from the
// breakdown Subscores and the resolved tier. It walks the fixed factor order so
// the output is deterministic and every factor gets a card. A factor whose
// Subscore has Support 0 (or is absent from the map) is NOT banded: it renders an
// honest "not measured"/"not available at your size" availability, band
// "not_assessed", and an empty improvement line — never a 0, a band, or a guessed
// line. This is the honesty firewall of the presentation layer.
func factorPresentations(subscores map[string]contract.Subscore, tier string) []model.FactorPresentation {
	factors := make([]model.FactorPresentation, 0, len(presentationFactorOrder))
	for _, key := range presentationFactorOrder {
		sub, measured := subscores[key]
		card := model.FactorPresentation{
			Key:   key,
			Label: presentation.LabelFor(key),
		}
		if !measured || sub.Support <= 0 {
			// Not measured: honest absence, no band, no line.
			card.Availability = presentation.UnmeasuredAvailabilityFor(key)
			card.Band = presentation.BandNotAssessed
			factors = append(factors, card)
			continue
		}
		card.Availability = presentation.AvailabilityFor(key)
		card.Band = presentation.BandFor(key, sub.Value, tier)
		card.ImprovementLine = presentation.ImprovementLineFor(key, card.Band)
		if key == contract.ComponentEngagementAuthenticity {
			card.AuthenticityNote = presentation.AuthenticityNote
		}
		factors = append(factors, card)
	}
	return factors
}

// subscoreMap keys the four composite factors by their stable component names
// for the breakdown JSON. The fraud authenticity sub-signal is NOT here — it is
// not a composite component; it rides in Breakdown.AuthenticitySignal.
func subscoreMap(score contract.Score) map[string]contract.Subscore {
	return map[string]contract.Subscore{
		contract.ComponentEngagementAuthenticity: score.EngagementAuthenticity,
		contract.ComponentAudienceQuality:        score.AudienceQuality,
		contract.ComponentConsistencyReliability: score.ConsistencyReliability,
		contract.ComponentBrandFitClarity:        score.BrandFitClarity,
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
