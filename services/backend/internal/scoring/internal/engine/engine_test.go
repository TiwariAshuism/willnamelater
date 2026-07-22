package engine

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

const eps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func ptr[T any](v T) *T { return &v }

// benchFor returns the bootstrap benchmark for a tier, so the ladder tests run
// against the real seeded cells rather than invented numbers.
func benchFor(t *testing.T, tier string) Benchmark {
	t.Helper()
	for _, bb := range BootstrapBenchmarks() {
		if bb.Tier == tier {
			return bb.Benchmark
		}
	}
	t.Fatalf("no bootstrap benchmark for tier %q", tier)
	return Benchmark{}
}

// TestPercentileScoreLadderEveryCell asserts that for every seeded benchmark
// cell, an observed rate sitting exactly on a percentile lands on that
// percentile's ladder anchor. This is the per-benchmark-cell coverage the score
// engine's correctness rests on.
func TestPercentileScoreLadderEveryCell(t *testing.T) {
	t.Parallel()

	for _, bb := range BootstrapBenchmarks() {
		bb := bb
		t.Run(bb.Tier, func(t *testing.T) {
			t.Parallel()
			b := bb.Benchmark
			cases := []struct {
				at   float64
				want float64
			}{
				{b.P10, 0.10},
				{b.P25, 0.30},
				{b.P50, 0.50},
				{b.P75, 0.75},
				{b.P90, 0.95},
			}
			for _, c := range cases {
				if got := percentileScore(c.at, b); !approx(got, c.want) {
					t.Fatalf("percentileScore(%v) = %v, want %v", c.at, got, c.want)
				}
			}
		})
	}
}

// TestPercentileScoreMonotonic is the label-free property that a higher observed
// engagement rate never produces a lower engagement score, across every cell.
func TestPercentileScoreMonotonic(t *testing.T) {
	t.Parallel()

	for _, bb := range BootstrapBenchmarks() {
		bb := bb
		t.Run(bb.Tier, func(t *testing.T) {
			t.Parallel()
			b := bb.Benchmark
			prev := -1.0
			for v := 0.0; v <= b.P90*3; v += b.P90 / 50 {
				got := percentileScore(v, b)
				if got < prev-eps {
					t.Fatalf("non-monotonic at v=%v: %v < %v", v, got, prev)
				}
				if got < 0 || got > 1 {
					t.Fatalf("percentileScore(%v)=%v out of [0,1]", v, got)
				}
				prev = got
			}
		})
	}
}

// TestPercentileScoreBounds checks the below-p10 and above-p90 tails stay in
// range and behave (zero rate floors at 0, very high rate saturates at 1).
func TestPercentileScoreBounds(t *testing.T) {
	t.Parallel()

	b := benchFor(t, tierMicro)
	if got := percentileScore(0, b); !approx(got, 0) {
		t.Fatalf("zero rate = %v, want 0", got)
	}
	if got := percentileScore(b.P90*100, b); !approx(got, 1) {
		t.Fatalf("huge rate = %v, want 1", got)
	}
}

// TestAudienceQualitySubscore covers the honest-degradation contract of the
// Audience Quality factor: it is DROPPED (Support 0) when no demographics are
// present or the account is below the 100-follower Meta threshold, and it scores
// legibility + concentration when they are.
func TestAudienceQualitySubscore(t *testing.T) {
	t.Parallel()

	aud := &connector.AudienceBreakdown{
		Countries: map[string]float64{"US": 0.6, "GB": 0.3},
		AgeGroups: map[string]float64{"18-24": 0.5, "25-34": 0.4},
		Gender:    map[string]float64{"female": 0.8, "male": 0.2},
	}
	withAud := connector.Snapshot{Followers: 50_000, Audience: aud}
	noAud := connector.Snapshot{Followers: 50_000}
	tooSmall := connector.Snapshot{Followers: 80, Audience: aud}

	if s := audienceQualitySubscore([]connector.Snapshot{noAud}, 50_000); s.Support != 0 {
		t.Fatalf("absent audience should drop the factor, got support %v", s.Support)
	}
	if s := audienceQualitySubscore([]connector.Snapshot{tooSmall}, 80); s.Support != 0 {
		t.Fatalf("under 100 followers should drop the factor, got support %v", s.Support)
	}
	got := audienceQualitySubscore([]connector.Snapshot{withAud}, 50_000)
	if got.Support <= 0 || got.SupportKind != contract.SupportCoverage || got.Basis != contract.BasisClosedForm {
		t.Fatalf("present audience = %+v, want measured coverage/closed_form", got)
	}
	// All three dimensions reported -> full coverage; a clean, concentrated audience
	// scores well above neutral.
	if !approx(got.Support, 1) {
		t.Fatalf("three dimensions should be full coverage, got %v", got.Support)
	}
	if got.Value <= neutralScore {
		t.Fatalf("a legible, concentrated audience should score above neutral, got %v", got.Value)
	}
}

