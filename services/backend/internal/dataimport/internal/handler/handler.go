// Package handler is the dataimport module's HTTP transport. It is thin: bind,
// call the service, render — every error routes through httpx.RenderError so a
// wrapped cause never reaches the client.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/model"
	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the dataimport endpoints over the service.
type Handler struct {
	svc *service.Service
}

// New builds a Handler over svc.
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the import routes. The composition root applies the auth
// middleware to the group it passes in; an upload is always owned by a caller.
func (h *Handler) Register(rg gin.IRouter) {
	rg.POST("/imports/instagram", h.importInstagram)
}

// importInstagram handles POST /imports/instagram.
func (h *Handler) importInstagram(c *gin.Context) {
	var req model.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errs.New(errs.KindInvalid, "dataimport.malformed_body",
			"request body is malformed or missing required fields"))
		return
	}

	resp, err := h.svc.ImportInstagramCSV(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}
