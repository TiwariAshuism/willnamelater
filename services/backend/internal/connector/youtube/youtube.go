// Package youtube implements connector.Connector for the YouTube Data API v3.
//
// It is the only platform integration that returns real data without a platform
// app review, so it carries the product demo. Every design choice here bends to
// one constraint: the YouTube Data API grants a 10,000-unit DAILY quota, and
// each call spends units:
//
//	channels.list        1 unit  -> subscriber/view counts + uploads playlist id
//	playlistItems.list   1 unit  -> recent video ids (per page)
//	videos.list          1 unit  -> per-video statistics, up to 50 ids per call
//	commentThreads.list  1 unit  -> comment author, text, timestamp (per page)
//	search.list        100 units -> NEVER USED (see getChannel)
//
// CostOf computes the worst-case unit cost of a Fetch up front so the
// orchestrator can pre-debit the quota ledger before spending a single unit.
//
// The connector fetches only public data with an API key, so it needs no OAuth
// token. Audience demographics, which require the separate YouTube Analytics API
// under an OAuth owner scope, are therefore advertised but degrade to a partial
// result rather than being fabricated.
package youtube

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Per-call quota costs, in YouTube Data API units. Every read this connector
// issues costs one unit; search.list (100 units) is intentionally never called.
const (
	channelsListCost       = 1
	playlistItemsListCost  = 1
	videosListCost         = 1
	commentThreadsListCost = 1
)

// API paging sizes. These are the documented maximums for each endpoint, chosen
// to spend the fewest units per item fetched.
const (
	playlistPageSize = 50  // playlistItems.list maxResults ceiling
	videosBatchSize  = 50  // videos.list accepts up to 50 ids per call
	commentsPageSize = 100 // commentThreads.list maxResults ceiling
)

// Default request bounds, applied when a Config leaves a field zero.
const (
	defaultMaxPosts                = 50
	defaultMaxCommentPagesPerVideo = 2
	defaultMaxComments             = 500
)

// Metric names emitted from a channel's statistics.
const (
	metricSubscribers = "subscribers"
	metricViews       = "views"
	metricVideos      = "videos"
)

// Doer is the minimal HTTP contract the connector depends on: the standard
// *http.Client satisfies it, and tests supply a fake, so no test touches the
// network.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config constructs a Connector. Required fields are BaseURL, APIKey and HTTP;
// the bound fields default when left zero.
type Config struct {
	// BaseURL is the YouTube Data API v3 root, e.g.
	// "https://www.googleapis.com/youtube/v3", with no trailing slash. Required.
	BaseURL string
	// APIKey is a Google API key with the YouTube Data API v3 enabled. Public
	// reads need no OAuth, which is what makes this the review-free demo path.
	// Required. It is a secret: it is placed only on outbound requests and never
	// copied into an error or log line.
	APIKey string
	// HTTP is the injected HTTP client. Required; tests pass a fake Doer.
	HTTP Doer
	// DefaultMaxPosts caps recent videos when FetchRequest.MaxPosts is zero.
	// Defaults to defaultMaxPosts.
	DefaultMaxPosts int
	// MaxCommentPagesPerVideo hard-caps commentThreads pages per video so one
	// heavily-commented video cannot drain the daily quota. Defaults to
	// defaultMaxCommentPagesPerVideo.
	MaxCommentPagesPerVideo int
	// MaxComments hard-caps total comments sampled across all videos in one
	// Fetch. Defaults to defaultMaxComments.
	MaxComments int
	// Now is an injectable clock for deterministic tests. Defaults to time.Now.
	Now func() time.Time
}

// Connector integrates the YouTube Data API v3. It is safe for concurrent use:
// it holds only immutable configuration and issues no shared mutable state, so
// the orchestrator may call Fetch from many goroutines.
type Connector struct {
	baseURL                 string
	apiKey                  string
	http                    Doer
	defaultMaxPosts         int
	maxCommentPagesPerVideo int
	maxComments             int
	now                     func() time.Time
}

var _ connector.Connector = (*Connector)(nil)

