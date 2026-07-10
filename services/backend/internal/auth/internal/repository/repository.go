// Package repository implements the auth module's data access: user records and
// the refresh-token session family. It stores only the SHA-256 hash of a refresh
// token, never the token itself, so a database disclosure cannot be replayed.
package repository

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Repository is the data-access contract the auth service depends on.
//
// It is hand-written, and apigen's repository layer is deliberately not
// generated for this module. apigen mirrors the *API* interface into the
// repository, producing methods like Register(ctx, RegisterRequest) — a
// request/response shape, not a data shape. Auth's persistence is row-level
// access to the users and sessions tables, which that mirror cannot express
// without dragging business logic into the data layer. RotateSession is the one
// multi-statement operation, kept atomic here so the service never has to
// juggle a transaction.
type Repository interface {
	CreateUser(ctx context.Context, in model.NewUser) (model.User, error)
	UserByEmail(ctx context.Context, email string) (model.User, error)
	UserByID(ctx context.Context, id uuid.UUID) (model.User, error)

	CreateSession(ctx context.Context, in model.NewSession) (model.Session, error)
	SessionByRefreshHash(ctx context.Context, hash []byte) (model.Session, error)
	RevokeSession(ctx context.Context, id uuid.UUID) error
	RevokeUserSessions(ctx context.Context, userID uuid.UUID) error
	RotateSession(ctx context.Context, oldID uuid.UUID, next model.NewSession) (model.Session, error)
}

// Sentinel errors let the service translate a known data condition into the
// right domain error and message without the repository deciding transport
// concerns.
var (
	// ErrUserNotFound means no user matched the lookup.
	ErrUserNotFound = errors.New("repository: user not found")
	// ErrEmailTaken means the email violates the case-insensitive unique index.
	ErrEmailTaken = errors.New("repository: email already registered")
	// ErrSessionNotFound means no live session matched, including the case where
	// a rotation lost the race because the row was already revoked.
	ErrSessionNotFound = errors.New("repository: session not found")
)

// uniqueViolation is the PostgreSQL SQLSTATE for a unique-constraint breach.
const uniqueViolation = "23505"

// pgRepository is the PostgreSQL-backed Repository.
type pgRepository struct {
	pool *db.Pool
}

var _ Repository = (*pgRepository)(nil)

// New returns a Repository backed by pool.
func New(pool *db.Pool) Repository {
	return &pgRepository{pool: pool}
}

const userColumns = `id, email, password_hash, full_name, role, status, email_verified, created_at, updated_at`

// CreateUser inserts a user and returns the stored row. A duplicate email maps
// to ErrEmailTaken.
func (r *pgRepository) CreateUser(ctx context.Context, in model.NewUser) (model.User, error) {
	const query = `
INSERT INTO users (email, password_hash, full_name)
VALUES ($1, $2, $3)
RETURNING ` + userColumns

	row := r.pool.QueryRow(ctx, query, in.Email, nullString(in.PasswordHash), nullString(in.FullName))
	user, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return model.User{}, ErrEmailTaken
		}
		return model.User{}, wrapDB(err, "create user")
	}
	return user, nil
}

// UserByEmail looks up a user case-insensitively, matching the lower(email)
// unique index.
func (r *pgRepository) UserByEmail(ctx context.Context, email string) (model.User, error) {
	const query = `SELECT ` + userColumns + ` FROM users WHERE lower(email) = lower($1)`

	user, err := scanUser(r.pool.QueryRow(ctx, query, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.User{}, ErrUserNotFound
		}
		return model.User{}, wrapDB(err, "user by email")
	}
	return user, nil
}

