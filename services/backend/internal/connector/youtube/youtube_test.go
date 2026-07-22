package youtube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Shape fixtures below are hand-built from the public YouTube Data API v3
// reference (developers.google.com/youtube/v3/docs) — the documented JSON shapes
// of channels, playlistItems, videos, commentThreads, and the standard Google
// error envelope. They are NOT captured from any real account.

const (
	channelBodyVisible = `{"items":[{"id":"UCchannel","snippet":{"title":"Test Channel"},` +
		`"statistics":{"subscriberCount":"2340000","hiddenSubscriberCount":false,` +
		`"viewCount":"178000000","videoCount":"5700"},` +
		`"contentDetails":{"relatedPlaylists":{"uploads":"UUuploads"}}}]}`

	channelBodyHiddenSubs = `{"items":[{"id":"UCchannel","snippet":{"title":"Test Channel"},` +
		`"statistics":{"subscriberCount":"0","hiddenSubscriberCount":true,` +
		`"viewCount":"178000000","videoCount":"5700"},` +
		`"contentDetails":{"relatedPlaylists":{"uploads":"UUuploads"}}}]}`

	playlistBodyTwo = `{"items":[` +
		`{"contentDetails":{"videoId":"vid1","videoPublishedAt":"2021-01-01T00:00:00Z"}},` +
		`{"contentDetails":{"videoId":"vid2","videoPublishedAt":"2021-02-01T00:00:00Z"}}]}`

	videosBodyTwo = `{"items":[` +
		`{"id":"vid1","snippet":{"title":"First","publishedAt":"2021-01-01T00:00:00Z"},` +
		`"statistics":{"viewCount":"1000","likeCount":"100","commentCount":"10"}},` +
		`{"id":"vid2","snippet":{"title":"Second","publishedAt":"2021-02-01T00:00:00Z"},` +
		`"statistics":{"viewCount":"2000","likeCount":"200","commentCount":"20"}}]}`

	commentsBodyVid1 = `{"items":[{"snippet":{"topLevelComment":{"snippet":{` +
		`"authorChannelId":{"value":"UCcommenterA"},"textOriginal":"nice one",` +
		`"publishedAt":"2021-01-02T00:00:00Z"}}}}]}`

	commentsBodyVid2 = `{"items":[{"snippet":{"topLevelComment":{"snippet":{` +
		`"authorChannelId":{"value":"UCcommenterB"},"textOriginal":"great",` +
		`"publishedAt":"2021-02-02T00:00:00Z"}}}}]}`

	quotaErrorBody = `{"error":{"code":403,"message":"quota exceeded",` +
		`"errors":[{"reason":"quotaExceeded","domain":"youtube.quota"}]}}`

	rateLimitErrorBody = `{"error":{"code":403,"message":"rate limit",` +
		`"errors":[{"reason":"rateLimitExceeded","domain":"usageLimits"}]}}`

	commentsDisabledBody = `{"error":{"code":403,"message":"disabled",` +
		`"errors":[{"reason":"commentsDisabled","domain":"youtube.commentThread"}]}}`

	notFoundBody = `{"error":{"code":404,"message":"not found","errors":[{"reason":"notFound"}]}}`

	serverErrorBody = `{"error":{"code":500,"message":"backend error","errors":[{"reason":"backendError"}]}}`
)

// stubResponse is one canned HTTP reply for an endpoint.
type stubResponse struct {
	status int
	body   string
	header http.Header
}

// stubDoer answers requests from per-endpoint queues consumed in call order, and
// records how many times each endpoint was hit. It optionally runs a hook before
// responding, which the cancellation test uses to cancel mid-fetch.
type stubDoer struct {
	t      *testing.T
	queues map[string][]stubResponse
	calls  map[string]int
	hook   func(endpoint string, req *http.Request)
}

func newStubDoer(t *testing.T, queues map[string][]stubResponse) *stubDoer {
	t.Helper()
	return &stubDoer{t: t, queues: queues, calls: map[string]int{}}
}

func endpointName(p string) string { return path.Base(p) }

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	ep := endpointName(req.URL.Path)
	if s.hook != nil {
		s.hook(ep, req)
	}
	// After a hook cancellation the connector's get() checks ctx before the call,
	// but if a transport error is expected the hook may have cancelled ctx; honor
	// it here as a real client would.
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	idx := s.calls[ep]
	queue := s.queues[ep]
	if idx >= len(queue) {
		s.t.Fatalf("unexpected call #%d to endpoint %q", idx, ep)
	}
	s.calls[ep]++

	r := queue[idx]
	h := r.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{
		StatusCode: r.status,
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Header:     h,
	}, nil
}

