// Package handler is the metrics module's HTTP layer. It is deliberately thin:
// each handler binds the request, calls the service, and renders — every error
// goes through httpx.RenderError so a wrapped cause can never leak to a client.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/metrics/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// MetricsHandler serves the metrics module's read routes.
type MetricsHandler struct {
	svc service.MetricsService
}

// New builds a handler over svc.
func New(svc service.MetricsService) *MetricsHandler {
	return &MetricsHandler{svc: svc}
}

// Register mounts the metrics routes on r.
func (h *MetricsHandler) Register(r gin.IRouter) {
	r.GET("/influencers/:id/metrics", h.getInfluencerMetrics)
	r.GET("/influencers/:id/posts", h.listInfluencerPosts)
}

// getInfluencerMetrics handles GET /influencers/:id/metrics.
func (h *MetricsHandler) getInfluencerMetrics(c *gin.Context) {
	var req model.MetricSeriesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		httpx.RenderError(c, errs.Wrap(err, errs.KindInvalid, "metrics.invalid_query", "invalid metrics query parameters"))
		return
	}

	resp, err := h.svc.GetInfluencerMetrics(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// listInfluencerPosts handles GET /influencers/:id/posts.
func (h *MetricsHandler) listInfluencerPosts(c *gin.Context) {
	var req model.ListPostsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		httpx.RenderError(c, errs.Wrap(err, errs.KindInvalid, "metrics.invalid_query", "invalid posts query parameters"))
		return
	}

	resp, err := h.svc.ListInfluencerPosts(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
