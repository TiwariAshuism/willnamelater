// Package handler is the influencer module's HTTP transport. Handlers are thin:
// they bind the request, call the service, and render the result or route the
// error through httpx.RenderError so a wrapped cause never reaches the client.
// The generated apigen handler is deliberately not used because it renders every
// error as a 500 with the raw error string.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/influencer/internal/model"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the influencer HTTP endpoints over an InfluencerService.
type Handler struct {
	svc service.InfluencerService
}

// New builds a Handler over svc.
func New(svc service.InfluencerService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes mounts the influencer endpoints on r. The composition root
// calls it after building the module; this package never touches the router
// itself.
func (h *Handler) RegisterRoutes(r gin.IRouter) {
	r.POST("/influencers", h.CreateInfluencer)
	r.GET("/influencers", h.ListInfluencers)
	r.GET("/influencers/:id", h.GetInfluencer)
	r.PATCH("/influencers/:id", h.UpdateInfluencer)
	r.POST("/influencers/:id/handles", h.AddHandle)
	r.DELETE("/influencers/:id/handles/:handleID", h.DeleteHandle)
}

// CreateInfluencer handles POST /influencers.
func (h *Handler) CreateInfluencer(c *gin.Context) {
	var req model.CreateInfluencerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}

	resp, err := h.svc.CreateInfluencer(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// GetInfluencer handles GET /influencers/:id.
func (h *Handler) GetInfluencer(c *gin.Context) {
	resp, err := h.svc.GetInfluencer(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListInfluencers handles GET /influencers.
func (h *Handler) ListInfluencers(c *gin.Context) {
	var req model.ListInfluencersRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		httpx.RenderError(c, errMalformedQuery())
		return
	}

	resp, err := h.svc.ListInfluencers(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// UpdateInfluencer handles PATCH /influencers/:id.
func (h *Handler) UpdateInfluencer(c *gin.Context) {
	var req model.UpdateInfluencerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}

	resp, err := h.svc.UpdateInfluencer(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// AddHandle handles POST /influencers/:id/handles.
func (h *Handler) AddHandle(c *gin.Context) {
	var req model.AddHandleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}

	resp, err := h.svc.AddHandle(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// DeleteHandle handles DELETE /influencers/:id/handles/:handleID.
func (h *Handler) DeleteHandle(c *gin.Context) {
	if err := h.svc.DeleteHandle(c.Request.Context(), c.Param("id"), c.Param("handleID")); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// errMalformedBody is the domain error for a request body gin could not bind.
// The binder's own message is discarded so nothing about internal struct shape
// leaks to the client.
func errMalformedBody() error {
	return errs.New(errs.KindInvalid, "influencer.request_invalid", "request body is malformed")
}

// errMalformedQuery is the domain error for query parameters gin could not bind.
func errMalformedQuery() error {
	return errs.New(errs.KindInvalid, "influencer.query_invalid", "query parameters are malformed")
}
