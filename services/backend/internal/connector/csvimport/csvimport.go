// Package csvimport implements connector.Connector for a platform served from a
// creator's own uploaded data rather than a live API. It is the real-data path
// for Instagram while the Meta Graph API grant is pending review: the dataimport
// module stores the creator's uploaded Insights export, and this connector reads
// the latest upload for a handle back at audit time.
//
// It fabricates nothing. When no upload exists for a handle it returns a
// not-found error, so the orchestrator records that platform as failed and
// proceeds with a partial audit rather than inventing numbers. A CSV upload
// carries no per-comment data, so every snapshot it produces is marked Partial:
// the coordination (co-commenter) signal genuinely cannot be computed from this
// source, and the audit must say so.
package csvimport

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Connector serves a platform's data from uploaded datasets in imported_dataset.
type Connector struct {
	platform connector.Platform
	pool     *db.Pool
}

// New builds a csvimport connector for a platform over the shared pool. Instagram
// is the platform it serves today; the platform is a parameter so the same
// upload-backed path can serve another network later without a new type.
func New(platform connector.Platform, pool *db.Pool) *Connector {
	return &Connector{platform: platform, pool: pool}
}

// Platform returns the platform this connector serves.
func (c *Connector) Platform() connector.Platform { return c.platform }

// Capabilities reports what an uploaded dataset can supply. A CSV Insights export
// carries profile reach, a follower point, and recent posts, but no per-comment
// data and no audience demographics, so those capabilities are not advertised.
func (c *Connector) Capabilities() []connector.Capability {
	return []connector.Capability{
		connector.CapabilityProfile,
		connector.CapabilityMetrics,
		connector.CapabilityRecentPosts,
	}
}

// storedDataset is the row shape the connector reads back. The JSON columns are
// the connector's own types, written by the dataimport module, so no translation
// layer sits between upload and audit.
type storedDataset struct {
	followers  int64
	capturedAt time.Time
	posts      []connector.Post
	metrics    []connector.MetricPoint
	comments   []connector.Comment
}

// Fetch returns a Snapshot built from the most recent uploaded dataset for the
// requested handle. It honours ctx cancellation through the query. With no
// upload for the handle it returns a not-found error and no snapshot.
func (c *Connector) Fetch(ctx context.Context, req connector.FetchRequest) (connector.Snapshot, error) {
	ds, err := c.latest(ctx, req.Handle)
	if err != nil {
		return connector.Snapshot{}, err
	}

	posts := ds.posts
	if req.MaxPosts > 0 && len(posts) > req.MaxPosts {
		posts = posts[:req.MaxPosts]
	}

	return connector.Snapshot{
		Platform:   c.platform,
		Handle:     req.Handle,
		AccountID:  req.AccountID,
		CapturedAt: ds.capturedAt,
		Followers:  ds.followers,
		Metrics:    ds.metrics,
		Posts:      posts,
		Comments:   ds.comments,
		// Always partial: an uploaded export is inherently less complete than a
		// live API pull (no comment-level data, no audience breakdown), so the
		// audit must never read it as full coverage.
		Partial: true,
	}, nil
}

// latest reads the most recent uploaded dataset for (platform, handle).
func (c *Connector) latest(ctx context.Context, handle string) (storedDataset, error) {
	const q = `SELECT followers, captured_at, posts_jsonb, metrics_jsonb, comments_jsonb
		FROM imported_dataset
		WHERE platform = $1::platform AND handle = $2
		ORDER BY created_at DESC
		LIMIT 1`

	var (
		ds                       storedDataset
		postsRaw, metRaw, comRaw []byte
	)
	err := c.pool.QueryRow(ctx, q, string(c.platform), handle).
		Scan(&ds.followers, &ds.capturedAt, &postsRaw, &metRaw, &comRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storedDataset{}, errs.New(errs.KindNotFound, "csvimport.no_dataset",
				"no uploaded data exists for this handle; upload an Instagram export first")
		}
		return storedDataset{}, errs.Wrap(err, errs.KindUnavailable, "csvimport.read_failed",
			"could not read the uploaded dataset")
	}

	if err := decodeJSON(postsRaw, &ds.posts); err != nil {
		return storedDataset{}, err
	}
	if err := decodeJSON(metRaw, &ds.metrics); err != nil {
		return storedDataset{}, err
	}
	if err := decodeJSON(comRaw, &ds.comments); err != nil {
		return storedDataset{}, err
	}
	return ds, nil
}

// decodeJSON unmarshals a stored JSON column, treating an empty/NULL column as an
// empty slice rather than an error.
func decodeJSON(raw []byte, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return errs.Wrap(err, errs.KindInternal, "csvimport.decode_failed",
			"stored dataset is corrupt and could not be decoded")
	}
	return nil
}
