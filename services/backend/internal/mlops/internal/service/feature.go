package service

import (
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
)

// followerMetricNames are the metric-point names that carry a follower/audience
// count. The follower-spike quality rule and the account-age proxy read the
// densest such series a connector returned.
var followerMetricNames = map[string]struct{}{
	"followers":   {},
	"subscribers": {},
}

// primarySnapshot returns the snapshot the feature vector describes: the one with
// the largest follower count, ties broken by the first occurrence. It returns
// false when there are no snapshots (a no-op capture).
func primarySnapshot(snaps []connector.Snapshot) (connector.Snapshot, bool) {
	if len(snaps) == 0 {
		return connector.Snapshot{}, false
	}
	best := snaps[0]
	for _, s := range snaps[1:] {
		if s.Followers > best.Followers {
			best = s
		}
	}
	return best, true
}

// computeFeatureVector builds the frozen feature vector for a capture from its
// primary snapshot and fraud sub-vector, using the deterministic formulas the
// resolved contract fixes. A feature the platform did not report is left nil (a
// JSON null), never zero-filled. capturedAt anchors the account-age proxy.
func computeFeatureVector(capture contract.FeatureCapture, primary connector.Snapshot, capturedAt time.Time) model.FeatureVector {
	postCount := len(primary.Posts)
	followerCount := primary.Followers

	v := model.FeatureVector{
		FakeFollowerRate:         capture.Fraud.FakeFollowerRate,
		BotCommentRate:           capture.Fraud.BotCommentRate,
		EngagementAnomaly:        capture.Fraud.EngagementAnomaly,
		CliqueCount:              capture.Fraud.CliqueCount,
		CliqueMembershipFraction: capture.Fraud.CliqueMembershipFraction,
		Confidence:               capture.Fraud.Confidence,

		FollowerCount: followerCount,
		PostCount:     postCount,
		Niche:         capture.Niche,
		Tier:          capture.Tier,
		Platform:      string(primary.Platform),
		// Verified is carried straight through: nil when the platform did not
		// expose a verified flag (never invented as false).
		Verified: primary.Verified,
	}

	// FollowingCount + FollowerFollowingRatio are set only when the platform
	// reported a real positive following count (Instagram's follows_count). A
	// missing count (YouTube, an unset export) leaves both nil — never zero-filled,
	// and no divide-by-zero ratio.
	if primary.Following > 0 {
		following := primary.Following
		v.FollowingCount = &following
		ratio := float64(followerCount) / float64(following)
		v.FollowerFollowingRatio = &ratio
	}

	if er := meanEngagementRate(primary.Posts, followerCount); er != nil {
		v.EngagementRate = er
	}
	if variance := engagementRateVariance(primary.Posts, followerCount); variance != nil {
		v.EngagementRateVariance = variance
	}
	if clr := commentLikeRatio(primary.Posts); clr != nil {
		v.CommentLikeRatio = clr
	}
	if cadence := postingCadencePerWeek(primary.Posts); cadence != nil {
		v.PostingCadencePerWeek = cadence
	}
	if age := accountAgeDaysProxy(primary, capturedAt); age != nil {
		v.AccountAgeDaysProxy = age
	}
	return v
}

// perPostEngagement returns (likes+comments)/max(follower_count,1) for one post.
func perPostEngagement(p connector.Post, followerCount int64) float64 {
	denom := followerCount
	if denom < 1 {
		denom = 1
	}
	return float64(p.Likes+p.Comments) / float64(denom)
}

// meanEngagementRate is the mean per-post engagement rate, or nil when there are
// no posts.
func meanEngagementRate(posts []connector.Post, followerCount int64) *float64 {
	if len(posts) == 0 {
		return nil
	}
	var sum float64
	for _, p := range posts {
		sum += perPostEngagement(p, followerCount)
	}
	mean := sum / float64(len(posts))
	return &mean
}

// engagementRateVariance is the population variance of the per-post engagement
// rate, or nil when there are fewer than two posts.
func engagementRateVariance(posts []connector.Post, followerCount int64) *float64 {
	if len(posts) < 2 {
		return nil
	}
	rates := make([]float64, len(posts))
	var sum float64
	for i, p := range posts {
		rates[i] = perPostEngagement(p, followerCount)
		sum += rates[i]
	}
	mean := sum / float64(len(rates))
	var ss float64
	for _, r := range rates {
		d := r - mean
		ss += d * d
	}
	variance := ss / float64(len(rates))
	return &variance
}

// commentLikeRatio is sum(comments)/(sum(likes)+1), or nil when there are no
// posts.
func commentLikeRatio(posts []connector.Post) *float64 {
	if len(posts) == 0 {
		return nil
	}
	var likes, comments int64
	for _, p := range posts {
		likes += p.Likes
		comments += p.Comments
	}
	ratio := float64(comments) / float64(likes+1)
	return &ratio
}

// postingCadencePerWeek is post_count / max(weeks between earliest and latest
// post, 1), or nil when there are fewer than two posts.
func postingCadencePerWeek(posts []connector.Post) *float64 {
	if len(posts) < 2 {
		return nil
	}
	earliest, latest := posts[0].PublishedAt, posts[0].PublishedAt
	for _, p := range posts[1:] {
		if p.PublishedAt.Before(earliest) {
			earliest = p.PublishedAt
		}
		if p.PublishedAt.After(latest) {
			latest = p.PublishedAt
		}
	}
	weeks := latest.Sub(earliest).Hours() / (24 * 7)
	if weeks < 1 {
		weeks = 1
	}
	cadence := float64(len(posts)) / weeks
	return &cadence
}

// accountAgeDaysProxy is days from the earliest observed metric/post timestamp to
// capturedAt, or nil when the snapshot carries no dated observation. It is a
// PROXY — platform APIs do not expose account creation date — and is documented
// as an estimate, never presented as truth.
func accountAgeDaysProxy(snap connector.Snapshot, capturedAt time.Time) *float64 {
	var earliest time.Time
	consider := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	for _, m := range snap.Metrics {
		consider(m.At)
	}
	for _, p := range snap.Posts {
		consider(p.PublishedAt)
	}
	if earliest.IsZero() {
		return nil
	}
	days := capturedAt.Sub(earliest).Hours() / 24
	if days < 0 {
		days = 0
	}
	return &days
}
