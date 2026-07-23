// Package handler is the whitelabel module's HTTP transport. Handlers are thin:
// each binds the request, calls the service, and renders — every error goes
// through httpx.RenderError so a wrapped cause can never leak to a client. While
// the service is a scaffold each route answers 501 Not Implemented through the
// shared error vocabulary; the transport is real so nothing here changes when
// branding lands.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/whitelabel/internal/model"
	"github.com/getnyx/influaudit/backend/internal/whitelabel/internal/service"
)

// Handler serves the whitelabel module's routes over a WhitelabelService.
type Handler struct {
	svc service.WhitelabelService
}

// New builds a Handler over svc.
func New(svc service.WhitelabelService) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the whitelabel routes on rg. The composition root applies the
// auth middleware to the group it passes in; this handler adds none of its own.
func (h *Handler) Register(rg gin.IRouter) {
	g := rg.Group("/whitelabel")
	g.GET("", h.get)
	g.PUT("", h.update)
}

// get handles GET /whitelabel.
func (h *Handler) get(c *gin.Context) {
	resp, err := h.svc.GetWhitelabel(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// update handles PUT /whitelabel.
func (h *Handler) update(c *gin.Context) {
	var req model.UpdateWhitelabelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errs.New(errs.KindInvalid, "whitelabel.request_invalid", "request body is malformed"))
		return
	}

	resp, err := h.svc.UpdateWhitelabel(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
