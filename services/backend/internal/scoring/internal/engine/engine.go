package engine

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

// ErrNoWeights is returned when the weight set has no positive total, which
// makes the composite undefined. It signals a misconfigured weight row, not a
// bad audit.
var ErrNoWeights = errors.New("scoring: weight set sums to zero")

// Reach saturation bounds. Reach maps follower count onto [0,1] on a log10 scale
// between these edges: a ~1K-follower account sits at the floor, a 10M+ account
// saturates the ceiling. The scale is logarithmic because audience reach spans
// orders of magnitude and its marginal influence diminishes with size.
const (
	reachFloor = 1_000.0
	reachCeil  = 10_000_000.0
)

// engagementDepthSpan is the (comments + shares) / (likes + 1) ratio at which
// the content-quality signal saturates. Deeper interactions than a passive like
// — comments and shares — indicate stronger content; the span is wide because
// this is a soft, exploratory signal weighted at only 0.05.
const engagementDepthSpan = 0.15

// growthSpan is the standard deviation of period-over-period follower growth
// that drives the consistency growth component to zero. Half-again swings
// between consecutive readings read as erratic.
const growthSpan = 0.5

// Fraud sub-signal weights inside the authenticity subscore. Fake followers
// weigh most (they inflate the reach the whole score rewards), then bot comments
// and the engagement anomaly. They sum to one.
const (
	fraudWeightFakeFollowers = 0.40
	fraudWeightBotComments   = 0.30
	fraudWeightAnomaly       = 0.30
)

// Input is everything Compute needs, all of it already in memory: the resolved
// (niche, tier), the platform snapshots that succeeded, the fraud signal, and
// the active weights and engagement benchmark. Compute performs no lookups, so a
// test constructs an Input directly and asserts the whole surface.
type Input struct {
	Niche               string
	Tier                string
	Snapshots           []connector.Snapshot
	Fraud               contract.FraudInput
	Weights             Weights
	EngagementBenchmark Benchmark
}

// Compute is the pure heart of the scoring engine. It derives the five subscores
// from the snapshots and fraud signal, weights them into the composite, and
// stamps the weight and benchmark versions. It never touches the database, the
// clock, or the network.
//
// Composite (PRD §6):
//
//	0.30·reach + 0.30·engagement_quality + 0.25·authenticity +
//	0.10·consistency + 0.05·content_quality
//
// with the caller's weights normalized to sum to one.
func Compute(in Input) (contract.Score, error) {
	if in.Weights.sum() <= 0 {
		return contract.Score{}, ErrNoWeights
	}

	platforms := contributingPlatforms(in.Snapshots)
	followers := representativeFollowers(in.Snapshots)

	reach := reachSubscore(followers)
	engagement := engagementSubscore(in.Snapshots, followers, in.EngagementBenchmark)
	authenticity := authenticitySubscore(in.Fraud)
	consistency := consistencySubscore(in.Snapshots)
	content := contentSubscore(in.Snapshots)

	w := in.Weights
	total := w.sum()
	overall01 := (w.Reach*reach.Value +
		w.EngagementQuality*engagement.Value +
		w.Authenticity*authenticity.Value +
		w.Consistency*consistency.Value +
		w.ContentQuality*content.Value) / (total * 100)

	overallConf := (w.Reach*reach.Confidence +
		w.EngagementQuality*engagement.Confidence +
		w.Authenticity*authenticity.Confidence +
		w.Consistency*consistency.Confidence +
		w.ContentQuality*content.Confidence) / total

	return contract.Score{
		Niche:                 in.Niche,
		Tier:                  in.Tier,
		Overall:               clamp01(overall01) * 100,
		Reach:                 reach,
		EngagementQuality:     engagement,
		Authenticity:          authenticity,
		Consistency:           consistency,
		ContentQuality:        content,
		OverallConfidence:     clamp01(overallConf),
		WeightsVersion:        w.Version,
		BenchmarkVersion:      in.EngagementBenchmark.Version,
		BenchmarkLabel:        in.EngagementBenchmark.Label,
		ContributingPlatforms: platforms,
	}, nil
}

