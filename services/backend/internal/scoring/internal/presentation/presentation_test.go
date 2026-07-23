package presentation

import (
	"strings"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

// allFactors is the four live composite factors, in composite order.
var allFactors = []string{
	contract.ComponentEngagementAuthenticity,
	contract.ComponentAudienceQuality,
	contract.ComponentConsistencyReliability,
	contract.ComponentBrandFitClarity,
}

func TestAvailabilityFor(t *testing.T) {
	cases := []struct {
		factor string
		want   string
	}{
		{contract.ComponentEngagementAuthenticity, AvailabilityVerified},
		{contract.ComponentAudienceQuality, AvailabilityVerified},
		{contract.ComponentConsistencyReliability, AvailabilityVerified},
		{contract.ComponentBrandFitClarity, AvailabilityModeled},
		{"unknown_factor", ""},
	}
	for _, c := range cases {
		if got := AvailabilityFor(c.factor); got != c.want {
			t.Errorf("AvailabilityFor(%q) = %q, want %q", c.factor, got, c.want)
		}
	}
}

func TestUnmeasuredAvailabilityFor(t *testing.T) {
	cases := []struct {
		factor string
		want   string
	}{
		// Audience Quality below the follower floor is a size fact, not a failing.
		{contract.ComponentAudienceQuality, AvailabilityNotAvailableAtSize},
		{contract.ComponentEngagementAuthenticity, AvailabilityNotMeasured},
		{contract.ComponentConsistencyReliability, AvailabilityNotMeasured},
		{contract.ComponentBrandFitClarity, AvailabilityNotMeasured},
		{"unknown_factor", AvailabilityNotMeasured},
	}
	for _, c := range cases {
		if got := UnmeasuredAvailabilityFor(c.factor); got != c.want {
			t.Errorf("UnmeasuredAvailabilityFor(%q) = %q, want %q", c.factor, got, c.want)
		}
	}
}

func TestLabelFor(t *testing.T) {
	cases := []struct {
		factor string
		want   string
	}{
		{contract.ComponentEngagementAuthenticity, "Engagement Authenticity"},
		{contract.ComponentAudienceQuality, "Audience Quality"},
		{contract.ComponentConsistencyReliability, "Consistency & Reliability"},
		{contract.ComponentBrandFitClarity, "Brand-Fit Clarity"},
		{"unknown_factor", "unknown_factor"}, // total: unknown key echoes back
	}
	for _, c := range cases {
		if got := LabelFor(c.factor); got != c.want {
			t.Errorf("LabelFor(%q) = %q, want %q", c.factor, got, c.want)
		}
	}
}

// TestBandFor exercises the exact directional-v1 placeholder cuts on the 0..100
// composite scale: strong >= 66, solid [40, 66), needs_work < 40. The boundaries
// (66.0, 40.0) and the values just below them are asserted explicitly.
func TestBandFor(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		want  string
	}{
		{"far above strong", 100, BandStrong},
		{"exactly strong cut", 66.0, BandStrong},
		{"just below strong cut", 65.999, BandSolid},
		{"mid solid", 50, BandSolid},
		{"exactly solid cut", 40.0, BandSolid},
		{"just below solid cut", 39.999, BandNeedsWork},
		{"zero", 0, BandNeedsWork},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The cut is the same for every factor in v1, and tier is not read.
			for _, f := range allFactors {
				for _, tier := range []string{"", "nano", "micro", "mid", "macro", "mega"} {
					if got := BandFor(f, c.value, tier); got != c.want {
						t.Errorf("BandFor(%q, %v, %q) = %q, want %q", f, c.value, tier, got, c.want)
					}
				}
			}
		})
	}
}

