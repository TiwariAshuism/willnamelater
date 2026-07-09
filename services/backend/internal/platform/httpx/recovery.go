package httpx

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// errPanic is the client-facing result of any recovered panic. It carries no
// detail about what actually failed; the real panic value and stack go to the
// logs only.
var errPanic = errs.New(errs.KindInternal, "internal", "internal server error")

// Recovery converts a panic in a downstream handler into a 500 rendered through
// RenderError, logging the panic value and stack for diagnosis.
//
// http.ErrAbortHandler is re-panicked rather than swallowed: net/http uses it
// as a sentinel to abort a response, and honoring that contract is required for
// correct behavior of the standard library server.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			// A handler may panic with an error that wraps ErrAbortHandler, so
			// unwrap rather than comparing identity.
			if err, ok := r.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(r)
			}

			slog.ErrorContext(c.Request.Context(), "panic recovered",
				slog.String("request_id", RequestIDFromContext(c.Request.Context())),
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
			)

			// Once bytes are on the wire the status and body are fixed; we can
			// only stop further handlers from running, not rewrite the reply.
			if c.Writer.Written() {
				c.Abort()
				return
			}
			RenderError(c, errPanic)
		}()

		c.Next()
	}
}
