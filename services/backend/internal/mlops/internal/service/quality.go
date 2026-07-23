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
	// maxFraudRisk rejects a row whose current-champion fraud-risk
	// estimate is this high: a gamed account must not teach the model that fraud
	// looks normal (feedback-poisoning guard).
	maxFraudRisk = 0.30
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
	// maxPromotedMediaFraction rejects a promotion-heavy account: when this much of
	// the sampled media was boosted, the engagement counters (and any Insights reach)
	// include audience the account PAID for, and a model trained on them learns that
	// ad spend is organic virality.
	maxPromotedMediaFraction = 0.30
)

// Quality reason codes. Each failing rule appends its code; a row is clean
// (quality_ok) only when no code fired. Rejected rows are still stored with their
// reasons for admin review and excluded from the training export by default.
//
// One of them is not like the others. reasonFraudRiskHigh is derived from OUR OWN
// heuristic's output, not from an observation, so it may not overrule a human
// label — see model.TrainingEligible, which is what the export actually filters on.
const (
	reasonFraudRiskHigh    = model.ReasonFraudRiskHigh
	reasonAccountTooNew    = "account_too_new"
	reasonFollowerSpike    = "follower_spike"
	reasonInsufficientPost = "insufficient_posts"
	reasonNoFraudEstimate  = "no_fraud_estimate"
	reasonPromotionHeavy   = "promotion_heavy_media"
)

// evaluateQuality computes the data-quality verdict for a capture from the
// current champion's fraud read, the computed vector, and the primary snapshot's
// follower series. It returns the ordered reason codes; quality_ok is the empty
// check (len == 0), which the caller records. The codes are appended in a fixed
// order so the stored reasons are deterministic.
func evaluateQuality(capture contract.FeatureCapture, vec model.FeatureVector, primary connector.Snapshot) []string {
	fraud := capture.Fraud
	reasons := make([]string, 0, 5)

	// Without the current model's read we cannot quality-check the row at all, so
	// this is evaluated first and the other fraud-derived checks still run on
	// whatever values are present.
	if !fraud.Present {
		reasons = append(reasons, reasonNoFraudEstimate)
	}
	// The anti-gaming filter reads the honest composite risk score (0-100). A nil
	// score means the signal was never observed, and an unobserved account is not
	// filtered as suspicious: absence is not evidence.
	//
	// This reason is recorded, but it never censors a row a HUMAN later labelled:
	// the disputed accounts are precisely the high-risk ones, and letting our own
	// estimate exclude them starves the positive class forever (model.TrainingEligible).
	if fraud.RiskScore != nil && *fraud.RiskScore/100 >= maxFraudRisk {
		reasons = append(reasons, reasonFraudRiskHigh)
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
	// A nil fraction is "the connector could not observe the boost split", which is
	// not the same as "nothing was boosted" — it fires no reason, and the reach label
	// is withheld instead (RecordFeatureRow).
	if capture.PromotedMediaFraction != nil && *capture.PromotedMediaFraction >= maxPromotedMediaFraction {
		reasons = append(reasons, reasonPromotionHeavy)
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
