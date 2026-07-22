package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// SignupHandler serves the PUBLIC OAuth-as-signup endpoints: an anonymous
// visitor starts a Meta authorization with only an email, and the callback
// creates their account. It is a separate handler from the protected connect
// Handler so the two surfaces — and their route groups — stay independent.
type SignupHandler struct {
	svc service.SignupService
}

// NewSignup builds a SignupHandler over svc.
func NewSignup(svc service.SignupService) *SignupHandler {
	return &SignupHandler{svc: svc}
}

// RegisterPublicRoutes mounts the signup endpoints on r. The composition root
// calls it against the PUBLIC (unauthenticated) route group: these routes create
// the session rather than requiring one.
//
// The callback is mounted on both GET and POST because the provider redirects
// the browser back with a GET, while some form_post response modes POST instead;
// both carry the same code/state query parameters.
func (h *SignupHandler) RegisterPublicRoutes(r gin.IRouter) {
	g := r.Group("/oauth/meta/signup")
	g.POST("/start", h.Start)
	// Meta redirects the browser back with a GET; the callback is GET-only, matching
	// the connect callback and the documented OpenAPI surface.
	g.GET("/callback", h.Callback)
}

// Start handles POST /oauth/meta/signup/start. It reads the captured email from
// the JSON body and returns the provider consent URL.
func (h *SignupHandler) Start(c *gin.Context) {
	var req model.SignupStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errs.New(errs.KindInvalid, "oauth.email_required",
			"an email address is required to sign up"))
		return
	}

	resp, err := h.svc.AuthorizeSignup(c.Request.Context(), req.Email)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Callback handles GET/POST /oauth/meta/signup/callback. The provider redirects
// here with the result in the query string; the parameters are read off the
// request and handed to the service, which creates the account and returns a
// session the client uses to log in.
//
// error_description is untrusted, provider-reflected content: it is passed to the
// service only to distinguish outcomes and is never written into the response.
func (h *SignupHandler) Callback(c *gin.Context) {
	session, err := h.svc.CallbackSignup(c.Request.Context(), service.CallbackParams{
		Code:             c.Query("code"),
		State:            c.Query("state"),
		Error:            c.Query("error"),
		ErrorDescription: c.Query("error_description"),
	})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}
