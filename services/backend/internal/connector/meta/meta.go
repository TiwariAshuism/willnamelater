// Package meta implements connector.Connector for Instagram over the Meta Graph
// API (graph.facebook.com). It audits a Business/Creator account the influencer
// has connected over OAuth: media, per-media insights (reach, impressions,
// saved, shares) and comments.
//
// Unlike the YouTube connector, the Meta Graph API exposes NO public
// unauthenticated path — every read requires the connected user's OAuth access
// token — and it has no username→id resolution for an arbitrary account. A Fetch
// therefore requires both a non-nil, unexpired OAuthToken and the connected
// account's numeric Instagram user id (FetchRequest.AccountID). Neither is
// fabricated: a missing token is KindUnauthorized and a missing id is
// KindInvalid, so the orchestrator records the platform as failed rather than
// inventing coverage.
//
// The Graph API meters usage as the bucketed_calls model: a bucket of calls per
// rolling window (see internal/connector/ratelimit). CostOf reports the number
// of API calls a Fetch may spend so the orchestrator can reserve them from the
// bucket up front. Application- and page-level bucket exhaustion surface as
// connector.QuotaExhaustedError (the window's allowance is spent); per-user and
// custom throttles surface as connector.RateLimitError. Either, when it strikes
// the insights or comments phase, degrades to a partial Snapshot rather than
// failing the whole audit.
//
// This connector stays enabled:false in connectors.yaml until Meta app review
// clears; the csvimport connector is the real Instagram data path until then.
package meta

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// API paging sizes. These are documented ceilings for each edge, chosen to spend
// the fewest calls per item fetched.
const (
	mediaPageSize    = 25 // the media edge's default/typical page ceiling
	commentsPageSize = 50 // the comments edge page size
)

// Default request bounds, applied when a Config leaves a field zero. They are
// deliberately modest: each sampled media costs one insights call and up to
// maxCommentPagesPerMedia comment calls, so the defaults keep a single audit well
// inside one hourly call bucket.
const (
	defaultMaxPosts                = 25
	defaultMaxCommentPagesPerMedia = 2
	defaultMaxComments             = 500
)

// Metric names emitted for an account and its media.
const (
	metricFollowers  = "followers"
	metricMediaCount = "media_count"
	metricReach      = "reach"
	metricSaved      = "saved"
)

// Graph API insight metric tokens requested per media. impressions and shares
// map onto Post fields; reach and saved are surfaced as time-stamped
// MetricPoints (real per-media readings, never fabricated).
const (
	insightImpressions = "impressions"
	insightReach       = "reach"
	insightSaved       = "saved"
	insightShares      = "shares"
)

// Doer is the minimal HTTP contract the connector depends on: the standard
// *http.Client satisfies it, and tests supply a fake, so no test touches the
// network.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config constructs a Connector. BaseURL and HTTP are required; the bound fields
// default when left zero. The OAuth access token is NOT held here: it is
// per-user and per-fetch, and arrives on each FetchRequest.Token.
type Config struct {
	// BaseURL is the Meta Graph API root including the version segment, e.g.
	// "https://graph.facebook.com/v21.0", with no trailing slash. Required.
	BaseURL string
	// HTTP is the injected HTTP client. Required; tests pass a fake Doer.
	HTTP Doer
	// DefaultMaxPosts caps recent media when FetchRequest.MaxPosts is zero.
	// Defaults to defaultMaxPosts.
	DefaultMaxPosts int
	// MaxCommentPagesPerMedia hard-caps comment pages per media so one
	// heavily-commented post cannot drain the call bucket. Defaults to
	// defaultMaxCommentPagesPerMedia.
	MaxCommentPagesPerMedia int
	// MaxComments hard-caps total comments sampled across all media in one Fetch.
	// Defaults to defaultMaxComments.
	MaxComments int
	// Now is an injectable clock for deterministic tests. Defaults to time.Now.
	Now func() time.Time
}

// Connector integrates the Meta Graph API for Instagram. It is safe for
// concurrent use: it holds only immutable configuration and no shared mutable
// state, so the orchestrator may call Fetch from many goroutines.
type Connector struct {
	baseURL                 string
	http                    Doer
	defaultMaxPosts         int
	maxCommentPagesPerMedia int
	maxComments             int
	now                     func() time.Time
}

