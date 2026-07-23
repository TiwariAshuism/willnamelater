package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

type fakeService struct {
	metricsResp model.MetricSeriesResponse
	metricsErr  error
	postsResp   []model.PostResponse
	postsErr    error
	summaryResp model.ProfileSummaryResponse
	summaryErr  error
}

func (f *fakeService) GetInfluencerMetrics(context.Context, string, model.MetricSeriesRequest) (model.MetricSeriesResponse, error) {
	return f.metricsResp, f.metricsErr
}

func (f *fakeService) ListInfluencerPosts(context.Context, string, model.ListPostsRequest) ([]model.PostResponse, error) {
	return f.postsResp, f.postsErr
}

func (f *fakeService) GetInfluencerProfileSummary(context.Context, string) (model.ProfileSummaryResponse, error) {
	return f.summaryResp, f.summaryErr
}

func newRouter(svc *fakeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).Register(r)
	return r
}

func do(r *gin.Engine, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestGetInfluencerMetricsOK(t *testing.T) {
	svc := &fakeService{metricsResp: model.MetricSeriesResponse{
		InfluencerID: "abc",
		Series:       []model.MetricSeries{{Platform: "youtube", Metric: "subscribers"}},
	}}
	rec := do(newRouter(svc), "/influencers/abc/metrics?metric=subscribers")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got model.MetricSeriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.InfluencerID != "abc" || len(got.Series) != 1 {
		t.Fatalf("body = %+v", got)
	}
}

func TestGetInfluencerMetricsMapsDomainError(t *testing.T) {
	svc := &fakeService{metricsErr: errs.New(errs.KindInvalid, "metrics.invalid_influencer_id", "influencer id must be a uuid")}
	rec := do(newRouter(svc), "/influencers/bad/metrics")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Error.Code != "metrics.invalid_influencer_id" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

// TestHandlerNeverLeaksWrappedCause is the security guarantee: a cause carrying
// a secret must never reach the client, only the client-safe message.
func TestHandlerNeverLeaksWrappedCause(t *testing.T) {
	const secret = "postgres://user:sup3rSECRETpw@db:5432"
	cause := errors.New("dial " + secret)
	svc := &fakeService{metricsErr: errs.Wrap(cause, errs.KindUnavailable, "metrics.query_metrics", "could not read metric series")}

	rec := do(newRouter(svc), "/influencers/abc/metrics")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response leaked the wrapped cause: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "dial ") {
		t.Fatalf("response leaked cause text: %s", rec.Body.String())
	}
}

func TestListInfluencerPostsEmptyIsArray(t *testing.T) {
	svc := &fakeService{postsResp: []model.PostResponse{}}
	rec := do(newRouter(svc), "/influencers/abc/posts")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty posts must render as [], got %s", rec.Body.String())
	}
}

func TestListInfluencerPostsInvalidQuery(t *testing.T) {
	svc := &fakeService{}
	// limit is an int; a non-numeric value fails binding before the service runs.
	rec := do(newRouter(svc), "/influencers/abc/posts?limit=notanumber")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

func TestGetInfluencerProfileSummaryOK(t *testing.T) {
	er := 0.031
	svc := &fakeService{summaryResp: model.ProfileSummaryResponse{
		InfluencerID: "abc",
		MetricsStrip: model.MetricsStrip{EngagementRate: &er},
		Readiness:    model.Readiness{Fraction: 0.5, Fields: []model.ReadinessField{{Field: "profile", Present: true}}},
	}}
	rec := do(newRouter(svc), "/influencers/abc/profile-summary")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got model.ProfileSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.InfluencerID != "abc" || got.MetricsStrip.EngagementRate == nil || got.Readiness.Fraction != 0.5 {
		t.Fatalf("body = %+v", got)
	}
}

func TestGetInfluencerProfileSummaryMapsDomainError(t *testing.T) {
	svc := &fakeService{summaryErr: errs.New(errs.KindInvalid, "metrics.invalid_influencer_id", "influencer id must be a uuid")}
	rec := do(newRouter(svc), "/influencers/bad/profile-summary")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
