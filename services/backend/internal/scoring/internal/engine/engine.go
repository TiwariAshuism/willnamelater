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

// ErrInsufficientEvidence is returned when too little of the composite's weight
// rests on components we actually measured. The engine refuses to invent a number
// out of absent data: the caller must disclose that the account could not be
// scored, which is the honest outcome and the one a trust product must be able to
// give.
var ErrInsufficientEvidence = errors.New("scoring: too few measured components to score")

// Audience-size saturation bounds. The component maps FOLLOWER COUNT onto [0,1]
// on a log10 scale between these edges: a ~1K-follower account sits at the floor,
// a 10M+ account saturates the ceiling. The scale is logarithmic because audience
// size spans orders of magnitude and its marginal influence diminishes with size.
//
// This component was once called "reach", which was a lie with a dangerous edge:
// it is computed ENTIRELY from follower count — the exact quantity purchased
// followers inflate — so a creator who buys 100K followers scored HIGHER on the
// dimension the product exists to police. It is now named for what it measures,
// and its confidence reflects that a follower count is a weak proxy for the reach
// it was pretending to be. Real reach is only knowable from first-party Insights
// (Flow A), and when we have it, it belongs in a separate, honest component.
const (
	audienceFloor = 1_000.0
	audienceCeil  = 10_000_000.0
)

// audienceSizeConfidence is the confidence of the audience-size component when a
// follower count IS known. It is deliberately not 1.0: follower count is a
// self-reported, purchasable number and a weak proxy for actual reach, so the
// component must not be able to dominate the composite at full certainty.
const audienceSizeConfidence = 0.5

// neutralScore is the midpoint a subscore is shrunk toward in proportion to how
// little we trust it. Shrinking to the midpoint (rather than to 0 or 100) is the
// only choice that does not smuggle in a verdict: low confidence must mean "we
// don't know", not "we suspect the worst" or "we assume the best".
const neutralScore = 50.0

// minEvidencedWeight is the share of the composite's weight that must rest on
// ACTUALLY MEASURED components before a number may be published at all. Below it,
// too much of the score would be inference over absence, so the engine refuses to
// produce a composite and the deliverable discloses that it could not score the
// account. An audit that measured almost nothing must say so, not emit a
// confident-looking 50-something.
const minEvidencedWeight = 0.5

// engagementDepthSpan is the (comments + shares) / (likes + 1) ratio at which
// the content-quality signal saturates. Deeper interactions than a passive like
// — comments and shares — indicate stronger content; the span is wide because
// this is a soft, exploratory signal weighted at only 0.05.
const engagementDepthSpan = 0.15

// growthSpan is the standard deviation of period-over-period follower growth
// that drives the consistency growth component to zero. Half-again swings
// between consecutive readings read as erratic.
const growthSpan = 0.5

