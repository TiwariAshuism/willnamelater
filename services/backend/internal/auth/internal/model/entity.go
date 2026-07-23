package model

import (
	"time"

	"github.com/google/uuid"
)

// Account roles. They mirror the CHECK constraint on users.role.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Account statuses. They mirror the CHECK constraint on users.status. Only
// StatusActive may authenticate.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusDeleted   = "deleted"
)

// User is a row of the users table. PasswordHash is empty for accounts that
// authenticate only through a social provider (the column is NULL there).
type User struct {
	ID            uuid.UUID
	Email         string
	PasswordHash  string
	FullName      string
	Role          string
	Status        string
	EmailVerified bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewUser carries the fields needed to insert a user. The database assigns the
// id, role, status, and timestamps.
type NewUser struct {
	Email        string
	PasswordHash string
	FullName     string
}

// Session is a row of the sessions table. RefreshTokenHash is the SHA-256 digest
// of the opaque refresh token; the token itself is never stored. RevokedAt is
// nil while the session is live.
type Session struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	RefreshTokenHash []byte
	UserAgent        string
	IP               string
	IssuedAt         time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
}

// Revoked reports whether the session has been revoked.
func (s Session) Revoked() bool { return s.RevokedAt != nil }

// Expired reports whether the session's refresh window has closed at now.
func (s Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }

// NewSession carries the fields needed to insert a session. The database assigns
// the id and issued_at.
type NewSession struct {
	UserID           uuid.UUID
	RefreshTokenHash []byte
	UserAgent        string
	IP               string
	ExpiresAt        time.Time
}