// TestBrandFitSubscore covers the caption-driven Brand-Fit factor: dropped when no
// captions exist, and a track record of disclosed brand work scores above neutral.
func TestBrandFitSubscore(t *testing.T) {
	t.Parallel()

	if s := brandFitSubscore([]connector.Snapshot{{Posts: []connector.Post{{Likes: 10}}}}); s.Support != 0 {
		t.Fatalf("no captions should drop the factor, got support %v", s.Support)
	}
	sponsored := []connector.Snapshot{{Posts: []connector.Post{
		{Caption: "brand collab #ad"},
		{Caption: "another #sponsored post"},
		{Caption: "just a normal caption"},
	}}}
	got := brandFitSubscore(sponsored)
	if got.Support <= 0 || got.SupportKind != contract.SupportCoverage {
		t.Fatalf("captioned posts = %+v, want measured coverage", got)
	}
	if got.Value <= neutralScore {
		t.Fatalf("a disclosed brand-work track record should score above neutral, got %v", got.Value)
	}
}

// TestAuthenticitySubscore covers the fraud blend over the OBSERVED signals, the
// absent-fraud neutral, and the champion override.
//
// The old table asserted a fabricated shape: a bare {Present: true} with no
// measurements scored a fully-confident 100 ("clean"), and absent signals were
// read as hard zeros at full weight. Both expectations are inverted below —
// nothing observed is now {50, 0} (an unexamined account, not a clean one), and a
// missing signal has its weight renormalized away rather than voting "clean".
func TestAuthenticitySubscore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		in          contract.FraudInput
		wantVal     float64
		wantSupport float64
	}{
		{"absent is neutral", contract.FraudInput{Present: false}, 50, 0},
		{
			// CHANGED EXPECTATION: this used to be the "clean" case scoring {100, 0.8}.
			// A fraud pass that ran but observed nothing certified the account. It is
			// now indistinguishable from no pass at all: neutral, zero confidence.
			"present but nothing observed is neutral, not clean",
			contract.FraudInput{Present: true, Confidence: 0.8},
			50, 0,
		},
		{
			// Both signals observed and maximal: full presentWeight, so confidence is
			// the input confidence unscaled.
			"all fraud",
			contract.FraudInput{Present: true, RiskScore: ptr(100.0), CliqueMembershipFraction: ptr(1.0), Confidence: 0.5},
			0, 0.5,
		},
		{
			// Both signals observed and clean. This is what a genuine 100 now requires:
			// two measurements that came back clean, not two measurements we never took.
			"both signals observed clean",
			contract.FraudInput{Present: true, RiskScore: ptr(0.0), CliqueMembershipFraction: ptr(0.0), Confidence: 0.9},
			100, 0.9,
		},
		{
			// The renormalization guarantee: with no comments to analyze there is no
			// clique signal, so the risk score carries the blend at FULL weight.
			// 0.65*0.8 renormalized over 0.65 = 0.8 fraud -> 20. Treating the absent
			// clique as a clean 0 would have given 1 - 0.65*0.8 = 0.48 -> 48, dragging
			// a badly fraudulent account halfway to respectable.
			"risk only renormalizes rather than drifting clean",
			contract.FraudInput{Present: true, RiskScore: ptr(80.0), Confidence: 1},
			20, fraudWeightRisk, // conf scaled by the share of the vector we saw
		},
		{
			// Symmetric: coordination alone carries the blend, at its own weight.
			"coordination only renormalizes",
			contract.FraudInput{Present: true, CliqueMembershipFraction: ptr(0.5), Confidence: 1},
			50, fraudWeightCoordination,
		},
		{
			// RiskScore is 0-100, the clique fraction 0-1; both clamp, and confidence
			// clamps to 1 before being scaled by the observed weight.
			"signals clamp above range",
			contract.FraudInput{Present: true, RiskScore: ptr(500.0), Confidence: 2},
			0, fraudWeightRisk, // risk clamps to 1 -> 1-1 = 0 ; conf clamps to 1, then *0.65
		},
		{
			// A promoted champion refines the whole vector: its 0-100 score is the
			// fraud aggregate, superseding the heuristic blend, at unscaled confidence
			// (it saw the full feature vector).
			"refined score supersedes heuristic",
			contract.FraudInput{Present: true, Confidence: 0.9, RefinedScore: ptr(75.0)},
			25, 0.9, // 1 - 0.75 = 0.25 -> 25
		},
		{
			// The refined score clamps to [0,100] just like the heuristic path.
			"refined score clamps above 100",
			contract.FraudInput{Present: true, RiskScore: ptr(100.0), Confidence: 1, RefinedScore: ptr(150.0)},
			0, 1, // clamps to 100 -> 1-1 = 0
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := authenticitySubscore(tt.in)
			if !approx(got.Value, tt.wantVal) {
				t.Fatalf("value = %v, want %v", got.Value, tt.wantVal)
			}
			if !approx(got.Support, tt.wantSupport) {
				t.Fatalf("support = %v, want %v", got.Support, tt.wantSupport)
			}
		})
	}
}

