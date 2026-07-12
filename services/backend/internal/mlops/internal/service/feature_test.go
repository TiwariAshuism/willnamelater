package service

import (
	"math"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
)

const eps = 1e-9

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
	capture := contract.FeatureCapture{
		Fraud: contract.FraudSignal{FakeFollowerRate: 0.1, Confidence: 0.7, CliqueCount: 2},
		Niche: "tech", Tier: "micro",
	}
	vec := computeFeatureVector(capture, primary, capturedAt)

	if vec.FollowerCount != 1000 || vec.PostCount != 2 || vec.Platform != "instagram" {
		t.Fatalf("scalar features wrong: %+v", vec)
	}
	// Fraud sub-vector copied verbatim.
	if vec.FakeFollowerRate != 0.1 || vec.Confidence != 0.7 || vec.CliqueCount != 2 {
		t.Fatalf("fraud sub-vector not copied: %+v", vec)
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
