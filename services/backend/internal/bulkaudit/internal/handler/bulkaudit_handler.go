// Package handler is the bulkaudit module's HTTP transport. Handlers are thin:
// each binds the request, calls the service, and renders — every error goes
// through httpx.RenderError so a wrapped cause can never leak to a client. While
// the service is a scaffold each route answers 501 Not Implemented through the
// shared error vocabulary; the transport is real so nothing here changes when the
// orchestrator lands.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/bulkaudit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/bulkaudit/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the bulkaudit module's routes over a BulkAuditService.
type Handler struct {
	svc service.BulkAuditService
}

// New builds a Handler over svc.
func New(svc service.BulkAuditService) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the bulkaudit routes on rg. The composition root applies the
// auth middleware to the group it passes in; this handler adds none of its own.
func (h *Handler) Register(rg gin.IRouter) {
	g := rg.Group("/bulk-audits")
	g.POST("", h.create)
	g.GET("", h.list)
	g.GET("/:id", h.get)
}

// create handles POST /bulk-audits.
func (h *Handler) create(c *gin.Context) {
	var req model.CreateBulkAuditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errs.New(errs.KindInvalid, "bulkaudit.request_invalid", "request body is malformed"))
		return
	}

	resp, err := h.svc.CreateBulkAudit(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// list handles GET /bulk-audits.
func (h *Handler) list(c *gin.Context) {
	resp, err := h.svc.ListBulkAudits(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// get handles GET /bulk-audits/:id.
func (h *Handler) get(c *gin.Context) {
	resp, err := h.svc.GetBulkAudit(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