// engagedSnapshot is a snapshot with posts and a follower base, so the engagement
// subscore has something to place against a benchmark.
func engagedSnapshot() connector.Snapshot {
	return connector.Snapshot{
		Platform:  connector.PlatformYouTube,
		Followers: 50_000,
		Posts:     []connector.Post{{Likes: 1_000, Comments: 50}},
	}
}

// TestConfidenceRisesWithSamples is the low-confidence-at-low-n property, and it
// now applies ONLY to a corpus benchmark: confidence rises with the number of
// DISTINCT INFLUENCERS actually observed in the cell. A sample count is a
// confidence's only legitimate source.
func TestConfidenceRisesWithSamples(t *testing.T) {
	t.Parallel()

	snap := engagedSnapshot()
	b := benchFor(t, tierMicro)
	b.Source = SourceCorpus

	prev := -1.0
	for _, n := range []int{1, 10, 30, 100, 1_000, 10_000} {
		b.SampleSize = n
		got := engagementSubscore([]connector.Snapshot{snap}, snap.Followers, b)
		if got.Support <= prev {
			t.Fatalf("confidence did not rise at n=%d: %v <= %v", n, got.Support, prev)
		}
		if got.SupportKind != contract.SupportConfidence || got.Basis != contract.BasisCorpus {
			t.Fatalf("corpus subscore = (%q,%q), want (corpus, confidence)", got.Basis, got.SupportKind)
		}
		prev = got.Support
	}
}

