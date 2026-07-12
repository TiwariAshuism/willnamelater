package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeDoer is a test double for the injected Doer. It records the request it
// received and returns a canned response built from status+body, or an error,
// so no test touches the network. The response is constructed inside Do so the
// only closer of its body is the client under test.
type fakeDoer struct {
	status  int
	body    string
	err     error
	gotReq  *http.Request
	gotBody []byte
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.gotReq = req
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		f.gotBody = b
	}
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Header:     make(http.Header),
	}, nil
}

// sampleSnapshot is an API-shape fixture, not captured user data: it exercises
// the mapping from every Snapshot field the builders read. Times are UTC so a
// JSON round-trip reproduces them exactly.
func sampleSnapshot() connector.Snapshot {
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	return connector.Snapshot{
		Platform:  connector.PlatformYouTube,
		Handle:    "@example",
		AccountID: "chan-1",
		Followers: 1000,
		Metrics: []connector.MetricPoint{
			{At: at, Name: "subscribers", Value: 1000},
			{At: at.AddDate(0, 0, 1), Name: "subscribers", Value: 1050},
			{At: at, Name: "following", Value: 42},
			{At: at, Name: "views", Value: 500000},
		},
		Posts: []connector.Post{
			{ID: "v1", PublishedAt: at, Likes: 10, Comments: 3, Views: 900},
			{ID: "v2", PublishedAt: at.AddDate(0, 0, 2), Likes: 20, Comments: 5, Views: 0},
		},
		Comments: []connector.Comment{
			{PostID: "v1", AuthorID: "u1", Text: "great", At: at},
			{PostID: "v1", AuthorID: "u2", Text: "nice", At: at},
			{PostID: "v2", AuthorID: "u1", Text: "cool", At: at},
		},
	}
}

func TestScoreFraudEncodesRequest(t *testing.T) {
	snap := sampleSnapshot()
	want := BuildFraudRequest(snap)
	wantBody, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}

	doer := &fakeDoer{status: http.StatusOK, body: `{
		"score": 12.5, "confidence": 0.3, "model_version": "heuristics-0.1",
		"estimate": true, "observed": true, "signals": [], "flags": [],
		"generated_at": "2026-07-01T12:00:00Z"
	}`}
	client := New("http://ml.internal/", doer)

	resp, err := client.ScoreFraud(context.Background(), want)
	if err != nil {
		t.Fatalf("ScoreFraud: %v", err)
	}

	if doer.gotReq.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", doer.gotReq.Method)
	}
	if got := doer.gotReq.URL.String(); got != "http://ml.internal/v1/fraud/score" {
		t.Errorf("url = %q, want .../v1/fraud/score", got)
	}
	if ct := doer.gotReq.Header.Get("Content-Type"); ct != contentTypeJSON {
		t.Errorf("content-type = %q, want %q", ct, contentTypeJSON)
	}
	if !bytes.Equal(doer.gotBody, wantBody) {
		t.Errorf("request body mismatch\n got: %s\nwant: %s", doer.gotBody, wantBody)
	}
	// Score is a POINTER now: a number the service could compute decodes to a
	// non-nil 12.5 with Observed true. (It was a bare float64, which made an
	// unobservable account and a genuinely clean one decode identically as 0.)
	if resp.Score == nil || *resp.Score != 12.5 {
		t.Errorf("score = %v, want 12.5", resp.Score)
	}
	if !resp.Observed {
		t.Error("observed = false, want true when the service returned a score")
	}
	if resp.ModelVersion != "heuristics-0.1" || !resp.Estimate {
		t.Errorf("decoded response = %+v, want heuristics-0.1 / estimate", resp)
	}
}

// TestScoreFraudNullScoreIsNotZero is the honest-absence guarantee on the wire: an
// account for which NOT ONE signal could be computed comes back with a null score
// and observed=false. It must decode to a nil Score, never a 0 — a 0 on a 0-100
// risk scale is a perfectly clean account, the exact opposite of an unexamined one.
func TestScoreFraudNullScoreIsNotZero(t *testing.T) {
	doer := &fakeDoer{status: http.StatusOK, body: `{
		"score": null, "confidence": 0.0, "model_version": "heuristics-0.1",
		"estimate": true, "observed": false, "signals": [], "flags": [],
		"generated_at": "2026-07-01T12:00:00Z"
	}`}
	client := New("http://ml.internal", doer)

	resp, err := client.ScoreFraud(context.Background(), BuildFraudRequest(sampleSnapshot()))
	if err != nil {
		t.Fatalf("ScoreFraud: %v", err)
	}
	if resp.Score != nil {
		t.Errorf("score = %v, want nil for an unobservable account", *resp.Score)
	}
	if resp.Observed {
		t.Error("observed = true, want false when no signal could be computed")
	}
}

