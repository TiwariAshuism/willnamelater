package service

import (
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
)

// contains reports whether reasons includes code.
func contains(reasons []string, code string) bool {
	for _, r := range reasons {
		if r == code {
			return true
		}
	}
	return false
}

// cleanVector is a feature vector that passes the vector-derived quality rules:
// old enough account and enough posts. The values are synthetic, chosen to sit
// clearly on the accepting side of each threshold — no real business data.
func cleanVector() model.FeatureVector {
	age := 400.0
	return model.FeatureVector{PostCount: 12, AccountAgeDaysProxy: &age}
}

// risk is a measured fraud-risk estimate on the honest 0-100 scale. It is a
// pointer because nil is a distinct state — "the signal was never observed" —
// and the filter must treat the two differently.
func risk(v float64) *float64 { return &v }

// evalQuality runs the filter over a capture carrying just the fraud sub-vector,
// which is what most of these cases vary.
func evalQuality(fraud contract.FraudSignal, vec model.FeatureVector, snap connector.Snapshot) []string {
	return evaluateQuality(contract.FeatureCapture{Fraud: fraud}, vec, snap)
}

func TestQualityAcceptsCleanRow(t *testing.T) {
	// EXPECTATION CHANGED: the signal is RiskScore (0-100 composite), not
	// FakeFollowerRate (a "rate" nothing ever measured — it was this same score
	// renamed). 4.0/100 = 0.04, the old fraction, so the verdict is unchanged.
	fraud := contract.FraudSignal{Present: true, RiskScore: risk(4)}
	reasons := evalQuality(fraud, cleanVector(), connector.Snapshot{})
	if len(reasons) != 0 {
		t.Fatalf("a clean row must have no reasons, got %v", reasons)
	}
}

// EXPECTATION CHANGED: renamed from TestQualityRejectsHighFakeFollowerRate. The
// rule is unchanged in substance — only in candour: it reads the composite risk
// estimate it always read, now under its true name. 30.0/100 = 0.30 = maxFraudRisk.
func TestQualityRejectsHighFraudRisk(t *testing.T) {
	fraud := contract.FraudSignal{Present: true, RiskScore: risk(30)}
	reasons := evalQuality(fraud, cleanVector(), connector.Snapshot{})
	if !contains(reasons, reasonFraudRiskHigh) {
		t.Fatalf("want %q for a 30/100 risk estimate, got %v", reasonFraudRiskHigh, reasons)
	}
}

// Absence is not evidence. A nil risk score means the ml service never produced
// one, which is not a reason to brand the account suspicious — the row is simply
// unfiltered by this rule. Under the old plain-float64 field an unobserved signal
// arrived as 0.0 and silently passed as "clean"; now it is nil and the rule does
// not fire in either direction.
func TestQualityDoesNotFlagAnUnobservedRiskScore(t *testing.T) {
	fraud := contract.FraudSignal{Present: true, RiskScore: nil}
	reasons := evalQuality(fraud, cleanVector(), connector.Snapshot{})
	if contains(reasons, reasonFraudRiskHigh) {
		t.Fatalf("a nil risk score must not be flagged %q: absence is not evidence, got %v", reasonFraudRiskHigh, reasons)
	}
}

// A risk estimate just under the threshold is accepted: the boundary is >=.
func TestQualityAcceptsRiskBelowThreshold(t *testing.T) {
	fraud := contract.FraudSignal{Present: true, RiskScore: risk(29.9)}
	reasons := evalQuality(fraud, cleanVector(), connector.Snapshot{})
	if contains(reasons, reasonFraudRiskHigh) {
		t.Fatalf("29.9/100 sits below maxFraudRisk (%v), got %v", maxFraudRisk, reasons)
	}
}

func TestQualityRejectsMissingFraudEstimate(t *testing.T) {
	fraud := contract.FraudSignal{Present: false}
	reasons := evalQuality(fraud, cleanVector(), connector.Snapshot{})
	if !contains(reasons, reasonNoFraudEstimate) {
		t.Fatalf("want %q when no fraud estimate is present, got %v", reasonNoFraudEstimate, reasons)
	}
}

