// Package presentation is the scoring module's deterministic PRESENTATION layer:
// pure lookup + banding that turns a computed factor Subscore into the three
// human-facing strings a result page renders per metric card — an availability
// tier, a tier-relative status band, and a plain-language improvement line.
//
// It contains NO model, NO randomness, and reaches NO network or LLM. Every
// output is a stable string enum produced by a total function over its inputs, so
// the same (factor, value, tier) always yields the same card. The bands are
// DIRECTIONAL v1 (documented at each cut) — conservative placeholders on the
// 0..100 composite scale and the PRD §15 engagement-rate tier bands — to be
// replaced by real percentile bands once a corpus exists. Nothing here claims
// false precision.
//
// The one honesty rule the whole layer exists to keep: a factor that was NOT
// measured (Subscore.Support == 0) must NEVER be handed to BandFor or
// ImprovementLineFor. The caller detects Support == 0 first and renders the
// unmeasured availability + BandNotAssessed + an empty line; see the service
// mapper. These functions band only values that rest on a real measurement.
package presentation

import "github.com/getnyx/influaudit/backend/internal/scoring/contract"

// Availability tiers describe WHERE a factor's data comes from, mapping the PRD
// availability legend onto the four live factors. They are stable, persisted-safe
// string enums surfaced on each metric card.
const (
	// AvailabilityVerified: pulled from Meta and un-editable (engagement rate,
	// save/share rate, reach ratio, follower-derived audience, posting history).
	AvailabilityVerified = "verified"
	// AvailabilityModeled: a directional signal COMPUTED on pulled data
	// (caption-detected #ad/#sponsored history + light brand-safety).
	AvailabilityModeled = "modeled"
	// AvailabilityDeclared: self-reported, NEVER scored. No live factor maps here;
	// the enum exists so the legend is complete and a future declared field is named.
	AvailabilityDeclared = "declared"
	// AvailabilityScopePlus: needs an added OAuth scope before it can be pulled.
	// No live factor maps here yet; named for the same reason as declared.
	AvailabilityScopePlus = "scope_plus"

	// AvailabilityNotMeasured: the factor could be verified in principle but this
	// audit did not measure it (Support == 0). It is rendered as an honest absence,
	// never as a 0 or a band.
	AvailabilityNotMeasured = "not_measured"
	// AvailabilityNotAvailableAtSize: Audience Quality specifically — Meta returns
	// no demographics below 100 followers (PRD §9), so an unmeasured audience is
	// "not available at your size", a size fact, not a failing.
	AvailabilityNotAvailableAtSize = "not_available_at_size"
)

// Status bands are RELATIVE TO FOLLOWER TIER (PRD §15) and DIRECTIONAL v1: for
// the four composite factor values (0..100) no clean external benchmark exists
// yet, so the cuts below are conservative placeholders, not percentiles. They
// will be replaced by real percentile bands once a corpus exists.
const (
	// BandStrong: value >= bandStrongMin. A reinforcing card.
	BandStrong = "strong"
	// BandSolid: bandSolidMin <= value < bandStrongMin. Room to sharpen.
	BandSolid = "solid"
	// BandNeedsWork: value < bandSolidMin. The most actionable card.
	BandNeedsWork = "needs_work"
	// BandNotAssessed: the factor was not measured (Support == 0). NEVER a pill —
	// no value was banded, so no band is claimed.
	BandNotAssessed = "not_assessed"
)

// Placeholder cuts on the 0..100 composite scale. DIRECTIONAL v1 — documented as
// such, unit-tested at the exact boundaries, and deliberately conservative: a
// factor must clear ~66 to read "strong" and dip below ~40 to read "needs work",
// leaving a wide "solid" middle so the card never over-praises a thin proxy.
const (
	bandStrongMin = 66.0
	bandSolidMin  = 40.0
)

