package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// The response structs below map only the fields this connector consumes. Their
// shapes are derived from the public Meta Graph API reference for the Instagram
// Graph API (developers.facebook.com/docs/instagram-api) — the IG User node, the
// media edge, the media insights edge, the comments edge, and the standard Graph
// API error envelope — and are NOT captured user data. On the media edge the
// account's own engagement counters (like_count, comments_count) arrive inline;
// reach/impressions/saved/shares come from the separate per-media insights edge.

// userNode is the IG User node: identity, follower/following count and media
// count. follows_count is the account's following count, used downstream for the
// follower/following ratio feature.
type userNode struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	FollowersCount int64  `json:"followers_count"`
	FollowsCount   int64  `json:"follows_count"`
	MediaCount     int64  `json:"media_count"`
}

// mediaListResponse is one page of the media edge.
type mediaListResponse struct {
	Data   []mediaNode `json:"data"`
	Paging paging      `json:"paging"`
}

type mediaNode struct {
	ID            string `json:"id"`
	Caption       string `json:"caption"`
	MediaType     string `json:"media_type"`
	Permalink     string `json:"permalink"`
	Timestamp     string `json:"timestamp"`
	LikeCount     int64  `json:"like_count"`
	CommentsCount int64  `json:"comments_count"`
}

// paging carries the Graph API cursor. The connector follows cursors.after
// rather than the absolute paging.next URL so it never re-issues a URL that
// embeds the access token supplied by the platform.
type paging struct {
	Cursors struct {
		After string `json:"after"`
	} `json:"cursors"`
}

// insightsResponse is the media insights edge. Each metric reports its scalar in
// values[0].value; audience-style metrics report an object there instead, which
// insightValue skips rather than failing the decode.
type insightsResponse struct {
	Data []struct {
		Name   string `json:"name"`
		Period string `json:"period"`
		Values []struct {
			Value json.RawMessage `json:"value"`
		} `json:"values"`
	} `json:"data"`
}

// demographicsResponse is the account-level follower_demographics insight in the
// total_value + breakdown shape: each breakdown result pairs a single dimension
// value (an age bucket, a gender code, or an ISO country code) with a follower
// count. Its shape is derived from the public Graph API reference, not captured
// data.
type demographicsResponse struct {
	Data []struct {
		Name       string `json:"name"`
		TotalValue struct {
			Breakdowns []struct {
				Results []struct {
					DimensionValues []string    `json:"dimension_values"`
					Value           json.Number `json:"value"`
				} `json:"results"`
			} `json:"breakdowns"`
		} `json:"total_value"`
	} `json:"data"`
}

// commentsResponse is one page of the comments edge.
type commentsResponse struct {
	Data   []commentNode `json:"data"`
	Paging paging        `json:"paging"`
}

type commentNode struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	Username  string `json:"username"`
	From      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
}

// apiErrorResponse is the standard Graph API error envelope. The numeric code
// (and, for insights availability, error_subcode) is the machine token we
// classify on.
type apiErrorResponse struct {
	Error struct {
		Message      string `json:"message"`
		Type         string `json:"type"`
		Code         int    `json:"code"`
		ErrorSubcode int    `json:"error_subcode"`
		FBTraceID    string `json:"fbtrace_id"`
	} `json:"error"`
}

// Graph API error codes we classify on. These are documented values of the
// error.code field.
const (
	codeApplicationRateLimit = 4   // app-level bucket exhausted for the window
	codePermissionDenied     = 10  // permission not granted
	codeUserRateLimit        = 17  // per-user request limit reached
	codePageRateLimit        = 32  // page-level bucket exhausted for the window
	codeInvalidParameter     = 100 // malformed request / unsupported field
	codeAccessTokenInvalid   = 190 // token expired or invalid
	codeCustomRateLimit      = 613 // custom-level rate limit reached
)

// subcodeInsightsUnavailable is error.error_subcode when a media object does not
// support insights (e.g. certain media types). It is skipped per media rather
// than aborting the audit — the analogue of YouTube's commentsDisabled.
const subcodeInsightsUnavailable = 2108006

// errInsightsUnavailable is an internal sentinel: a media whose insights are not
// available yields a 400/100 with subcodeInsightsUnavailable that is not a quota
// or rate-limit failure. The insights loop skips such a media rather than
// aborting. It never escapes the package.
var errInsightsUnavailable = errors.New("meta: insights unavailable for media")

