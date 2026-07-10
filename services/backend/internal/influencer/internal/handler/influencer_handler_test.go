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

	"github.com/getnyx/influaudit/backend/internal/influencer/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeService is a configurable InfluencerService stand-in for the transport
// tests. Each field is the outcome the corresponding endpoint should produce.
type fakeService struct {
	infResp    model.InfluencerResponse
	listResp   model.ListInfluencersResponse
	handleResp model.HandleResponse
	err        error
}

func (f *fakeService) CreateInfluencer(context.Context, model.CreateInfluencerRequest) (model.InfluencerResponse, error) {
	return f.infResp, f.err
}

func (f *fakeService) GetInfluencer(context.Context, string) (model.InfluencerResponse, error) {
	return f.infResp, f.err
}

func (f *fakeService) ListInfluencers(context.Context, model.ListInfluencersRequest) (model.ListInfluencersResponse, error) {
	return f.listResp, f.err
}

func (f *fakeService) UpdateInfluencer(context.Context, string, model.UpdateInfluencerRequest) (model.InfluencerResponse, error) {
	return f.infResp, f.err
}

func (f *fakeService) AddHandle(context.Context, string, model.AddHandleRequest) (model.HandleResponse, error) {
	return f.handleResp, f.err
}

func (f *fakeService) DeleteHandle(context.Context, string, string) error {
	return f.err
}

func newRouter(svc *fakeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreateInfluencerSuccess(t *testing.T) {
	t.Parallel()

	svc := &fakeService{infResp: model.InfluencerResponse{ID: "abc"}}
	rec := do(newRouter(svc), http.MethodPost, "/influencers", `{"display_name":"x"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestDeleteHandleSuccessNoContent(t *testing.T) {
	t.Parallel()

	rec := do(newRouter(&fakeService{}), http.MethodDelete, "/influencers/1/handles/2", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestMalformedBodyIsBadRequest(t *testing.T) {
	t.Parallel()

	rec := do(newRouter(&fakeService{}), http.MethodPost, "/influencers", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var env struct {
		Error errs.Error `json:"error"`
	}
	decodeEnvelope(t, rec, &env)
	if env.Error.Code != "influencer.request_invalid" {
		t.Fatalf("code = %q, want influencer.request_invalid", env.Error.Code)
	}
}

func TestErrorKindMapsToStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "not found",
			err:        errs.New(errs.KindNotFound, "influencer.not_found", "influencer does not exist"),
			wantStatus: http.StatusNotFound,
			wantCode:   "influencer.not_found",
		},
		{
			name:       "conflict",
			err:        errs.New(errs.KindConflict, "influencer.handle_conflict", "already exists"),
			wantStatus: http.StatusConflict,
			wantCode:   "influencer.handle_conflict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(newRouter(&fakeService{err: tt.err}), http.MethodGet, "/influencers/"+validUUID, "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var env struct {
				Error errs.Error `json:"error"`
			}
			decodeEnvelope(t, rec, &env)
			if env.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", env.Error.Code, tt.wantCode)
			}
		})
	}
}

// TestWrappedCauseNeverLeaks is the security assertion required by the task: a
// service error that wraps a cause carrying a secret must render as its safe
// Message and stable Code only. Neither the cause nor the secret may appear in
// the response body.
func TestWrappedCauseNeverLeaks(t *testing.T) {
	t.Parallel()

	const secret = "postgres://admin:sup3r-s3cret@10.0.0.5:5432/influ"
	cause := errors.New("dial " + secret + ": connection refused")
	wrapped := errs.Wrap(cause, errs.KindInternal, "influencer.get_failed", "could not load influencer")

	rec := do(newRouter(&fakeService{err: wrapped}), http.MethodGet, "/influencers/"+validUUID, "")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("response body leaked the secret: %s", body)
	}
	if strings.Contains(body, "connection refused") {
		t.Fatalf("response body leaked the wrapped cause: %s", body)
	}

	var env struct {
		Error errs.Error `json:"error"`
	}
	decodeEnvelope(t, rec, &env)
	if env.Error.Message != "could not load influencer" {
		t.Fatalf("message = %q, want the safe domain message", env.Error.Message)
	}
}

const validUUID = "6f9619ff-8b86-d011-b42d-00cf4fc964ff"

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response envelope: %v (body=%s)", err, rec.Body.String())
	}
}