// Fraud sub-signal weights inside the authenticity subscore. Two INDEPENDENT
// measurements are blended:
//
//   - RiskScore, the ml service's per-account composite (growth spike, engagement
//     deviation, like/comment ratio, UnDBot), already renormalized over whatever
//     it could observe; and
//   - CliqueMembershipFraction, the co-commenter coordination signal from a
//     different model entirely.
//
// The engagement anomaly is NOT a third term: it is already inside RiskScore, and
// blending it again would double-count it.
//
// The weights are renormalized over whichever terms are actually present, so an
// audit with no comments (no clique signal) is scored on the risk score alone at
// full weight — not dragged toward "clean" by a coordination signal we never
// measured.
const (
	fraudWeightRisk         = 0.65
	fraudWeightCoordination = 0.35
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

	reach := audienceSizeSubscore(followers)
	engagement := engagementSubscore(in.Snapshots, followers, in.EngagementBenchmark)
	authenticity := authenticitySubscore(in.Fraud)
	consistency := consistencySubscore(in.Snapshots)
	content := contentSubscore(in.Snapshots)

	// The composite is a weighted mean over the components we ACTUALLY MEASURED.
	//
	// It used to be a weighted mean of every component's Value, with Confidence
	// tracked as a separate, parallel number that never touched the score. A
	// component with zero confidence — engagement with no posts, say, which returns
	// a neutral {Value: 50, Confidence: 0} — still contributed its full weight×50 to
	// the headline. A third of the composite could be invented 50s while the
	// customer read a confident-looking number.
	//
	// Now a zero-confidence component is DROPPED and its weight renormalized away,
	// and a surviving component is shrunk toward neutral in proportion to how little
	// we trust it. An audit that measured almost nothing yields no number at all.
	w := in.Weights
	components := []struct {
		weight float64
		sub    contract.Subscore
	}{
		{w.Reach, reach},
		{w.EngagementQuality, engagement},
		{w.Authenticity, authenticity},
		{w.Consistency, consistency},
		{w.ContentQuality, content},
	}

	var weightedValue, weightedConf, evidencedWeight float64
	for _, c := range components {
		if c.sub.Support <= 0 {
			// Not measured. It contributes nothing and forfeits its weight.
			continue
		}
		// Shrink toward neutral by support: a half-supported 90 should not move the
		// composite as far as a fully-supported 90.
		effective := neutralScore + c.sub.Support*(c.sub.Value-neutralScore)
		weightedValue += c.weight * effective
		weightedConf += c.weight * c.sub.Support
		evidencedWeight += c.weight
	}

	total := w.sum()

	// Too little of the score rests on real evidence to publish a number. Returning
	// a composite here would be an invention dressed as a measurement, so the score
	// is withheld and the caller must disclose the absence.
	if evidencedWeight/total < minEvidencedWeight {
		return contract.Score{}, ErrInsufficientEvidence
	}

	overall01 := weightedValue / (evidencedWeight * 100)
	overallConf := weightedConf / total

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

// audienceSizeSubscore maps FOLLOWER COUNT onto [0,1] on a log scale between
// audienceFloor and audienceCeil.
//
// It measures audience SIZE, not reach. Follower count is self-reported and
// purchasable — it is the very number a fraudulent account inflates — so this
// component is capped at audienceSizeConfidence rather than the full certainty it
// once claimed. At Confidence: 1 and weight 0.30 it was the heaviest term in the
// composite, which meant buying followers RAISED an account's audit score. It no
// longer can dominate, and it is no longer named for a thing it does not measure.
//
// Support is zero when no follower count is known: the size is then absent, not
// zero, and a zero-support component is dropped from the composite entirely. When
// a count IS known the support is audienceSizeConfidence — a documented PRIOR
// discount on a purchasable number, not a confidence anyone measured, so it is
// stamped SupportPrior.
func audienceSizeSubscore(followers int64) contract.Subscore {
	if followers <= 0 {
		return contract.Subscore{
			Value:       0,
			Basis:       contract.BasisClosedForm,
			Support:     0,
			SupportKind: contract.SupportNone,
		}
	}
	num := math.Log10(float64(followers)) - math.Log10(audienceFloor)
	den := math.Log10(audienceCeil) - math.Log10(audienceFloor)
	return contract.Subscore{
		Value:       clamp01(num/den) * 100,
		Basis:       contract.BasisClosedForm,
		Support:     audienceSizeConfidence,
		SupportKind: contract.SupportPrior,
	}
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
// double-counting it would punish genuinely strong creators.
//
// Its basis and support come from the benchmark's provenance: against a corpus
// cell it is a real percentile whose confidence rises with the number of distinct
// influencers observed; against a cold-start band it is a closed-form ladder over
// reference constants, carrying the documented BootstrapPriorSupport and nothing
// more.
func engagementSubscore(snaps []connector.Snapshot, followers int64, b Benchmark) contract.Subscore {
	observed, ok := ObservedEngagementRate(snaps)
	if !ok || followers <= 0 {
		// No posts to judge: neutral value, no support.
		return contract.Subscore{Value: 50, Basis: benchmarkBasis(b), Support: 0, SupportKind: contract.SupportNone}
	}
	support, kind := benchmarkSupport(b)
	return contract.Subscore{
		Value:       percentileScore(observed, b) * 100,
		Basis:       benchmarkBasis(b),
		Support:     support,
		SupportKind: kind,
	}
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
		return contract.Subscore{Value: 50, Basis: contract.BasisClosedForm, Support: 0, SupportKind: contract.SupportNone}
	}

	// A promoted champion refines the whole vector: its score IS the calibrated
	// fraud probability (0-100, higher = more fraud), trained on the fraud label,
	// so it supersedes the heuristic blend. Cold start (RefinedScore nil) uses the
	// explainable blend below. The basis names the exact model version, so a reader
	// can tell a champion's output from the arithmetic blend it replaced.
	if f.RefinedScore != nil {
		fraud := clamp01(*f.RefinedScore / 100)
		return contract.Subscore{
			Value:       (1 - fraud) * 100,
			Basis:       contract.ModelBasis(f.ModelVersion),
			Support:     clamp01(f.Confidence),
			SupportKind: contract.SupportConfidence,
		}
	}

	// Blend only the signals actually OBSERVED, renormalizing their weights. An
	// absent signal contributes nothing and takes its weight with it — it is never
	// clamped to zero, which would be a full-weight vote for "clean" on a
	// measurement we never made.
	var weighted, presentWeight float64
	if f.RiskScore != nil {
		weighted += fraudWeightRisk * clamp01(*f.RiskScore/100)
		presentWeight += fraudWeightRisk
	}
	if f.CliqueMembershipFraction != nil {
		weighted += fraudWeightCoordination * clamp01(*f.CliqueMembershipFraction)
		presentWeight += fraudWeightCoordination
	}

	// A fraud pass ran but produced no usable signal. That is not a clean account —
	// it is an unexamined one. Return neutral at zero support, exactly as if no
	// pass had run, so it cannot certify anything.
	if presentWeight == 0 {
		return contract.Subscore{Value: 50, Basis: contract.BasisClosedForm, Support: 0, SupportKind: contract.SupportNone}
	}

	fraud := clamp01(weighted / presentWeight)
	// Scale the ml service's own confidence by the share of the signal vector we
	// actually saw: an authenticity verdict resting on one of two signals is not as
	// trustworthy as one resting on both, and the number must say so. The support
	// therefore IS a confidence (the model's, discounted by observed coverage), but
	// the value itself is an arithmetic blend of model outputs — a closed form, not
	// a model trained to produce it.
	conf := clamp01(f.Confidence) * presentWeight
	return contract.Subscore{
		Value:       (1 - fraud) * 100,
		Basis:       contract.BasisClosedForm,
		Support:     clamp01(conf),
		SupportKind: contract.SupportConfidence,
	}
}

// consistencySubscore blends growth smoothness (steady rather than spiky
// follower trajectory) and posting cadence regularity. Each component is used
// only when it has enough data; the value is the mean of the available components
// and the support the mean of their COVERAGES — how much of the series each one
// got to look at. Coverage, not confidence: a long series makes the closed form
// better fed, not validated.
func consistencySubscore(snaps []connector.Snapshot) contract.Subscore {
	var values, coverages []float64

	if v, c, ok := growthSmoothness(snaps); ok {
		values = append(values, v)
		coverages = append(coverages, c)
	}
	if v, c, ok := cadenceRegularity(snaps); ok {
		values = append(values, v)
		coverages = append(coverages, c)
	}
	if len(values) == 0 {
		return contract.Subscore{Value: 50, Basis: contract.BasisClosedForm, Support: 0, SupportKind: contract.SupportNone}
	}
	return contract.Subscore{
		Value:       mean(values) * 100,
		Basis:       contract.BasisClosedForm,
		Support:     mean(coverages),
		SupportKind: contract.SupportCoverage,
	}
}

// growthSmoothness measures how steady the follower trajectory is. It reads the
// follower/subscriber metric series across snapshots, orders it by time, and
// scores 1 minus the (clamped) standard deviation of period-over-period growth
// rates. It needs at least three points; COVERAGE (how much of the series we
// have, saturating at twelve points) grows with the series length. Coverage is
// not a claim that the smoothness proxy detects anything.
func growthSmoothness(snaps []connector.Snapshot) (value, coverage float64, ok bool) {
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
// posts; COVERAGE grows with the post count and says nothing about accuracy.
func cadenceRegularity(snaps []connector.Snapshot) (value, coverage float64, ok bool) {
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

// contentPostsForFullCoverage is the number of posts at which the content proxy
// has all the data it can use. Beyond it, coverage saturates — MORE POSTS DO NOT
// MAKE THE PROXY MORE CORRECT, they only stop it being starved.
const contentPostsForFullCoverage = 10.0

// contentSubscore rewards interaction depth: comments and shares relative to
// likes, since a share or a comment is a stronger signal of resonance than a
// passive like. It uses the median across posts so one viral post does not
// dominate. With no posts it is neutral and unsupported.
//
// Its support is postCount/10 — and that number is DATA COVERAGE, nothing else.
// It was previously emitted in a field called Confidence, which said, to anyone
// reading the score, that an account with ten posts had a 100%-confident content
// score. What it actually meant was "we saw ten posts". Whether the
// comments-and-shares-over-likes ratio tracks content quality at all is an open
// question this number cannot and does not answer, which is exactly why the kind
// is stamped SupportCoverage and the basis is closed_form.
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
		return contract.Subscore{Value: 50, Basis: contract.BasisClosedForm, Support: 0, SupportKind: contract.SupportNone}
	}
	value := clamp01(median(depths)/engagementDepthSpan) * 100
	return contract.Subscore{
		Value:       value,
		Basis:       contract.BasisClosedForm,
		Support:     clamp01(float64(postCount) / contentPostsForFullCoverage),
		SupportKind: contract.SupportCoverage,
	}
}
