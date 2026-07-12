// Package handler is the mlops module's HTTP transport. Handlers are thin: each
// parses the request (query string or JSON body), calls the service, and renders
// — every error goes through httpx.RenderError so a wrapped cause can never reach
// the client. The generated apigen handler is deliberately not used because it
// renders every error as a 500 with the raw error string.
//
// Authorization lives in the service, not here: the admin routes resolve the
// admin guard and the prediction-ingest route resolves the ml service token, both
// from the request context the composition root's middleware populates on the
// groups these routes are mounted under.
package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the mlops module's HTTP endpoints over an MLOpsService.
type Handler struct {
	svc service.MLOpsService
}

// New builds a Handler over svc.
func New(svc service.MLOpsService) *Handler {
	return &Handler{svc: svc}
}

// RegisterAdmin mounts the admin routes under rg + "/admin/mlops". The
// composition root passes a group carrying the admin JWT middleware; the service
// enforces the admin role through its AdminGuard port.
func (h *Handler) RegisterAdmin(rg gin.IRouter) {
	g := rg.Group("/admin/mlops")
	g.GET("/feature-rows", h.exportFeatureRows)
	g.POST("/models", h.registerModel)
	g.POST("/models/:version/promote", h.promoteModel)
	g.GET("/canaries", h.listCanaries)
	g.POST("/canaries", h.createCanary)
}

// RegisterService mounts the prediction-ingest route under rg + "/ml". The
// composition root passes a group carrying the ml service-token middleware; the
// service enforces the token through its ServiceAuth port.
func (h *Handler) RegisterService(rg gin.IRouter) {
	g := rg.Group("/ml")
	g.POST("/predictions", h.ingestPrediction)
}

// exportFeatureRows handles GET /admin/mlops/feature-rows.
func (h *Handler) exportFeatureRows(c *gin.Context) {
	query, err := parseFeatureRowQuery(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	resp, err := h.svc.ExportFeatureRows(c.Request.Context(), query)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// registerModel handles POST /admin/mlops/models.
func (h *Handler) registerModel(c *gin.Context) {
	var req model.RegisterModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}
	resp, err := h.svc.RegisterModel(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// promoteModel handles POST /admin/mlops/models/:version/promote.
func (h *Handler) promoteModel(c *gin.Context) {
	var req model.PromoteModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}
	resp, err := h.svc.PromoteModel(c.Request.Context(), c.Param("version"), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// listCanaries handles GET /admin/mlops/canaries.
func (h *Handler) listCanaries(c *gin.Context) {
	query := model.CanaryQuery{ModelName: c.Query("model_name")}
	if raw := c.Query("active"); raw != "" {
		active, err := strconv.ParseBool(raw)
		if err != nil {
			httpx.RenderError(c, errs.New(errs.KindInvalid, "mlops.invalid_active", "active must be a boolean"))
			return
		}
		query.Active = active
		query.HasActive = true
	}
	resp, err := h.svc.ListCanaries(c.Request.Context(), query)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// createCanary handles POST /admin/mlops/canaries.
func (h *Handler) createCanary(c *gin.Context) {
	var req model.CreateCanaryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}
	resp, err := h.svc.CreateCanary(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// ingestPrediction handles POST /ml/predictions. A successful append returns 202
// Accepted: the log is best-effort and append-only.
func (h *Handler) ingestPrediction(c *gin.Context) {
	var req model.PredictionLogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}
	resp, err := h.svc.IngestPrediction(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, resp)
}

// parseFeatureRowQuery reads the export query string. A malformed since or limit
// is an invalid-input error rather than a silently ignored default, so a caller's
// mistake surfaces instead of returning the wrong window.
func parseFeatureRowQuery(c *gin.Context) (model.FeatureRowQuery, error) {
	query := model.FeatureRowQuery{Quality: c.Query("quality")}

	if raw := c.Query("since"); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return model.FeatureRowQuery{}, errs.New(errs.KindInvalid, "mlops.invalid_since", "since must be an RFC3339 timestamp")
		}
		query.Since = since
	}
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return model.FeatureRowQuery{}, errs.New(errs.KindInvalid, "mlops.invalid_limit", "limit must be an integer")
		}
		query.Limit = limit
	}
	if query.Quality != "" && query.Quality != "ok" && query.Quality != "all" {
		return model.FeatureRowQuery{}, errs.New(errs.KindInvalid, "mlops.invalid_quality", "quality must be 'ok' or 'all'")
	}
	return query, nil
}

// errMalformedBody is the domain error for a request body gin could not bind. The
// binder's own message is discarded so nothing about internal struct shape leaks.
func errMalformedBody() error {
	return errs.New(errs.KindInvalid, "mlops.request_invalid", "request body is malformed")
}
