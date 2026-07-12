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

	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeService is a configurable MLOpsService stand-in for the transport tests.
type fakeService struct {
	export    model.FeatureRowExportResponse
	register  model.RegisterModelResponse
	promote   model.PromoteModelResponse
	canaries  model.CanaryListResponse
	canary    model.CanaryResponse
	predict   model.PredictionLogResponse
	err       error
	lastQuery model.FeatureRowQuery
}

func (f *fakeService) ExportFeatureRows(_ context.Context, req model.FeatureRowQuery) (model.FeatureRowExportResponse, error) {
	f.lastQuery = req
	return f.export, f.err
}

func (f *fakeService) RegisterModel(context.Context, model.RegisterModelRequest) (model.RegisterModelResponse, error) {
	return f.register, f.err
}

func (f *fakeService) PromoteModel(context.Context, string, model.PromoteModelRequest) (model.PromoteModelResponse, error) {
	return f.promote, f.err
}

func (f *fakeService) ListCanaries(context.Context, model.CanaryQuery) (model.CanaryListResponse, error) {
	return f.canaries, f.err
}

func (f *fakeService) CreateCanary(context.Context, model.CreateCanaryRequest) (model.CanaryResponse, error) {
	return f.canary, f.err
}

func (f *fakeService) IngestPrediction(context.Context, model.PredictionLogRequest) (model.PredictionLogResponse, error) {
	return f.predict, f.err
}

func newRouter(svc *fakeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc)
	h.RegisterAdmin(r)
	h.RegisterService(r)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The security-critical transport test: a service error whose wrapped cause
// carries a secret must render only the domain code and message, never the cause.
func TestRegister_WrappedCauseNeverLeaks(t *testing.T) {
	const secret = "postgres://user:sup3rs3cret@db:5432/influaudit"
	svc := &fakeService{err: errs.Wrap(errors.New(secret), errs.KindInternal, "mlops.register_failed", "could not record the challenger")}
	r := newRouter(svc)

	// A body that binds (every required field present) so the service's wrapped
	// error is what renders, not a bind failure.
	body := `{"model_name":"fraud","version":"lgbm-x","manifest":{},"model_file_name":"model.txt",` +
		`"model_file_b64":"eA==","validation_report":{},"data_floor_counts":{}}`
	rec := do(r, http.MethodPost, "/admin/mlops/models", body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "sup3rs3cret") {
		t.Fatalf("wrapped cause leaked to the client: %s", rec.Body.String())
	}
}

func TestExport_ParsesQuery(t *testing.T) {
	svc := &fakeService{}
	r := newRouter(svc)
	rec := do(r, http.MethodGet, "/admin/mlops/feature-rows?quality=all&limit=42&since=2026-07-10T00:00:00Z", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if svc.lastQuery.Quality != "all" || svc.lastQuery.Limit != 42 || svc.lastQuery.Since.IsZero() {
		t.Fatalf("query not parsed: %+v", svc.lastQuery)
	}
}

func TestExport_RejectsBadSince(t *testing.T) {
	r := newRouter(&fakeService{})
	rec := do(r, http.MethodGet, "/admin/mlops/feature-rows?since=yesterday", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed since must be 400, got %d", rec.Code)
	}
}

func TestIngest_Returns202(t *testing.T) {
	svc := &fakeService{predict: model.PredictionLogResponse{Accepted: true}}
	r := newRouter(svc)
	body := `{"model_name":"fraud","champion_version":"v","features_hash":"h"}`
	rec := do(r, http.MethodPost, "/ml/predictions", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest must return 202, got %d", rec.Code)
	}
	var resp model.PredictionLogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.Accepted {
		t.Fatalf("ingest response wrong: %s", rec.Body.String())
	}
}

func TestPromote_Returns200(t *testing.T) {
	svc := &fakeService{promote: model.PromoteModelResponse{ModelName: "fraud", ChampionVersion: "lgbm-x"}}
	r := newRouter(svc)
	rec := do(r, http.MethodPost, "/admin/mlops/models/lgbm-x/promote", `{"model_name":"fraud"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote must return 200, got %d", rec.Code)
	}
}
