package meta

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

// Shape fixtures below are hand-built from the public Meta Graph API reference
// for the Instagram Graph API (developers.facebook.com/docs/instagram-api) — the
// documented JSON shapes of the IG User node, the media edge, the media insights
// edge, the comments edge, and the standard Graph API error envelope. They are
// NOT captured from any real account.

const (
	testAccountID = "17841400000000000"

	userBody = `{"id":"17841400000000000","username":"testcreator",` +
		`"followers_count":152340,"media_count":840}`

	mediaBodyTwo = `{"data":[` +
		`{"id":"media1","caption":"first post","media_type":"IMAGE",` +
		`"permalink":"https://www.instagram.com/p/AAA/","timestamp":"2021-09-01T12:00:00+0000",` +
		`"like_count":1200,"comments_count":45},` +
		`{"id":"media2","caption":"second post","media_type":"VIDEO",` +
		`"permalink":"https://www.instagram.com/p/BBB/","timestamp":"2021-09-05T12:00:00+0000",` +
		`"like_count":2400,"comments_count":90}],"paging":{"cursors":{"after":""}}}`

	insightsBodyMedia1 = `{"data":[` +
		`{"name":"impressions","period":"lifetime","values":[{"value":5000}]},` +
		`{"name":"reach","period":"lifetime","values":[{"value":4200}]},` +
		`{"name":"saved","period":"lifetime","values":[{"value":75}]},` +
		`{"name":"shares","period":"lifetime","values":[{"value":30}]}]}`

	insightsBodyMedia2 = `{"data":[` +
		`{"name":"impressions","period":"lifetime","values":[{"value":9000}]},` +
		`{"name":"reach","period":"lifetime","values":[{"value":7000}]},` +
		`{"name":"saved","period":"lifetime","values":[{"value":120}]},` +
		`{"name":"shares","period":"lifetime","values":[{"value":60}]}]}`

	commentsBodyMedia1 = `{"data":[{"id":"c1","text":"love this","timestamp":"2021-09-02T08:00:00+0000",` +
		`"username":"fan_a","from":{"id":"user_a","username":"fan_a"}}],"paging":{"cursors":{"after":""}}}`

	commentsBodyMedia2 = `{"data":[{"id":"c2","text":"great work","timestamp":"2021-09-06T08:00:00+0000",` +
		`"username":"fan_b","from":{"id":"user_b","username":"fan_b"}}],"paging":{"cursors":{"after":""}}}`

	appRateLimitBody = `{"error":{"message":"Application request limit reached",` +
		`"type":"OAuthException","code":4,"fbtrace_id":"AtraceA"}}`

	pageRateLimitBody = `{"error":{"message":"Page request limit reached",` +
		`"type":"OAuthException","code":32,"fbtrace_id":"AtraceP"}}`

	userRateLimitBody = `{"error":{"message":"User request limit reached",` +
		`"type":"OAuthException","code":17,"fbtrace_id":"AtraceU"}}`

	customRateLimitBody = `{"error":{"message":"calls exceeded rate limit",` +
		`"type":"OAuthException","code":613,"fbtrace_id":"AtraceC"}}`

	tokenInvalidBody = `{"error":{"message":"Error validating access token",` +
		`"type":"OAuthException","code":190,"error_subcode":463,"fbtrace_id":"AtraceT"}}`

	permissionBody = `{"error":{"message":"permission not granted",` +
		`"type":"OAuthException","code":10,"fbtrace_id":"AtracePerm"}}`

	invalidParamBody = `{"error":{"message":"Unsupported get request",` +
		`"type":"GraphMethodException","code":100,"error_subcode":33,"fbtrace_id":"AtraceI"}}`

	insightsUnavailableBody = `{"error":{"message":"Insights not available for this media",` +
		`"type":"OAuthException","code":100,"error_subcode":2108006,"fbtrace_id":"AtraceIns"}}`

	notFoundBody = `{"error":{"message":"Unknown path components",` +
		`"type":"OAuthException","code":803,"fbtrace_id":"AtraceNF"}}`

	serverErrorBody = `{"error":{"message":"An unexpected error has occurred",` +
		`"type":"OAuthException","code":1,"fbtrace_id":"AtraceS"}}`
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

// endpointKind classifies a request path into a stub queue key. The user node is
// addressed by its bare id; the edges end in /media, /insights, /comments.
func endpointKind(p string) string {
	switch base := path.Base(p); base {
	case "media", "insights", "comments":
		return base
	default:
		return "user"
	}
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	ep := endpointKind(req.URL.Path)
	if s.hook != nil {
		s.hook(ep, req)
	}
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

func testNow() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) }

