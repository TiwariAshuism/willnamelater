package model

import "github.com/getnyx/influaudit/backend/internal/mlops/contract"

// ToFeatureRowItem projects a stored feature row onto its export DTO. Features is
// passed through verbatim so the frozen key order and JSON nulls survive; the
// label sources are flattened from *string to a possibly-empty string the
// omitempty tag drops when absent.
func ToFeatureRowItem(r FeatureRow) FeatureRowItem {
	item := FeatureRowItem{
		AuditJobID:            r.AuditJobID.String(),
		InfluencerID:          r.InfluencerID.String(),
		Platform:              r.Platform,
		Features:              r.Features,
		FraudLabel:            r.FraudLabel,
		ReachLabel:            r.ReachLabel,
		ReachIsOrganic:        r.ReachOrganic,
		QualityOK:             r.QualityOK,
		QualityReasons:        r.QualityReasons,
		TrainingEligible:      TrainingEligible(r.QualityReasons, r.FraudLabel, r.FraudLabelEvidence),
		ModelVersionAtCapture: r.ModelVersionAtCapture,
		VerificationTier:      r.VerificationTier,
		CapturedAt:            r.CapturedAt,
	}
	if r.QualityReasons == nil {
		item.QualityReasons = []string{}
	}
	if r.FraudLabelSource != nil {
		item.FraudLabelSource = *r.FraudLabelSource
	}
	if r.FraudLabelEvidence != nil {
		item.FraudLabelEvidence = *r.FraudLabelEvidence
	}

	// A label with no OBSERVATION behind it is exported as UNLABELLED — fraud_label
	// is nulled — no matter how clean the row otherwise is.
	//
	// TrainingEligible alone is not enough to prevent this: it governs only whether
	// our own fraud-risk reason may censor the row, so a heuristic-echo label
	// sitting on a row with no quality reasons at all would sail through as a
	// perfectly eligible y. The trainer is also told to filter on the evidence, but
	// relying on a downstream consumer to remember is how laundering happens. So the
	// echo never leaves this process wearing a label.
	//
	// The evidence kind still ships, so the trainer can SEE that the row exists and
	// why it is unlabelled, and so a near-empty positive class reads as the honest
	// state of the world rather than as a bug.
	if r.FraudLabel != nil && !contract.FraudLabelEvidence(item.FraudLabelEvidence).Observable() {
		item.FraudLabel = nil
	}
	if r.ReachLabelSource != nil {
		item.ReachLabelSource = *r.ReachLabelSource
	}
	return item
}

// ToCanaryItem projects a stored canary onto its DTO.
func ToCanaryItem(c Canary) CanaryItem {
	return CanaryItem{
		ID:               c.ID.String(),
		ModelName:        c.ModelName,
		AuditJobID:       c.AuditJobID.String(),
		Label:            c.Label,
		Features:         c.Features,
		ExpectedLabel:    c.ExpectedLabel,
		ExpectedReachMin: c.ExpectedReachMin,
		ExpectedReachMax: c.ExpectedReachMax,
		ProvenanceKind:   c.ProvenanceKind,
		Active:           c.Active,
		CreatedAt:        c.CreatedAt,
	}
}
