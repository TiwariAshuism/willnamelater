package service

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
)

const eps = 1e-9

// iptr is an observed clique count. (risk(), its float64 twin, lives in
// quality_test.go.) Both exist because an observation must now be spelled out:
// the zero value of the field is nil — "never looked" — not 0.
func iptr(v int) *int { return &v }

func TestPrimarySnapshotPicksMaxFollowers(t *testing.T) {
	snaps := []connector.Snapshot{
		{Platform: connector.PlatformYouTube, Followers: 500},
		{Platform: connector.PlatformInstagram, Followers: 9000},
		{Platform: connector.PlatformX, Followers: 1200},
	}
	primary, ok := primarySnapshot(snaps)
	if !ok || primary.Platform != connector.PlatformInstagram {
		t.Fatalf("expected the instagram snapshot (max followers), got %+v", primary)
	}
}

func TestComputeFeatureVectorFormulas(t *testing.T) {
	capturedAt := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	posts := []connector.Post{
		{Likes: 90, Comments: 10, PublishedAt: capturedAt.AddDate(0, 0, -14)},
		{Likes: 100, Comments: 0, PublishedAt: capturedAt.AddDate(0, 0, -7)},
	}
	primary := connector.Snapshot{
		Platform:  connector.PlatformInstagram,
		Followers: 1000,
		Posts:     posts,
		Metrics:   []connector.MetricPoint{{Name: "followers", At: capturedAt.AddDate(0, 0, -100), Value: 900}},
	}
	// EXPECTATION CHANGED: the fraud sub-vector no longer has fake_follower_rate
	// (the risk score renamed) or bot_comment_rate (a duplicate of
	// clique_membership_fraction). It carries RiskScore, and every measurement is a
	// pointer so an unobserved one can be told apart from a measured zero.
	capture := contract.FeatureCapture{
		Fraud: contract.FraudSignal{Present: true, RiskScore: risk(10), Confidence: 0.7, CliqueCount: iptr(2)},
		Niche: "tech", Tier: "micro",
	}
	vec := computeFeatureVector(capture, primary, capturedAt)

	if vec.FollowerCount != 1000 || vec.PostCount != 2 || vec.Platform != "instagram" {
		t.Fatalf("scalar features wrong: %+v", vec)
	}
	// Fraud sub-vector copied verbatim — pointers and all.
	if vec.RiskScore == nil || *vec.RiskScore != 10 || vec.Confidence != 0.7 {
		t.Fatalf("fraud sub-vector not copied: %+v", vec)
	}
	if vec.CliqueCount == nil || *vec.CliqueCount != 2 {
		t.Fatalf("clique_count = %v, want 2 carried through", vec.CliqueCount)
	}
	// engagement_rate = mean of (100/1000, 100/1000) = 0.1
	if vec.EngagementRate == nil || math.Abs(*vec.EngagementRate-0.1) > eps {
		t.Fatalf("engagement_rate = %v, want 0.1", vec.EngagementRate)
	}
	// comment_like_ratio = sum(comments)/(sum(likes)+1) = 10/191
	if vec.CommentLikeRatio == nil || math.Abs(*vec.CommentLikeRatio-10.0/191.0) > eps {
		t.Fatalf("comment_like_ratio = %v, want 10/191", vec.CommentLikeRatio)
	}
	// posting_cadence: 2 posts over 1 week => 2.0
	if vec.PostingCadencePerWeek == nil || math.Abs(*vec.PostingCadencePerWeek-2.0) > eps {
		t.Fatalf("posting_cadence = %v, want 2.0", vec.PostingCadencePerWeek)
	}
	// account_age_days_proxy: earliest observation is the metric at -100 days.
	if vec.AccountAgeDaysProxy == nil || math.Abs(*vec.AccountAgeDaysProxy-100.0) > eps {
		t.Fatalf("account_age_days_proxy = %v, want 100", vec.AccountAgeDaysProxy)
	}
	// Foundation gaps stay null.
	if vec.FollowingCount != nil || vec.Verified != nil || vec.FollowerFollowingRatio != nil {
		t.Fatalf("following/verified/ratio must be null: %+v", vec)
	}
}