// get performs one GET against the Graph API, decoding a 200 body into out.
// Non-2xx responses are classified into the shared error vocabulary. ctx
// cancellation is honored: a cancelled ctx surfaces as ctx.Err(), never as an
// opaque transport failure. path is the node/edge path, e.g. "<id>" or
// "<id>/media".
func (c *Connector) get(ctx context.Context, token, path string, params url.Values, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// The access token authenticates the read. It is a secret and is placed only
	// on the request; it is never copied into any returned error.
	params.Set("access_token", token)
	requestURL := c.baseURL + "/" + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "meta.request_build",
			"could not build meta request")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errs.Wrap(err, errs.KindUnavailable, "meta.transport",
			"meta api request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errs.Wrap(err, errs.KindUnavailable, "meta.read",
			"could not read meta response")
	}

	if resp.StatusCode != http.StatusOK {
		return c.classifyError(resp, body)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return errs.Wrap(err, errs.KindInternal, "meta.decode",
			"could not decode meta response")
	}
	return nil
}

// classifyError maps a non-2xx Graph API response onto the shared error
// vocabulary. The cause carries only the HTTP status and the (public) error
// code/subcode/type — never the request URL or access token — so RenderError can
// log it without leaking the credential, while exposing only the safe domain
// Message.
//
// The Graph API meters usage with a bucketed_calls model, so bucket exhaustion
// is the "quota" case: application- and page-level limits (codes 4, 32) are the
// coarse window allowance being spent and surface as QuotaExhaustedError, while
// per-user and custom throttles (codes 17, 613) and a bare HTTP 429 are the
// transient RateLimitError. Both degrade a later phase to a partial Snapshot.
func (c *Connector) classifyError(resp *http.Response, body []byte) error {
	var apiErr apiErrorResponse
	_ = json.Unmarshal(body, &apiErr) // best effort; body may not be JSON

	e := apiErr.Error
	cause := fmt.Errorf("meta graph api: http %d code %d subcode %d type %q",
		resp.StatusCode, e.Code, e.ErrorSubcode, e.Type)

	switch e.Code {
	case codeApplicationRateLimit, codePageRateLimit:
		return connector.NewQuotaExhaustedError(connector.PlatformInstagram, c.quotaResetAt(resp.Header), cause)
	case codeUserRateLimit, codeCustomRateLimit:
		return connector.NewRateLimitError(connector.PlatformInstagram, retryAfter(resp.Header), cause)
	case codeAccessTokenInvalid:
		return errs.Wrap(cause, errs.KindUnauthorized, "meta.unauthorized",
			"meta rejected the access token")
	case codePermissionDenied:
		return errs.Wrap(cause, errs.KindForbidden, "meta.forbidden",
			"meta denied access to the requested resource")
	case codeInvalidParameter:
		if e.ErrorSubcode == subcodeInsightsUnavailable {
			return errInsightsUnavailable
		}
		return errs.Wrap(cause, errs.KindInvalid, "meta.invalid_request",
			"meta rejected the request as invalid")
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return connector.NewRateLimitError(connector.PlatformInstagram, retryAfter(resp.Header), cause)
	case http.StatusNotFound:
		return errs.Wrap(cause, errs.KindNotFound, "meta.not_found",
			"the requested instagram resource was not found")
	case http.StatusUnauthorized:
		return errs.Wrap(cause, errs.KindUnauthorized, "meta.unauthorized",
			"meta rejected the access token")
	case http.StatusForbidden:
		return errs.Wrap(cause, errs.KindForbidden, "meta.forbidden",
			"meta denied access to the requested resource")
	case http.StatusBadRequest:
		return errs.Wrap(cause, errs.KindInvalid, "meta.invalid_request",
			"meta rejected the request as invalid")
	default:
		return errs.Wrap(cause, errs.KindUnavailable, "meta.upstream",
			"meta api returned an unexpected error")
	}
}