// UserByID looks up a user by primary key.
func (r *pgRepository) UserByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	const query = `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	user, err := scanUser(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.User{}, ErrUserNotFound
		}
		return model.User{}, wrapDB(err, "user by id")
	}
	return user, nil
}

const sessionColumns = `id, user_id, refresh_token_hash, user_agent, host(ip) AS ip, issued_at, expires_at, revoked_at`

// CreateSession inserts a session and returns the stored row.
func (r *pgRepository) CreateSession(ctx context.Context, in model.NewSession) (model.Session, error) {
	session, err := insertSession(ctx, r.pool, in)
	if err != nil {
		return model.Session{}, wrapDB(err, "create session")
	}
	return session, nil
}

// SessionByRefreshHash looks up a session by the SHA-256 hash of its refresh
// token. A revoked or expired row is still returned; the caller decides what a
// non-live session means.
func (r *pgRepository) SessionByRefreshHash(ctx context.Context, hash []byte) (model.Session, error) {
	const query = `SELECT ` + sessionColumns + ` FROM sessions WHERE refresh_token_hash = $1`

	session, err := scanSession(r.pool.QueryRow(ctx, query, hash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Session{}, ErrSessionNotFound
		}
		return model.Session{}, wrapDB(err, "session by refresh hash")
	}
	return session, nil
}

// RevokeSession marks one session revoked. It is idempotent: revoking an
// already-revoked or absent session is not an error.
func (r *pgRepository) RevokeSession(ctx context.Context, id uuid.UUID) error {
	const query = `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`

	if _, err := r.pool.Exec(ctx, query, id); err != nil {
		return wrapDB(err, "revoke session")
	}
	return nil
}

// RevokeUserSessions revokes every live session of a user. It is the response to
// detected refresh-token reuse: with no session-lineage column in the schema,
// the whole of a user's session set is treated as the compromised family.
func (r *pgRepository) RevokeUserSessions(ctx context.Context, userID uuid.UUID) error {
	const query = `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`

	if _, err := r.pool.Exec(ctx, query, userID); err != nil {
		return wrapDB(err, "revoke user sessions")
	}
	return nil
}

// RotateSession atomically revokes the old session and inserts its replacement.
// The revoke is conditional on the row still being live, so two concurrent
// refreshes of the same token cannot both succeed: the loser updates zero rows
// and gets ErrSessionNotFound, which the service treats as reuse.
func (r *pgRepository) RotateSession(ctx context.Context, oldID uuid.UUID, next model.NewSession) (model.Session, error) {
	const revoke = `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`

	var rotated model.Session
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, revoke, oldID)
		if err != nil {
			return wrapDB(err, "revoke rotated session")
		}
		if tag.RowsAffected() != 1 {
			return ErrSessionNotFound
		}

		rotated, err = insertSession(ctx, tx, next)
		if err != nil {
			return wrapDB(err, "insert rotated session")
		}
		return nil
	})
	if err != nil {
		return model.Session{}, err
	}
	return rotated, nil
}

// querier is the subset of pgx used to insert a session, satisfied by both the
// pool and a transaction so insertSession serves the plain and rotated paths.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertSession inserts one session over q and returns the stored row.
func insertSession(ctx context.Context, q querier, in model.NewSession) (model.Session, error) {
	const query = `
INSERT INTO sessions (user_id, refresh_token_hash, user_agent, ip, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING ` + sessionColumns

	row := q.QueryRow(ctx, query, in.UserID, in.RefreshTokenHash, nullString(in.UserAgent), inetArg(in.IP), in.ExpiresAt)
	return scanSession(row)
}

// scanUser reads one users row, mapping the nullable password_hash and full_name
// columns onto empty strings.
func scanUser(row pgx.Row) (model.User, error) {
	var (
		u            model.User
		passwordHash *string
		fullName     *string
	)
	if err := row.Scan(&u.ID, &u.Email, &passwordHash, &fullName, &u.Role, &u.Status, &u.EmailVerified, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return model.User{}, err
	}
	u.PasswordHash = deref(passwordHash)
	u.FullName = deref(fullName)
	return u, nil
}

// scanSession reads one sessions row, mapping the nullable user_agent, ip, and
// revoked_at columns.
func scanSession(row pgx.Row) (model.Session, error) {
	var (
		s         model.Session
		userAgent *string
		ip        *string
		revokedAt *time.Time
	)
	if err := row.Scan(&s.ID, &s.UserID, &s.RefreshTokenHash, &userAgent, &ip, &s.IssuedAt, &s.ExpiresAt, &revokedAt); err != nil {
		return model.Session{}, err
	}
	s.UserAgent = deref(userAgent)
	s.IP = deref(ip)
	s.RevokedAt = revokedAt
	return s, nil
}

// nullString returns nil for the empty string so an absent optional value is
// stored as SQL NULL rather than an empty string.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// inetArg parses ip into a netip.Addr for the inet column, returning nil (SQL
// NULL) when the address is empty or unparseable. A bad client-reported address
// must not fail an otherwise valid login.
func inetArg(ip string) any {
	if ip == "" {
		return nil
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	return addr
}

// deref returns the pointed-to string, or "" for a nil pointer.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// wrapDB turns an infrastructure failure into a generic, retryable domain error.
// The underlying cause is preserved for logs but never reaches the client.
func wrapDB(err error, op string) error {
	return errs.Wrap(err, errs.KindUnavailable, "auth.storage_unavailable", "auth storage is temporarily unavailable: "+op)
}
