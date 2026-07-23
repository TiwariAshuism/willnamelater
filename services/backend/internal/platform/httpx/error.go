package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// errorEnvelope is the sole shape in which errors reach a client. It exposes a
// stable machine code and a human message and nothing else; causes, stack
// frames, and internal detail never appear here.
type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RenderError writes err to the client as the canonical error envelope and
// aborts the handler chain.
//
// The HTTP status comes from errs.Status. The client-facing code and message
// come only from a domain *errs.Error, whose Message field is by contract safe
// to expose; any other error is reported as a generic internal failure so that
// an unclassified error can never leak its wording. The full error, including
// its wrapped cause, is logged but never sent to the client.
func RenderError(c *gin.Context, err error) {
	status := errs.Status(err)

	code, message := "internal", "internal server error"
	var domain *errs.Error
	if errors.As(err, &domain) {
		code, message = domain.Code, domain.Message
	}

	logError(c, status, code, err)

	c.AbortWithStatusJSON(status, errorEnvelope{Error: errorDetail{Code: code, Message: message}})
}

// logError records the error server-side. Server faults (5xx) carry the wrapped
// cause and warrant an error-level line; client faults (4xx) are expected
// outcomes and are logged at info level to avoid drowning real failures.
func logError(c *gin.Context, status int, code string, err error) {
	ctx := c.Request.Context()
	attrs := []any{
		slog.String("request_id", RequestIDFromContext(ctx)),
		slog.Int("status", status),
		slog.String("code", code),
		slog.String("error", err.Error()),
	}

	if status >= http.StatusInternalServerError {
		slog.ErrorContext(ctx, "request failed", attrs...)
		return
	}
	slog.InfoContext(ctx, "request rejected", attrs...)
}
