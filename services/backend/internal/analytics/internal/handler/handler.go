// Package handler is the analytics module's HTTP transport. Handlers are thin:
// they read the request under a fixed body limit, compute the server-side context
// (User-Agent, referrer), call the service, and route every error through
// httpx.RenderError so a wrapped cause never reaches the client.
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/analytics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/analytics/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// maxEventBody bounds the public ingest body. POST /events is unauthenticated, so
// an unbounded read would let an anonymous caller stream arbitrary bytes into
// memory; a funnel event is a few hundred bytes, so 16 KiB is generous but finite.
const maxEventBody = 16 << 10

// Handler serves the analytics endpoints over the analytics Service.
type Handler struct {
	svc *service.Service
}

// New builds a Handler over svc.
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterPublicRoutes mounts the unauthenticated first-party ingest endpoint. It
// carries no auth middleware: the browser posting a funnel event holds no session.
func (h *Handler) RegisterPublicRoutes(rg gin.IRouter) {
	rg.POST("/events", h.ingest)
}

// RegisterProtectedRoutes mounts the optional aggregate read behind the auth
// middleware the composition root applies to the group it passes in.
func (h *Handler) RegisterProtectedRoutes(rg gin.IRouter) {
	rg.GET("/events/summary", h.summary)
}

// ingest handles POST /events. It reads the body under a fixed limit, binds the
// event, and records it with the server-computed User-Agent and referrer. A
// recorded event returns 202 Accepted — the client is not waiting on the write.
func (h *Handler) ingest(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxEventBody)

	var req model.IngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.RenderError(c, errs.New(errs.KindInvalid, "analytics.body_too_large", "event payload is too large"))
			return
		}
		httpx.RenderError(c, errs.New(errs.KindInvalid, "analytics.invalid_body", "event body is malformed"))
		return
	}

	meta := model.IngestMeta{
		UserAgent: c.GetHeader("User-Agent"),
		Referrer:  c.GetHeader("Referer"),
	}
	if err := h.svc.Ingest(c.Request.Context(), req, meta); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}

// summary handles GET /events/summary and returns the per-type event counts.
func (h *Handler) summary(c *gin.Context) {
	counts, err := h.svc.Summary(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"counts": counts})
}
