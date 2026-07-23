// Package handler is the scoring module's HTTP transport. Handlers are thin:
// each binds the path, calls the service, and renders the result or routes the
// error through httpx.RenderError so a wrapped cause never reaches the client.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/service"
)

// Handler serves the scoring module's read routes over a ScoringService.
type Handler struct {
	svc service.ScoringService
}

// New builds a Handler over svc.
func New(svc service.ScoringService) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the scoring read routes on r.
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/influencers/:id/score", h.getLatestScore)
	r.GET("/influencers/:id/score/history", h.getScoreHistory)
}

// getLatestScore handles GET /influencers/:id/score.
func (h *Handler) getLatestScore(c *gin.Context) {
	resp, err := h.svc.GetLatestScore(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// getScoreHistory handles GET /influencers/:id/score/history.
func (h *Handler) getScoreHistory(c *gin.Context) {
	resp, err := h.svc.GetScoreHistory(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
