package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// The response structs below map only the fields this connector consumes. Their
// shapes are derived from the public YouTube Data API v3 reference
// (developers.google.com/youtube/v3/docs) — channels, playlistItems, videos and
// commentThreads resources — and are NOT captured user data. Numeric counters
// arrive as decimal strings in the API, so they are parsed with parseCount.

type channelListResponse struct {
	Items []channelResource `json:"items"`
}

type channelResource struct {
	ID      string `json:"id"`
	Snippet struct {
		Title string `json:"title"`
	} `json:"snippet"`
	Statistics struct {
		SubscriberCount       string `json:"subscriberCount"`
		HiddenSubscriberCount bool   `json:"hiddenSubscriberCount"`
		ViewCount             string `json:"viewCount"`
		VideoCount            string `json:"videoCount"`
	} `json:"statistics"`
	ContentDetails struct {
		RelatedPlaylists struct {
			Uploads string `json:"uploads"`
		} `json:"relatedPlaylists"`
	} `json:"contentDetails"`
}

type playlistItemsResponse struct {
	Items []struct {
		ContentDetails struct {
			VideoID          string `json:"videoId"`
			VideoPublishedAt string `json:"videoPublishedAt"`
		} `json:"contentDetails"`
	} `json:"items"`
	NextPageToken string `json:"nextPageToken"`
}

type videoResource struct {
	ID      string `json:"id"`
	Snippet struct {
		Title       string `json:"title"`
		PublishedAt string `json:"publishedAt"`
	} `json:"snippet"`
	Statistics struct {
		ViewCount    string `json:"viewCount"`
		LikeCount    string `json:"likeCount"`
		CommentCount string `json:"commentCount"`
	} `json:"statistics"`
}

type videosResponse struct {
	Items []videoResource `json:"items"`
}

type commentThreadsResponse struct {
	Items []struct {
		Snippet struct {
			TopLevelComment struct {
				Snippet struct {
					AuthorChannelID struct {
						Value string `json:"value"`
					} `json:"authorChannelId"`
					TextOriginal string `json:"textOriginal"`
					PublishedAt  string `json:"publishedAt"`
				} `json:"snippet"`
			} `json:"topLevelComment"`
		} `json:"snippet"`
	} `json:"items"`
	NextPageToken string `json:"nextPageToken"`
}

// apiErrorResponse is the standard Google API error envelope. reason strings
// (errors[].reason) are the machine tokens we classify on.
type apiErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Errors  []struct {
			Reason string `json:"reason"`
			Domain string `json:"domain"`
		} `json:"errors"`
	} `json:"error"`
}

// YouTube error reason tokens we classify on. These are documented values of
// the errors[].reason field in the Google API error envelope.
const (
	reasonQuotaExceeded         = "quotaExceeded"
	reasonDailyLimitExceeded    = "dailyLimitExceeded"
	reasonRateLimitExceeded     = "rateLimitExceeded"
	reasonUserRateLimitExceeded = "userRateLimitExceeded"
	reasonCommentsDisabled      = "commentsDisabled"
)

// errCommentsDisabled is an internal sentinel: a video with comments turned off
// yields a 403/commentsDisabled that is not a quota or rate-limit failure. The
// comment-fetch loop skips such a video rather than aborting the audit. It never
// escapes the package.
var errCommentsDisabled = errors.New("youtube: comments disabled for video")

// get performs one GET against the YouTube Data API, decoding a 200 body into
// out. Non-2xx responses are classified into the shared error vocabulary. ctx
// cancellation is honored: a cancelled ctx surfaces as ctx.Err(), never as an
// opaque transport failure.
func (c *Connector) get(ctx context.Context, endpoint string, params url.Values, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// The API key authenticates public-data reads. It is a secret and is placed
	// only in the request; it is never copied into any returned error.
	params.Set("key", c.apiKey)
	requestURL := c.baseURL + "/" + endpoint + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "youtube.request_build",
			"could not build youtube request")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errs.Wrap(err, errs.KindUnavailable, "youtube.transport",
			"youtube api request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errs.Wrap(err, errs.KindUnavailable, "youtube.read",
			"could not read youtube response")
	}

	if resp.StatusCode != http.StatusOK {
		return c.classifyError(resp, body)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return errs.Wrap(err, errs.KindInternal, "youtube.decode",
			"could not decode youtube response")
	}
	return nil
}

