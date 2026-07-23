package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// saltStore reads and seeds crypto_salt over the shared pool.
type saltStore struct {
	pool *db.Pool
}

// NewSaltStore builds the crypto_salt accessor over pool.
func NewSaltStore(pool *db.Pool) SaltStore {
	return &saltStore{pool: pool}
}

var _ SaltStore = (*saltStore)(nil)

// Load reads the sealed salt for name. A missing row is reported as (_, false,
// nil) so first-boot seeding is an ordinary control-flow branch, not an error.
func (s *saltStore) Load(ctx context.Context, name string) (crypto.Sealed, bool, error) {
	var sealed crypto.Sealed
	err := s.pool.QueryRow(ctx,
		`SELECT salt_enc, dek_wrapped FROM crypto_salt WHERE name = $1`, name,
	).Scan(&sealed.Ciphertext, &sealed.WrappedDEK)
	if errors.Is(err, pgx.ErrNoRows) {
		return crypto.Sealed{}, false, nil
	}
	if err != nil {
		return crypto.Sealed{}, false, errs.Wrap(err, errs.KindUnavailable, "metrics.salt_load", "could not read the comment-author salt")
	}
	return sealed, true, nil
}

// Insert seeds a sealed salt. ON CONFLICT DO NOTHING makes a concurrent first
// boot safe: whichever node inserts first wins, and the losers fall through to
// re-read the winner's row. crypto_salt forbids UPDATE and DELETE at the
// database, so this INSERT is the table's only mutation path.
func (s *saltStore) Insert(ctx context.Context, name string, sealed crypto.Sealed) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO crypto_salt (name, salt_enc, dek_wrapped) VALUES ($1, $2, $3) ON CONFLICT (name) DO NOTHING`,
		name, sealed.Ciphertext, sealed.WrappedDEK,
	); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "metrics.salt_insert", "could not seed the comment-author salt")
	}
	return nil
}