// TestBootstrapBandCountsNoSamples is the FABRICATED-n guarantee. A bootstrap band
// rests on ZERO observations, so:
//
//   - its SampleSize is 0 (persisted NULL), not a nominal 10; and
//   - the subscore built on it takes the named BootstrapPriorSupport constant,
//     stamped SupportPrior — it is NOT run through confidenceForSamples, which would
//     turn an invented sample count into a customer-facing "confidence".
//
// The old code set bootstrapSampleSize = 10 and shipped confidenceForSamples(10) ≈
// 0.25 as a measured confidence. The 0.25 survives; the fake n does not.
func TestBootstrapBandCountsNoSamples(t *testing.T) {
	t.Parallel()

	snap := engagedSnapshot()
	for _, bb := range BootstrapBenchmarks() {
		bb := bb
		t.Run(bb.Tier, func(t *testing.T) {
			t.Parallel()
			if bb.Benchmark.SampleSize != 0 {
				t.Fatalf("bootstrap sample size = %d, want 0: no observations exist behind a reference band",
					bb.Benchmark.SampleSize)
			}
			if bb.Benchmark.Source != SourceBootstrap {
				t.Fatalf("source = %q, want bootstrap", bb.Benchmark.Source)
			}

			got := engagementSubscore([]connector.Snapshot{snap}, snap.Followers, bb.Benchmark)
			if !approx(got.Support, BootstrapPriorSupport) {
				t.Fatalf("support = %v, want the named prior %v", got.Support, BootstrapPriorSupport)
			}
			if got.SupportKind != contract.SupportPrior {
				t.Fatalf("support kind = %q, want %q — a prior must never be dressed as a measurement",
					got.SupportKind, contract.SupportPrior)
			}
			if got.Basis != contract.BasisClosedForm {
				t.Fatalf("basis = %q, want closed_form: a reference ladder is arithmetic, not a corpus percentile", got.Basis)
			}
			// And the prior is genuinely low: the band must never look like evidence.
			if got.Support >= 0.3 {
				t.Fatalf("bootstrap prior = %v, want < 0.3 (low)", got.Support)
			}
		})
	}
}

// TestSubscoreBasisIsAlwaysStamped pins defect C: every subscore must declare what
// produced it and what its support number means, so a customer can tell a
// closed-form proxy from a corpus percentile from a trained model.
func TestSubscoreBasisIsAlwaysStamped(t *testing.T) {
	t.Parallel()

	in := baseInput()
	got, err := Compute(in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	subs := map[string]contract.Subscore{
		"engagement_authenticity": got.EngagementAuthenticity,
		"audience_quality":        got.AudienceQuality,
		"consistency_reliability": got.ConsistencyReliability,
		"brand_fit_clarity":       got.BrandFitClarity,
		"authenticity_signal":     got.FraudAuthenticity,
	}
	validBasis := map[string]bool{contract.BasisClosedForm: true, contract.BasisCorpus: true}
	validKind := map[string]bool{
		contract.SupportCoverage:   true,
		contract.SupportPrior:      true,
		contract.SupportConfidence: true,
		contract.SupportNone:       true,
	}
	for name, s := range subs {
		if !validBasis[s.Basis] && !strings.HasPrefix(s.Basis, contract.BasisModelPrefix) {
			t.Fatalf("%s basis = %q, want closed_form | corpus | model:<version>", name, s.Basis)
		}
		if !validKind[s.SupportKind] {
			t.Fatalf("%s support kind = %q, want coverage | prior | confidence | none", name, s.SupportKind)
		}
	}

	// The content-depth sub-signal's support is postCount/10 — DATA COVERAGE. Three
	// posts in baseInput means 0.3 of the coverage the proxy can use, and it must say
	// so rather than call the number a confidence. (Depth now feeds Engagement
	// Authenticity; it is asserted here on the sub-signal function directly.)
	depth := contentSubscore(in.Snapshots)
	if depth.SupportKind != contract.SupportCoverage {
		t.Fatalf("content support kind = %q, want coverage — postCount/10 is not a confidence",
			depth.SupportKind)
	}
	if !approx(depth.Support, 0.3) {
		t.Fatalf("content coverage = %v, want 0.3 (3 of 10 posts)", depth.Support)
	}
	// Ten posts saturate coverage at 1.0 — and the proxy is exactly as unvalidated as
	// it was at one post. Full coverage is not a claim of correctness.
	many := make([]connector.Post, 10)
	for i := range many {
		many[i] = connector.Post{Likes: 100, Comments: 5}
	}
	saturated := contentSubscore([]connector.Snapshot{{Posts: many}})
	if !approx(saturated.Support, 1) || saturated.SupportKind != contract.SupportCoverage {
		t.Fatalf("saturated content = %+v, want coverage 1.0", saturated)
	}
}

// TestAuthenticityBasisNamesTheModel pins that a promoted champion's output is
// labelled with its model version, so it is never mistaken for the arithmetic blend
// it supersedes (or the other way round).
func TestAuthenticityBasisNamesTheModel(t *testing.T) {
	t.Parallel()

	refined := authenticitySubscore(contract.FraudInput{
		Present: true, Confidence: 0.9, RefinedScore: ptr(75.0), ModelVersion: "fraud-2026-06-01",
	})
	if refined.Basis != "model:fraud-2026-06-01" {
		t.Fatalf("basis = %q, want model:fraud-2026-06-01", refined.Basis)
	}
	if refined.SupportKind != contract.SupportConfidence {
		t.Fatalf("support kind = %q, want confidence", refined.SupportKind)
	}

	heuristic := authenticitySubscore(contract.FraudInput{Present: true, RiskScore: ptr(10.0), Confidence: 0.8})
	if heuristic.Basis != contract.BasisClosedForm {
		t.Fatalf("heuristic basis = %q, want closed_form — an arithmetic blend is not a model", heuristic.Basis)
	}
}

// effective mirrors the engine's confidence shrink: a subscore only moves the
// composite away from neutral in proportion to how much it is trusted. The tests
// below expect EFFECTIVE values, not raw ones — the composite used to be a mean
// of raw Values with confidence tracked in a parallel number that never touched
// the score.
func effective(s contract.Subscore) float64 {
	return neutralScore + s.Support*(s.Value-neutralScore)
}

// TestComputeIsolatesEachWeight sets one weight to 1 and the rest to 0, so the
// composite must equal that single subscore's EFFECTIVE (confidence-shrunk)
// value. This exercises every weight cell.
func TestComputeIsolatesEachWeight(t *testing.T) {
	t.Parallel()

	in := baseInput()
	base, err := Compute(in)
	if err != nil {
		t.Fatalf("compute base: %v", err)
	}

	tests := []struct {
		name string
		w    Weights
		want float64
	}{
		{"engagement authenticity only", Weights{EngagementAuthenticity: 1}, effective(base.EngagementAuthenticity)},
		{"audience quality only", Weights{AudienceQuality: 1}, effective(base.AudienceQuality)},
		{"consistency only", Weights{ConsistencyReliability: 1}, effective(base.ConsistencyReliability)},
		{"brand fit only", Weights{BrandFitClarity: 1}, effective(base.BrandFitClarity)},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			iso := in
			iso.Weights = tt.w
			got, err := Compute(iso)
			if err != nil {
				t.Fatalf("compute: %v", err)
			}
			if !approx(got.Overall, tt.want) {
				t.Fatalf("overall = %v, want %v", got.Overall, tt.want)
			}
		})
	}
}

