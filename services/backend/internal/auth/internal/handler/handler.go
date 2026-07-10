// Package handler is the auth module's HTTP transport. Handlers are thin: they
// parse the request, call the service, and render either the result or, through
// httpx.RenderError, the domain error. No business logic lives here, and no
// wrapped cause is ever written to the response.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the auth endpoints over an AuthService.
type Handler struct {
	svc service.AuthService
}

// New returns a Handler backed by svc.
func New(svc service.AuthService) *Handler {
	return &Handler{svc: svc}
}

// Register handles POST /auth/register.
func (h *Handler) Register(c *gin.Context) {
	var req model.RegisterRequest
	if !bindJSON(c, &req) {
		return
	}
	req.UserAgent = c.Request.UserAgent()
	req.IP = c.ClientIP()

	resp, err := h.svc.Register(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// Login handles POST /auth/login.
func (h *Handler) Login(c *gin.Context) {
	var req model.LoginRequest
	if !bindJSON(c, &req) {
		return
	}
	req.UserAgent = c.Request.UserAgent()
	req.IP = c.ClientIP()

	resp, err := h.svc.Login(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Refresh handles POST /auth/refresh.
func (h *Handler) Refresh(c *gin.Context) {
	var req model.RefreshRequest
	if !bindJSON(c, &req) {
		return
	}
	req.UserAgent = c.Request.UserAgent()
	req.IP = c.ClientIP()

	resp, err := h.svc.Refresh(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(c *gin.Context) {
	var req model.LogoutRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := h.svc.Logout(c.Request.Context(), req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Me handles GET /auth/me. It relies on RequireAuth having placed the caller's
// identity on the request context.
func (h *Handler) Me(c *gin.Context) {
	resp, err := h.svc.Me(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// bindJSON decodes the request body into dst, rendering a 400 and returning
// false on a malformed or invalid body. The binding error is logged by
// RenderError but never returned to the client, so a validation message can
// never echo a submitted password back.
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		httpx.RenderError(c, errs.Wrap(err, errs.KindInvalid, "auth.invalid_request", "the request body is missing required fields or is malformed"))
		return false
	}
	return true
}