// Engagement-rate tier bands (PRD §15), as FRACTIONS to match the engine's
// observed engagement rate (engagements / followers, e.g. 0.05 == 5%). Only Micro
// and Mid are specified in v1; other tiers have no published band yet and yield
// BandNotAssessed rather than an invented cut. DIRECTIONAL v1, to be replaced by
// percentile bands.
const (
	erMicroStrongMin = 0.05  // Micro (10K–100K): Strong ~5%+
	erMicroSolidMin  = 0.025 // Micro: Solid ~2.5–5%; below is Needs-work
	erMidStrongMin   = 0.03  // Mid (100K–500K): Strong ~3%+
	erMidSolidMin    = 0.015 // Mid: Solid ~1.5–3%; below is Needs-work
)

// Tier keys, mirrored from the engine so this leaf stays dependency-free of the
// engine package. They are the stable industry buckets TierForFollowers emits.
const (
	tierMicro = "micro"
	tierMid   = "mid"
)

// labels are the human factor names rendered on each card's heading.
var labels = map[string]string{
	contract.ComponentEngagementAuthenticity: "Engagement Authenticity",
	contract.ComponentAudienceQuality:        "Audience Quality",
	contract.ComponentConsistencyReliability: "Consistency & Reliability",
	contract.ComponentBrandFitClarity:        "Brand-Fit Clarity",
}

// availabilities is the INTRINSIC availability of each factor — where its data
// comes from when it IS measured. The unmeasured overrides (not_measured /
// not_available_at_size) are the caller's job, applied when Support == 0.
//
// Engagement Authenticity is VERIFIED: its engagement rate, save/share rate and
// reach ratio are pulled from Meta. Its fraud layer is MODELED, surfaced as a
// sub-note (AuthenticityNote), not by downgrading the whole factor.
var availabilities = map[string]string{
	contract.ComponentEngagementAuthenticity: AvailabilityVerified,
	contract.ComponentAudienceQuality:        AvailabilityVerified,
	contract.ComponentConsistencyReliability: AvailabilityVerified,
	contract.ComponentBrandFitClarity:        AvailabilityModeled,
}

// AuthenticityNote is the MODELED authenticity sub-note carried alongside the
// otherwise-VERIFIED Engagement Authenticity factor (PRD): the engagement metrics
// are verified, but the fraud/bot authenticity read on top of them is a model
// output, and the card says so rather than passing it off as verified.
const AuthenticityNote = "Authenticity (fraud/bot) is a modeled signal computed on the verified engagement data."

// LabelFor returns the human factor name for a component key, or the raw key if
// the key is unknown (never a panic — a total function).
func LabelFor(componentKey string) string {
	if l, ok := labels[componentKey]; ok {
		return l
	}
	return componentKey
}

// AvailabilityFor returns a factor's INTRINSIC availability tier (verified or
// modeled) — where its data comes from when the factor was measured. It does NOT
// encode the unmeasured case: a caller holding a Subscore with Support == 0 must
// render AvailabilityNotMeasured (or, for Audience Quality,
// AvailabilityNotAvailableAtSize) instead of this. Unknown keys return an empty
// string.
func AvailabilityFor(componentKey string) string {
	return availabilities[componentKey]
}

// UnmeasuredAvailabilityFor returns the honest availability string for a factor
// that was NOT measured (Support == 0). Audience Quality below 100 followers is
// "not available at your size" (a size fact); every other factor is simply "not
// measured". This is the single place the unmeasured override is decided.
func UnmeasuredAvailabilityFor(componentKey string) string {
	if componentKey == contract.ComponentAudienceQuality {
		return AvailabilityNotAvailableAtSize
	}
	return AvailabilityNotMeasured
}

// BandFor places a MEASURED composite factor value (0..100) into a directional v1
// status band. The tier is accepted for signature stability and forward
// compatibility — once percentile bands land the cut will depend on tier — but in
// v1 the composite cuts are tier-INDEPENDENT (no external per-tier benchmark
// exists for the composite values yet), so tier is not read here. The observed
// engagement rate, which DOES have tier bands today, is handled by
// EngagementRateBand.
//
// Callers must not pass an unmeasured factor here: a Subscore with Support == 0
// has no value to band and must render BandNotAssessed directly.
func BandFor(componentKey string, value float64, tier string) string {
	_ = componentKey // same cuts for every factor in v1; kept for a per-factor future.
	_ = tier         // composite cuts are tier-independent in v1; see doc comment.
	switch {
	case value >= bandStrongMin:
		return BandStrong
	case value >= bandSolidMin:
		return BandSolid
	default:
		return BandNeedsWork
	}
}