// TestComputeWeightsNormalize asserts unnormalized weights are scaled: doubling
// every weight leaves the composite unchanged.
func TestComputeWeightsNormalize(t *testing.T) {
	t.Parallel()

	in := baseInput()
	a, err := Compute(in)
	if err != nil {
		t.Fatalf("compute a: %v", err)
	}
	in.Weights = Weights{EngagementAuthenticity: 0.60, AudienceQuality: 0.60, ConsistencyReliability: 0.40, BrandFitClarity: 0.40}
	b, err := Compute(in)
	if err != nil {
		t.Fatalf("compute b: %v", err)
	}
	if !approx(a.Overall, b.Overall) {
		t.Fatalf("scaled weights changed overall: %v vs %v", a.Overall, b.Overall)
	}
}

func TestComputeRejectsZeroWeights(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.Weights = Weights{}
	if _, err := Compute(in); !errors.Is(err, ErrNoWeights) {
		t.Fatalf("err = %v, want ErrNoWeights", err)
	}
}

// TestComputeDropsZeroConfidenceSubscore is the no-invented-evidence guarantee: a
// factor we could not measure is DROPPED and its weight renormalized away. It used
// to contribute its full weight at an invented value of 50.
//
// The account here has posts and a fraud pass (so Engagement Authenticity is
// measured) but no captions (so Brand-Fit is unmeasured). Brand-Fit's weight is
// renormalized away and the composite is Engagement Authenticity's effective value
// alone — never dragged by an invented Brand-Fit 50.
func TestComputeDropsZeroConfidenceSubscore(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.Snapshots = []connector.Snapshot{{
		Platform:  connector.PlatformYouTube,
		Followers: 50_000,
		Posts:     []connector.Post{{Likes: 1_000, Comments: 50}}, // no caption -> brand-fit absent
	}}
	in.Fraud = contract.FraudInput{Present: true, RiskScore: ptr(10.0), Confidence: 0.8}
	in.Weights = Weights{EngagementAuthenticity: 1, BrandFitClarity: 0.5}

	got, err := Compute(in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if got.BrandFitClarity.Support != 0 {
		t.Fatalf("brand-fit support = %v, want 0 (no captions)", got.BrandFitClarity.Support)
	}
	if got.AudienceQuality.Support != 0 {
		t.Fatalf("audience-quality support = %v, want 0 (no demographics)", got.AudienceQuality.Support)
	}

	want := effective(got.EngagementAuthenticity)
	if !approx(got.Overall, want) {
		t.Fatalf("overall = %v, want %v (engagement authenticity alone, weight renormalized)", got.Overall, want)
	}
	// The old behaviour would have been brand-fit's 0.5 weight carrying an invented 50.
	fabricated := (1*want + 0.5*neutralScore) / 1.5
	if approx(got.Overall, fabricated) {
		t.Fatalf("overall = %v: unmeasured brand-fit still contributed an invented 50", got.Overall)
	}
}

// TestComputeMinEvidenceBoundary pins the exact 50%-weight threshold. A
// sub-100-follower, caption-less audit with posts, a fraud pass and a cadence
// series measures Engagement Authenticity (.30) + Consistency (.20) = exactly 0.50
// of the weight — it PASSES by a hair. Strip the cadence series and only .30
// remains, which correctly yields ErrInsufficientEvidence. This boundary is
// load-bearing: a future weight tweak must not silently cross it.
func TestComputeMinEvidenceBoundary(t *testing.T) {
	t.Parallel()

	tbase := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	timedPosts := []connector.Post{
		{PublishedAt: tbase, Likes: 500, Comments: 30},
		{PublishedAt: tbase.AddDate(0, 0, 3), Likes: 520, Comments: 28},
		{PublishedAt: tbase.AddDate(0, 0, 6), Likes: 480, Comments: 26},
	}
	untimedPosts := []connector.Post{
		{Likes: 500, Comments: 30}, {Likes: 520, Comments: 28}, {Likes: 480, Comments: 26},
	}

	// Exactly 0.50 evidenced (EA + Consistency): passes.
	in := baseInput()
	in.Snapshots = []connector.Snapshot{{Platform: connector.PlatformInstagram, Followers: 90, Posts: timedPosts}}
	in.Fraud = contract.FraudInput{Present: true, RiskScore: ptr(20.0), Confidence: 0.7}
	in.Weights = BootstrapWeights()
	got, err := Compute(in)
	if err != nil {
		t.Fatalf("at exactly 0.50 evidenced weight the score must publish, got %v", err)
	}
	if got.AudienceQuality.Support != 0 || got.BrandFitClarity.Support != 0 {
		t.Fatalf("audience/brand-fit must be dropped under 100 followers / no captions, got %+v", got)
	}

	// Only 0.30 evidenced (EA alone, no cadence series): withheld.
	thin := in
	thin.Snapshots = []connector.Snapshot{{Platform: connector.PlatformInstagram, Followers: 90, Posts: untimedPosts}}
	if _, err := Compute(thin); !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("err = %v, want ErrInsufficientEvidence below the 0.50 floor", err)
	}
}