// TestEngagementRateBand exercises the PRD §15 tier bands as fractions. Micro:
// strong >=5%, solid >=2.5%, else needs_work. Mid: strong >=3%, solid >=1.5%,
// else needs_work. Every other tier has no published band and is not_assessed.
func TestEngagementRateBand(t *testing.T) {
	cases := []struct {
		name string
		rate float64
		tier string
		want string
	}{
		// Micro (10K–100K)
		{"micro strong exact", 0.05, tierMicro, BandStrong},
		{"micro strong above", 0.08, tierMicro, BandStrong},
		{"micro solid just below strong", 0.04999, tierMicro, BandSolid},
		{"micro solid exact", 0.025, tierMicro, BandSolid},
		{"micro needs-work just below solid", 0.02499, tierMicro, BandNeedsWork},
		{"micro needs-work zero", 0, tierMicro, BandNeedsWork},
		// Mid (100K–500K)
		{"mid strong exact", 0.03, tierMid, BandStrong},
		{"mid strong above", 0.06, tierMid, BandStrong},
		{"mid solid just below strong", 0.02999, tierMid, BandSolid},
		{"mid solid exact", 0.015, tierMid, BandSolid},
		{"mid needs-work just below solid", 0.01499, tierMid, BandNeedsWork},
		{"mid needs-work zero", 0, tierMid, BandNeedsWork},
		// Tiers with no published band yet.
		{"nano not assessed", 0.10, "nano", BandNotAssessed},
		{"macro not assessed", 0.10, "macro", BandNotAssessed},
		{"mega not assessed", 0.10, "mega", BandNotAssessed},
		{"empty tier not assessed", 0.10, "", BandNotAssessed},
		{"unknown tier not assessed", 0.10, "colossal", BandNotAssessed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EngagementRateBand(c.rate, c.tier); got != c.want {
				t.Errorf("EngagementRateBand(%v, %q) = %q, want %q", c.rate, c.tier, got, c.want)
			}
		})
	}
}

// TestImprovementLineForEveryFactorAndBand asserts a non-empty line for every
// (factor, banded state) and that not_assessed / unknown keys yield the empty
// string — the layer never guesses a line for something it did not band.
func TestImprovementLineForEveryFactorAndBand(t *testing.T) {
	bandedStates := []string{BandStrong, BandSolid, BandNeedsWork}
	for _, f := range allFactors {
		for _, b := range bandedStates {
			line := ImprovementLineFor(f, b)
			if strings.TrimSpace(line) == "" {
				t.Errorf("ImprovementLineFor(%q, %q) is empty; every banded state needs a line", f, b)
			}
			// PRD §7: a diagnosis, not a course, at most two sentences.
			if n := strings.Count(line, ". "); n > 1 {
				t.Errorf("ImprovementLineFor(%q, %q) has more than two sentences: %q", f, b, line)
			}
			// Every line must point at brand-readiness, never at follower growth for
			// its own sake.
			if strings.Contains(strings.ToLower(line), "grow your following") ||
				strings.Contains(strings.ToLower(line), "gain followers") ||
				strings.Contains(strings.ToLower(line), "more followers") {
				t.Errorf("ImprovementLineFor(%q, %q) chases follower growth: %q", f, b, line)
			}
		}
		// not_assessed and unknown band -> empty.
		if got := ImprovementLineFor(f, BandNotAssessed); got != "" {
			t.Errorf("ImprovementLineFor(%q, not_assessed) = %q, want empty", f, got)
		}
		if got := ImprovementLineFor(f, "unknown_band"); got != "" {
			t.Errorf("ImprovementLineFor(%q, unknown_band) = %q, want empty", f, got)
		}
	}
	// Unknown factor -> empty for any band.
	if got := ImprovementLineFor("unknown_factor", BandNeedsWork); got != "" {
		t.Errorf("ImprovementLineFor(unknown_factor, needs_work) = %q, want empty", got)
	}
}

// TestImprovementLineNeedsWorkIsPRDVerbatim pins the needs_work copy to the PRD §7
// source lines, so an edit to the actionable state is a deliberate, reviewed change.
func TestImprovementLineNeedsWorkIsPRDVerbatim(t *testing.T) {
	want := map[string]string{
		contract.ComponentEngagementAuthenticity: "Post save-worthy carousels and how-tos, reply in the first hour, and ask questions. Never buy followers or engagement — a brand's fraud check flags the same risk.",
		contract.ComponentAudienceQuality:        "Know and state who follows you; a tight, defined, brand-relevant audience beats broad random reach.",
		contract.ComponentConsistencyReliability: "Keep a reliable posting schedule and execute multiple formats — it tells a brand you deliver and lowers their lift.",
		contract.ComponentBrandFitClarity:        "Log past brand work with real numbers and keep a coherent niche; a track record commands premium rates.",
	}
	for factor, w := range want {
		if got := ImprovementLineFor(factor, BandNeedsWork); got != w {
			t.Errorf("ImprovementLineFor(%q, needs_work) = %q, want PRD verbatim %q", factor, got, w)
		}
	}
}