// classifyError maps a non-2xx YouTube response onto the shared error
// vocabulary. The cause carries only the HTTP status and the (public) reason
// token — never the request URL or API key — so RenderError can log it without
// leaking the credential, while exposing only the safe domain Message.
func (c *Connector) classifyError(resp *http.Response, body []byte) error {
	var apiErr apiErrorResponse
	_ = json.Unmarshal(body, &apiErr) // best effort; body may not be JSON

	reason := ""
	if len(apiErr.Error.Errors) > 0 {
		reason = apiErr.Error.Errors[0].Reason
	}
	cause := fmt.Errorf("youtube api: http %d reason %q", resp.StatusCode, reason)

	switch resp.StatusCode {
	case http.StatusForbidden:
		switch reason {
		case reasonQuotaExceeded, reasonDailyLimitExceeded:
			return connector.NewQuotaExhaustedError(connector.PlatformYouTube, c.quotaResetAt(), cause)
		case reasonRateLimitExceeded, reasonUserRateLimitExceeded:
			return connector.NewRateLimitError(connector.PlatformYouTube, retryAfter(resp.Header), cause)
		case reasonCommentsDisabled:
			return errCommentsDisabled
		default:
			return errs.Wrap(cause, errs.KindForbidden, "youtube.forbidden",
				"youtube denied access to the requested resource")
		}
	case http.StatusTooManyRequests:
		return connector.NewRateLimitError(connector.PlatformYouTube, retryAfter(resp.Header), cause)
	case http.StatusNotFound:
		return errs.Wrap(cause, errs.KindNotFound, "youtube.not_found",
			"the requested youtube resource was not found")
	case http.StatusBadRequest:
		return errs.Wrap(cause, errs.KindInvalid, "youtube.invalid_request",
			"youtube rejected the request as invalid")
	case http.StatusUnauthorized:
		return errs.Wrap(cause, errs.KindUnauthorized, "youtube.unauthorized",
			"youtube rejected the api credentials")
	default:
		return errs.Wrap(cause, errs.KindUnavailable, "youtube.upstream",
			"youtube api returned an unexpected error")
	}
}

// getChannel resolves the subject channel with a single 1-unit channels.list
// call, returning subscriber/view/video counts and the uploads playlist id.
//
// It resolves a handle via channels.list?forHandle= (1 unit). It deliberately
// NEVER calls search.list, which costs 100 units — 100x more — and would let a
// few dozen handle resolutions exhaust the entire 10,000-unit daily budget.
func (c *Connector) getChannel(ctx context.Context, req connector.FetchRequest) (channelResource, error) {
	params := url.Values{}
	params.Set("part", "snippet,statistics,contentDetails")
	if req.AccountID != "" {
		params.Set("id", req.AccountID)
	} else {
		params.Set("forHandle", req.Handle)
	}

	var resp channelListResponse
	if err := c.get(ctx, "channels", params, &resp); err != nil {
		return channelResource{}, err
	}
	if len(resp.Items) == 0 {
		return channelResource{}, errs.New(errs.KindNotFound, "youtube.channel_not_found",
			"no youtube channel matched the requested handle")
	}
	return resp.Items[0], nil
}

