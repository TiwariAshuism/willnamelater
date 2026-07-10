// Package repository is the oauth module's data-access layer. It holds the two
// stores behind the service's ports: a pgx-backed TokenStore over the
// oauth_token table and a Redis-backed StateStore over the short-lived CSRF/PKCE
// state. Both persist ciphertext or opaque tokens only; neither ever sees a
// plaintext credential.
package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// pgUniqueViolation is the SQLSTATE PostgreSQL returns for a unique-constraint
// violation. On the oauth_token upsert it is the signal that two writers raced
// on the same (user_id, platform, provider_account_id) key.
const pgUniqueViolation = "23505"

// oauth_token stores ciphertext only: access_token_enc, refresh_token_enc, and
// dek_wrapped are bytea envelopes sealed by the service before they reach this
// layer. There is deliberately no plaintext token column, so none is written or
// read here.
const (
	insertColumns     = "user_id, platform, provider_account_id, access_token_enc, refresh_token_enc, dek_wrapped, scopes, access_expires_at"
	connectionColumns = "platform, provider_account_id, scopes, created_at, access_expires_at"
)

// tokenStore is the pgx-backed service.TokenStore.
type tokenStore struct {
	pool *db.Pool
}

var _ service.TokenStore = (*tokenStore)(nil)

// NewTokenStore builds the pgx-backed TokenStore over pool. It returns the port
// interface so callers depend on the contract, not this concrete type.
func NewTokenStore(pool *db.Pool) service.TokenStore {
	return &tokenStore{pool: pool}
}

// Upsert inserts a sealed token record, or refreshes the sealed columns of an
// existing connection when the same account is reconnected.
//
// The conflict target is (user_id, platform, provider_account_id): that is the
// UNIQUE constraint migration 000003 actually declares on oauth_token. Postgres
// requires the ON CONFLICT target to match an existing unique constraint, so a
// two-column (user_id, platform) target would raise "no unique or exclusion
// constraint matching the ON CONFLICT specification" at runtime. Keying on the
// full constraint lets a user hold several accounts on one platform, each its
// own row, which the connection model (ListByUser, DeleteByUserPlatform) already
// supports.
func (s *tokenStore) Upsert(ctx context.Context, tok model.EncryptedToken) error {
	const q = "INSERT INTO oauth_token (" + insertColumns + ") " +
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8) " +
		"ON CONFLICT (user_id, platform, provider_account_id) DO UPDATE SET " +
		"access_token_enc = EXCLUDED.access_token_enc, " +
		"refresh_token_enc = EXCLUDED.refresh_token_enc, " +
		"dek_wrapped = EXCLUDED.dek_wrapped, " +
		"scopes = EXCLUDED.scopes, " +
		"access_expires_at = EXCLUDED.access_expires_at"

	_, err := s.pool.Exec(ctx, q,
		tok.UserID,
		tok.Platform,
		tok.ProviderAccountID,
		tok.AccessTokenEnc,
		tok.RefreshTokenEnc,
		tok.DEKWrapped,
		tok.Scopes,
		tok.AccessExpiresAt,
	)
	if err != nil {
		// The upsert resolves the only unique constraint, so a unique violation
		// is not expected on the happy path; it is mapped to a conflict rather
		// than a generic 500 so a concurrent write racing on the unique index
		// surfaces as a retryable 409 instead of leaking as an internal error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return errs.Wrap(err, errs.KindConflict, "oauth.token_conflict",
				"this connection already exists")
		}
		return errs.Wrap(err, errs.KindUnavailable, "oauth.token_persist_failed",
			"could not persist the connection")
	}
	return nil
}

// ListByUser returns the caller's connections as non-secret projections, oldest
// first. The projection excludes every ciphertext column so a listing never
// pulls a sealed token into memory.
func (s *tokenStore) ListByUser(ctx context.Context, userID uuid.UUID) ([]model.Connection, error) {
	const q = "SELECT " + connectionColumns + " FROM oauth_token WHERE user_id = $1 " +
		"ORDER BY created_at ASC, platform ASC"

	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "oauth.token_list_failed",
			"could not list connections")
	}
	defer rows.Close()

	conns := make([]model.Connection, 0)
	for rows.Next() {
		var c model.Connection
		if err := rows.Scan(&c.Platform, &c.ProviderAccountID, &c.Scopes, &c.ConnectedAt, &c.AccessExpiresAt); err != nil {
			return nil, errs.Wrap(err, errs.KindUnavailable, "oauth.token_list_failed",
				"could not list connections")
		}
		conns = append(conns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "oauth.token_list_failed",
			"could not list connections")
	}
	return conns, nil
}

// DeleteByUserPlatform removes every connection the caller holds on a platform
// and reports how many rows were deleted, so the service can distinguish a real
// disconnect from a no-op on a platform that was never connected.
func (s *tokenStore) DeleteByUserPlatform(ctx context.Context, userID uuid.UUID, platform string) (int64, error) {
	tag, err := s.pool.Exec(ctx, "DELETE FROM oauth_token WHERE user_id = $1 AND platform = $2", userID, platform)
	if err != nil {
		return 0, errs.Wrap(err, errs.KindUnavailable, "oauth.token_delete_failed",
			"could not disconnect the provider")
	}
	return tag.RowsAffected(), nil
}
