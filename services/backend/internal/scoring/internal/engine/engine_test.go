package engine

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

const eps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

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

// TestReachSubscore checks the log-scale reach mapping at its anchors.
func TestReachSubscore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		followers int64
		wantVal   float64
		wantConf  float64
	}{
		{"none", 0, 0, 0},
		{"floor", 1_000, 0, 1},
		{"ceil", 10_000_000, 100, 1},
		{"above ceil clamps", 100_000_000, 100, 1},
		{"geometric midpoint", 100_000, 50, 1}, // sqrt(1e3*1e7)=1e5
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := reachSubscore(tt.followers)
			if !approx(got.Value, tt.wantVal) {
				t.Fatalf("value = %v, want %v", got.Value, tt.wantVal)
			}
			if !approx(got.Confidence, tt.wantConf) {
				t.Fatalf("confidence = %v, want %v", got.Confidence, tt.wantConf)
			}
		})
	}
}

// TestAuthenticitySubscore covers the fraud blend and the absent-fraud neutral.
func TestAuthenticitySubscore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       contract.FraudInput
		wantVal  float64
		wantConf float64
	}{
		{"absent is neutral", contract.FraudInput{Present: false}, 50, 0},
		{"clean", contract.FraudInput{Present: true, Confidence: 0.8}, 100, 0.8},
		{
			"all fraud",
			contract.FraudInput{Present: true, FakeFollowerRate: 1, BotCommentRate: 1, EngagementAnomaly: 1, Confidence: 0.5},
			0, 0.5,
		},
		{
			"half fake followers only",
			contract.FraudInput{Present: true, FakeFollowerRate: 0.5, Confidence: 1},
			80, 1, // 1 - 0.4*0.5 = 0.8
		},
		{
			"rates clamp above one",
			contract.FraudInput{Present: true, FakeFollowerRate: 5, Confidence: 2},
			60, 1, // fake clamps to 1 -> 1-0.4=0.6 ; conf clamps to 1
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
			if !approx(got.Confidence, tt.wantConf) {
				t.Fatalf("confidence = %v, want %v", got.Confidence, tt.wantConf)
			}
		})
	}
}

// TestConfidenceRisesWithSamples is the low-confidence-at-low-n property: the
// engagement subscore's confidence increases monotonically with benchmark
// sample size and is low at the bootstrap sample size.
func TestConfidenceRisesWithSamples(t *testing.T) {
	t.Parallel()

	snap := connector.Snapshot{
		Platform:  connector.PlatformYouTube,
		Followers: 50_000,
		Posts:     []connector.Post{{Likes: 1_000, Comments: 50}},
	}
	b := benchFor(t, tierMicro)

	prev := -1.0
	for _, n := range []int{1, 10, 30, 100, 1_000, 10_000} {
		b.SampleSize = n
		got := engagementSubscore([]connector.Snapshot{snap}, snap.Followers, b).Confidence
		if got <= prev {
			t.Fatalf("confidence did not rise at n=%d: %v <= %v", n, got, prev)
		}
		prev = got
	}

	b.SampleSize = bootstrapSampleSize
	if c := engagementSubscore([]connector.Snapshot{snap}, snap.Followers, b).Confidence; c >= 0.3 {
		t.Fatalf("bootstrap confidence = %v, want < 0.3 (low)", c)
	}
}

// TestComputeIsolatesEachWeight sets one weight to 1 and the rest to 0, so the
// composite must equal that single subscore. This exercises every weight cell.
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
		{"reach only", Weights{Reach: 1}, base.Reach.Value},
		{"engagement only", Weights{EngagementQuality: 1}, base.EngagementQuality.Value},
		{"authenticity only", Weights{Authenticity: 1}, base.Authenticity.Value},
		{"consistency only", Weights{Consistency: 1}, base.Consistency.Value},
		{"content only", Weights{ContentQuality: 1}, base.ContentQuality.Value},
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
	if s := consistencySubscore(bare); s.Confidence != 0 || !approx(s.Value, 50) {
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
	if s.Confidence <= 0 {
		t.Fatalf("steady consistency confidence = %v, want > 0", s.Confidence)
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
	if s := contentSubscore(nil); s.Confidence != 0 {
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
		Niche:               "beauty",
		Tier:                tierMicro,
		Snapshots:           []connector.Snapshot{snap},
		Fraud:               contract.FraudInput{Present: true, FakeFollowerRate: 0.05, BotCommentRate: 0.1, EngagementAnomaly: 0.15, Confidence: 0.7},
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
