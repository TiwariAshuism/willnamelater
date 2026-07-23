// Package handler is the audit module's HTTP transport. Handlers are thin: each
// binds the request, calls the service, and renders — every error goes through
// httpx.RenderError so a wrapped cause can never reach the client. The generated
// apigen handler is deliberately not used because it renders every error as a
// 500 with the raw error string.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/audit/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the audit module's HTTP endpoints over an AuditService.
type Handler struct {
	svc service.AuditService
}

// New builds a Handler over svc.
func New(svc service.AuditService) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the audit routes on rg. The composition root applies the auth
// middleware to the group it passes in; every audit route acts on behalf of a
// signed-in caller.
func (h *Handler) Register(rg gin.IRouter) {
	g := rg.Group("/audits")
	g.POST("", h.Submit)
	g.GET("", h.List)
	g.GET("/:id", h.Get)
}

// Submit handles POST /audits.
func (h *Handler) Submit(c *gin.Context) {
	var req model.SubmitAuditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}

	resp, err := h.svc.SubmitAudit(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, resp)
}

// Get handles GET /audits/:id.
func (h *Handler) Get(c *gin.Context) {
	resp, err := h.svc.GetAudit(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// List handles GET /audits.
func (h *Handler) List(c *gin.Context) {
	resp, err := h.svc.ListAudits(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// errMalformedBody is the domain error for a request body gin could not bind.
// The binder's own message is discarded so nothing about internal struct shape
// leaks to the client.
func errMalformedBody() error {
	return errs.New(errs.KindInvalid, "audit.request_invalid", "request body is malformed")
}