// New validates cfg and builds a Connector, applying defaults for the bound
// fields. Missing required fields are a KindInvalid configuration error.
func New(cfg Config) (*Connector, error) {
	if cfg.BaseURL == "" {
		return nil, errs.New(errs.KindInvalid, "youtube.config",
			"youtube connector requires a base url")
	}
	if cfg.APIKey == "" {
		return nil, errs.New(errs.KindInvalid, "youtube.config",
			"youtube connector requires an api key")
	}
	if cfg.HTTP == nil {
		return nil, errs.New(errs.KindInvalid, "youtube.config",
			"youtube connector requires an http client")
	}

	c := &Connector{
		baseURL:                 strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:                  cfg.APIKey,
		http:                    cfg.HTTP,
		defaultMaxPosts:         cfg.DefaultMaxPosts,
		maxCommentPagesPerVideo: cfg.MaxCommentPagesPerVideo,
		maxComments:             cfg.MaxComments,
		now:                     cfg.Now,
	}
	if c.defaultMaxPosts <= 0 {
		c.defaultMaxPosts = defaultMaxPosts
	}
	if c.maxCommentPagesPerVideo <= 0 {
		c.maxCommentPagesPerVideo = defaultMaxCommentPagesPerVideo
	}
	if c.maxComments <= 0 {
		c.maxComments = defaultMaxComments
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

// Platform returns connector.PlatformYouTube.
func (c *Connector) Platform() connector.Platform { return connector.PlatformYouTube }

// Capabilities returns every capability this connector advertises. A fresh slice
// is returned each call so a caller cannot mutate shared state.
func (c *Connector) Capabilities() []connector.Capability {
	return []connector.Capability{
		connector.CapabilityProfile,
		connector.CapabilityMetrics,
		connector.CapabilityRecentPosts,
		connector.CapabilityComments,
		// Audience demographics are NOT advertised: they require the YouTube
		// Analytics API under an OAuth owner scope, which this key-based public
		// fetch cannot use. A structural, permanent gap is not something we can
		// serve, so it is not claimed — and a request for it is silently left nil
		// rather than marking every YouTube audit perpetually "partial".
	}
}

// CostOf returns the maximum number of quota units a Fetch of req may spend. It
// is an upper bound (a channel with fewer videos, or comment pages that end
// early, spend less), which is exactly what the orchestrator needs to pre-debit
// the ledger safely before the fetch begins.
func (c *Connector) CostOf(req connector.FetchRequest) int {
	cost := channelsListCost // channels.list is always issued

	nPosts := c.effectiveMaxPosts(req)
	needVideoIDs := req.Wants(connector.CapabilityRecentPosts) || req.Wants(connector.CapabilityComments)
	if needVideoIDs && nPosts > 0 {
		cost += ceilDiv(nPosts, playlistPageSize) * playlistItemsListCost
	}
	if req.Wants(connector.CapabilityRecentPosts) && nPosts > 0 {
		cost += ceilDiv(nPosts, videosBatchSize) * videosListCost
	}
	if req.Wants(connector.CapabilityComments) && nPosts > 0 {
		cost += nPosts * c.maxCommentPagesPerVideo * commentThreadsListCost
	}
	return cost
}

// Fetch retrieves a Snapshot for the requested channel. See the Connector
// interface for the full contract. The mandatory channels.list call fails the
// fetch if it errors; any quota or rate-limit error in the later post or comment
// phases degrades to a partial Snapshot instead of failing the whole audit.
func (c *Connector) Fetch(ctx context.Context, req connector.FetchRequest) (connector.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return connector.Snapshot{}, err
	}

	snap := connector.Snapshot{
		Platform:   connector.PlatformYouTube,
		Source:     connector.SourceYouTubeAPI,
		Handle:     req.Handle,
		CapturedAt: c.now(),
	}

	// Phase 1 (mandatory): resolve the channel. This yields profile identity,
	// metrics, and the uploads playlist every later phase needs. A quota error
	// here means no usable snapshot, so it propagates.
	channel, err := c.getChannel(ctx, req)
	if err != nil {
		return connector.Snapshot{}, err
	}
	snap.AccountID = channel.ID
	if !channel.Statistics.HiddenSubscriberCount {
		snap.Followers = parseCount(channel.Statistics.SubscriberCount)
	}
	if req.Wants(connector.CapabilityMetrics) {
		snap.Metrics = c.metricsFrom(channel, snap.CapturedAt)
	}

	// Phase 2: recent posts and/or the videos comments are attached to. Both
	// capabilities need the uploads video ids.
	needPosts := req.Wants(connector.CapabilityRecentPosts) || req.Wants(connector.CapabilityComments)
	var posts []connector.Post
	if needPosts {
		posts, err = c.listUploads(ctx, channel.ContentDetails.RelatedPlaylists.Uploads, c.effectiveMaxPosts(req))
		if err != nil {
			if degradable(err) {
				snap.Partial = true
				return snap, nil
			}
			return connector.Snapshot{}, err
		}
	}
	if req.Wants(connector.CapabilityRecentPosts) && len(posts) > 0 {
		if err := c.enrich(ctx, posts); err != nil {
			if degradable(err) {
				snap.Posts = posts
				snap.Partial = true
				return snap, nil
			}
			return connector.Snapshot{}, err
		}
	}
	// Posts are attached whenever they were fetched, so every Comment.PostID
	// below references a Post present in this same Snapshot.
	if needPosts {
		snap.Posts = posts
	}

	// Phase 3: comments. A quota/rate-limit failure here is the canonical
	// partial case: keep the profile+metrics+posts already gathered.
	if req.Wants(connector.CapabilityComments) && len(posts) > 0 {
		comments, cerr := c.listComments(ctx, posts)
		snap.Comments = comments
		if cerr != nil {
			if degradable(cerr) {
				snap.Partial = true
				return snap, nil
			}
			return connector.Snapshot{}, cerr
		}
	}

	// Audience demographics are not served by this key-based public fetch (see
	// Capabilities). A request for them leaves Snapshot.Audience nil — never a
	// fabricated distribution — and does NOT mark the snapshot partial: the gap is
	// structural and permanent, not a transient shortfall, and the score's Audience
	// Quality factor drops honestly when Audience is nil.

	return snap, nil
}

// metricsFrom projects a channel's current statistics into MetricPoints stamped
// at capture time. The public API exposes no history, so this is the densest
// series available: one point per counter. A hidden subscriber count is omitted
// rather than reported as zero.
func (c *Connector) metricsFrom(ch channelResource, at time.Time) []connector.MetricPoint {
	points := make([]connector.MetricPoint, 0, 3)
	if !ch.Statistics.HiddenSubscriberCount {
		points = append(points, connector.MetricPoint{
			At: at, Name: metricSubscribers, Value: float64(parseCount(ch.Statistics.SubscriberCount)),
		})
	}
	points = append(points,
		connector.MetricPoint{At: at, Name: metricViews, Value: float64(parseCount(ch.Statistics.ViewCount))},
		connector.MetricPoint{At: at, Name: metricVideos, Value: float64(parseCount(ch.Statistics.VideoCount))},
	)
	return points
}

// effectiveMaxPosts resolves how many recent videos to consider: the request's
// value when set, otherwise the connector default.
func (c *Connector) effectiveMaxPosts(req connector.FetchRequest) int {
	if req.MaxPosts > 0 {
		return req.MaxPosts
	}
	return c.defaultMaxPosts
}

// degradable reports whether err is a quota or rate-limit failure, the two
// conditions under which a post/comment phase yields a partial Snapshot instead
// of failing the audit.
func degradable(err error) bool {
	var quota *connector.QuotaExhaustedError
	var rate *connector.RateLimitError
	return errors.As(err, &quota) || errors.As(err, &rate)
}

// ceilDiv returns ceil(a/b) for positive b; it is used for page-count bounds.
func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// watchURL builds the canonical public watch URL for a video id.
func watchURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + videoID
}

// parseCount parses a YouTube decimal-string counter into an int64, yielding 0
// for an empty or malformed value rather than failing the whole fetch.
func parseCount(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// parseTime parses an RFC 3339 timestamp, yielding the zero time on any error so
// one bad timestamp cannot abort an audit.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
