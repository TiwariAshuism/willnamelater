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

// TestAudienceSizeSubscore checks the log-scale FOLLOWER-COUNT mapping at its
// anchors. It was TestReachSubscore, asserting Confidence: 1 — the component was
// named for a thing it never measured (reach) and carried full certainty on a
// purchasable number, which made buying followers RAISE the audit score. The
// confidence expectation below is now audienceSizeConfidence (0.5), deliberately
// capped so the component cannot dominate the composite.
func TestAudienceSizeSubscore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		followers   int64
		wantVal     float64
		wantSupport float64
	}{
		{"none", 0, 0, 0},
		{"floor", 1_000, 0, audienceSizeConfidence},
		{"ceil", 10_000_000, 100, audienceSizeConfidence},
		{"above ceil clamps", 100_000_000, 100, audienceSizeConfidence},
		{"geometric midpoint", 100_000, 50, audienceSizeConfidence}, // sqrt(1e3*1e7)=1e5
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := audienceSizeSubscore(tt.followers)
			if !approx(got.Value, tt.wantVal) {
				t.Fatalf("value = %v, want %v", got.Value, tt.wantVal)
			}
			if !approx(got.Support, tt.wantSupport) {
				t.Fatalf("support = %v, want %v", got.Support, tt.wantSupport)
			}
		})
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
		"reach":        got.Reach,
		"engagement":   got.EngagementQuality,
		"authenticity": got.Authenticity,
		"consistency":  got.Consistency,
		"content":      got.ContentQuality,
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

	// The content subscore's support is postCount/10 — DATA COVERAGE. Three posts in
	// baseInput means 0.3 of the coverage the proxy can use, and it must say so
	// rather than call the number a confidence.
	if got.ContentQuality.SupportKind != contract.SupportCoverage {
		t.Fatalf("content support kind = %q, want coverage — postCount/10 is not a confidence",
			got.ContentQuality.SupportKind)
	}
	if !approx(got.ContentQuality.Support, 0.3) {
		t.Fatalf("content coverage = %v, want 0.3 (3 of 10 posts)", got.ContentQuality.Support)
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
		{"reach only", Weights{Reach: 1}, effective(base.Reach)},
		{"engagement only", Weights{EngagementQuality: 1}, effective(base.EngagementQuality)},
		{"authenticity only", Weights{Authenticity: 1}, effective(base.Authenticity)},
		{"consistency only", Weights{Consistency: 1}, effective(base.Consistency)},
		{"content only", Weights{ContentQuality: 1}, effective(base.ContentQuality)},
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
	in.Weights = Weights{Reach: 0.60, EngagementQuality: 0.60, Authenticity: 0.50, Consistency: 0.20, ContentQuality: 0.10}
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
// component we could not measure is DROPPED and its weight renormalized away. It
// used to contribute its full weight at an invented value of 50, so a third of a
// headline number could be fabricated neutrality while the customer read a
// confident-looking score.
//
// The account here is a 10M-follower snapshot with no posts and no fraud pass:
// only audience size is measured. Its raw 100 shrinks to 75 at confidence 0.5,
// and 75 is the whole composite.
func TestComputeDropsZeroConfidenceSubscore(t *testing.T) {
	t.Parallel()

	in := baseInput()
	in.Snapshots = []connector.Snapshot{{Platform: connector.PlatformYouTube, Followers: 10_000_000}}
	in.Fraud = contract.FraudInput{} // no fraud pass ran -> authenticity is {50, 0}
	in.Weights = Weights{Reach: 1, Authenticity: 0.5}

	got, err := Compute(in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if got.Authenticity.Support != 0 {
		t.Fatalf("authenticity support = %v, want 0 (unmeasured)", got.Authenticity.Support)
	}

	want := effective(got.Reach) // 50 + 0.5*(100-50) = 75
	if !approx(got.Overall, want) {
		t.Fatalf("overall = %v, want %v (audience size alone, weight renormalized)", got.Overall, want)
	}
	// The old behaviour: authenticity's 0.5 weight carrying an invented 50.
	fabricated := (1*want + 0.5*neutralScore) / 1.5
	if approx(got.Overall, fabricated) {
		t.Fatalf("overall = %v: unmeasured authenticity still contributed an invented 50", got.Overall)
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

	snaps := []connector.Snapshot{
		{Platform: connector.PlatformYouTube, Followers: 40_000, Posts: []connector.Post{{Likes: 100, Comments: 5}}},
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
		{PublishedAt: base, Likes: 2_000, Comments: 120, Shares: 40},
		{PublishedAt: base.AddDate(0, 0, 3), Likes: 2_400, Comments: 150, Shares: 55},
		{PublishedAt: base.AddDate(0, 0, 6), Likes: 1_900, Comments: 100, Shares: 30},
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
