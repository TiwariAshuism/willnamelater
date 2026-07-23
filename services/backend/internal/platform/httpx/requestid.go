// Package httpx holds the transport-layer middleware shared by every HTTP
// module: request correlation, panic recovery, and the single renderer that
// turns a domain error into a client response. Business logic lives in the
// modules; this package only concerns itself with the wire.
package httpx

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HeaderRequestID is the canonical header used to correlate a request across
// the client, this service, and downstream logs and traces.
const HeaderRequestID = "X-Request-Id"

// contextKey is unexported so no other package can collide with or overwrite
// the values this package stores on a request context.
type contextKey int

const requestIDKey contextKey = iota

// RequestID correlates each request with a UUID.
//
// An inbound X-Request-Id is honored only when it parses as a UUID, and even
// then it is re-emitted in canonical form. Anything else is discarded and a
// fresh UUID is minted. This is deliberate: the value flows into log lines and
// response headers, so accepting arbitrary client input would permit log
// forging and header injection.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := mint(c.GetHeader(HeaderRequestID))

		ctx := context.WithValue(c.Request.Context(), requestIDKey, id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(HeaderRequestID, id)

		c.Next()
	}
}

// mint returns the canonical form of inbound when it is a valid UUID, otherwise
// a newly generated UUID. Canonicalizing rather than echoing the raw header is
// what neutralizes injection: the returned string is always a bare UUID.
func mint(inbound string) string {
	if parsed, err := uuid.Parse(inbound); err == nil {
		return parsed.String()
	}
	return uuid.NewString()
}

// RequestIDFromContext returns the request ID stored by RequestID, or the empty
// string if the middleware did not run for this context.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
