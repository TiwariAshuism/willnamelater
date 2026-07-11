// Package handler is the admin module's HTTP transport. Handlers are thin: each
// binds the request, calls the service, and renders — every error goes through
// httpx.RenderError so a wrapped cause can never reach the client. The generated
// apigen handler is deliberately not used because it renders every error as a
// 500 with the raw error string.
//
// Authorization lives in the service, not here: the service resolves the caller
// (for filing) or the admin guard (for every /admin route) from the request
// context, which the composition root's auth middleware populates on the group
// these routes are mounted under.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/admin/internal/model"
	"github.com/getnyx/influaudit/backend/internal/admin/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the admin module's HTTP endpoints over an AdminService.
type Handler struct {
	svc service.AdminService
}

// New builds a Handler over svc.
func New(svc service.AdminService) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the admin routes on rg. The composition root applies the auth
// middleware to the group it passes in: filing a dispute needs a signed-in
// caller, and every /admin route additionally requires the admin role, which the
// service enforces through its AdminGuard port.
func (h *Handler) Register(rg gin.IRouter) {
	rg.POST("/audits/:id/disputes", h.fileDispute)

	admin := rg.Group("/admin")
	admin.GET("/disputes", h.listDisputeQueue)
	admin.POST("/disputes/:id/resolve", h.resolveDispute)
	admin.GET("/costs", h.costDashboard)
	admin.GET("/queues", h.queueMonitor)
	admin.GET("/training/labels", h.exportLabels)
}

// fileDispute handles POST /audits/:id/disputes.
func (h *Handler) fileDispute(c *gin.Context) {
	var req model.FileDisputeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}

	resp, err := h.svc.FileDispute(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// listDisputeQueue handles GET /admin/disputes.
func (h *Handler) listDisputeQueue(c *gin.Context) {
	resp, err := h.svc.ListDisputeQueue(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// resolveDispute handles POST /admin/disputes/:id/resolve.
func (h *Handler) resolveDispute(c *gin.Context) {
	var req model.ResolveDisputeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}

	resp, err := h.svc.ResolveDispute(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// costDashboard handles GET /admin/costs.
func (h *Handler) costDashboard(c *gin.Context) {
	resp, err := h.svc.CostDashboard(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// queueMonitor handles GET /admin/queues.
func (h *Handler) queueMonitor(c *gin.Context) {
	resp, err := h.svc.QueueMonitor(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// exportLabels handles GET /admin/training/labels.
func (h *Handler) exportLabels(c *gin.Context) {
	resp, err := h.svc.ExportLabels(c.Request.Context())
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
	return errs.New(errs.KindInvalid, "admin.request_invalid", "request body is malformed")
}
