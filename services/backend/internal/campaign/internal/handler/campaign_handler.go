// Package handler is the campaign module's HTTP transport. Handlers are thin:
// each binds the request, calls the service, and renders — every error goes
// through httpx.RenderError so a wrapped cause can never leak to a client. While
// the service is a scaffold each route answers 501 Not Implemented through the
// shared error vocabulary; the transport is real so nothing here changes when
// campaign management lands.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/campaign/internal/model"
	"github.com/getnyx/influaudit/backend/internal/campaign/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the campaign module's routes over a CampaignService.
type Handler struct {
	svc service.CampaignService
}

// New builds a Handler over svc.
func New(svc service.CampaignService) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the campaign routes on rg. The composition root applies the
// auth middleware to the group it passes in; this handler adds none of its own.
func (h *Handler) Register(rg gin.IRouter) {
	g := rg.Group("/campaigns")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
}

// create handles POST /campaigns.
func (h *Handler) create(c *gin.Context) {
	var req model.CreateCampaignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errs.New(errs.KindInvalid, "campaign.request_invalid", "request body is malformed"))
		return
	}

	resp, err := h.svc.CreateCampaign(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// list handles GET /campaigns.
func (h *Handler) list(c *gin.Context) {
	resp, err := h.svc.ListCampaigns(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// get handles GET /campaigns/:id.
func (h *Handler) get(c *gin.Context) {
	resp, err := h.svc.GetCampaign(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