// listUploads pages the uploads playlist (1 unit/page) to collect up to limit
// recent video ids, returning skeleton Posts (id, url, publish time). The page
// count is hard-bounded by ceil(limit/playlistPageSize) so it cannot run away.
func (c *Connector) listUploads(ctx context.Context, playlistID string, limit int) ([]connector.Post, error) {
	if playlistID == "" || limit <= 0 {
		return nil, nil
	}

	posts := make([]connector.Post, 0, limit)
	pageToken := ""
	maxPages := ceilDiv(limit, playlistPageSize)

	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		params := url.Values{}
		params.Set("part", "contentDetails")
		params.Set("playlistId", playlistID)
		params.Set("maxResults", strconv.Itoa(playlistPageSize))
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		var resp playlistItemsResponse
		if err := c.get(ctx, "playlistItems", params, &resp); err != nil {
			return nil, err
		}

		for _, it := range resp.Items {
			videoID := it.ContentDetails.VideoID
			if videoID == "" {
				continue
			}
			posts = append(posts, connector.Post{
				ID:          videoID,
				URL:         watchURL(videoID),
				PublishedAt: parseTime(it.ContentDetails.VideoPublishedAt),
			})
			if len(posts) >= limit {
				return posts, nil
			}
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return posts, nil
}

// enrich fills per-video engagement counters via videos.list, batching up to
// videosBatchSize ids per 1-unit call. Posts are mutated in place; a video the
// API omits keeps its zero counters rather than being guessed.
func (c *Connector) enrich(ctx context.Context, posts []connector.Post) error {
	for start := 0; start < len(posts); start += videosBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}

		end := start + videosBatchSize
		if end > len(posts) {
			end = len(posts)
		}
		batch := posts[start:end]

		ids := make([]string, len(batch))
		for i := range batch {
			ids[i] = batch[i].ID
		}

		params := url.Values{}
		params.Set("part", "snippet,statistics")
		params.Set("id", strings.Join(ids, ","))
		params.Set("maxResults", strconv.Itoa(videosBatchSize))

		var resp videosResponse
		if err := c.get(ctx, "videos", params, &resp); err != nil {
			return err
		}

		byID := make(map[string]videoResource, len(resp.Items))
		for _, v := range resp.Items {
			byID[v.ID] = v
		}
		for i := range batch {
			v, ok := byID[batch[i].ID]
			if !ok {
				continue
			}
			batch[i].Caption = v.Snippet.Title
			batch[i].Likes = parseCount(v.Statistics.LikeCount)
			batch[i].Comments = parseCount(v.Statistics.CommentCount)
			batch[i].Views = parseCount(v.Statistics.ViewCount)
			if t := parseTime(v.Snippet.PublishedAt); !t.IsZero() {
				batch[i].PublishedAt = t
			}
		}
	}
	return nil
}

// listComments samples top-level comments for each post via commentThreads.list
// (1 unit/page). It stops per video at maxCommentPagesPerVideo pages and stops
// entirely at maxComments total, so a video with a million comments cannot drain
// the daily quota. A video with comments disabled is skipped, not fatal.
//
// On a quota or rate-limit error it returns the comments gathered so far
// alongside the error, letting the caller degrade to a partial snapshot. Each
// returned Comment.PostID equals the Post.ID it was fetched for, which is what
// makes the co-commenter graph reconstructable.
func (c *Connector) listComments(ctx context.Context, posts []connector.Post) ([]connector.Comment, error) {
	out := make([]connector.Comment, 0, c.maxComments)

	for _, p := range posts {
		if len(out) >= c.maxComments {
			break
		}

		pageToken := ""
		for page := 0; page < c.maxCommentPagesPerVideo; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if len(out) >= c.maxComments {
				break
			}

			params := url.Values{}
			params.Set("part", "snippet")
			params.Set("videoId", p.ID)
			params.Set("maxResults", strconv.Itoa(commentsPageSize))
			params.Set("order", "time")
			if pageToken != "" {
				params.Set("pageToken", pageToken)
			}

			var resp commentThreadsResponse
			if err := c.get(ctx, "commentThreads", params, &resp); err != nil {
				if errors.Is(err, errCommentsDisabled) {
					break // this video only; keep auditing the rest
				}
				return out, err
			}

			for _, it := range resp.Items {
				s := it.Snippet.TopLevelComment.Snippet
				out = append(out, connector.Comment{
					PostID:   p.ID,
					AuthorID: s.AuthorChannelID.Value,
					Text:     s.TextOriginal,
					At:       parseTime(s.PublishedAt),
				})
				if len(out) >= c.maxComments {
					break
				}
			}

			if resp.NextPageToken == "" {
				break
			}
			pageToken = resp.NextPageToken
		}
	}
	return out, nil
}

// quotaResetAt reports the next YouTube quota rollover. The daily budget resets
// at midnight US Pacific time; the API does not report it in the response, so we
// compute it. If the zoneinfo database is unavailable we return the zero time,
// which the QuotaExhaustedError treats as "reset time unknown".
func (c *Connector) quotaResetAt() time.Time {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.Time{}
	}
	now := c.now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
}

// retryAfter parses a Retry-After header, accepting either a delay in seconds or
// an HTTP date. It returns zero when the header is absent, unparseable, or in
// the past — zero is a valid "no hint" value for RateLimitError.
func retryAfter(h http.Header) time.Duration {
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
