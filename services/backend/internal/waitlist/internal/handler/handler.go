// Package handler is the waitlist module's HTTP transport. The single handler
// reads the request under a fixed body limit, calls the service, and routes every
// error through httpx.RenderError so a wrapped cause never reaches the client.
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/model"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/service"
)

// maxBody bounds the public capture body. POST /waitlist is unauthenticated, so an
// unbounded read would let an anonymous caller stream arbitrary bytes; a capture
// is tiny, so 8 KiB is generous but finite.
const maxBody = 8 << 10

// Handler serves the waitlist endpoint over the waitlist Service.
type Handler struct {
	svc *service.Service
}

// New builds a Handler over svc.
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterPublicRoutes mounts the unauthenticated capture endpoint. It carries no
// auth middleware: a visitor leaving their email holds no session.
func (h *Handler) RegisterPublicRoutes(rg gin.IRouter) {
	rg.POST("/waitlist", h.capture)
}

// capture handles POST /waitlist. It reads the body under a fixed limit, binds the
// request, and records the capture. The write is idempotent on (email, source), so
// a repeat submission returns the same 202 Accepted as the first.
func (h *Handler) capture(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)

	var req model.CaptureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.RenderError(c, errs.New(errs.KindInvalid, "waitlist.body_too_large", "request body is too large"))
			return
		}
		httpx.RenderError(c, errs.New(errs.KindInvalid, "waitlist.invalid_body", "request body is malformed"))
		return
	}

	if err := h.svc.Capture(c.Request.Context(), req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}
