// Package errs defines the error vocabulary shared across modules and the
// mapping from those errors to HTTP status codes.
//
// Handlers must not construct HTTP status codes directly. They classify a
// service error through Status and let the middleware render it. This keeps
// business meaning in the service layer and transport concerns in the handler.
package errs

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind classifies an error by the caller-visible outcome it implies.
type Kind int

// The Kind values below are the complete set of caller-visible outcomes. Each
// maps to exactly one HTTP status in statusByKind.
const (
	// KindInternal is the zero value: an unexpected failure. Never surfaced
	// verbatim to clients.
	KindInternal       Kind = iota
	KindInvalid             // malformed or semantically invalid input
	KindUnauthorized        // missing or invalid credentials
	KindForbidden           // authenticated but not permitted
	KindNotFound            // addressed resource does not exist
	KindConflict            // violates a uniqueness or state invariant
	KindQuotaExceeded       // caller is over their plan or API quota
	KindRateLimited         // upstream or internal rate limit hit; retryable
	KindUnavailable         // dependency down; retryable
	KindNotImplemented      // scaffolded but not yet wired
)

// Error is a domain error carrying a Kind, a stable machine-readable code, a
// human-readable message safe to return to clients, and an optional cause.
type Error struct {
	Kind    Kind
	Code    string // stable identifier, e.g. "audit.quota_exceeded"
	Message string // safe to expose to the client
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// New builds an Error without a cause.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Wrap builds an Error carrying an underlying cause. The cause is never
// rendered to clients; it exists for logs and errors.Is/As.
func Wrap(cause error, kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message, cause: cause}
}

// KindOf reports the Kind of err, defaulting to KindInternal for errors that
// did not originate in a module's domain layer.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// statusByKind is the single source of truth for error-to-transport mapping.
var statusByKind = map[Kind]int{
	KindInternal:       http.StatusInternalServerError,
	KindInvalid:        http.StatusBadRequest,
	KindUnauthorized:   http.StatusUnauthorized,
	KindForbidden:      http.StatusForbidden,
	KindNotFound:       http.StatusNotFound,
	KindConflict:       http.StatusConflict,
	KindQuotaExceeded:  http.StatusPaymentRequired,
	KindRateLimited:    http.StatusTooManyRequests,
	KindUnavailable:    http.StatusServiceUnavailable,
	KindNotImplemented: http.StatusNotImplemented,
}

// Status maps err to the HTTP status code that should be returned for it.
func Status(err error) int {
	if status, ok := statusByKind[KindOf(err)]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// ErrNotImplemented marks a module that is scaffolded but not yet wired. It is
// returned at the service boundary of deferred modules so that calling one is
// an explicit, typed failure rather than a silent nil result.
var ErrNotImplemented = New(KindNotImplemented, "not_implemented", "this capability is not available yet")
