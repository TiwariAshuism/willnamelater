package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRecoveryPassesThroughWhenNoPanic(t *testing.T) {
	router := gin.New()
	router.Use(Recovery())
	router.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

// TestRecoveryHidesPanicValue asserts a recovered panic yields a generic 500
// whose body never contains the panic value, while the value and a stack trace
// are logged for diagnosis.
func TestRecoveryHidesPanicValue(t *testing.T) {
	const secret = "db_password=hunter2_SECRET"

	logs := captureLogs(t)

	router := gin.New()
	router.Use(RequestID(), Recovery())
	router.GET("/", func(*gin.Context) { panic("query failed: " + secret) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := rec.Body.String(); !strings.Contains(got, "internal server error") {
		t.Errorf("body = %q, want the generic internal message", got)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response body leaked the panic value: %s", rec.Body.String())
	}

	logged := logs.String()
	if !strings.Contains(logged, secret) {
		t.Error("expected the panic value to be logged")
	}
	if !strings.Contains(logged, "panic recovered") {
		t.Error("expected a 'panic recovered' log line")
	}
	// debug.Stack output always names the runtime panic frame.
	if !strings.Contains(logged, "panic") || !strings.Contains(logged, "goroutine") {
		t.Error("expected a stack trace to be logged")
	}
}

// TestRecoveryRepanicsAbortHandler asserts the Go convention that
// http.ErrAbortHandler is propagated rather than converted into a 500, whether
// it is panicked directly or wrapped by an intermediate handler.
func TestRecoveryRepanicsAbortHandler(t *testing.T) {
	tests := []struct {
		name       string
		panicValue any
	}{
		{"sentinel", http.ErrAbortHandler},
		{"wrapped sentinel", fmt.Errorf("upstream closed: %w", http.ErrAbortHandler)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(Recovery())
			router.GET("/", func(*gin.Context) { panic(tt.panicValue) })

			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected http.ErrAbortHandler to be re-panicked, but it was swallowed")
				}
				err, ok := r.(error)
				if !ok || !errors.Is(err, http.ErrAbortHandler) {
					t.Fatalf("re-panicked with %v, want an error wrapping http.ErrAbortHandler", r)
				}
			}()

			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		})
	}
}