// getUser resolves the connected account's node: identity, follower count and
// media count, in one call.
func (c *Connector) getUser(ctx context.Context, token, accountID string) (userNode, error) {
	params := url.Values{}
	params.Set("fields", "id,username,followers_count,follows_count,media_count")

	var u userNode
	if err := c.get(ctx, token, accountID, params, &u); err != nil {
		return userNode{}, err
	}
	if u.ID == "" {
		return userNode{}, errs.New(errs.KindNotFound, "meta.account_not_found",
			"no instagram account matched the connected id")
	}
	return u, nil
}

// listMedia pages the media edge to collect up to limit recent posts, returning
// Posts with the inline engagement counters (likes, comments) already filled;
// per-media insights are attached later by enrichInsights. The page count is
// hard-bounded by ceil(limit/mediaPageSize) so it cannot run away.
func (c *Connector) listMedia(ctx context.Context, token, accountID string, limit int) ([]connector.Post, error) {
	if limit <= 0 {
		return nil, nil
	}

	posts := make([]connector.Post, 0, limit)
	after := ""
	maxPages := ceilDiv(limit, mediaPageSize)

	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		params := url.Values{}
		params.Set("fields", "id,caption,media_type,permalink,timestamp,like_count,comments_count")
		params.Set("limit", strconv.Itoa(mediaPageSize))
		if after != "" {
			params.Set("after", after)
		}

		var resp mediaListResponse
		if err := c.get(ctx, token, accountID+"/media", params, &resp); err != nil {
			return nil, err
		}

		for _, m := range resp.Data {
			if m.ID == "" {
				continue
			}
			posts = append(posts, connector.Post{
				ID:          m.ID,
				URL:         m.Permalink,
				PublishedAt: parseTime(m.Timestamp),
				Caption:     m.Caption,
				MediaType:   m.MediaType,
				Likes:       m.LikeCount,
				Comments:    m.CommentsCount,
			})
			if len(posts) >= limit {
				return posts, nil
			}
		}

		if resp.Paging.Cursors.After == "" {
			break
		}
		after = resp.Paging.Cursors.After
	}
	return posts, nil
}

// enrichInsights fills per-media insight fields via one insights call per media
// (reach, impressions, saved, shares). impressions maps onto Post.Views (falling
// back to reach when impressions is absent, as some media types report only
// reach) and shares onto Post.Shares; reach and saved are returned as
// time-stamped MetricPoints, as is a per-media reach ratio (reach / account
// followers) when both are known. For video media it additionally makes a
// SEPARATE best-effort Reels average-watch-time call, so a media type that does
// not support that metric never costs the media its core insights. Posts are
// mutated in place.
//
// A media whose insights are unavailable is skipped (errInsightsUnavailable),
// not fatal. On a quota or rate-limit error it returns the MetricPoints gathered
// so far alongside the error, letting the caller degrade to a partial snapshot.
func (c *Connector) enrichInsights(ctx context.Context, token string, posts []connector.Post, followers int64) ([]connector.MetricPoint, error) {
	points := make([]connector.MetricPoint, 0, len(posts)*3)

	for i := range posts {
		if err := ctx.Err(); err != nil {
			return points, err
		}

		params := url.Values{}
		params.Set("metric", insightImpressions+","+insightReach+","+insightSaved+","+insightShares)

		var resp insightsResponse
		if err := c.get(ctx, token, posts[i].ID+"/insights", params, &resp); err != nil {
			if errors.Is(err, errInsightsUnavailable) {
				continue // this media only; keep enriching the rest
			}
			return points, err
		}

		metrics := insightValues(resp)
		if v, ok := metrics[insightImpressions]; ok {
			posts[i].Views = v
		} else if v, ok := metrics[insightReach]; ok {
			posts[i].Views = v
		}
		if v, ok := metrics[insightShares]; ok {
			posts[i].Shares = v
		}
		if v, ok := metrics[insightReach]; ok {
			points = append(points, connector.MetricPoint{At: posts[i].PublishedAt, Name: metricReach, Value: float64(v)})
			// Reach ratio: how far the post escaped the follower bubble. Emitted only
			// when the follower base is a real positive count — never divided by an
			// unknown or zero, which would fabricate a ratio.
			if followers > 0 {
				points = append(points, connector.MetricPoint{
					At: posts[i].PublishedAt, Name: metricReachRatio, Value: float64(v) / float64(followers),
				})
			}
		}
		if v, ok := metrics[insightSaved]; ok {
			points = append(points, connector.MetricPoint{At: posts[i].PublishedAt, Name: metricSaved, Value: float64(v)})
		}

		// Reels average watch time is valid only on video media. A separate,
		// best-effort call isolates its failure from the core insights above.
		if posts[i].MediaType == mediaTypeVideo {
			watch, werr := c.reelsWatchTime(ctx, token, posts[i].ID)
			if werr != nil {
				if errors.Is(werr, errInsightsUnavailable) {
					continue // not a reel after all; the core insights still stand
				}
				return points, werr // quota/rate-limit -> caller marks partial
			}
			if watch != nil {
				points = append(points, connector.MetricPoint{
					At: posts[i].PublishedAt, Name: metricReelsWatchTime, Value: *watch,
				})
			}
		}
	}
	return points, nil
}

