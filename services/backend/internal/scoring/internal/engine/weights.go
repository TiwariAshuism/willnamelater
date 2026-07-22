// Package engine is the pure scoring core. Compute turns a set of platform
// snapshots plus a fraud signal, active weights, and an active engagement
// benchmark into a Score, with no I/O of any kind — which is what lets it be
// exhaustively table-tested over every weight and benchmark cell.
package engine

import (
	"math"

	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
)

// DefaultTier is the tier used when a follower count cannot place an account,
// and BaselineNiche / BaselineTier key the cold-start rows every lookup falls
// back to (see the repository's resolution order).
const (
	BaselineNiche = "default"
	BaselineTier  = "default"
)

// Tier bands, in followers. These mirror the industry size buckets maintained by
// the influencer module (model.TierForFollowers). They are duplicated here, not
// imported, because that policy lives under internal/influencer/internal and Go
// forbids a sibling module from reaching into it; the bands are a stable,
// well-known industry constant rather than evolving business logic.
const (
	tierNano  = "nano"
	tierMicro = "micro"
	tierMid   = "mid"
	tierMacro = "macro"
	tierMega  = "mega"

	microFloor = 10_000
	midFloor   = 100_000
	macroFloor = 500_000
	megaFloor  = 1_000_000
)

// TierForFollowers derives the audience-size tier from a follower count, with
// half-open upper edges (the named floor is inclusive, the next floor exclusive)
// exactly as the influencer module derives it.
func TierForFollowers(followers int64) string {
	switch {
	case followers >= megaFloor:
		return tierMega
	case followers >= macroFloor:
		return tierMacro
	case followers >= midFloor:
		return tierMid
	case followers >= microFloor:
		return tierMicro
	default:
		return tierNano
	}
}

// Tiers lists the tiers in ascending size order. Bootstrap seeding walks it so a
// benchmark exists for every band.
func Tiers() []string {
	return []string{tierNano, tierMicro, tierMid, tierMacro, tierMega}
}

// Weights is an active weight set: the four hireability-factor weights (PRD §6)
// and the version that produced them. The weights need not pre-sum to one —
// Compute normalizes them — so an operator can INSERT a new vertical's weights
// without hand-checking that they total exactly 1.0.
type Weights struct {
	EngagementAuthenticity float64
	AudienceQuality        float64
	ConsistencyReliability float64
	BrandFitClarity        float64
	Version                int
}

// sum returns the total of the four factor weights.
func (w Weights) sum() float64 {
	return w.EngagementAuthenticity + w.AudienceQuality + w.ConsistencyReliability + w.BrandFitClarity
}

// Benchmark is an active benchmark generation for one (niche, tier, metric)
// cell: the percentile band, its summary statistics, the sample size behind it,
// and the provenance that qualifies it.
type Benchmark struct {
	Metric string
	P10    float64
	P25    float64
	P50    float64
	P75    float64
	P90    float64
	Mean   float64
	Stddev float64
	// SampleSize is the number of DISTINCT INFLUENCERS observed in the cell — and
	// only that. It is 0 (persisted as SQL NULL) for a SourceBootstrap band, which
	// rests on no observations whatsoever. It is never a nominal or notional count:
	// every value here is something that was actually counted.
	SampleSize int
	Version    int
	Source     string
	Label      string
}

// corpusThreshold is the number of DISTINCT INFLUENCERS a cell needs before its
// bootstrap band may be replaced by corpus percentiles. It also sets the
// confidence half-life below: a benchmark built on corpusThreshold distinct
// influencers carries 0.5 confidence.
const corpusThreshold = 30

// confidenceForSamples maps a REAL, COUNTED sample size to a [0,1) confidence via
// n/(n+k) with k = corpusThreshold. n is the number of DISTINCT INFLUENCERS behind
// a corpus benchmark — never a nominal stand-in. It is called only for
// SourceCorpus benchmarks: a bootstrap band has no samples at all and takes
// BootstrapPriorSupport instead, so no prior is ever laundered through this
// function into a number that looks measured.
func confidenceForSamples(n int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(n) / float64(n+corpusThreshold)
}

// benchmarkSupport returns the support a subscore inherits from its benchmark and
// the KIND of claim that support makes.
//
// A corpus benchmark's support is a genuine confidence: it rises with the number
// of distinct influencers actually observed in the cell. A bootstrap band's
// support is a documented prior over zero observations. Keeping these apart is the
// whole point — the previous code ran the bootstrap band through
// confidenceForSamples(10) and shipped the result as a measured confidence.
func benchmarkSupport(b Benchmark) (support float64, kind string) {
	if b.Source == SourceCorpus {
		return confidenceForSamples(b.SampleSize), contract.SupportConfidence
	}
	return BootstrapPriorSupport, contract.SupportPrior
}

// benchmarkBasis names what produced a value scored against this benchmark: a
// percentile within a real reference population (corpus), or a closed-form ladder
// against fixed reference constants (bootstrap) — which is arithmetic, not a
// measurement of any population.
func benchmarkBasis(b Benchmark) string {
	if b.Source == SourceCorpus {
		return contract.BasisCorpus
	}
	return contract.BasisClosedForm
}

// clamp01 bounds v into [0,1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// clamp bounds v into [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// mean returns the arithmetic mean of xs, or 0 for an empty slice.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// stddev returns the population standard deviation of xs, or 0 for fewer than
// two values.
func stddev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := mean(xs)
	var s float64
	for _, x := range xs {
		d := x - m
		s += d * d
	}
	return math.Sqrt(s / float64(len(xs)))
}

// median returns the median of xs (which it sorts a copy of), or 0 when empty.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	insertionSort(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

// insertionSort sorts xs ascending in place. The slices here are tiny (one
// audit's posts and metric points), so a dependency-free insertion sort keeps
// the pure package free of imports beyond math.
func insertionSort(xs []float64) {
	for i := 1; i < len(xs); i++ {
		v := xs[i]
		j := i - 1
		for j >= 0 && xs[j] > v {
			xs[j+1] = xs[j]
			j--
		}
		xs[j+1] = v
	}
}
