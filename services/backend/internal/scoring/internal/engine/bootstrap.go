package engine

// MetricEngagementRate is the benchmark metric the engagement subscore reads:
// mean per-post engagements (likes + comments + shares) divided by followers.
const MetricEngagementRate = "engagement_rate"

// BootstrapLabel is the provenance label stamped on every cold-start benchmark
// and surfaced in the score breakdown and the report, so a reader can tell a
// reference-band score from one grounded in real corpus percentiles.
const BootstrapLabel = "industry-bootstrap v1"

// bootstrapVersion is the version number of the cold-start generation. Corpus
// recomputation writes strictly higher versions.
const bootstrapVersion = 1

// BenchmarkLabelFor renders the human-facing provenance label for a benchmark
// from its source and version: a bootstrap generation is always the fixed
// "industry-bootstrap v1" reference tag, while a corpus generation is labelled
// with its version so a reader can tell how many recomputations back it is.
func BenchmarkLabelFor(source string, version int) string {
	if source == "bootstrap" {
		return BootstrapLabel
	}
	return "corpus v" + itoa(version)
}

// itoa avoids pulling strconv into the otherwise math-only pure package for a
// single small, non-negative conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// bootstrapSampleSize is the nominal sample size attributed to a bootstrap band.
// It is deliberately small: these are priors, not measurements, so every
// subscore built on them carries low confidence (confidenceForSamples(10) ≈
// 0.25) until real corpus data (SampleSize >= corpusThreshold) supersedes them.
const bootstrapSampleSize = 10

// BootstrapWeights returns the cold-start weight set: the composite weighting
// mandated by the PRD (0.30 reach, 0.30 engagement quality, 0.25 authenticity,
// 0.10 consistency, 0.05 content quality). It is seeded under the baseline
// (niche, tier) cell; a vertical that needs a different weighting is an INSERT of
// an active row for its own (niche, tier), never a code change.
func BootstrapWeights() Weights {
	return Weights{
		Reach:             0.30,
		EngagementQuality: 0.30,
		Authenticity:      0.25,
		Consistency:       0.10,
		ContentQuality:    0.05,
		Version:           bootstrapVersion,
	}
}

// engagementBand is a per-tier cold-start engagement-rate band. The bands widen
// and overlap across tiers on purpose: they encode only the coarse, uncontested
// industry observation that engagement rate declines as an audience grows, with
// no pretence of precision.
type engagementBand struct {
	tier                    string
	p10, p25, p50, p75, p90 float64
}

// bootstrapEngagementBands are REFERENCE DATA — an industry prior akin to a
// starter tax table, not fabricated user rows. They are intentionally NOT the
// numbers in services/ml features/engagement.py _ENGAGEMENT_CURVE
// (0.050/0.035/0.020/0.015, floor 0.012): that curve's only corroboration across
// 24 researched sources was a competitor's marketing blog
// (product/research/fraud-detection-signals.md §8), so it is not citable.
//
// These are coarse, deliberately wide bands reflecting the broadly reported
// pattern that per-post engagement (engagements / followers) is high for the
// smallest creators and compresses toward a low single-digit percentage at the
// top. They vary by tier only; niche is left at the baseline cell because there
// is no citable niche-specific prior at cold start. Real per-(niche, tier)
// percentiles replace them, cell by cell, once corpus recomputation reaches the
// sample threshold — which is the whole reason confidence stays low here.
var bootstrapEngagementBands = []engagementBand{
	{tier: tierNano, p10: 0.020, p25: 0.035, p50: 0.055, p75: 0.085, p90: 0.130},
	{tier: tierMicro, p10: 0.014, p25: 0.024, p50: 0.040, p75: 0.062, p90: 0.095},
	{tier: tierMid, p10: 0.009, p25: 0.017, p50: 0.028, p75: 0.045, p90: 0.070},
	{tier: tierMacro, p10: 0.006, p25: 0.012, p50: 0.021, p75: 0.034, p90: 0.055},
	{tier: tierMega, p10: 0.004, p25: 0.009, p50: 0.016, p75: 0.027, p90: 0.045},
}

// BootstrapBenchmark pairs a cold-start benchmark with the tier it belongs to,
// so the seeder can key each row without re-deriving the tier order.
type BootstrapBenchmark struct {
	Tier      string
	Benchmark Benchmark
}

// BootstrapBenchmarks returns the cold-start engagement-rate benchmarks, one per
// tier under the baseline niche. Mean is taken at the band midpoint (p50) and
// stddev approximated from the p10..p90 span (a normal's 10th–90th percentiles
// are ~2.56 sigma apart), so a consumer that wants a parametric form has one that
// is consistent with the band.
func BootstrapBenchmarks() []BootstrapBenchmark {
	const p10p90Sigmas = 2.5631 // z(0.9) - z(0.1)
	out := make([]BootstrapBenchmark, 0, len(bootstrapEngagementBands))
	for _, b := range bootstrapEngagementBands {
		out = append(out, BootstrapBenchmark{
			Tier: b.tier,
			Benchmark: Benchmark{
				Metric:     MetricEngagementRate,
				P10:        b.p10,
				P25:        b.p25,
				P50:        b.p50,
				P75:        b.p75,
				P90:        b.p90,
				Mean:       b.p50,
				Stddev:     (b.p90 - b.p10) / p10p90Sigmas,
				SampleSize: bootstrapSampleSize,
				Version:    bootstrapVersion,
				Source:     "bootstrap",
				Label:      BootstrapLabel,
			},
		})
	}
	return out
}
