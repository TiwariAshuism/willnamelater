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

func TestQualityAcceptsCleanRow(t *testing.T) {
	fraud := contract.FraudSignal{Present: true, FakeFollowerRate: 0.04}
	reasons := evaluateQuality(fraud, cleanVector(), connector.Snapshot{})
	if len(reasons) != 0 {
		t.Fatalf("a clean row must have no reasons, got %v", reasons)
	}
}

func TestQualityRejectsHighFakeFollowerRate(t *testing.T) {
	fraud := contract.FraudSignal{Present: true, FakeFollowerRate: 0.30}
	reasons := evaluateQuality(fraud, cleanVector(), connector.Snapshot{})
	if !contains(reasons, reasonFakeFollowerHigh) {
		t.Fatalf("want %q for a 0.30 fake-follower estimate, got %v", reasonFakeFollowerHigh, reasons)
	}
}

func TestQualityRejectsMissingFraudEstimate(t *testing.T) {
	fraud := contract.FraudSignal{Present: false}
	reasons := evaluateQuality(fraud, cleanVector(), connector.Snapshot{})
	if !contains(reasons, reasonNoFraudEstimate) {
		t.Fatalf("want %q when no fraud estimate is present, got %v", reasonNoFraudEstimate, reasons)
	}
}

func TestQualityRejectsNewAccount(t *testing.T) {
	age := 30.0
	vec := model.FeatureVector{PostCount: 12, AccountAgeDaysProxy: &age}
	reasons := evaluateQuality(contract.FraudSignal{Present: true}, vec, connector.Snapshot{})
	if !contains(reasons, reasonAccountTooNew) {
		t.Fatalf("want %q for a 30-day-old account, got %v", reasonAccountTooNew, reasons)
	}
}

// A null account-age proxy must NOT trip the too-new rule: absence is not youth.
func TestQualityAllowsNullAccountAge(t *testing.T) {
	vec := model.FeatureVector{PostCount: 12, AccountAgeDaysProxy: nil}
	reasons := evaluateQuality(contract.FraudSignal{Present: true}, vec, connector.Snapshot{})
	if contains(reasons, reasonAccountTooNew) {
		t.Fatalf("a null account age must not trip too-new, got %v", reasons)
	}
}

func TestQualityRejectsInsufficientPosts(t *testing.T) {
	age := 400.0
	vec := model.FeatureVector{PostCount: 4, AccountAgeDaysProxy: &age}
	reasons := evaluateQuality(contract.FraudSignal{Present: true}, vec, connector.Snapshot{})
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
	reasons := evaluateQuality(contract.FraudSignal{Present: true}, cleanVector(), snap)
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
	reasons := evaluateQuality(contract.FraudSignal{Present: true}, cleanVector(), snap)
	if contains(reasons, reasonFollowerSpike) {
		t.Fatalf("growth outside the 24h window must not be a spike, got %v", reasons)
	}
}
