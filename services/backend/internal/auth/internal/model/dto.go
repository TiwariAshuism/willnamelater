// Package model holds the auth module's request/response DTOs and the domain
// entities that back the users and sessions tables. The DTOs are the types the
// apigen-generated service and repository interfaces are expressed in; the
// entities are the shapes the repository reads and writes.
package model

import (
	"time"

	"github.com/google/uuid"
)

// RegisterRequest is the body of POST /auth/register.
//
// UserAgent and IP are transport metadata recorded on the created session. They
// are populated by the handler from the HTTP request and are never bound from
// the client-supplied JSON, so a caller cannot spoof them.
type RegisterRequest struct {
	Email     string `json:"email" binding:"required"`
	Password  string `json:"password" binding:"required"`
	FullName  string `json:"full_name"`
	UserAgent string `json:"-"`
	IP        string `json:"-"`
}

// LoginRequest is the body of POST /auth/login.
type LoginRequest struct {
	Email     string `json:"email" binding:"required"`
	Password  string `json:"password" binding:"required"`
	UserAgent string `json:"-"`
	IP        string `json:"-"`
}

// RefreshRequest is the body of POST /auth/refresh. RefreshToken is the opaque
// base64url token handed to the client by a previous auth response.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
	UserAgent    string `json:"-"`
	IP           string `json:"-"`
}

// LogoutRequest is the body of POST /auth/logout.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// AuthResponse is returned by register, login, and refresh. RefreshToken is the
// only time the opaque refresh secret is ever exposed; only its hash is stored.
type AuthResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	TokenType    string       `json:"token_type"`
	ExpiresIn    int64        `json:"expires_in"`
	User         UserResponse `json:"user"`
}

// UserResponse is the client-safe projection of a user. It never carries the
// password hash, status, or any other internal field.
type UserResponse struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	FullName      string    `json:"full_name,omitempty"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
}