// Fewer than two posts leaves variance and cadence null (not zero), and a single
// post still yields an engagement_rate.
func TestComputeFeatureVectorSparsePosts(t *testing.T) {
	capturedAt := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	primary := connector.Snapshot{
		Platform:  connector.PlatformInstagram,
		Followers: 500,
		Posts:     []connector.Post{{Likes: 50, Comments: 5, PublishedAt: capturedAt.AddDate(0, 0, -1)}},
	}
	vec := computeFeatureVector(contract.FeatureCapture{}, primary, capturedAt)
	if vec.EngagementRate == nil {
		t.Fatal("one post must still yield an engagement_rate")
	}
	if vec.EngagementRateVariance != nil {
		t.Fatalf("variance must be null with <2 posts, got %v", *vec.EngagementRateVariance)
	}
	if vec.PostingCadencePerWeek != nil {
		t.Fatalf("cadence must be null with <2 posts, got %v", *vec.PostingCadencePerWeek)
	}
}

// THE anti-poisoning guarantee. The vector is FROZEN into the training set: what
// it says is what the fraud model learns. An audit that analyzed no commenters —
// every Instagram and CSV audit, since neither pulls comment events — must freeze
// clique_count as JSON null, which LightGBM reads as native-missing.
//
// Under the old plain-float64 vector it froze as 0, so "we never looked" entered
// the training data as a confident measurement at a specific point in feature
// space, indistinguishable from an account we HAD examined and found clean. The
// model then learned that the absence of evidence is evidence of absence, on a
// dataset dominated by unobserved rows.
func TestFeatureVectorFreezesUnobservedSignalsAsJSONNull(t *testing.T) {
	capturedAt := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	primary := connector.Snapshot{Platform: connector.PlatformInstagram, Followers: 1000}
	// A fraud pass that produced a risk score but could not analyze a single
	// commenter, and had no benchmark for engagement.
	capture := contract.FeatureCapture{
		Fraud: contract.FraudSignal{Present: true, RiskScore: risk(41), Confidence: 0.25},
	}

	vec := computeFeatureVector(capture, primary, capturedAt)
	raw, err := vec.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(raw)

	for _, key := range []string{"clique_count", "clique_membership_fraction", "engagement_anomaly"} {
		if !strings.Contains(got, `"`+key+`":null`) {
			t.Errorf("frozen vector must carry %q as JSON null (unobserved), got %s", key, got)
		}
		// The precise failure being guarded: a zero-filled unobserved signal.
		if strings.Contains(got, `"`+key+`":0`) {
			t.Errorf("%q froze as 0 — an unobserved signal was recorded as a real measurement: %s", key, got)
		}
	}
	// What WAS observed is present as a number, not null.
	if !strings.Contains(got, `"risk_score":41`) {
		t.Errorf("an observed risk score must freeze as its value, got %s", got)
	}
	// The removed keys must not reappear anywhere in the frozen vector: neither was
	// ever measured (fake_follower_rate was the risk score renamed; bot_comment_rate
	// duplicated clique_membership_fraction, handing the model a collinear pair
	// dressed as independent evidence).
	for _, gone := range []string{"fake_follower_rate", "bot_comment_rate"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q is still in the frozen vector: %s", gone, got)
		}
	}
}

// A measured zero is not absence: it must freeze as 0, not null. The guarantee
// runs both ways or it is worthless.
func TestFeatureVectorFreezesObservedZeroAsZero(t *testing.T) {
	capture := contract.FeatureCapture{
		Fraud: contract.FraudSignal{Present: true, CliqueCount: iptr(0)},
	}
	vec := computeFeatureVector(capture, connector.Snapshot{Platform: connector.PlatformYouTube}, time.Now())

	raw, err := vec.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"clique_count":0`) {
		t.Errorf("a measured 0 clique count must freeze as 0, not null: %s", raw)
	}
}

// No posts and no metrics: every post-derived feature and the age proxy are null.
func TestComputeFeatureVectorNoData(t *testing.T) {
	vec := computeFeatureVector(contract.FeatureCapture{}, connector.Snapshot{Platform: connector.PlatformInstagram, Followers: 10}, time.Now())
	if vec.EngagementRate != nil || vec.CommentLikeRatio != nil || vec.AccountAgeDaysProxy != nil {
		t.Fatalf("expected null post-derived features with no posts: %+v", vec)
	}
	if vec.PostCount != 0 || vec.FollowerCount != 10 {
		t.Fatalf("scalars wrong: %+v", vec)
	}
}