// validToken is a live, never-expiring token for the fetch path.
func validToken() *connector.OAuthToken {
	return &connector.OAuthToken{AccessToken: "SECRET_TOKEN_VALUE"}
}

// newTestConnector builds a connector with deterministic bounds and clock.
func newTestConnector(t *testing.T, doer Doer) *Connector {
	t.Helper()
	c, err := New(Config{
		BaseURL:                 "https://api.example/v21.0",
		HTTP:                    doer,
		DefaultMaxPosts:         25,
		MaxCommentPagesPerMedia: 2,
		MaxComments:             500,
		Now:                     testNow,
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
		{name: "missing base url", cfg: Config{HTTP: newStubDoer(t, nil)}},
		{name: "missing http", cfg: Config{BaseURL: "b"}},
		{name: "valid", cfg: Config{BaseURL: "b", HTTP: newStubDoer(t, nil)}, ok: true},
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
	c, err := New(Config{BaseURL: "b", HTTP: newStubDoer(t, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.defaultMaxPosts != defaultMaxPosts ||
		c.maxCommentPagesPerMedia != defaultMaxCommentPagesPerMedia ||
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
	if c.Platform() != connector.PlatformInstagram {
		t.Fatalf("platform = %q", c.Platform())
	}
	want := map[connector.Capability]bool{
		connector.CapabilityProfile:           true,
		connector.CapabilityMetrics:           true,
		connector.CapabilityRecentPosts:       true,
		connector.CapabilityAudienceBreakdown: true,
		connector.CapabilityComments:          true,
	}
	got := c.Capabilities()
	if len(got) != len(want) {
		t.Fatalf("cap count = %d, want %d", len(got), len(want))
	}
	for _, capability := range got {
		if !want[capability] {
			t.Fatalf("unexpected capability %q", capability)
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
			want: 1, // user node
		},
		{
			name: "metrics only",
			req:  connector.FetchRequest{Capabilities: []connector.Capability{connector.CapabilityMetrics}},
			want: 1,
		},
		{
			name: "recent posts default 25",
			req:  connector.FetchRequest{Capabilities: []connector.Capability{connector.CapabilityRecentPosts}},
			want: 1 + 1 + 25, // user + 1 media page + 25 insights calls
		},
		{
			name: "comments only default 25",
			req:  connector.FetchRequest{Capabilities: []connector.Capability{connector.CapabilityComments}},
			want: 1 + 1 + 25*2, // user + 1 media page + 25 media * 2 comment pages
		},
		{
			name: "recent posts and comments, 60 posts",
			req: connector.FetchRequest{
				MaxPosts:     60,
				Capabilities: []connector.Capability{connector.CapabilityRecentPosts, connector.CapabilityComments},
			},
			// user(1) + media ceil(60/25)=3 + insights 60 + comments 60*2=120
			want: 1 + 3 + 60 + 120,
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
			// user(1) + media(1) + insights 25 + comments 25*2=50
			want: 1 + 1 + 25 + 50,
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
		"user":     {ok(userBody)},
		"media":    {ok(mediaBodyTwo)},
		"insights": {ok(insightsBodyMedia1), ok(insightsBodyMedia2)},
		"comments": {ok(commentsBodyMedia1), ok(commentsBodyMedia2)},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:    "@testcreator",
		AccountID: testAccountID,
		Token:     validToken(),
		MaxPosts:  25,
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
	if snap.Platform != connector.PlatformInstagram || snap.Handle != "@testcreator" || snap.AccountID != testAccountID {
		t.Fatalf("identity wrong: %+v", snap)
	}
	if snap.Followers != 152340 {
		t.Fatalf("followers = %d", snap.Followers)
	}

	// followers + media_count, then reach + saved for each of the two media.
	if len(snap.Metrics) != 6 {
		t.Fatalf("metrics = %d, want 6", len(snap.Metrics))
	}
	var sawFollowers, sawReach, sawSaved bool
	for _, m := range snap.Metrics {
		switch m.Name {
		case metricFollowers:
			sawFollowers = m.Value == 152340
		case metricReach:
			sawReach = true
		case metricSaved:
			sawSaved = true
		}
	}
	if !sawFollowers || !sawReach || !sawSaved {
		t.Fatalf("expected followers/reach/saved metrics, got %+v", snap.Metrics)
	}

	if len(snap.Posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(snap.Posts))
	}
	// media edge counters plus per-media insights (impressions->Views, shares->Shares).
	p0 := snap.Posts[0]
	if p0.ID != "media1" || p0.Likes != 1200 || p0.Comments != 45 || p0.Views != 5000 || p0.Shares != 30 {
		t.Fatalf("post0 enrichment wrong: %+v", p0)
	}
	if p0.URL != "https://www.instagram.com/p/AAA/" {
		t.Fatalf("post0 url wrong: %q", p0.URL)
	}
	if snap.Posts[1].Views != 9000 || snap.Posts[1].Shares != 60 {
		t.Fatalf("post1 enrichment wrong: %+v", snap.Posts[1])
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
	// Order: media1's comment links to media1, media2's to media2.
	if snap.Comments[0].PostID != "media1" || snap.Comments[1].PostID != "media2" {
		t.Fatalf("comment linkage order wrong: %+v", snap.Comments)
	}
	if snap.Comments[0].AuthorID != "user_a" {
		t.Fatalf("comment author id = %q, want user_a", snap.Comments[0].AuthorID)
	}
}

func TestFetchRequiresToken(t *testing.T) {
	t.Parallel()
	c := newTestConnector(t, newStubDoer(t, map[string][]stubResponse{}))

	tests := []struct {
		name  string
		token *connector.OAuthToken
	}{
		{name: "nil token", token: nil},
		{name: "expired token", token: &connector.OAuthToken{
			AccessToken: "x", Expiry: testNow().Add(-time.Hour),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := c.Fetch(context.Background(), connector.FetchRequest{
				Handle: "@x", AccountID: testAccountID, Token: tt.token,
			})
			if errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("want KindUnauthorized, got %v (%v)", errs.KindOf(err), err)
			}
		})
	}
}

func TestFetchRequiresAccountID(t *testing.T) {
	t.Parallel()
	c := newTestConnector(t, newStubDoer(t, map[string][]stubResponse{}))

	_, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle: "@x", Token: validToken(),
	})
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want KindInvalid, got %v (%v)", errs.KindOf(err), err)
	}
}

func TestFetchErrorClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		resp      stubResponse
		wantQuota bool
		wantRate  bool
		wantKind  errs.Kind
		wantRetry time.Duration
	}{
		{
			name:      "code 4 application rate limit is quota",
			resp:      stubResponse{status: http.StatusBadRequest, body: appRateLimitBody},
			wantQuota: true,
		},
		{
			name:      "code 32 page rate limit is quota",
			resp:      stubResponse{status: http.StatusBadRequest, body: pageRateLimitBody},
			wantQuota: true,
		},
		{
			name:     "code 17 user rate limit is rate",
			resp:     stubResponse{status: http.StatusBadRequest, body: userRateLimitBody},
			wantRate: true,
		},
		{
			name:     "code 613 custom rate limit is rate",
			resp:     stubResponse{status: http.StatusBadRequest, body: customRateLimitBody},
			wantRate: true,
		},
		{
			name: "business-use-case regain header sets retry-after",
			resp: stubResponse{
				status: http.StatusBadRequest, body: userRateLimitBody,
				header: http.Header{"X-Business-Use-Case-Usage": {`{"123":[{"type":"instagram","estimated_time_to_regain_access":19}]}`}},
			},
			wantRate:  true,
			wantRetry: 19 * time.Minute,
		},
		{
			name:     "http 429 is rate",
			resp:     stubResponse{status: http.StatusTooManyRequests, body: `{"error":{"message":"too many","code":0}}`},
			wantRate: true,
		},
		{
			name:     "code 190 invalid token is unauthorized",
			resp:     stubResponse{status: http.StatusBadRequest, body: tokenInvalidBody},
			wantKind: errs.KindUnauthorized,
		},
		{
			name:     "code 10 permission denied is forbidden",
			resp:     stubResponse{status: http.StatusForbidden, body: permissionBody},
			wantKind: errs.KindForbidden,
		},
		{
			name:     "code 100 invalid parameter is invalid",
			resp:     stubResponse{status: http.StatusBadRequest, body: invalidParamBody},
			wantKind: errs.KindInvalid,
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
			doer := newStubDoer(t, map[string][]stubResponse{"user": {tt.resp}})
			c := newTestConnector(t, doer)

			_, err := c.Fetch(context.Background(), connector.FetchRequest{
				Handle: "@x", AccountID: testAccountID, Token: validToken(),
			})
			if err == nil {
				t.Fatal("want error, got nil")
			}
			// The secret access token must never appear in an error surfaced to callers.
			if strings.Contains(err.Error(), "SECRET_TOKEN_VALUE") {
				t.Fatalf("error leaked access token: %v", err)
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
				if tt.wantRetry != 0 && r.RetryAfter != tt.wantRetry {
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
	// Profile, media, insights succeed; the first comments call is quota-exhausted.
	doer := newStubDoer(t, map[string][]stubResponse{
		"user":     {ok(userBody)},
		"media":    {ok(mediaBodyTwo)},
		"insights": {ok(insightsBodyMedia1), ok(insightsBodyMedia2)},
		"comments": {{status: http.StatusBadRequest, body: appRateLimitBody}},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:    "@x",
		AccountID: testAccountID,
		Token:     validToken(),
		Capabilities: []connector.Capability{
			connector.CapabilityProfile, connector.CapabilityMetrics,
			connector.CapabilityRecentPosts, connector.CapabilityComments,
		},
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
	if snap.Followers != 152340 {
		t.Fatalf("profile should survive: %d", snap.Followers)
	}
	if len(snap.Comments) != 0 {
		t.Fatalf("no comments should be gathered: %d", len(snap.Comments))
	}
}

func TestFetchPartialOnInsightsRateLimit(t *testing.T) {
	t.Parallel()
	// Profile and media succeed; the first insights call is user-rate-limited.
	doer := newStubDoer(t, map[string][]stubResponse{
		"user":     {ok(userBody)},
		"media":    {ok(mediaBodyTwo)},
		"insights": {{status: http.StatusBadRequest, body: userRateLimitBody}},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:    "@x",
		AccountID: testAccountID,
		Token:     validToken(),
		Capabilities: []connector.Capability{
			connector.CapabilityMetrics, connector.CapabilityRecentPosts, connector.CapabilityComments,
		},
	})
	if err != nil {
		t.Fatalf("insights rate-limit should degrade, not fail: %v", err)
	}
	if !snap.Partial {
		t.Fatal("snapshot should be partial")
	}
	if len(snap.Posts) != 2 {
		t.Fatalf("posts should survive: %d", len(snap.Posts))
	}
	// Comments phase must not run after a partial return.
	if doer.calls["comments"] != 0 {
		t.Fatalf("comments should not be fetched after partial: %d", doer.calls["comments"])
	}
}

func TestFetchInsightsUnavailableSkipsMedia(t *testing.T) {
	t.Parallel()
	// media1 has no insights (not fatal); media2 enriches normally.
	doer := newStubDoer(t, map[string][]stubResponse{
		"user":  {ok(userBody)},
		"media": {ok(mediaBodyTwo)},
		"insights": {
			{status: http.StatusBadRequest, body: insightsUnavailableBody},
			ok(insightsBodyMedia2),
		},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@x",
		AccountID:    testAccountID,
		Token:        validToken(),
		Capabilities: []connector.Capability{connector.CapabilityMetrics, connector.CapabilityRecentPosts},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Partial {
		t.Fatal("insights-unavailable on one media should not mark the snapshot partial")
	}
	if snap.Posts[0].Views != 0 || snap.Posts[0].Shares != 0 {
		t.Fatalf("skipped media should keep zero insight fields: %+v", snap.Posts[0])
	}
	if snap.Posts[1].Views != 9000 || snap.Posts[1].Shares != 60 {
		t.Fatalf("second media should enrich: %+v", snap.Posts[1])
	}
	// Base followers/media_count plus only media2's reach/saved.
	if len(snap.Metrics) != 4 {
		t.Fatalf("metrics = %d, want 4", len(snap.Metrics))
	}
}

func TestFetchPartialOnAudience(t *testing.T) {
	t.Parallel()
	doer := newStubDoer(t, map[string][]stubResponse{
		"user": {ok(userBody)},
	})
	c := newTestConnector(t, doer)

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@x",
		AccountID:    testAccountID,
		Token:        validToken(),
		Capabilities: []connector.Capability{connector.CapabilityProfile, connector.CapabilityAudienceBreakdown},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !snap.Partial {
		t.Fatal("audience request the connector cannot serve should be partial")
	}
	if snap.Audience != nil {
		t.Fatal("audience must be nil, never fabricated")
	}
	if doer.calls["media"]+doer.calls["insights"]+doer.calls["comments"] != 0 {
		t.Fatalf("audience-only fetch spent extra calls: %+v", doer.calls)
	}
}

func TestFetchContextCancelledUpfront(t *testing.T) {
	t.Parallel()
	doer := newStubDoer(t, map[string][]stubResponse{})
	c := newTestConnector(t, doer)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Fetch(ctx, connector.FetchRequest{
		Handle: "@x", AccountID: testAccountID, Token: validToken(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if doer.calls["user"] != 0 {
		t.Fatal("cancelled fetch should issue no calls")
	}
}

func TestFetchContextCancelledMidFetch(t *testing.T) {
	t.Parallel()
	// user resolves, but the hook cancels ctx right after, so the media phase
	// must observe cancellation and issue no further calls.
	doer := newStubDoer(t, map[string][]stubResponse{
		"user":  {ok(userBody)},
		"media": {ok(mediaBodyTwo)},
	})
	c := newTestConnector(t, doer)

	ctx, cancel := context.WithCancel(context.Background())
	doer.hook = func(endpoint string, _ *http.Request) {
		if endpoint == "user" {
			cancel()
		}
	}

	_, err := c.Fetch(ctx, connector.FetchRequest{
		Handle:       "@x",
		AccountID:    testAccountID,
		Token:        validToken(),
		Capabilities: []connector.Capability{connector.CapabilityRecentPosts},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if doer.calls["media"] != 0 {
		t.Fatal("media phase should not run after cancellation")
	}
}

func TestFetchCommentsHonorTotalCap(t *testing.T) {
	t.Parallel()
	// maxComments = 1 stops after media1's single comment even though media2 also
	// has a comment available.
	doer := newStubDoer(t, map[string][]stubResponse{
		"user":     {ok(userBody)},
		"media":    {ok(mediaBodyTwo)},
		"insights": {ok(insightsBodyMedia1), ok(insightsBodyMedia2)},
		"comments": {ok(commentsBodyMedia1)},
	})
	c, err := New(Config{
		BaseURL:                 "https://api.example/v21.0",
		HTTP:                    doer,
		MaxComments:             1,
		MaxCommentPagesPerMedia: 2,
		Now:                     testNow,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	snap, err := c.Fetch(context.Background(), connector.FetchRequest{
		Handle:       "@x",
		AccountID:    testAccountID,
		Token:        validToken(),
		Capabilities: []connector.Capability{connector.CapabilityRecentPosts, connector.CapabilityComments},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Comments) != 1 {
		t.Fatalf("total comment cap not honored: got %d", len(snap.Comments))
	}
	if doer.calls["comments"] != 1 {
		t.Fatalf("cap should stop after one comments call, got %d", doer.calls["comments"])
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
		{name: "retry-after seconds", header: http.Header{"Retry-After": {"45"}}, want: 45 * time.Second},
		{name: "retry-after zero", header: http.Header{"Retry-After": {"0"}}, want: 0},
		{name: "retry-after garbage", header: http.Header{"Retry-After": {"soon"}}, want: 0},
		{
			name:   "business use case regain minutes",
			header: http.Header{"X-Business-Use-Case-Usage": {`{"123":[{"estimated_time_to_regain_access":12}]}`}},
			want:   12 * time.Minute,
		},
		{
			name:   "regain header preferred over retry-after",
			header: http.Header{"Retry-After": {"5"}, "X-Business-Use-Case-Usage": {`{"123":[{"estimated_time_to_regain_access":30}]}`}},
			want:   30 * time.Minute,
		},
		{
			name:   "regain header malformed falls through to retry-after",
			header: http.Header{"Retry-After": {"5"}, "X-Business-Use-Case-Usage": {`not json`}},
			want:   5 * time.Second,
		},
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

func TestParseTime(t *testing.T) {
	t.Parallel()
	if !parseTime("").IsZero() || !parseTime("nonsense").IsZero() {
		t.Fatal("parseTime should be zero on bad input")
	}
	if got := parseTime("2021-09-01T12:00:00+0000"); got.IsZero() {
		t.Fatal("parseTime should parse the Graph API offset layout")
	}
	if got := parseTime("2021-09-01T12:00:00Z"); got.IsZero() {
		t.Fatal("parseTime should parse RFC3339 as a fallback")
	}
}
