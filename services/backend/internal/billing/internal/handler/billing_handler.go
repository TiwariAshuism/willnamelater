// Package handler is the billing module's HTTP transport. Handlers are thin:
// they bind the request, call the service, and render the result or route the
// error through httpx.RenderError so a wrapped cause never reaches the client.
// The generated apigen handler is deliberately not used because it renders every
// error as a 500 with the raw error string.
package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// maxWebhookBody bounds the webhook request body. The webhook is an
// unauthenticated endpoint, so an unbounded read would let an anonymous caller
// stream arbitrary bytes into memory; a Razorpay subscription event is a few
// kilobytes, so 64 KiB is generous while still finite.
const maxWebhookBody = 64 << 10

// signatureHeader carries the HMAC signature Razorpay computes over the exact
// webhook body. It is the only credential authenticating the webhook endpoint.
const signatureHeader = "X-Razorpay-Signature"

// Handler serves the billing HTTP endpoints over a BillingService.
type Handler struct {
	svc service.BillingService
}

// New builds a Handler over svc.
func New(svc service.BillingService) *Handler {
	return &Handler{svc: svc}
}

// RegisterProtectedRoutes mounts the endpoints that act on behalf of a signed-in
// caller. The composition root applies the auth middleware to the group it passes
// in; this handler adds none of its own.
func (h *Handler) RegisterProtectedRoutes(rg gin.IRouter) {
	g := rg.Group("/billing")
	g.GET("/plans", h.ListPlans)
	g.GET("/subscription", h.GetSubscription)
	g.POST("/subscribe", h.Subscribe)
}

// RegisterPublicRoutes mounts the endpoints that must NOT sit behind the auth
// middleware. The webhook caller is Razorpay, which holds no session and proves
// its identity with an HMAC signature over the raw body.
func (h *Handler) RegisterPublicRoutes(rg gin.IRouter) {
	rg.Group("/billing").POST("/webhook", h.Webhook)
}

// ListPlans handles GET /billing/plans.
func (h *Handler) ListPlans(c *gin.Context) {
	resp, err := h.svc.ListPlans(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetSubscription handles GET /billing/subscription.
func (h *Handler) GetSubscription(c *gin.Context) {
	resp, err := h.svc.GetSubscription(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Subscribe handles POST /billing/subscribe.
func (h *Handler) Subscribe(c *gin.Context) {
	var req model.SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.RenderError(c, errMalformedBody())
		return
	}

	resp, err := h.svc.Subscribe(c.Request.Context(), req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Webhook handles POST /billing/webhook. It is unauthenticated, so it reads the
// raw body under a fixed byte limit (an unauthenticated endpoint must not accept
// an unbounded body) and hands the exact bytes and the signature header to the
// service, which verifies the HMAC before any database write. The body is never
// bound or echoed back: changing the bytes would break verification, and
// reflecting them would turn the endpoint into an amplifier.
func (h *Handler) Webhook(c *gin.Context) {
	signature := c.GetHeader(signatureHeader)
	if signature == "" {
		// A webhook with no signature cannot be verified. Reject it before reading
		// the body or calling the service; the signature is the only thing that
		// authenticates this endpoint.
		httpx.RenderError(c, errMissingSignature())
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBody))
	if err != nil {
		httpx.RenderError(c, errWebhookTooLarge())
		return
	}

	if err := h.svc.Webhook(c.Request.Context(), model.WebhookRequest{RawBody: body, Signature: signature}); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusOK)
}

// errMalformedBody is the domain error for a request body gin could not bind.
// The binder's own message is discarded so nothing about internal struct shape
// leaks to the client.
func errMalformedBody() error {
	return errs.New(errs.KindInvalid, "billing.request_invalid", "request body is malformed")
}

// errMissingSignature is the domain error for a webhook that arrives without the
// signature header that authenticates it.
func errMissingSignature() error {
	return errs.New(errs.KindUnauthorized, "billing.webhook_unsigned", "missing webhook signature")
}

// errWebhookTooLarge is the domain error for a webhook body that exceeds the
// fixed limit for the unauthenticated endpoint.
func errWebhookTooLarge() error {
	return errs.New(errs.KindInvalid, "billing.webhook_too_large", "webhook payload is too large")
}
