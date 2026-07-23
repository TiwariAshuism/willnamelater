package service

import (
	"testing"

	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/presentation"
)

func findFactor(factors []model.FactorPresentation, key string) (model.FactorPresentation, bool) {
	for _, f := range factors {
		if f.Key == key {
			return f, true
		}
	}
	return model.FactorPresentation{}, false
}

// TestFactorPresentationsMeasured: every measured factor gets its intrinsic
// availability, a banded pill, and a non-empty improvement line; Engagement
// Authenticity additionally carries the modeled authenticity sub-note.
func TestFactorPresentationsMeasured(t *testing.T) {
	subs := map[string]contract.Subscore{
		contract.ComponentEngagementAuthenticity: {Value: 80, Support: 0.9}, // strong
		contract.ComponentAudienceQuality:        {Value: 50, Support: 0.6}, // solid
		contract.ComponentConsistencyReliability: {Value: 30, Support: 0.5}, // needs_work
		contract.ComponentBrandFitClarity:        {Value: 66, Support: 0.4}, // strong (exact cut)
	}
	factors := factorPresentations(subs, "micro")

	if len(factors) != 4 {
		t.Fatalf("got %d factors, want 4 (one card per factor)", len(factors))
	}
	// Stable composite order.
	wantOrder := []string{
		contract.ComponentEngagementAuthenticity,
		contract.ComponentAudienceQuality,
		contract.ComponentConsistencyReliability,
		contract.ComponentBrandFitClarity,
	}
	for i, key := range wantOrder {
		if factors[i].Key != key {
			t.Errorf("factor[%d].Key = %q, want %q", i, factors[i].Key, key)
		}
	}

	ea, _ := findFactor(factors, contract.ComponentEngagementAuthenticity)
	if ea.Availability != presentation.AvailabilityVerified {
		t.Errorf("engagement availability = %q, want verified", ea.Availability)
	}
	if ea.Band != presentation.BandStrong {
		t.Errorf("engagement band = %q, want strong", ea.Band)
	}
	if ea.ImprovementLine == "" {
		t.Error("engagement improvement line is empty; a measured factor must have one")
	}
	if ea.AuthenticityNote != presentation.AuthenticityNote {
		t.Errorf("engagement authenticity note = %q, want the modeled sub-note", ea.AuthenticityNote)
	}

	aq, _ := findFactor(factors, contract.ComponentAudienceQuality)
	if aq.Availability != presentation.AvailabilityVerified || aq.Band != presentation.BandSolid {
		t.Errorf("audience = (%q,%q), want (verified,solid)", aq.Availability, aq.Band)
	}
	if aq.AuthenticityNote != "" {
		t.Errorf("only engagement carries the authenticity note; audience had %q", aq.AuthenticityNote)
	}

	cr, _ := findFactor(factors, contract.ComponentConsistencyReliability)
	if cr.Availability != presentation.AvailabilityVerified || cr.Band != presentation.BandNeedsWork {
		t.Errorf("consistency = (%q,%q), want (verified,needs_work)", cr.Availability, cr.Band)
	}

	bf, _ := findFactor(factors, contract.ComponentBrandFitClarity)
	if bf.Availability != presentation.AvailabilityModeled || bf.Band != presentation.BandStrong {
		t.Errorf("brand-fit = (%q,%q), want (modeled,strong)", bf.Availability, bf.Band)
	}
}

// TestFactorPresentationsUnmeasured: a Support-0 factor is NEVER banded. It
// renders an honest availability, band not_assessed, and no improvement line.
// Audience Quality specifically reads "not available at your size".
func TestFactorPresentationsUnmeasured(t *testing.T) {
	subs := map[string]contract.Subscore{
		// Support 0 -> dropped/unmeasured, even though Value looks like a real number.
		contract.ComponentEngagementAuthenticity: {Value: 90, Support: 0},
		contract.ComponentAudienceQuality:        {Value: 0, Support: 0},
		// Consistency & Brand-Fit absent from the map entirely -> also unmeasured.
	}
	factors := factorPresentations(subs, "nano")

	ea, _ := findFactor(factors, contract.ComponentEngagementAuthenticity)
	if ea.Availability != presentation.AvailabilityNotMeasured {
		t.Errorf("unmeasured engagement availability = %q, want not_measured", ea.Availability)
	}
	if ea.Band != presentation.BandNotAssessed {
		t.Errorf("unmeasured engagement band = %q, want not_assessed", ea.Band)
	}
	if ea.ImprovementLine != "" {
		t.Errorf("unmeasured factor must have no improvement line, got %q", ea.ImprovementLine)
	}
	if ea.AuthenticityNote != "" {
		t.Errorf("unmeasured engagement must carry no authenticity note, got %q", ea.AuthenticityNote)
	}

	aq, _ := findFactor(factors, contract.ComponentAudienceQuality)
	if aq.Availability != presentation.AvailabilityNotAvailableAtSize {
		t.Errorf("unmeasured audience availability = %q, want not_available_at_size", aq.Availability)
	}
	if aq.Band != presentation.BandNotAssessed || aq.ImprovementLine != "" {
		t.Errorf("unmeasured audience = (%q, line %q), want (not_assessed, empty)", aq.Band, aq.ImprovementLine)
	}

	// Absent-from-map factors still get an honest card.
	cr, ok := findFactor(factors, contract.ComponentConsistencyReliability)
	if !ok {
		t.Fatal("absent consistency factor produced no card; every factor must have one")
	}
	if cr.Availability != presentation.AvailabilityNotMeasured || cr.Band != presentation.BandNotAssessed {
		t.Errorf("absent consistency = (%q,%q), want (not_measured,not_assessed)", cr.Availability, cr.Band)
	}
}

// TestToScoreResponsePresentation checks the presentation fields flow onto the
// read DTO: Factors is populated and EngagementRateBand is derived from the
// observed rate + tier (and omitted when the rate is absent).
func TestToScoreResponsePresentation(t *testing.T) {
	er := 0.06 // 6% -> strong at mid
	row := model.ScoreRow{
		Overall: 70,
		Breakdown: model.Breakdown{
			Tier:                   "mid",
			ObservedEngagementRate: &er,
			Subscores: map[string]contract.Subscore{
				contract.ComponentEngagementAuthenticity: {Value: 70, Support: 0.8},
			},
		},
	}
	resp := toScoreResponse(row)
	if len(resp.Factors) != 4 {
		t.Fatalf("resp.Factors len = %d, want 4", len(resp.Factors))
	}
	if resp.EngagementRateBand != presentation.BandStrong {
		t.Errorf("EngagementRateBand = %q, want strong (6%% at mid)", resp.EngagementRateBand)
	}

	// No observed rate -> band omitted.
	row.Breakdown.ObservedEngagementRate = nil
	if got := toScoreResponse(row).EngagementRateBand; got != "" {
		t.Errorf("EngagementRateBand with no observed rate = %q, want empty", got)
	}
}