var _ connector.Connector = (*Connector)(nil)

// New validates cfg and builds a Connector, applying defaults for the bound
// fields. Missing required fields are a KindInvalid configuration error.
func New(cfg Config) (*Connector, error) {
	if cfg.BaseURL == "" {
		return nil, errs.New(errs.KindInvalid, "meta.config",
			"meta connector requires a base url")
	}
	if cfg.HTTP == nil {
		return nil, errs.New(errs.KindInvalid, "meta.config",
			"meta connector requires an http client")
	}

	c := &Connector{
		baseURL:                 strings.TrimRight(cfg.BaseURL, "/"),
		http:                    cfg.HTTP,
		defaultMaxPosts:         cfg.DefaultMaxPosts,
		maxCommentPagesPerMedia: cfg.MaxCommentPagesPerMedia,
		maxComments:             cfg.MaxComments,
		now:                     cfg.Now,
	}
	if c.defaultMaxPosts <= 0 {
		c.defaultMaxPosts = defaultMaxPosts
	}
	if c.maxCommentPagesPerMedia <= 0 {
		c.maxCommentPagesPerMedia = defaultMaxCommentPagesPerMedia
	}
	if c.maxComments <= 0 {
		c.maxComments = defaultMaxComments
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

// Platform returns connector.PlatformInstagram.
func (c *Connector) Platform() connector.Platform { return connector.PlatformInstagram }

// Capabilities returns every capability this connector advertises. A fresh slice
// is returned each call so a caller cannot mutate shared state. Audience
// demographics are advertised but degrade to a partial result: they require the
// account-level insights endpoint under a lifetime period and a follower
// threshold, which is out of this connector's scope; the value is never
// fabricated.
func (c *Connector) Capabilities() []connector.Capability {
	return []connector.Capability{
		connector.CapabilityProfile,
		connector.CapabilityMetrics,
		connector.CapabilityRecentPosts,
		connector.CapabilityAudienceBreakdown,
		connector.CapabilityComments,
	}
}

// CostOf returns the maximum number of API calls a Fetch of req may spend
// against the platform's call bucket. It is an upper bound (an account with
// fewer media, or comment pages that end early, spends less), which is exactly
// what the orchestrator needs to reserve calls before the fetch begins.
func (c *Connector) CostOf(req connector.FetchRequest) int {
	cost := 1 // the user-node read is always issued

	nPosts := c.effectiveMaxPosts(req)
	needMedia := req.Wants(connector.CapabilityRecentPosts) || req.Wants(connector.CapabilityComments)
	if needMedia && nPosts > 0 {
		cost += ceilDiv(nPosts, mediaPageSize)
	}
	if req.Wants(connector.CapabilityRecentPosts) && nPosts > 0 {
		cost += nPosts // one insights call per media
	}
	if req.Wants(connector.CapabilityComments) && nPosts > 0 {
		cost += nPosts * c.maxCommentPagesPerMedia
	}
	return cost
}

// Fetch retrieves a Snapshot for the connected Instagram account. See the
// Connector interface for the full contract. The mandatory user-node read fails
// the fetch if it errors; any quota or rate-limit error in the later media,
// insights or comment phases degrades to a partial Snapshot instead of failing
// the whole audit.
func (c *Connector) Fetch(ctx context.Context, req connector.FetchRequest) (connector.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return connector.Snapshot{}, err
	}

	// The Graph API has no public path: every read needs the connected user's
	// live token. A missing or expired token is an auth failure, not a partial.
	if req.Token == nil || req.Token.Expired(c.now()) {
		return connector.Snapshot{}, errs.New(errs.KindUnauthorized, "meta.token_missing",
			"instagram fetch requires a valid oauth access token")
	}
	token := req.Token.AccessToken
	// The API resolves the account by its numeric Instagram user id, known from
	// the OAuth connection; there is no public username lookup to fall back on.
	if req.AccountID == "" {
		return connector.Snapshot{}, errs.New(errs.KindInvalid, "meta.account_id_missing",
			"instagram fetch requires the connected account id")
	}

	snap := connector.Snapshot{
		Platform:   connector.PlatformInstagram,
		Handle:     req.Handle,
		AccountID:  req.AccountID,
		CapturedAt: c.now(),
	}

	// Phase 1 (mandatory): the user node yields identity, follower count and the
	// account metrics. An error here means no usable snapshot, so it propagates.
	user, err := c.getUser(ctx, token, req.AccountID)
	if err != nil {
		return connector.Snapshot{}, err
	}
	snap.Followers = user.FollowersCount
	if req.Wants(connector.CapabilityMetrics) {
		snap.Metrics = c.metricsFrom(user, snap.CapturedAt)
	}

	// Phase 2: recent media. Both recent_posts and comments need the media ids.
	needMedia := req.Wants(connector.CapabilityRecentPosts) || req.Wants(connector.CapabilityComments)
	var posts []connector.Post
	if needMedia {
		posts, err = c.listMedia(ctx, token, req.AccountID, c.effectiveMaxPosts(req))
		if err != nil {
			if degradable(err) {
				snap.Partial = true
				return snap, nil
			}
			return connector.Snapshot{}, err
		}
		// Attach immediately so every Comment.PostID below references a Post
		// present in this same Snapshot, and so posts survive a later partial.
		snap.Posts = posts
	}

	// Phase 3: per-media insights. impressions/shares fill Post fields;
	// reach/saved are appended as time-stamped MetricPoints. A quota/rate-limit
	// error keeps whatever was gathered and degrades to partial.
	if req.Wants(connector.CapabilityRecentPosts) && len(posts) > 0 {
		extra, ierr := c.enrichInsights(ctx, token, posts)
		snap.Metrics = append(snap.Metrics, extra...)
		if ierr != nil {
			if degradable(ierr) {
				snap.Partial = true
				return snap, nil
			}
			return connector.Snapshot{}, ierr
		}
	}

	// Phase 4: comments. A quota/rate-limit failure here is the canonical partial
	// case: keep the profile, metrics and posts already gathered.
	if req.Wants(connector.CapabilityComments) && len(posts) > 0 {
		comments, cerr := c.listComments(ctx, token, posts)
		snap.Comments = comments
		if cerr != nil {
			if degradable(cerr) {
				snap.Partial = true
				return snap, nil
			}
			return connector.Snapshot{}, cerr
		}
	}

	// Audience demographics require the account-level insights endpoint under a
	// lifetime period and a 100-follower threshold, which this connector does not
	// call. Rather than fabricate a distribution we leave Snapshot.Audience nil
	// and mark the snapshot partial so the orchestrator records the gap honestly.
	if req.Wants(connector.CapabilityAudienceBreakdown) {
		snap.Partial = true
	}

	return snap, nil
}

// metricsFrom projects an account's current counters into MetricPoints stamped
// at capture time. The Graph API exposes no history on the user node, so this is
// the densest series available from it: one point per counter.
func (c *Connector) metricsFrom(u userNode, at time.Time) []connector.MetricPoint {
	return []connector.MetricPoint{
		{At: at, Name: metricFollowers, Value: float64(u.FollowersCount)},
		{At: at, Name: metricMediaCount, Value: float64(u.MediaCount)},
	}
}

// effectiveMaxPosts resolves how many recent media to consider: the request's
// value when set, otherwise the connector default.
func (c *Connector) effectiveMaxPosts(req connector.FetchRequest) int {
	if req.MaxPosts > 0 {
		return req.MaxPosts
	}
	return c.defaultMaxPosts
}

// degradable reports whether err is a quota or rate-limit failure, the two
// conditions under which a media/insights/comment phase yields a partial
// Snapshot instead of failing the audit.
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

// parseTime parses a Graph API timestamp, yielding the zero time on any error so
// one bad timestamp cannot abort an audit. Instagram stamps media and comments
// with a numeric UTC offset (e.g. "2021-09-30T18:00:00+0000"); RFC 3339 is
// accepted as a fallback.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02T15:04:05-0700", s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