// reelsWatchTime fetches the Reels average-watch-time insight for one video
// media, in a call separate from the core insights so its unavailability never
// costs the media its reach/saved figures. It returns (nil, nil) when the metric
// is simply not reported, (nil, errInsightsUnavailable) when the media does not
// support it (skip, not fatal), and (nil, err) on a quota/rate-limit error the
// caller degrades to partial. The value is the average watch time in seconds.
func (c *Connector) reelsWatchTime(ctx context.Context, token, mediaID string) (*float64, error) {
	params := url.Values{}
	params.Set("metric", insightReelsAvgWatchTime)

	var resp insightsResponse
	if err := c.get(ctx, token, mediaID+"/insights", params, &resp); err != nil {
		return nil, err
	}
	metrics := insightValues(resp)
	if v, ok := metrics[insightReelsAvgWatchTime]; ok {
		f := float64(v)
		return &f, nil
	}
	return nil, nil
}

// insightValues reduces an insights response to a name→scalar map, skipping any
// metric whose value is not a scalar integer (e.g. an audience object) rather
// than failing the whole fetch.
func insightValues(resp insightsResponse) map[string]int64 {
	out := make(map[string]int64, len(resp.Data))
	for _, d := range resp.Data {
		if len(d.Values) == 0 {
			continue
		}
		var n json.Number
		if err := json.Unmarshal(d.Values[0].Value, &n); err != nil {
			continue
		}
		v, err := n.Int64()
		if err != nil {
			continue
		}
		out[d.Name] = v
	}
	return out
}

// listComments samples comments for each post via the comments edge. It stops
// per media at maxCommentPagesPerMedia pages and stops entirely at maxComments
// total, so a post with a million comments cannot drain the call bucket.
//
// On a quota or rate-limit error it returns the comments gathered so far
// alongside the error, letting the caller degrade to a partial snapshot. Each
// returned Comment.PostID equals the Post.ID it was fetched for, which is what
// makes the co-commenter graph reconstructable. AuthorID prefers the commenter's
// stable id (from.id) and falls back to their username when the id is not
// granted, so the identifier is never empty.
func (c *Connector) listComments(ctx context.Context, token string, posts []connector.Post) ([]connector.Comment, error) {
	out := make([]connector.Comment, 0, c.maxComments)

	for _, p := range posts {
		if len(out) >= c.maxComments {
			break
		}

		after := ""
		for page := 0; page < c.maxCommentPagesPerMedia; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if len(out) >= c.maxComments {
				break
			}

			params := url.Values{}
			params.Set("fields", "id,text,timestamp,username,from")
			params.Set("limit", strconv.Itoa(commentsPageSize))
			if after != "" {
				params.Set("after", after)
			}

			var resp commentsResponse
			if err := c.get(ctx, token, p.ID+"/comments", params, &resp); err != nil {
				return out, err
			}

			for _, cm := range resp.Data {
				out = append(out, connector.Comment{
					PostID:   p.ID,
					AuthorID: commentAuthorID(cm),
					Text:     cm.Text,
					At:       parseTime(cm.Timestamp),
				})
				if len(out) >= c.maxComments {
					break
				}
			}

			if resp.Paging.Cursors.After == "" {
				break
			}
			after = resp.Paging.Cursors.After
		}
	}
	return out, nil
}

