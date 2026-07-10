package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
)

// IngestRepository persists a normalized snapshot. Every method runs inside a
// caller-provided transaction so the three tables are written atomically: the
// service opens one tx (platform/db.InTx) and threads it through here.
type IngestRepository interface {
	// UpsertPosts writes rows into post and returns a map from each row's
	// platform_post_id to the post's database id, so the caller can resolve the
	// foreign key when it writes the associated comment samples.
	UpsertPosts(ctx context.Context, tx pgx.Tx, rows []model.PostRow) (map[string]uuid.UUID, error)
	// InsertMetricPoints writes rows into metric_point in a single batched
	// statement, upserting on the series key so a re-run of the same audit is
	// idempotent rather than a duplicate-key failure.
	InsertMetricPoints(ctx context.Context, tx pgx.Tx, rows []model.MetricPointRow) error
	// InsertComments writes rows into comment_sample. Rows carry only the keyed
	// author hash, never a raw author id.
	InsertComments(ctx context.Context, tx pgx.Tx, rows []model.CommentRow) error
}

// SaltStore reads and seeds the sealed, application-wide pseudonymization salt
// held in crypto_salt. It operates on the pool directly, outside the ingest
// transaction: the salt is loaded once for the process lifetime.
type SaltStore interface {
	// Load returns the sealed salt for name. The bool is false, with a nil
	// error, when no row exists yet.
	Load(ctx context.Context, name string) (crypto.Sealed, bool, error)
	// Insert seeds a sealed salt. It must be safe under a concurrent first boot:
	// a losing racer's INSERT is ignored (ON CONFLICT DO NOTHING) rather than
	// erroring, and the winner's row is what every node subsequently reads.
	Insert(ctx context.Context, name string, sealed crypto.Sealed) error
}
