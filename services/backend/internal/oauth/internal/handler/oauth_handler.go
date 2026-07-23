// Package handler is the oauth module's HTTP transport. Handlers are thin: they
// read the request, call the service, and either render the result or route the
// error through httpx.RenderError so a wrapped cause never reaches the client.
// The generated apigen handler is deliberately not used because it renders every
// error as a 500 with the raw error string, and because the OAuth callback's
// query parameters cannot be expressed in the apigen interface.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Handler serves the oauth HTTP endpoints over an OAuthService.
type Handler struct {
	svc service.OAuthService
}

// New builds a Handler over svc.
func New(svc service.OAuthService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes mounts the oauth endpoints on r. The composition root calls it
// after building the module; this package never touches the router itself.
//
// The static /oauth/connections is registered alongside the /oauth/:provider
// parameterized routes; gin resolves the static segment ahead of the wildcard,
// so "connections" is never captured as a provider name.
func (h *Handler) RegisterRoutes(r gin.IRouter) {
	g := r.Group("/oauth")
	g.GET("/connections", h.Connections)
	g.GET("/:provider/authorize", h.Authorize)
	g.GET("/:provider/callback", h.Callback)
	g.DELETE("/:provider", h.Disconnect)
}

// Authorize handles GET /oauth/:provider/authorize.
func (h *Handler) Authorize(c *gin.Context) {
	resp, err := h.svc.Authorize(c.Request.Context(), c.Param("provider"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Callback handles GET /oauth/:provider/callback. The provider redirects the
// user's browser here with the result in the query string; the parameters are
// carried to the service on the context because the service's interface cannot
// express query parameters.
//
// error_description is untrusted, provider-reflected content. It is passed to
// the service (which uses it only to distinguish outcomes) but is never written
// into the response here, and the service does not echo it either.
func (h *Handler) Callback(c *gin.Context) {
	ctx := service.WithCallbackParams(c.Request.Context(), service.CallbackParams{
		Code:             c.Query("code"),
		State:            c.Query("state"),
		Error:            c.Query("error"),
		ErrorDescription: c.Query("error_description"),
	})

	resp, err := h.svc.Callback(ctx, c.Param("provider"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Connections handles GET /oauth/connections.
func (h *Handler) Connections(c *gin.Context) {
	resp, err := h.svc.Connections(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Disconnect handles DELETE /oauth/:provider.
func (h *Handler) Disconnect(c *gin.Context) {
	if err := h.svc.Disconnect(c.Request.Context(), c.Param("provider")); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