func TestQualityRejectsNewAccount(t *testing.T) {
	age := 30.0
	vec := model.FeatureVector{PostCount: 12, AccountAgeDaysProxy: &age}
	reasons := evalQuality(contract.FraudSignal{Present: true}, vec, connector.Snapshot{})
	if !contains(reasons, reasonAccountTooNew) {
		t.Fatalf("want %q for a 30-day-old account, got %v", reasonAccountTooNew, reasons)
	}
}

// A null account-age proxy must NOT trip the too-new rule: absence is not youth.
func TestQualityAllowsNullAccountAge(t *testing.T) {
	vec := model.FeatureVector{PostCount: 12, AccountAgeDaysProxy: nil}
	reasons := evalQuality(contract.FraudSignal{Present: true}, vec, connector.Snapshot{})
	if contains(reasons, reasonAccountTooNew) {
		t.Fatalf("a null account age must not trip too-new, got %v", reasons)
	}
}

func TestQualityRejectsInsufficientPosts(t *testing.T) {
	age := 400.0
	vec := model.FeatureVector{PostCount: 4, AccountAgeDaysProxy: &age}
	reasons := evalQuality(contract.FraudSignal{Present: true}, vec, connector.Snapshot{})
	if !contains(reasons, reasonInsufficientPost) {
		t.Fatalf("want %q for 4 posts, got %v", reasonInsufficientPost, reasons)
	}
}

func TestQualityRejectsFollowerSpike(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := connector.Snapshot{Metrics: []connector.MetricPoint{
		{Name: "followers", At: base, Value: 10000},
		{Name: "followers", At: base.Add(12 * time.Hour), Value: 16000}, // +60% in 12h
	}}
	reasons := evalQuality(contract.FraudSignal{Present: true}, cleanVector(), snap)
	if !contains(reasons, reasonFollowerSpike) {
		t.Fatalf("want %q for a 60%% jump in 12h, got %v", reasonFollowerSpike, reasons)
	}
}

// Steady growth spread over more than the spike window must not be flagged.
func TestQualityAllowsOrganicGrowth(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := connector.Snapshot{Metrics: []connector.MetricPoint{
		{Name: "followers", At: base, Value: 10000},
		{Name: "followers", At: base.Add(72 * time.Hour), Value: 16000}, // big, but over 72h
	}}
	reasons := evalQuality(contract.FraudSignal{Present: true}, cleanVector(), snap)
	if contains(reasons, reasonFollowerSpike) {
		t.Fatalf("growth outside the 24h window must not be a spike, got %v", reasons)
	}
}

// A promotion-heavy account's engagement counters — and any Insights reach — count
// audience the account PAID for. Training on them teaches the model that ad spend
// is organic virality, so the row is excluded.
func TestQualityRejectsPromotionHeavyMedia(t *testing.T) {
	heavy := 0.40
	capture := contract.FeatureCapture{
		Fraud:                 contract.FraudSignal{Present: true},
		PromotedMediaFraction: &heavy,
	}
	reasons := evaluateQuality(capture, cleanVector(), connector.Snapshot{})
	if !contains(reasons, reasonPromotionHeavy) {
		t.Fatalf("want %q when 40%% of media was boosted, got %v", reasonPromotionHeavy, reasons)
	}
}

// A nil fraction is "the connector could not observe the boost split", which is not
// "nothing was boosted": it fires no reason here (the reach LABEL is withheld
// instead), and it is never treated as a measured zero.
func TestQualityDoesNotFlagAnUnobservedPromotionFraction(t *testing.T) {
	reasons := evalQuality(contract.FraudSignal{Present: true}, cleanVector(), connector.Snapshot{})
	if contains(reasons, reasonPromotionHeavy) {
		t.Fatalf("an unobserved promotion fraction must not fire %q, got %v", reasonPromotionHeavy, reasons)
	}
}
