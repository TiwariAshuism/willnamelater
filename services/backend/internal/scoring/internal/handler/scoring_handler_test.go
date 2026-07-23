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

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
)

// fakeService is a configurable ScoringService stand-in for the transport tests.
type fakeService struct {
	latest  model.ScoreResponse
	history model.ScoreHistoryResponse
	err     error
}

func (f *fakeService) GetLatestScore(context.Context, string) (model.ScoreResponse, error) {
	return f.latest, f.err
}

func (f *fakeService) GetScoreHistory(context.Context, string) (model.ScoreHistoryResponse, error) {
	return f.history, f.err
}

func newRouter(svc *fakeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).Register(r)
	return r
}

func do(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const validUUID = "6f9619ff-8b86-d011-b42d-00cf4fc964ff"

func TestGetLatestScoreOK(t *testing.T) {
	t.Parallel()

	svc := &fakeService{latest: model.ScoreResponse{AuditJobID: "job", Overall: 80}}
	rec := do(newRouter(svc), "/influencers/"+validUUID+"/score")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestGetScoreHistoryOK(t *testing.T) {
	t.Parallel()

	rec := do(newRouter(&fakeService{}), "/influencers/"+validUUID+"/score/history")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestErrorKindMapsToStatus(t *testing.T) {
	t.Parallel()

	svc := &fakeService{err: errs.New(errs.KindNotFound, "scoring.score_not_found", "no score exists for this influencer")}
	rec := do(newRouter(svc), "/influencers/"+validUUID+"/score")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var env struct {
		Error errs.Error `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "scoring.score_not_found" {
		t.Fatalf("code = %q", env.Error.Code)
	}
}

// TestWrappedCauseNeverLeaks is the required security assertion: a service error
// wrapping a secret-bearing cause renders as its safe code and message only —
// neither the cause nor the secret appears in the response body.
func TestWrappedCauseNeverLeaks(t *testing.T) {
	t.Parallel()

	const secret = "postgres://admin:sup3r-s3cret@10.0.0.5:5432/influ"
	cause := errors.New("dial " + secret + ": connection refused")
	wrapped := errs.Wrap(cause, errs.KindUnavailable, "scoring.query_history", "could not read score history")

	rec := do(newRouter(&fakeService{err: wrapped}), "/influencers/"+validUUID+"/score/history")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, secret) || strings.Contains(body, "connection refused") {
		t.Fatalf("response leaked the wrapped cause: %s", body)
	}
	var env struct {
		Error errs.Error `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Message != "could not read score history" {
		t.Fatalf("message = %q, want the safe domain message", env.Error.Message)
	}
}