// getAudience resolves the account's follower demographics across the age,
// gender and country breakdowns — one insights call each — and normalizes each
// to fractions. It returns (nil, nil) when the account exposes no demographics
// at all (a valid empty result). Any transport, quota, permission or
// availability error is returned to the caller, which treats audience as
// best-effort and marks the snapshot partial rather than failing the audit.
func (c *Connector) getAudience(ctx context.Context, token, accountID string) (*connector.AudienceBreakdown, error) {
	age, err := c.demographicBreakdown(ctx, token, accountID, breakdownAge)
	if err != nil {
		return nil, err
	}
	gender, err := c.demographicBreakdown(ctx, token, accountID, breakdownGender)
	if err != nil {
		return nil, err
	}
	country, err := c.demographicBreakdown(ctx, token, accountID, breakdownCountry)
	if err != nil {
		return nil, err
	}

	ages := normalizeFractions(age)
	genders := normalizeGender(gender)
	countries := normalizeFractions(country)
	if ages == nil && genders == nil && countries == nil {
		return nil, nil
	}
	return &connector.AudienceBreakdown{
		Countries: countries,
		AgeGroups: ages,
		Gender:    genders,
	}, nil
}

// demographicBreakdown fetches one follower_demographics breakdown dimension and
// reduces it to a bucket→count map. A malformed count is skipped, not fatal.
func (c *Connector) demographicBreakdown(ctx context.Context, token, accountID, breakdown string) (map[string]int64, error) {
	params := url.Values{}
	params.Set("metric", metricFollowerDemographics)
	params.Set("period", "lifetime")
	params.Set("metric_type", "total_value")
	params.Set("breakdown", breakdown)

	var resp demographicsResponse
	if err := c.get(ctx, token, accountID+"/insights", params, &resp); err != nil {
		return nil, err
	}

	out := make(map[string]int64)
	for _, d := range resp.Data {
		for _, b := range d.TotalValue.Breakdowns {
			for _, r := range b.Results {
				if len(r.DimensionValues) == 0 {
					continue
				}
				n, err := r.Value.Int64()
				if err != nil {
					continue
				}
				out[r.DimensionValues[0]] = n
			}
		}
	}
	return out, nil
}

// commentAuthorID returns the stable commenter identifier: the from.id when the
// app has been granted it, otherwise the username. It is personal data about a
// third party and is pseudonymized before persistence by the metrics module.
func commentAuthorID(cm commentNode) string {
	if cm.From.ID != "" {
		return cm.From.ID
	}
	if cm.From.Username != "" {
		return cm.From.Username
	}
	return cm.Username
}

// quotaResetAt reports when the exhausted call bucket regains capacity. The
// Graph API reports it in the X-Business-Use-Case-Usage header as
// estimated_time_to_regain_access, in minutes; when absent we return the zero
// time, which QuotaExhaustedError treats as "reset time unknown".
func (c *Connector) quotaResetAt(h http.Header) time.Time {
	if d := regainAccess(h); d > 0 {
		return c.now().Add(d)
	}
	return time.Time{}
}

// retryAfter derives how long to wait before retrying a rate-limited call,
// preferring the Graph API's estimated_time_to_regain_access hint and falling
// back to a standard Retry-After header. It returns zero when neither is present
// — a valid "no hint" value for RateLimitError.
func retryAfter(h http.Header) time.Duration {
	if d := regainAccess(h); d > 0 {
		return d
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// regainAccess parses the X-Business-Use-Case-Usage header for the largest
// estimated_time_to_regain_access (in minutes) across its reported buckets,
// returning it as a Duration. The header value is a JSON object keyed by
// business id, each mapping to an array of per-use-case usage objects. It
// returns zero when the header is absent, unparseable, or reports no wait.
func regainAccess(h http.Header) time.Duration {
	v := h.Get("X-Business-Use-Case-Usage")
	if v == "" {
		return 0
	}
	var usage map[string][]struct {
		EstimatedTimeToRegainAccess int `json:"estimated_time_to_regain_access"`
	}
	if err := json.Unmarshal([]byte(v), &usage); err != nil {
		return 0
	}
	maxMinutes := 0
	for _, buckets := range usage {
		for _, b := range buckets {
			if b.EstimatedTimeToRegainAccess > maxMinutes {
				maxMinutes = b.EstimatedTimeToRegainAccess
			}
		}
	}
	if maxMinutes <= 0 {
		return 0
	}
	return time.Duration(maxMinutes) * time.Minute
}
