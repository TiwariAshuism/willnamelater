package ml

import "github.com/getnyx/influaudit/backend/internal/connector"

// Default co-commenter clique parameters, matching the service-side defaults in
// app.schemas.PodsDetectRequest. They are sent explicitly so the request is
// self-describing rather than relying on the server's defaults.
const (
	defaultMinPodSize     = 5
	defaultMinSharedPosts = 2
)

// Metric-point names that represent a follower-count reading. A connector emits
// "followers" (e.g. Instagram) or "subscribers" (YouTube) for the same quantity,
// so both feed the follower series.
const (
	metricFollowers   = "followers"
	metricSubscribers = "subscribers"
	metricFollowing   = "following"
)

// BuildFraudRequest assembles a FraudScoreRequest from a connector.Snapshot: the
// account totals, the follower-count time series drawn from the snapshot's
// metric points, and the per-post engagement counters (each carrying its post
// id as the schema requires).
//
// The snapshot carries no following-count, so AccountSnapshot.FollowingCount is
// taken from a "following" metric point when one is present and defaults to 0
// otherwise — a known gap the connector layer does not yet fill.
//
// EngagementBenchmark is left nil here: benchmarks are owned by the scoring
// module, not the connector snapshot, so the caller sets it when a sourced curve
// is available. Nil marshals to JSON null, which the service reads as "no
// benchmark".
func BuildFraudRequest(snap connector.Snapshot) FraudScoreRequest {
	req := FraudScoreRequest{
		Account: AccountSnapshot{
			Handle:         snap.Handle,
			Platform:       Platform(snap.Platform),
			FollowerCount:  snap.Followers,
			FollowingCount: followingCount(snap.Metrics),
		},
		FollowerSeries: followerSeries(snap.Metrics),
		Posts:          postMetrics(snap.Posts),
	}
	return req
}

// BuildPodsRequest assembles a PodsDetectRequest from a connector.Snapshot's
// comments. Each CommentEvent preserves its source comment's PostID so the
// service can join comments to posts and weight the co-commenter graph by the
// number of shared posts, and carries the comment text through the wire-contract
// text slot. The default clique parameters are sent explicitly.
func BuildPodsRequest(snap connector.Snapshot) PodsDetectRequest {
	events := make([]CommentEvent, 0, len(snap.Comments))
	for _, cm := range snap.Comments {
		events = append(events, CommentEvent{
			PostID:    cm.PostID,
			Commenter: cm.AuthorID,
			Text:      cm.Text,
			Timestamp: cm.At,
		})
	}
	return PodsDetectRequest{
		Events:         events,
		MinPodSize:     defaultMinPodSize,
		MinSharedPosts: defaultMinSharedPosts,
	}
}

// followerSeries projects the follower-count metric points of a snapshot into
// the FollowerPoint series the fraud scorer expects, preserving chronological
// order as the connector emitted it.
func followerSeries(metrics []connector.MetricPoint) []FollowerPoint {
	series := make([]FollowerPoint, 0, len(metrics))
	for _, m := range metrics {
		if m.Name != metricFollowers && m.Name != metricSubscribers {
			continue
		}
		series = append(series, FollowerPoint{
			Timestamp: m.At,
			Count:     int64(m.Value),
		})
	}
	return series
}

// followingCount returns the most recent following-count reading from the
// metric points, or 0 when the connector reported none.
func followingCount(metrics []connector.MetricPoint) int64 {
	var count int64
	for _, m := range metrics {
		if m.Name == metricFollowing {
			count = int64(m.Value)
		}
	}
	return count
}

// postMetrics projects a snapshot's posts into PostMetrics, carrying each post's
// id as the join key. Views is sent as a pointer to the reported counter; the
// connector leaves it zero when a platform does not expose views, which is
// carried through as an explicit 0 rather than a guess.
func postMetrics(posts []connector.Post) []PostMetrics {
	out := make([]PostMetrics, 0, len(posts))
	for _, p := range posts {
		views := p.Views
		out = append(out, PostMetrics{
			PostID:    p.ID,
			Timestamp: p.PublishedAt,
			Likes:     p.Likes,
			Comments:  p.Comments,
			Views:     &views,
		})
	}
	return out
}
