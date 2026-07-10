// Package repository persists uploaded datasets. It writes the imported_dataset
// table; the csvimport connector reads the same table through its own reader, so
// the two share the table's JSON column shapes (connector.Post / MetricPoint /
// Comment) as their contract.
package repository

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Repository stores datasets over the shared pool.
type Repository struct {
	pool *db.Pool
}

// New builds the repository.
func New(pool *db.Pool) *Repository {
	return &Repository{pool: pool}
}

// Insert stores one uploaded dataset and returns its id. The posts, metrics, and
// comments are marshalled to JSON in the connector's own shapes so the connector
// reads them back without a translation layer.
func (r *Repository) Insert(ctx context.Context, userID, influencerID uuid.UUID, platform connector.Platform, ds model.Dataset) (uuid.UUID, error) {
	posts, err := marshalJSON(ds.Posts)
	if err != nil {
		return uuid.Nil, err
	}
	metrics, err := marshalJSON(ds.Metrics)
	if err != nil {
		return uuid.Nil, err
	}
	comments, err := marshalJSON(ds.Comments)
	if err != nil {
		return uuid.Nil, err
	}

	const q = `INSERT INTO imported_dataset
		(user_id, influencer_id, platform, handle, followers, source, posts_jsonb, metrics_jsonb, comments_jsonb, captured_at)
		VALUES ($1, $2, $3::platform, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10)
		RETURNING id`

	// A nil influencer id stores NULL rather than the zero uuid, which would
	// violate the foreign key.
	var inf any
	if influencerID != uuid.Nil {
		inf = influencerID
	}

	var id uuid.UUID
	if err := r.pool.QueryRow(ctx, q,
		userID, inf, string(platform), ds.Handle, ds.Followers, ds.Source,
		posts, metrics, comments, ds.CapturedAt,
	).Scan(&id); err != nil {
		return uuid.Nil, errs.Wrap(err, errs.KindUnavailable, "dataimport.insert_failed", "could not store the uploaded dataset")
	}
	return id, nil
}

// marshalJSON encodes a slice to JSON, substituting an empty array for a nil
// slice so the stored column is always a valid JSON array, never null.
func marshalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "dataimport.encode_failed", "could not encode the dataset")
	}
	if string(b) == "null" {
		return []byte("[]"), nil
	}
	return b, nil
}
