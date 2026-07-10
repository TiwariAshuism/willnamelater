package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/token"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// fakeService is a programmable AuthService.
type fakeService struct {
	authResp model.AuthResponse
	userResp model.UserResponse
	err      error
	called   bool
}

func (f *fakeService) Register(context.Context, model.RegisterRequest) (model.AuthResponse, error) {
	f.called = true
	return f.authResp, f.err
}

func (f *fakeService) Login(context.Context, model.LoginRequest) (model.AuthResponse, error) {
	f.called = true
	return f.authResp, f.err
}

func (f *fakeService) Refresh(context.Context, model.RefreshRequest) (model.AuthResponse, error) {
	f.called = true
	return f.authResp, f.err
}

func (f *fakeService) Logout(context.Context, model.LogoutRequest) error {
	f.called = true
	return f.err
}

func (f *fakeService) Me(context.Context) (model.UserResponse, error) {
	f.called = true
	return f.userResp, f.err
}

// fakeVerifier returns preset claims and error for any token.
type fakeVerifier struct {
	claims token.Claims
	err    error
}

func (f fakeVerifier) Verify(string) (token.Claims, error) { return f.claims, f.err }

func registeredWithSubject(id uuid.UUID) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{Subject: id.String()}
}

func registeredWithRawSubject(sub string) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{Subject: sub}
}

func newRouter(h *Handler, v Verifier) *gin.Engine {
	r := gin.New()
	r.Use(httpx.RequestID())
	r.POST("/auth/register", h.Register)
	r.POST("/auth/login", h.Login)
	r.POST("/auth/refresh", h.Refresh)
	r.POST("/auth/logout", h.Logout)
	r.GET("/auth/me", RequireAuth(v), h.Me)
	return r
}

func do(r *gin.Engine, method, path, body, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRegisterSuccess(t *testing.T) {
	t.Parallel()

	svc := &fakeService{authResp: model.AuthResponse{AccessToken: "acc", RefreshToken: "ref", TokenType: "Bearer"}}
	r := newRouter(New(svc), fakeVerifier{})

	w := do(r, http.MethodPost, "/auth/register", `{"email":"a@b.com","password":"supersecret1"}`, "")

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"access_token":"acc"`) {
		t.Fatalf("body missing access token: %s", w.Body.String())
	}
}

func TestBindErrorIsRejected(t *testing.T) {
	t.Parallel()

	svc := &fakeService{}
	r := newRouter(New(svc), fakeVerifier{})

	w := do(r, http.MethodPost, "/auth/login", `{"email":"a@b.com"}`, "") // missing password

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if svc.called {
		t.Fatal("service must not be called when binding fails")
	}
	if !strings.Contains(w.Body.String(), `"code":"auth.invalid_request"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

// TestWrappedCauseNeverLeaks is the security-critical assertion: a service error
// that wraps a secret-bearing cause must never surface that cause to the client.
func TestWrappedCauseNeverLeaks(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t-postgres-dsn-and-password"
	svc := &fakeService{err: errs.Wrap(errors.New(secret), errs.KindInternal, "auth.boom", "internal error")}
	r := newRouter(New(svc), fakeVerifier{})

	w := do(r, http.MethodPost, "/auth/login", `{"email":"a@b.com","password":"supersecret1"}`, "")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("response leaked wrapped cause: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"auth.boom"`) {
		t.Fatalf("expected domain code in body: %s", w.Body.String())
	}
}

func TestLogoutReturnsNoContent(t *testing.T) {
	t.Parallel()

	svc := &fakeService{}
	r := newRouter(New(svc), fakeVerifier{})

	w := do(r, http.MethodPost, "/auth/logout", `{"refresh_token":"abc"}`, "")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestMeRequiresValidToken(t *testing.T) {
	t.Parallel()

	userID := uuid.New()

	tests := []struct {
		name       string
		verifier   Verifier
		authHeader string
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "valid token",
			verifier:   fakeVerifier{claims: token.Claims{Role: "user", RegisteredClaims: registeredWithSubject(userID)}},
			authHeader: "Bearer good.token.here",
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "missing header",
			verifier:   fakeVerifier{},
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong scheme",
			verifier:   fakeVerifier{},
			authHeader: "Basic abc",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "verify fails",
			verifier:   fakeVerifier{err: errors.New("bad signature")},
			authHeader: "Bearer bad",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "subject not a uuid",
			verifier:   fakeVerifier{claims: token.Claims{RegisteredClaims: registeredWithRawSubject("not-a-uuid")}},
			authHeader: "Bearer x",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeService{userResp: model.UserResponse{ID: userID, Email: "me@b.com"}}
			r := newRouter(New(svc), tc.verifier)

			w := do(r, http.MethodGet, "/auth/me", "", tc.authHeader)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if svc.called != tc.wantCalled {
				t.Fatalf("service called = %v, want %v", svc.called, tc.wantCalled)
			}
		})
	}
}
