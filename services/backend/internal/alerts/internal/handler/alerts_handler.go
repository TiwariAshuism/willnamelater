// Package handler is the alerts module's HTTP transport. Handlers are thin: each
// binds the request, calls the service, and renders — every error goes through
// httpx.RenderError so a wrapped cause can never leak to a client. While the
// service is a scaffold each route answers 501 Not Implemented through the shared
// error vocabulary; the transport is real so nothing here changes when the engine
// lands.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/alerts/internal/model"
	"github.com/getnyx/influaudit/backend/internal/alerts/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the alerts module's routes over an AlertsService.
type Handler struct {
	svc service.AlertsService
}

// New builds a Handler over svc.
func New(svc service.AlertsService) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the alerts routes on rg. The composition root applies the auth
// middleware to the group it passes in; this handler adds none of its own.
func (h *Handler) Register(rg gin.IRouter) {
	g := rg.Group("/alerts")
	g.GET("", h.list)
	g.POST("", h.create)
	g.DELETE("/:id", h.delete)
}

// list handles GET /alerts.
func (h *Handler) list(c *gin.Context) {
	resp, err := h.svc.ListAlerts(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// create handles POST /alerts.
func (h *Handler) create(c *gin.Context) {
	var req model.CreateAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errs.New(errs.KindInvalid, "alerts.request_invalid", "request body is malformed"))
		return
	}

	resp, err := h.svc.CreateAlert(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// delete handles DELETE /alerts/:id.
func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.DeleteAlert(c.Request.Context(), c.Param("id")); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