// contributingPlatforms returns, in a stable order, the platforms whose snapshot
// carried any usable data. A snapshot that produced nothing (no followers, posts
// or metrics) does not count toward what the score covers.
func contributingPlatforms(snaps []connector.Snapshot) []connector.Platform {
	out := make([]connector.Platform, 0, len(snaps))
	for _, s := range snaps {
		if s.Followers > 0 || len(s.Posts) > 0 || len(s.Metrics) > 0 {
			out = append(out, s.Platform)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// representativeFollowers is the largest follower count across the snapshots —
// the influencer's primary-platform audience, which sets the tier and anchors
// reach.
func representativeFollowers(snaps []connector.Snapshot) int64 {
	var largest int64
	for _, s := range snaps {
		if s.Followers > largest {
			largest = s.Followers
		}
	}
	return largest
}

// reachSubscore maps follower count onto [0,1] on a log scale between reachFloor
// and reachCeil. Confidence is full when a follower count is known and zero when
// it is not, since reach is otherwise undetermined.
func reachSubscore(followers int64) contract.Subscore {
	if followers <= 0 {
		return contract.Subscore{Value: 0, Confidence: 0}
	}
	num := math.Log10(float64(followers)) - math.Log10(reachFloor)
	den := math.Log10(reachCeil) - math.Log10(reachFloor)
	return contract.Subscore{Value: clamp01(num/den) * 100, Confidence: 1}
}

// ObservedEngagementRate is the mean per-post engagement rate across snapshots
// that report a follower base. It returns false when no post-bearing snapshot
// has a positive follower count, so the caller can treat the signal as absent
// rather than zero. It is exported so the service can persist the value that
// corpus recomputation later aggregates into real benchmark percentiles.
func ObservedEngagementRate(snaps []connector.Snapshot) (float64, bool) {
	var rates []float64
	for _, s := range snaps {
		if s.Followers <= 0 {
			continue
		}
		for _, p := range s.Posts {
			eng := float64(p.Likes + p.Comments + p.Shares)
			rates = append(rates, eng/float64(s.Followers))
		}
	}
	if len(rates) == 0 {
		return 0, false
	}
	return mean(rates), true
}

// engagementSubscore places the observed engagement rate within the benchmark's
// percentile band. It rewards healthy engagement without penalizing unusually
// high values here — inflated engagement is the authenticity subscore's job, and
// double-counting it would punish genuinely strong creators. Confidence tracks
// the benchmark's sample size, so a cold-start band yields a low-confidence
// subscore.
func engagementSubscore(snaps []connector.Snapshot, followers int64, b Benchmark) contract.Subscore {
	conf := confidenceForSamples(b.SampleSize)
	observed, ok := ObservedEngagementRate(snaps)
	if !ok || followers <= 0 {
		// No posts to judge: neutral value, no confidence.
		return contract.Subscore{Value: 50, Confidence: 0}
	}
	return contract.Subscore{Value: percentileScore(observed, b) * 100, Confidence: conf}
}

// percentileScore maps a value onto [0,1] against the benchmark's percentile
// ladder: p10→0.10, p25→0.30, p50→0.50, p75→0.75, p90→0.95, linearly between
// them, from 0 at zero up to the p10 anchor and easing from 0.95 toward 1.0
// above p90. The ladder is monotonically non-decreasing, so a higher observed
// rate never scores lower.
func percentileScore(v float64, b Benchmark) float64 {
	type knot struct{ x, y float64 }
	ladder := []knot{
		{0, 0},
		{b.P10, 0.10},
		{b.P25, 0.30},
		{b.P50, 0.50},
		{b.P75, 0.75},
		{b.P90, 0.95},
	}
	if v >= b.P90 {
		if b.P90 <= 0 {
			return 1
		}
		return clamp(0.95+0.05*((v-b.P90)/b.P90), 0.95, 1)
	}
	for i := 1; i < len(ladder); i++ {
		lo, hi := ladder[i-1], ladder[i]
		if v <= hi.x {
			if hi.x == lo.x {
				return hi.y
			}
			frac := (v - lo.x) / (hi.x - lo.x)
			return clamp01(lo.y + frac*(hi.y-lo.y))
		}
	}
	return 0.95
}

// authenticitySubscore turns the fraud signal into a positive authenticity
// value: the more fraud, the lower the score. With no fraud pass it is neutral
// and zero-confidence rather than clean, so a degraded audit never silently
// certifies an account.
func authenticitySubscore(f contract.FraudInput) contract.Subscore {
	if !f.Present {
		return contract.Subscore{Value: 50, Confidence: 0}
	}
	fraud := clamp01(fraudWeightFakeFollowers*clamp01(f.FakeFollowerRate) +
		fraudWeightBotComments*clamp01(f.BotCommentRate) +
		fraudWeightAnomaly*clamp01(f.EngagementAnomaly))
	return contract.Subscore{Value: (1 - fraud) * 100, Confidence: clamp01(f.Confidence)}
}

// consistencySubscore blends growth smoothness (steady rather than spiky
// follower trajectory) and posting cadence regularity. Each component is used
// only when it has enough data; the value is the mean of the available
// components and the confidence the mean of their data-backed confidences.
func consistencySubscore(snaps []connector.Snapshot) contract.Subscore {
	var values, confs []float64

	if v, c, ok := growthSmoothness(snaps); ok {
		values = append(values, v)
		confs = append(confs, c)
	}
	if v, c, ok := cadenceRegularity(snaps); ok {
		values = append(values, v)
		confs = append(confs, c)
	}
	if len(values) == 0 {
		return contract.Subscore{Value: 50, Confidence: 0}
	}
	return contract.Subscore{Value: mean(values) * 100, Confidence: mean(confs)}
}

// growthSmoothness measures how steady the follower trajectory is. It reads the
// follower/subscriber metric series across snapshots, orders it by time, and
// scores 1 minus the (clamped) standard deviation of period-over-period growth
// rates. It needs at least three points; confidence grows with the series
// length.
func growthSmoothness(snaps []connector.Snapshot) (value, confidence float64, ok bool) {
	type pt struct {
		at time.Time
		v  float64
	}
	var series []pt
	for _, s := range snaps {
		for _, m := range s.Metrics {
			if m.Name == "followers" || m.Name == "subscribers" {
				series = append(series, pt{at: m.At, v: m.Value})
			}
		}
	}
	if len(series) < 3 {
		return 0, 0, false
	}
	sort.Slice(series, func(i, j int) bool { return series[i].at.Before(series[j].at) })

	growth := make([]float64, 0, len(series)-1)
	for i := 1; i < len(series); i++ {
		prev := series[i-1].v
		growth = append(growth, (series[i].v-prev)/(math.Abs(prev)+1))
	}
	smooth := 1 - clamp01(stddev(growth)/growthSpan)
	return smooth, clamp01(float64(len(series)-2) / 10), true
}

// cadenceRegularity measures how evenly spaced an account's posts are. It orders
// post timestamps across snapshots and scores 1 minus the (clamped) coefficient
// of variation of the inter-post intervals. It needs at least three timestamped
// posts; confidence grows with the post count.
func cadenceRegularity(snaps []connector.Snapshot) (value, confidence float64, ok bool) {
	var times []time.Time
	for _, s := range snaps {
		for _, p := range s.Posts {
			if !p.PublishedAt.IsZero() {
				times = append(times, p.PublishedAt)
			}
		}
	}
	if len(times) < 3 {
		return 0, 0, false
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

	intervals := make([]float64, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		intervals = append(intervals, times[i].Sub(times[i-1]).Hours())
	}
	m := mean(intervals)
	if m <= 0 {
		return 0, 0, false
	}
	regularity := 1 - clamp01(stddev(intervals)/m)
	return regularity, clamp01(float64(len(times)-2) / 10), true
}

// contentSubscore rewards interaction depth: comments and shares relative to
// likes, since a share or a comment is a stronger signal of resonance than a
// passive like. It uses the median across posts so one viral post does not
// dominate. With no posts it is neutral and zero-confidence.
func contentSubscore(snaps []connector.Snapshot) contract.Subscore {
	var depths []float64
	var postCount int
	for _, s := range snaps {
		for _, p := range s.Posts {
			postCount++
			depths = append(depths, float64(p.Comments+p.Shares)/(float64(p.Likes)+1))
		}
	}
	if len(depths) == 0 {
		return contract.Subscore{Value: 50, Confidence: 0}
	}
	value := clamp01(median(depths)/engagementDepthSpan) * 100
	return contract.Subscore{Value: value, Confidence: clamp01(float64(postCount) / 10)}
}