// EngagementRateBand bands an OBSERVED engagement rate (a fraction, e.g. 0.05 for
// 5%) against the PRD §15 tier bands. Micro and Mid have published bands; every
// other tier returns BandNotAssessed rather than an invented cut. DIRECTIONAL v1.
func EngagementRateBand(rate float64, tier string) string {
	switch tier {
	case tierMicro:
		switch {
		case rate >= erMicroStrongMin:
			return BandStrong
		case rate >= erMicroSolidMin:
			return BandSolid
		default:
			return BandNeedsWork
		}
	case tierMid:
		switch {
		case rate >= erMidStrongMin:
			return BandStrong
		case rate >= erMidSolidMin:
			return BandSolid
		default:
			return BandNeedsWork
		}
	default:
		// No published band for this tier yet — honestly not assessed.
		return BandNotAssessed
	}
}

// improvementLines is keyed [componentKey][band]. The needs_work line is the PRD
// §7 copy VERBATIM (the most actionable state); solid and strong are reinforcing
// variants built from the same copy. Every line points at becoming more
// brand-ready (hireability) and NEVER at follower growth for its own sake. There
// is deliberately no BandNotAssessed entry: an unmeasured factor gets an empty
// line, never a guessed one.
var improvementLines = map[string]map[string]string{
	contract.ComponentEngagementAuthenticity: {
		BandNeedsWork: "Post save-worthy carousels and how-tos, reply in the first hour, and ask questions. Never buy followers or engagement — a brand's fraud check flags the same risk.",
		BandSolid:     "Solid engagement. Keep posting save-worthy carousels and how-tos and replying in the first hour — and never buy engagement, because a brand's fraud check flags the same risk.",
		BandStrong:    "Strong, authentic engagement — exactly what a brand's fraud check looks for. Keep replying fast and making save-worthy content to hold it.",
	},
	contract.ComponentAudienceQuality: {
		BandNeedsWork: "Know and state who follows you; a tight, defined, brand-relevant audience beats broad random reach.",
		BandSolid:     "Decent audience definition. Sharpen it — a tighter, clearly brand-relevant audience is worth more to a brand than broad random reach.",
		BandStrong:    "Well-defined, brand-relevant audience. Keep stating who follows you so a brand can match you to their customer.",
	},
	contract.ComponentConsistencyReliability: {
		BandNeedsWork: "Keep a reliable posting schedule and execute multiple formats — it tells a brand you deliver and lowers their lift.",
		BandSolid:     "Reasonably consistent. Tighten your posting schedule and vary formats — it tells a brand you deliver and lowers their lift.",
		BandStrong:    "Dependable, varied output — it signals to a brand that you deliver on time. Hold the schedule.",
	},
	contract.ComponentBrandFitClarity: {
		BandNeedsWork: "Log past brand work with real numbers and keep a coherent niche; a track record commands premium rates.",
		BandSolid:     "A track record is forming. Keep logging past brand work with real numbers and holding a coherent niche — it commands premium rates.",
		BandStrong:    "Clear niche and a visible brand-work track record — that commands premium rates. Keep logging results with real numbers.",
	},
}

// ImprovementLineFor returns the plain-language improvement line for a (factor,
// band) pair (PRD §7, a diagnosis not a course, at most two sentences). An
// unmeasured factor — band BandNotAssessed — or any unknown key/band returns the
// empty string: the layer would rather say nothing than guess a line for a factor
// it never measured.
func ImprovementLineFor(componentKey, band string) string {
	if byBand, ok := improvementLines[componentKey]; ok {
		return byBand[band]
	}
	return ""
}