func TestBuildFraudRequestMapsSnapshot(t *testing.T) {
	snap := sampleSnapshot()
	req := BuildFraudRequest(snap)

	if req.Account.Handle != "@example" {
		t.Errorf("handle = %q", req.Account.Handle)
	}
	if req.Account.Platform != PlatformYouTube {
		t.Errorf("platform = %q, want youtube", req.Account.Platform)
	}
	if req.Account.FollowerCount != 1000 {
		t.Errorf("follower_count = %d, want 1000", req.Account.FollowerCount)
	}
	if req.Account.FollowingCount != 42 {
		t.Errorf("following_count = %d, want 42 (from 'following' metric)", req.Account.FollowingCount)
	}

	// Only the two "subscribers" points feed the follower series; "following"
	// and "views" points are excluded.
	if len(req.FollowerSeries) != 2 {
		t.Fatalf("follower series len = %d, want 2", len(req.FollowerSeries))
	}
	if req.FollowerSeries[0].Count != 1000 || req.FollowerSeries[1].Count != 1050 {
		t.Errorf("follower series counts = %v, want [1000 1050]", req.FollowerSeries)
	}

	if len(req.Posts) != 2 {
		t.Fatalf("posts len = %d, want 2", len(req.Posts))
	}
	if req.Posts[0].PostID != "v1" || req.Posts[1].PostID != "v2" {
		t.Errorf("post ids = [%q %q], want [v1 v2]", req.Posts[0].PostID, req.Posts[1].PostID)
	}
	if req.Posts[0].Views == nil || *req.Posts[0].Views != 900 {
		t.Errorf("post[0].views = %v, want 900", req.Posts[0].Views)
	}
	if req.Posts[1].Views == nil || *req.Posts[1].Views != 0 {
		t.Errorf("post[1].views = %v, want explicit 0", req.Posts[1].Views)
	}
}

func TestBuildFraudRequestNoFollowingMetricDefaultsZero(t *testing.T) {
	snap := connector.Snapshot{
		Platform: connector.PlatformInstagram,
		Handle:   "h",
		Metrics:  []connector.MetricPoint{{Name: "followers", Value: 5, At: time.Unix(0, 0).UTC()}},
	}
	req := BuildFraudRequest(snap)
	if req.Account.FollowingCount != 0 {
		t.Errorf("following_count = %d, want 0 when no 'following' metric", req.Account.FollowingCount)
	}
	if len(req.FollowerSeries) != 1 || req.FollowerSeries[0].Count != 5 {
		t.Errorf("follower series = %v, want one point of 5", req.FollowerSeries)
	}
}

// TestBuildPodsRequestPreservesPostID is the load-bearing assertion for the
// co-commenter graph: every comment's PostID must survive assembly, or comments
// cannot be joined to posts and per-post coordination features are unrecoverable.
func TestBuildPodsRequestPreservesPostID(t *testing.T) {
	snap := sampleSnapshot()
	req := BuildPodsRequest(snap)

	if len(req.Events) != len(snap.Comments) {
		t.Fatalf("events len = %d, want %d", len(req.Events), len(snap.Comments))
	}
	for i, ev := range req.Events {
		if ev.PostID != snap.Comments[i].PostID {
			t.Errorf("event[%d].post_id = %q, want %q", i, ev.PostID, snap.Comments[i].PostID)
		}
		if ev.Commenter != snap.Comments[i].AuthorID {
			t.Errorf("event[%d].commenter = %q, want %q", i, ev.Commenter, snap.Comments[i].AuthorID)
		}
		if ev.Text != snap.Comments[i].Text {
			t.Errorf("event[%d].text = %q, want %q", i, ev.Text, snap.Comments[i].Text)
		}
	}
	if req.MinPodSize != defaultMinPodSize || req.MinSharedPosts != defaultMinSharedPosts {
		t.Errorf("clique params = (%d,%d), want (%d,%d)",
			req.MinPodSize, req.MinSharedPosts, defaultMinPodSize, defaultMinSharedPosts)
	}
}

// TestDetectPodsPostIDSurvivesWire confirms PostID survives not just assembly
// but JSON encoding onto the wire as post_id.
func TestDetectPodsPostIDSurvivesWire(t *testing.T) {
	snap := sampleSnapshot()
	doer := &fakeDoer{status: http.StatusOK, body: `{
		"pods": [], "commenters_analyzed": 3, "confidence": 0.2,
		"model_version": "pods-0.1", "estimate": true, "generated_at": "2026-07-01T12:00:00Z"
	}`}
	client := New("http://ml.internal", doer)

	if _, err := client.DetectPods(context.Background(), BuildPodsRequest(snap)); err != nil {
		t.Fatalf("DetectPods: %v", err)
	}

	var sent PodsDetectRequest
	if err := json.Unmarshal(doer.gotBody, &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v", err)
	}
	if len(sent.Events) != 3 {
		t.Fatalf("sent events = %d, want 3", len(sent.Events))
	}
	wantPostIDs := []string{"v1", "v1", "v2"}
	for i, ev := range sent.Events {
		if ev.PostID != wantPostIDs[i] {
			t.Errorf("sent event[%d].post_id = %q, want %q", i, ev.PostID, wantPostIDs[i])
		}
	}
	// The raw payload must carry the snake_case key the service parses.
	if !bytes.Contains(doer.gotBody, []byte(`"post_id"`)) {
		t.Errorf("wire payload missing post_id key: %s", doer.gotBody)
	}
}

