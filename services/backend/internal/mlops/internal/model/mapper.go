package model

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
		QualityOK:             r.QualityOK,
		QualityReasons:        r.QualityReasons,
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
		Label:            c.Label,
		Features:         c.Features,
		ExpectedLabel:    c.ExpectedLabel,
		ExpectedReachMin: c.ExpectedReachMin,
		ExpectedReachMax: c.ExpectedReachMax,
		Source:           c.Source,
		Active:           c.Active,
		CreatedAt:        c.CreatedAt,
	}
}