// ok wraps a 200 body.
func ok(body string) stubResponse { return stubResponse{status: http.StatusOK, body: body} }

// newTestConnector builds a connector with deterministic bounds and clock.
func newTestConnector(t *testing.T, doer Doer) *Connector {
	t.Helper()
	c, err := New(Config{
		BaseURL:                 "https://api.example/youtube/v3",
		APIKey:                  "SECRET_KEY_VALUE",
		HTTP:                    doer,
		DefaultMaxPosts:         50,
		MaxCommentPagesPerVideo: 2,
		MaxComments:             500,
		Now:                     func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNewValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{name: "missing base url", cfg: Config{APIKey: "k", HTTP: newStubDoer(t, nil)}},
		{name: "missing api key", cfg: Config{BaseURL: "b", HTTP: newStubDoer(t, nil)}},
		{name: "missing http", cfg: Config{BaseURL: "b", APIKey: "k"}},
		{name: "valid", cfg: Config{BaseURL: "b", APIKey: "k", HTTP: newStubDoer(t, nil)}, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tt.cfg)
			if tt.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				if errs.KindOf(err) != errs.KindInvalid {
					t.Fatalf("want KindInvalid, got %v", errs.KindOf(err))
				}
			}
		})
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	t.Parallel()
	c, err := New(Config{BaseURL: "b", APIKey: "k", HTTP: newStubDoer(t, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.defaultMaxPosts != defaultMaxPosts ||
		c.maxCommentPagesPerVideo != defaultMaxCommentPagesPerVideo ||
		c.maxComments != defaultMaxComments {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if c.now == nil {
		t.Fatal("clock not defaulted")
	}
}

func TestPlatformAndCapabilities(t *testing.T) {
	t.Parallel()
	c := newTestConnector(t, newStubDoer(t, nil))
	if c.Platform() != connector.PlatformYouTube {
		t.Fatalf("platform = %q", c.Platform())
	}
	want := map[connector.Capability]bool{
		connector.CapabilityProfile:     true,
		connector.CapabilityMetrics:     true,
		connector.CapabilityRecentPosts: true,
		connector.CapabilityComments:    true,
	}
	got := c.Capabilities()
	if len(got) != len(want) {
		t.Fatalf("cap count = %d, want %d", len(got), len(want))
	}
	for _, cap := range got {
		if !want[cap] {
			t.Fatalf("unexpected capability %q", cap)
		}
	}
}

func TestCostOf(t *testing.T) {
	t.Parallel()
	c := newTestConnector(t, newStubDoer(t, nil))

	tests := []struct {
		name string
		req  connector.FetchRequest
		want int
	}{
		{
			name: "profile only",
			req:  connector.FetchRequest{Capabilities: []connector.Capability{connector.CapabilityProfile}},
			want: 1, // channels.list
		},
		{
			name: "metrics only",
			req:  connector.FetchRequest{Capabilities: []connector.Capability{connector.CapabilityMetrics}},
			want: 1,
		},
		{
			name: "recent posts default 50",
			req:  connector.FetchRequest{Capabilities: []connector.Capability{connector.CapabilityRecentPosts}},
			want: 1 + 1 + 1, // channels + 1 playlist page + 1 videos batch
		},
		{
			name: "comments only default 50",
			req:  connector.FetchRequest{Capabilities: []connector.Capability{connector.CapabilityComments}},
			want: 1 + 1 + 50*2, // channels + 1 playlist page + 50 videos * 2 pages
		},
		{
			name: "recent posts and comments, 120 posts",
			req: connector.FetchRequest{
				MaxPosts:     120,
				Capabilities: []connector.Capability{connector.CapabilityRecentPosts, connector.CapabilityComments},
			},
			// channels(1) + playlist ceil(120/50)=3 + videos ceil(120/50)=3 + comments 120*2=240
			want: 1 + 3 + 3 + 240,
		},
		{
			name: "audience adds nothing",
			req: connector.FetchRequest{
				Capabilities: []connector.Capability{connector.CapabilityProfile, connector.CapabilityAudienceBreakdown},
			},
			want: 1,
		},
		{
			name: "empty capabilities means all",
			req:  connector.FetchRequest{},
			// wants everything: channels(1) + playlist(1) + videos(1) + comments 50*2=100
			want: 1 + 1 + 1 + 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := c.CostOf(tt.req); got != tt.want {
				t.Fatalf("CostOf = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFetchSuccessFull(t *testing.T) {
	t.Parallel()
	doer := newStubDoer(t, map[string][]stubResponse{
		"channels":       {ok(channelBodyVisible)},
		"playlistItems":  {ok(playlistBodyTwo)},
		"videos":         {ok(videosBodyTwo)},
		"commentThreads": {ok(commentsBodyVid1), ok(commentsBodyVid2)},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:   "@test",
		MaxPosts: 50,
		Capabilities: []connector.Capability{
			connector.CapabilityProfile, connector.CapabilityMetrics,
			connector.CapabilityRecentPosts, connector.CapabilityComments,
		},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Partial {
		t.Fatal("snapshot unexpectedly partial")
	}
	if snap.Platform != connector.PlatformYouTube || snap.Handle != "@test" || snap.AccountID != "UCchannel" {
		t.Fatalf("identity wrong: %+v", snap)
	}
	if snap.Followers != 2_340_000 {
		t.Fatalf("followers = %d", snap.Followers)
	}
	if len(snap.Metrics) != 3 {
		t.Fatalf("metrics = %d, want 3", len(snap.Metrics))
	}
	if len(snap.Posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(snap.Posts))
	}
	if snap.Posts[0].ID != "vid1" || snap.Posts[0].Likes != 100 || snap.Posts[0].Views != 1000 || snap.Posts[0].Comments != 10 {
		t.Fatalf("post enrichment wrong: %+v", snap.Posts[0])
	}
	if snap.Posts[0].URL != "https://www.youtube.com/watch?v=vid1" {
		t.Fatalf("post url wrong: %q", snap.Posts[0].URL)
	}

	// Comment -> post linkage: every comment references a post present in the snapshot.
	postIDs := map[string]bool{}
	for _, p := range snap.Posts {
		postIDs[p.ID] = true
	}
	if len(snap.Comments) != 2 {
		t.Fatalf("comments = %d, want 2", len(snap.Comments))
	}
	for _, cm := range snap.Comments {
		if !postIDs[cm.PostID] {
			t.Fatalf("comment PostID %q references no post in snapshot", cm.PostID)
		}
		if cm.AuthorID == "" {
			t.Fatal("comment missing author id")
		}
	}
	// Order: vid1's comment links to vid1, vid2's to vid2.
	if snap.Comments[0].PostID != "vid1" || snap.Comments[1].PostID != "vid2" {
		t.Fatalf("comment linkage order wrong: %+v", snap.Comments)
	}
}

func TestFetchHiddenSubscribersOmitsMetric(t *testing.T) {
	t.Parallel()
	doer := newStubDoer(t, map[string][]stubResponse{
		"channels": {ok(channelBodyHiddenSubs)},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@test",
		Capabilities: []connector.Capability{connector.CapabilityProfile, connector.CapabilityMetrics},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Followers != 0 {
		t.Fatalf("hidden subs should yield 0 followers, got %d", snap.Followers)
	}
	// Only views + videos metrics; subscribers omitted rather than zero-guessed.
	if len(snap.Metrics) != 2 {
		t.Fatalf("metrics = %d, want 2", len(snap.Metrics))
	}
	for _, m := range snap.Metrics {
		if m.Name == metricSubscribers {
			t.Fatal("subscribers metric should be omitted when hidden")
		}
	}
}

func TestFetchErrorClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		resp       stubResponse
		wantQuota  bool
		wantRate   bool
		wantKind   errs.Kind
		wantRetry  time.Duration
		checkRetry bool
	}{
		{
			name:      "403 quotaExceeded",
			resp:      stubResponse{status: http.StatusForbidden, body: quotaErrorBody},
			wantQuota: true,
		},
		{
			name:       "403 rateLimitExceeded",
			resp:       stubResponse{status: http.StatusForbidden, body: rateLimitErrorBody},
			wantRate:   true,
			checkRetry: true,
			wantRetry:  0,
		},
		{
			name:       "429 with retry-after seconds",
			resp:       stubResponse{status: http.StatusTooManyRequests, body: rateLimitErrorBody, header: http.Header{"Retry-After": {"30"}}},
			wantRate:   true,
			checkRetry: true,
			wantRetry:  30 * time.Second,
		},
		{
			name:     "404 not found",
			resp:     stubResponse{status: http.StatusNotFound, body: notFoundBody},
			wantKind: errs.KindNotFound,
		},
		{
			name:     "500 server error is unavailable",
			resp:     stubResponse{status: http.StatusInternalServerError, body: serverErrorBody},
			wantKind: errs.KindUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doer := newStubDoer(t, map[string][]stubResponse{"channels": {tt.resp}})
			c := newTestConnector(t, doer)

			_, err := c.Fetch(context.Background(), connector.FetchRequest{Handle: "@test"})
			if err == nil {
				t.Fatal("want error, got nil")
			}

			// The secret API key must never appear in an error surfaced to callers.
			if strings.Contains(err.Error(), "SECRET_KEY_VALUE") {
				t.Fatalf("error leaked api key: %v", err)
			}

			switch {
			case tt.wantQuota:
				var q *connector.QuotaExhaustedError
				if !errors.As(err, &q) {
					t.Fatalf("want QuotaExhaustedError, got %T: %v", err, err)
				}
				if errs.Status(err) != http.StatusPaymentRequired {
					t.Fatalf("quota status = %d", errs.Status(err))
				}
			case tt.wantRate:
				var r *connector.RateLimitError
				if !errors.As(err, &r) {
					t.Fatalf("want RateLimitError, got %T: %v", err, err)
				}
				if errs.Status(err) != http.StatusTooManyRequests {
					t.Fatalf("rate status = %d", errs.Status(err))
				}
				if tt.checkRetry && r.RetryAfter != tt.wantRetry {
					t.Fatalf("RetryAfter = %v, want %v", r.RetryAfter, tt.wantRetry)
				}
			default:
				if errs.KindOf(err) != tt.wantKind {
					t.Fatalf("kind = %v, want %v", errs.KindOf(err), tt.wantKind)
				}
			}
		})
	}
}

func TestFetchPartialOnCommentQuota(t *testing.T) {
	t.Parallel()
	// Profile, posts succeed; the first commentThreads call is quota-exhausted.
	doer := newStubDoer(t, map[string][]stubResponse{
		"channels":       {ok(channelBodyVisible)},
		"playlistItems":  {ok(playlistBodyTwo)},
		"videos":         {ok(videosBodyTwo)},
		"commentThreads": {{status: http.StatusForbidden, body: quotaErrorBody}},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@test",
		Capabilities: []connector.Capability{connector.CapabilityProfile, connector.CapabilityMetrics, connector.CapabilityRecentPosts, connector.CapabilityComments},
	})
	if err != nil {
		t.Fatalf("comment-quota should degrade, not fail: %v", err)
	}
	if !snap.Partial {
		t.Fatal("snapshot should be partial")
	}
	if len(snap.Posts) != 2 {
		t.Fatalf("posts should survive: %d", len(snap.Posts))
	}
	if snap.Followers != 2_340_000 {
		t.Fatalf("profile should survive: %d", snap.Followers)
	}
	if len(snap.Comments) != 0 {
		t.Fatalf("no comments should be gathered: %d", len(snap.Comments))
	}
}

func TestFetchCommentsDisabledSkipsVideo(t *testing.T) {
	t.Parallel()
	// vid1 has comments disabled (not fatal); vid2 yields a comment.
	doer := newStubDoer(t, map[string][]stubResponse{
		"channels":      {ok(channelBodyVisible)},
		"playlistItems": {ok(playlistBodyTwo)},
		"videos":        {ok(videosBodyTwo)},
		"commentThreads": {
			{status: http.StatusForbidden, body: commentsDisabledBody},
			ok(commentsBodyVid2),
		},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@test",
		Capabilities: []connector.Capability{connector.CapabilityRecentPosts, connector.CapabilityComments},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Partial {
		t.Fatal("comments-disabled on one video should not mark the snapshot partial")
	}
	if len(snap.Comments) != 1 || snap.Comments[0].PostID != "vid2" {
		t.Fatalf("want single comment on vid2, got %+v", snap.Comments)
	}
}

// TestFetchAudienceRequestIsNilNotPartial pins the structural-gap contract: the
// key-based YouTube fetch does not serve audience demographics, so a request for
// them leaves Audience nil WITHOUT marking the snapshot partial. The gap is
// permanent, not a transient shortfall, and the score's Audience Quality factor
// drops honestly on a nil Audience — flagging every YouTube audit "partial" would
// be misleading.
func TestFetchAudienceRequestIsNilNotPartial(t *testing.T) {
	t.Parallel()
	doer := newStubDoer(t, map[string][]stubResponse{
		"channels": {ok(channelBodyVisible)},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@test",
		Capabilities: []connector.Capability{connector.CapabilityProfile, connector.CapabilityAudienceBreakdown},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Partial {
		t.Fatal("a structural audience gap must not mark the whole audit partial")
	}
	if snap.Audience != nil {
		t.Fatal("audience must be nil, never fabricated")
	}
	// No post/comment endpoints should have been touched.
	if doer.calls["playlistItems"]+doer.calls["videos"]+doer.calls["commentThreads"] != 0 {
		t.Fatalf("audience-only fetch spent extra quota: %+v", doer.calls)
	}
}

func TestFetchContextCancelledUpfront(t *testing.T) {
	t.Parallel()
	doer := newStubDoer(t, map[string][]stubResponse{})
	c := newTestConnector(t, doer)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Fetch(ctx, connector.FetchRequest{Handle: "@test"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if doer.calls["channels"] != 0 {
		t.Fatal("cancelled fetch should issue no calls")
	}
}

func TestFetchContextCancelledMidFetch(t *testing.T) {
	t.Parallel()
	// channels succeeds, but the hook cancels ctx right after, so the uploads
	// phase must observe cancellation and return promptly.
	doer := newStubDoer(t, map[string][]stubResponse{
		"channels":      {ok(channelBodyVisible)},
		"playlistItems": {ok(playlistBodyTwo)},
	})
	c := newTestConnector(t, doer)

	ctx, cancel := context.WithCancel(context.Background())
	doer.hook = func(endpoint string, _ *http.Request) {
		if endpoint == "channels" {
			cancel()
		}
	}

	_, err := c.Fetch(ctx, connector.FetchRequest{
		Handle:       "@test",
		Capabilities: []connector.Capability{connector.CapabilityRecentPosts},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if doer.calls["playlistItems"] != 0 {
		t.Fatal("uploads phase should not run after cancellation")
	}
}

func TestFetchCommentsHonorTotalCap(t *testing.T) {
	t.Parallel()
	// maxComments = 1 stops after the first video's single comment even though a
	// second video with comments is available.
	doer := newStubDoer(t, map[string][]stubResponse{
		"channels":       {ok(channelBodyVisible)},
		"playlistItems":  {ok(playlistBodyTwo)},
		"videos":         {ok(videosBodyTwo)},
		"commentThreads": {ok(commentsBodyVid1)},
	})
	c, err := New(Config{
		BaseURL:                 "https://api.example/youtube/v3",
		APIKey:                  "k",
		HTTP:                    doer,
		MaxComments:             1,
		MaxCommentPagesPerVideo: 2,
		Now:                     func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@test",
		Capabilities: []connector.Capability{connector.CapabilityRecentPosts, connector.CapabilityComments},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Comments) != 1 {
		t.Fatalf("total comment cap not honored: got %d", len(snap.Comments))
	}
	if doer.calls["commentThreads"] != 1 {
		t.Fatalf("cap should stop after one commentThreads call, got %d", doer.calls["commentThreads"])
	}
}

func TestRetryAfterParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{name: "absent", header: http.Header{}, want: 0},
		{name: "seconds", header: http.Header{"Retry-After": {"45"}}, want: 45 * time.Second},
		{name: "zero", header: http.Header{"Retry-After": {"0"}}, want: 0},
		{name: "negative", header: http.Header{"Retry-After": {"-5"}}, want: 0},
		{name: "garbage", header: http.Header{"Retry-After": {"soon"}}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := retryAfter(tt.header); got != tt.want {
				t.Fatalf("retryAfter = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseHelpers(t *testing.T) {
	t.Parallel()
	if parseCount("") != 0 || parseCount("abc") != 0 || parseCount("42") != 42 {
		t.Fatal("parseCount wrong")
	}
	if !parseTime("").IsZero() || !parseTime("nonsense").IsZero() {
		t.Fatal("parseTime should be zero on bad input")
	}
	if got := parseTime("2021-01-01T00:00:00Z"); got.IsZero() {
		t.Fatal("parseTime should parse RFC3339")
	}
}