func TestClassifyCommentsDecodesResponse(t *testing.T) {
	doer := &fakeDoer{status: http.StatusOK, body: `{
		"classifications": [{"id": "c1", "label": "generic", "confidence": 0.5, "signals": ["short"]}],
		"low_quality_ratio": 1.0, "confidence": 0.3, "model_version": "comments-0.1",
		"estimate": true, "generated_at": "2026-07-01T12:00:00Z"
	}`}
	client := New("http://ml.internal", doer)

	resp, err := client.ClassifyComments(context.Background(), CommentsClassifyRequest{
		Comments: []CommentItem{{ID: "c1", Text: "nice"}},
	})
	if err != nil {
		t.Fatalf("ClassifyComments: %v", err)
	}
	if len(resp.Classifications) != 1 || resp.Classifications[0].Label != CommentLabelGeneric {
		t.Errorf("classifications = %+v, want one 'generic'", resp.Classifications)
	}
	if resp.LowQualityRatio != 1.0 {
		t.Errorf("low_quality_ratio = %v, want 1.0", resp.LowQualityRatio)
	}
}

func TestErrorEnvelopeMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantKind errs.Kind
		wantCode string
	}{
		{"bad request", http.StatusBadRequest, errs.KindInvalid, "ml.invalid"},
		{"unprocessable", http.StatusUnprocessableEntity, errs.KindInvalid, "ml.invalid"},
		{"unauthorized", http.StatusUnauthorized, errs.KindUnauthorized, "ml.unauthorized"},
		{"forbidden", http.StatusForbidden, errs.KindForbidden, "ml.forbidden"},
		{"not found", http.StatusNotFound, errs.KindNotFound, "ml.not_found"},
		{"conflict", http.StatusConflict, errs.KindConflict, "ml.conflict"},
		{"payment required", http.StatusPaymentRequired, errs.KindQuotaExceeded, "ml.quota_exceeded"},
		{"too many requests", http.StatusTooManyRequests, errs.KindRateLimited, "ml.rate_limited"},
		{"internal", http.StatusInternalServerError, errs.KindUnavailable, "ml.unavailable"},
		{"service unavailable", http.StatusServiceUnavailable, errs.KindUnavailable, "ml.unavailable"},
		{"unmapped teapot", http.StatusTeapot, errs.KindUnavailable, "ml.unavailable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"code": "ml.server_detail", "message": "SECRET internal trace"}`
			doer := &fakeDoer{status: tc.status, body: body}
			client := New("http://ml.internal", doer)

			_, err := client.ScoreFraud(context.Background(), FraudScoreRequest{})
			if err == nil {
				t.Fatalf("expected error for status %d", tc.status)
			}
			if got := errs.KindOf(err); got != tc.wantKind {
				t.Errorf("kind = %d, want %d", got, tc.wantKind)
			}

			var e *errs.Error
			if !errors.As(err, &e) {
				t.Fatalf("error is not *errs.Error: %v", err)
			}
			if e.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", e.Code, tc.wantCode)
			}
			// The ML service's raw message must never become the client-facing
			// Message; it lives only in the wrapped cause (for logs).
			if strings.Contains(e.Message, "SECRET") {
				t.Errorf("client-facing message leaked ml detail: %q", e.Message)
			}
			if !strings.Contains(err.Error(), "SECRET") {
				t.Errorf("cause should retain ml detail for logs, got: %v", err)
			}
		})
	}
}

func TestTransportFailureMapsToUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"timeout", context.DeadlineExceeded},
		{"connection refused", errors.New("dial tcp: connection refused")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doer := &fakeDoer{err: tc.err}
			client := New("http://ml.internal", doer)

			_, err := client.ScoreFraud(context.Background(), FraudScoreRequest{})
			if err == nil {
				t.Fatal("expected error on transport failure")
			}
			if got := errs.KindOf(err); got != errs.KindUnavailable {
				t.Errorf("kind = %d, want KindUnavailable so the audit degrades to partial", got)
			}
		})
	}
}

func TestDecodeFailureIsInternal(t *testing.T) {
	doer := &fakeDoer{status: http.StatusOK, body: `{not json`}
	client := New("http://ml.internal", doer)

	_, err := client.ScoreFraud(context.Background(), FraudScoreRequest{})
	if err == nil {
		t.Fatal("expected decode error")
	}
	if got := errs.KindOf(err); got != errs.KindInternal {
		t.Errorf("kind = %d, want KindInternal for a malformed 200 body", got)
	}
}
