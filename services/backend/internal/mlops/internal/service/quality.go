package service

import (
	"sort"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
)

// Data-quality / anti-gaming thresholds. They are documented, tunable constants
// with a single source here: the filter is the one place that decides whether a
// completed audit teaches the model anything. The audit's own result is never
// affected — only the training fold is.
const (
	// maxFakeFollowerRate rejects a row whose current-champion fake-follower
	// estimate is this high: a gamed account must not teach the model that fraud
	// looks normal (feedback-poisoning guard).
	maxFakeFollowerRate = 0.30
	// minAccountAgeDays rejects brand-new accounts, whose metrics are volatile and
	// easily gamed.
	minAccountAgeDays = 90.0
	// maxFollowerGrowthRatio rejects a bought-follower spike: adjacent-interval
	// growth above this fraction inside the spike window.
	maxFollowerGrowthRatio = 0.50
	// followerSpikeWindow bounds how close two follower readings must be for their
	// growth to count as a spike rather than organic accumulation.
	followerSpikeWindow = 24 * time.Hour
	// minPostsForStableFeatures rejects a row with too few posts for stable
	// engagement features.
	minPostsForStableFeatures = 5
)

// Quality reason codes. Each failing rule appends its code; a row is clean
// (quality_ok) only when no code fired. Rejected rows are still stored with their
// reasons for admin review and excluded from the training export by default.
const (
	reasonFakeFollowerHigh = "fake_follower_estimate_high"
	reasonAccountTooNew    = "account_too_new"
	reasonFollowerSpike    = "follower_spike"
	reasonInsufficientPost = "insufficient_posts"
	reasonNoFraudEstimate  = "no_fraud_estimate"
)

// evaluateQuality computes the data-quality verdict for a capture from the
// current champion's fraud read, the computed vector, and the primary snapshot's
// follower series. It returns the ordered reason codes; quality_ok is the empty
// check (len == 0), which the caller records. The codes are appended in a fixed
// order so the stored reasons are deterministic.
func evaluateQuality(fraud contract.FraudSignal, vec model.FeatureVector, primary connector.Snapshot) []string {
	reasons := make([]string, 0, 4)

	// Without the current model's read we cannot quality-check the row at all, so
	// this is evaluated first and the other fraud-derived checks still run on
	// whatever values are present.
	if !fraud.Present {
		reasons = append(reasons, reasonNoFraudEstimate)
	}
	if fraud.FakeFollowerRate >= maxFakeFollowerRate {
		reasons = append(reasons, reasonFakeFollowerHigh)
	}
	if vec.AccountAgeDaysProxy != nil && *vec.AccountAgeDaysProxy < minAccountAgeDays {
		reasons = append(reasons, reasonAccountTooNew)
	}
	if hasFollowerSpike(primary.Metrics) {
		reasons = append(reasons, reasonFollowerSpike)
	}
	if vec.PostCount < minPostsForStableFeatures {
		reasons = append(reasons, reasonInsufficientPost)
	}
	return reasons
}

// hasFollowerSpike reports whether the follower series contains an adjacent pair,
// within the spike window, whose growth exceeds the threshold — the signature of
// bought followers. Points outside the follower/subscriber metric names are
// ignored, and the series is sorted by time before adjacent pairs are compared.
func hasFollowerSpike(metrics []connector.MetricPoint) bool {
	series := make([]connector.MetricPoint, 0, len(metrics))
	for _, m := range metrics {
		if _, ok := followerMetricNames[m.Name]; ok {
			series = append(series, m)
		}
	}
	if len(series) < 2 {
		return false
	}
	sort.Slice(series, func(i, j int) bool { return series[i].At.Before(series[j].At) })

	for i := 1; i < len(series); i++ {
		prev, curr := series[i-1], series[i]
		if curr.At.Sub(prev.At) > followerSpikeWindow {
			continue
		}
		base := prev.Value
		if base < 1 {
			base = 1
		}
		if (curr.Value-prev.Value)/base > maxFollowerGrowthRatio {
			return true
		}
	}
	return false
}
