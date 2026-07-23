package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/oauth"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
	"github.com/getnyx/influaudit/backend/internal/platform/metasig"
	"github.com/getnyx/influaudit/backend/internal/report"
)

// This file wires Meta's two mandatory privacy callbacks. Meta Platform Terms
// §3.d obliges us to "update or delete Platform Data promptly after receiving a
// request from us or the User" — and Meta enforces that by calling US:
//
//   - the DEAUTHORIZE callback fires when a person removes our app from their
//     Instagram/Facebook account; and
//   - the DATA DELETION REQUEST callback fires when they ask Meta to have their
//     data erased, and must return a confirmation the person can check.
//
// Both are unauthenticated POSTs whose only credential is a `signed_request`
// HMAC'd with our app secret (verified in internal/platform/metasig). Both are
// idempotent and must succeed for a user we do not know — otherwise Meta retries
// forever against an endpoint that will never do anything.
//
// The erasure is deliberately WIDER than the token: deleting the OAuth token
// alone would leave the reports and share grants derived from that creator's
// Insights reachable, which is exactly the retention §3.d forbids. So a deletion
// cascades — tokens erased, published reports revoked, every share grant withdrawn.

// maxCallbackBody bounds the unauthenticated callback body. A signed_request is a
// few hundred bytes; 16 KiB is generous and finite, so an anonymous caller cannot
// stream arbitrary bytes into memory.
const maxCallbackBody = 16 << 10

// metaCallbacks serves Meta's deauthorize and data-deletion callbacks. It holds
// the app secret (the signed_request key), the oauth module (which owns the
// tokens) and the report module (which owns the data derived from them).
type metaCallbacks struct {
	appSecret string
	oauth     *oauth.Module
	report    *report.Module
	// publicBaseURL builds the status URL Meta shows the user. Meta requires the
	// deletion response to carry a URL where the person can check the request.
	publicBaseURL string
}

// RegisterPublicRoutes mounts the callbacks on the public group. They must NOT
// sit behind the auth middleware: Meta holds no session. The signed_request is
// the authentication, and an unconfigured app secret rejects every call (metasig
// treats an empty secret as "verify nothing", which fails closed).
func (m metaCallbacks) RegisterPublicRoutes(rg gin.IRouter) {
	g := rg.Group("/oauth/meta")
	g.POST("/deauthorize", m.deauthorize)
	g.POST("/data-deletion", m.dataDeletion)
}

// deauthorize handles Meta's deauthorize callback: the person removed our app, so
// their consent is gone. We erase the connection and everything derived from it.
// Meta ignores the body; it only needs a 2xx, so a user we do not know still gets
// one.
func (m metaCallbacks) deauthorize(c *gin.Context) {
	payload, err := m.verified(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}

	ctx := c.Request.Context()
	if _, err := m.erase(ctx, payload.UserID); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusOK)
}

// dataDeletion handles Meta's data-deletion-request callback. Meta requires a JSON
// body carrying a confirmation code and a URL the person can visit to check the
// status of their request, so this one echoes both. The confirmation code is the
// app-scoped user id: it is the only stable handle both sides already share, and
// it discloses nothing the caller did not just send us.
func (m metaCallbacks) dataDeletion(c *gin.Context) {
	payload, err := m.verified(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}

	ctx := c.Request.Context()
	if _, err := m.erase(ctx, payload.UserID); err != nil {
		httpx.RenderError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"url":               m.publicBaseURL + "/privacy/data-deletion?id=" + payload.UserID,
		"confirmation_code": payload.UserID,
	})
}

// verified reads the signed_request off the form body and verifies its HMAC. It is
// the single gate both callbacks pass through: nothing below it may act on an
// unverified payload, and every failure is the same opaque unauthorized error.
func (m metaCallbacks) verified(c *gin.Context) (metasig.Payload, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCallbackBody)
	if err := c.Request.ParseForm(); err != nil {
		return metasig.Payload{}, errs.Wrap(err, errs.KindInvalid, "meta.callback_unreadable",
			"the callback body could not be read")
	}
	return metasig.Verify(c.Request.PostFormValue("signed_request"), m.appSecret)
}

// erase is the cascade. The token goes first (it is the live capability — the
// thing that could still pull fresh data), then everything derived from it. A
// creator we cannot resolve is not an error: they never connected, or already
// disconnected, and the request is honestly satisfied by doing nothing.
//
// Report revocation failing after the token was erased is logged, not surfaced:
// the capability is already gone, and returning an error would make Meta retry a
// deletion whose most important half already succeeded. The residue is a revoked-
// but-not-yet-revoked report row, which the operator can see in the log.
func (m metaCallbacks) erase(ctx context.Context, providerUserID string) (uuid.UUID, error) {
	userID, found, err := m.oauth.ForgetProviderUser(ctx, "meta", providerUserID)
	if err != nil {
		return uuid.Nil, err
	}
	if !found {
		return uuid.Nil, nil
	}

	if _, err := m.report.RevokeAllForUser(ctx, userID); err != nil {
		slog.ErrorContext(ctx, "meta deletion: token erased but reports not revoked",
			"user_id", userID, "error", err)
	}
	return userID, nil
}