// TestComputeInsufficientEvidence asserts the engine REFUSES to publish a number
// when too little of the composite's weight rests on things it actually measured.
// A followers-only snapshot with no posts, no metrics and no fraud pass evidences
// only reach (0.30 of 1.0), below minEvidencedWeight — previously this produced a
// confident-looking mid-50s composite out of four invented neutrals.
func TestComputeInsufficientEvidence(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.Snapshots = []connector.Snapshot{{Platform: connector.PlatformYouTube, Followers: 50_000}}
	in.Fraud = contract.FraudInput{}

	if _, err := Compute(in); !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("err = %v, want ErrInsufficientEvidence", err)
	}
}

// TestComputeStampsVersionsAndLabel checks the reproducibility stamps and
// provenance label are copied from the inputs onto the score.
func TestComputeStampsVersionsAndLabel(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.Weights.Version = 7
	in.EngagementBenchmark.Version = 4
	in.EngagementBenchmark.Label = "corpus v4"

	got, err := Compute(in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if got.WeightsVersion != 7 || got.BenchmarkVersion != 4 {
		t.Fatalf("versions = (%d,%d), want (7,4)", got.WeightsVersion, got.BenchmarkVersion)
	}
	if got.BenchmarkLabel != "corpus v4" {
		t.Fatalf("label = %q, want corpus v4", got.BenchmarkLabel)
	}
}

// TestContributingPlatformsPartial covers the partial-audit case: one platform
// with data and one empty snapshot means only the productive platform is named,
// so a partial audit is never silently understated.
func TestContributingPlatformsPartial(t *testing.T) {
	t.Parallel()

	tbase := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snaps := []connector.Snapshot{
		{
			Platform:  connector.PlatformYouTube,
			Followers: 40_000,
			Posts: []connector.Post{
				{PublishedAt: tbase, Caption: "look #ad", Likes: 100, Comments: 5},
				{PublishedAt: tbase.AddDate(0, 0, 3), Caption: "hi", Likes: 110, Comments: 6},
				{PublishedAt: tbase.AddDate(0, 0, 6), Caption: "tip", Likes: 90, Comments: 4},
			},
			Audience: &connector.AudienceBreakdown{Countries: map[string]float64{"US": 0.7, "GB": 0.2}},
		},
		{Platform: connector.PlatformInstagram}, // fully empty: rate-limited before any data
	}
	in := baseInput()
	in.Snapshots = snaps

	got, err := Compute(in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(got.ContributingPlatforms) != 1 || got.ContributingPlatforms[0] != connector.PlatformYouTube {
		t.Fatalf("contributing = %v, want [youtube]", got.ContributingPlatforms)
	}
}

// TestContributingPlatformsMerged checks two productive platforms both appear
// and the follower base is the larger of the two.
func TestContributingPlatformsMerged(t *testing.T) {
	t.Parallel()

	snaps := []connector.Snapshot{
		{Platform: connector.PlatformYouTube, Followers: 40_000, Posts: []connector.Post{{Likes: 100, Comments: 5}}},
		{Platform: connector.PlatformInstagram, Followers: 120_000, Posts: []connector.Post{{Likes: 300, Comments: 9}}},
	}
	if f := representativeFollowers(snaps); f != 120_000 {
		t.Fatalf("representative followers = %d, want 120000", f)
	}
	if got := contributingPlatforms(snaps); len(got) != 2 {
		t.Fatalf("contributing = %v, want 2 platforms", got)
	}
}

// TestConsistencyNeedsData asserts consistency is neutral and zero-confidence
// when there are too few metric points and posts to judge, then becomes
// data-backed once a regular series is supplied.
func TestConsistencyNeedsData(t *testing.T) {
	t.Parallel()

	bare := []connector.Snapshot{{Platform: connector.PlatformYouTube, Followers: 10_000}}
	if s := consistencySubscore(bare); s.Support != 0 || !approx(s.Value, 50) {
		t.Fatalf("bare consistency = %+v, want neutral zero-confidence", s)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	steady := []connector.Snapshot{{
		Platform:  connector.PlatformYouTube,
		Followers: 10_400,
		Metrics: []connector.MetricPoint{
			{At: base, Name: "followers", Value: 10_000},
			{At: base.AddDate(0, 0, 1), Name: "followers", Value: 10_100},
			{At: base.AddDate(0, 0, 2), Name: "followers", Value: 10_200},
			{At: base.AddDate(0, 0, 3), Name: "followers", Value: 10_300},
		},
	}}
	s := consistencySubscore(steady)
	if s.Support <= 0 {
		t.Fatalf("steady consistency coverage = %v, want > 0", s.Support)
	}
	if s.Value < 90 {
		t.Fatalf("steady growth should score high, got %v", s.Value)
	}
}

// TestConsistencySpikyLowerThanSteady is a monotonicity-style property: an
// erratic follower trajectory scores no higher than a steady one.
func TestConsistencySpikyLowerThanSteady(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := func(vals ...float64) []connector.Snapshot {
		pts := make([]connector.MetricPoint, len(vals))
		for i, v := range vals {
			pts[i] = connector.MetricPoint{At: base.AddDate(0, 0, i), Name: "followers", Value: v}
		}
		return []connector.Snapshot{{Platform: connector.PlatformYouTube, Followers: int64(vals[len(vals)-1]), Metrics: pts}}
	}
	steady := consistencySubscore(series(10_000, 10_100, 10_200, 10_300))
	spiky := consistencySubscore(series(10_000, 25_000, 11_000, 40_000))
	if spiky.Value > steady.Value {
		t.Fatalf("spiky %v should not exceed steady %v", spiky.Value, steady.Value)
	}
}

// TestContentDepthRewardsInteraction checks deeper interaction (comments/shares
// vs likes) scores higher.
func TestContentDepthRewardsInteraction(t *testing.T) {
	t.Parallel()

	shallow := []connector.Snapshot{{Posts: []connector.Post{{Likes: 1_000, Comments: 1}}}}
	deep := []connector.Snapshot{{Posts: []connector.Post{{Likes: 1_000, Comments: 150, Shares: 80}}}}
	if contentSubscore(deep).Value <= contentSubscore(shallow).Value {
		t.Fatalf("deep interaction should outscore shallow")
	}
	if s := contentSubscore(nil); s.Support != 0 {
		t.Fatalf("no posts should be zero-confidence, got %+v", s)
	}
}

// baseInput is a representative, fully-populated audit for the composite tests.
// The snapshot values are constructed in-test to exercise the pure function;
// they are not seeded business data.
func baseInput() Input {
	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	posts := []connector.Post{
		{PublishedAt: base, Caption: "new drop #ad", Likes: 2_000, Comments: 120, Shares: 40},
		{PublishedAt: base.AddDate(0, 0, 3), Caption: "loving this", Likes: 2_400, Comments: 150, Shares: 55},
		{PublishedAt: base.AddDate(0, 0, 6), Caption: "tutorial time", Likes: 1_900, Comments: 100, Shares: 30},
	}
	metrics := []connector.MetricPoint{
		{At: base, Name: "subscribers", Value: 49_000},
		{At: base.AddDate(0, 0, 3), Name: "subscribers", Value: 49_600},
		{At: base.AddDate(0, 0, 6), Name: "subscribers", Value: 50_000},
	}
	snap := connector.Snapshot{
		Platform:  connector.PlatformYouTube,
		Followers: 50_000,
		Posts:     posts,
		Metrics:   metrics,
		// Audience present so the Audience Quality factor is measured in the composite
		// tests. A well-defined, US-concentrated audience.
		Audience: &connector.AudienceBreakdown{
			Countries: map[string]float64{"US": 0.6, "GB": 0.2, "CA": 0.1},
			AgeGroups: map[string]float64{"18-24": 0.5, "25-34": 0.4},
			Gender:    map[string]float64{"female": 0.7, "male": 0.3},
		},
	}
	return Input{
		Niche:     "beauty",
		Tier:      tierMicro,
		Snapshots: []connector.Snapshot{snap},
		// Both fraud signals observed: a low composite risk score (0-100) and a small
		// coordinated-commenter fraction (0-1). The old fixture set a FakeFollowerRate
		// (the risk score under a name nothing ever measured), a BotCommentRate (a
		// bit-for-bit duplicate of the clique fraction) and an EngagementAnomaly (a
		// structural constant already inside the risk score).
		Fraud:               contract.FraudInput{Present: true, RiskScore: ptr(5.0), CliqueMembershipFraction: ptr(0.1), Confidence: 0.7},
		Weights:             BootstrapWeights(),
		EngagementBenchmark: benchForTier(tierMicro),
	}
}

func benchForTier(tier string) Benchmark {
	for _, bb := range BootstrapBenchmarks() {
		if bb.Tier == tier {
			return bb.Benchmark
		}
	}
	return Benchmark{}
}
