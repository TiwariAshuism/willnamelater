// Package handler is the report module's HTTP transport. Handlers are thin: each
// resolves the caller's audit, assembles the deliverable, and renders it —
// every error goes through httpx.RenderError so a wrapped cause never reaches
// the client.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/report/internal/service"
)

// Handler serves the report module's read endpoints over the report Service.
type Handler struct {
	svc *service.Service
}

// New builds a Handler over svc.
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the report routes. They hang off the audit resource because a
// report is the deliverable of a specific audit. The composition root applies
// the auth middleware to the group it passes in; both routes act on behalf of a
// signed-in caller and the service scopes every read to that caller.
func (h *Handler) Register(rg gin.IRouter) {
	rg.GET("/audits/:id/report", h.get)
	rg.GET("/audits/:id/report.pdf", h.getPDF)
}

// get handles GET /audits/:id/report and returns the assembled report as JSON.
func (h *Handler) get(c *gin.Context) {
	report, err := h.svc.Assemble(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// getPDF handles GET /audits/:id/report.pdf and streams the rendered PDF as a
// download.
func (h *Handler) getPDF(c *gin.Context) {
	pdf, err := h.svc.PDF(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="influaudit-report.pdf"`)
	c.Data(http.StatusOK, "application/pdf", pdf)
}
