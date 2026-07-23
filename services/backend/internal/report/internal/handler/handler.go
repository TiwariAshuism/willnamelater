// Package handler is the report module's HTTP transport. Handlers are thin: each
// resolves the caller's audit, assembles the deliverable, and renders it —
// every error goes through httpx.RenderError so a wrapped cause never reaches
// the client.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/report/internal/render"
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

// Register mounts the caller-scoped report routes. They hang off the audit
// resource because a report is the deliverable of a specific audit. The
// composition root applies the auth middleware to the group it passes in; every
// route here acts on behalf of a signed-in caller and the service scopes each
// read to that caller. Publishing mints the shareable badge and PDF.
func (h *Handler) Register(rg gin.IRouter) {
	rg.GET("/audits/:id/report", h.get)
	rg.GET("/audits/:id/report.pdf", h.getPDF)
	rg.POST("/audits/:id/report/publish", h.publish)
	rg.DELETE("/audits/:id/report/publish", h.revoke)
	rg.POST("/audits/:id/report/share", h.share)
}

// RegisterPublic mounts the unauthenticated public badge route. It is served
// without the auth middleware — a badge is a shareable public credential — and
// reads only the frozen snapshot captured at publish time, never private data.
func (h *Handler) RegisterPublic(rg gin.IRouter) {
	rg.GET("/reports/:slug", h.getPublicBadge)
	// The /@handle acquisition page. `@` is not gin-router-safe as a static
	// segment, so the durable route is /handle/:handle; the composition root
	// rewrites an incoming /@handle onto it. It is an alias — the opaque slug
	// stays the durable key — and serves the same public projection.
	rg.GET("/handle/:handle", h.getPublicBadgeByHandle)
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

// publish handles POST /audits/:id/report/publish: it renders and stores the
// report and returns the durable public slug plus a shareable PDF link.
func (h *Handler) publish(c *gin.Context) {
	result, err := h.svc.Publish(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// revoke handles DELETE /audits/:id/report/publish: the creator withdraws a
// published report. The public slug stops resolving and every share grant on it
// is withdrawn. It returns 204 and is idempotent — revoking twice is not an error.
func (h *Handler) revoke(c *gin.Context) {
	if err := h.svc.Revoke(c.Request.Context(), c.Param("id")); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// share handles POST /audits/:id/report/share: the creator expressly directs us to
// disclose their published report to a named brand for a stated purpose. A
// malformed body (no recipient, no purpose) is rejected — an unnamed recipient is
// not a direction we can act on.
func (h *Handler) share(c *gin.Context) {
	var req render.ShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errs.Wrap(err, errs.KindInvalid, "report.invalid_body",
			"name the recipient and state the purpose of the share"))
		return
	}
	result, err := h.svc.Share(c.Request.Context(), c.Param("id"), req.Recipient, req.Purpose)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// getPublicBadge handles GET /reports/:slug: the unauthenticated public badge
// projection for a published report.
func (h *Handler) getPublicBadge(c *gin.Context) {
	badge, err := h.svc.PublicBadge(c.Request.Context(), c.Param("slug"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, badge)
}

// getPublicBadgeByHandle handles GET /handle/:handle (the /@handle alias): the
// same unauthenticated badge projection, resolved to the newest live report for
// the creator's Instagram handle.
func (h *Handler) getPublicBadgeByHandle(c *gin.Context) {
	badge, err := h.svc.PublicBadgeByHandle(c.Request.Context(), c.Param("handle"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, badge)
}
