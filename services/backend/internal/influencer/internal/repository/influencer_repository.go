// Package repository is the influencer module's data-access layer. It owns every
// SQL statement against the influencer and influencer_handle tables and maps
// rows to and from the module's DTOs. The generated InfluencerRepository
// interface is its contract; the service depends only on that interface.
//
// The derived influencer.tier column is maintained here, not accepted from
// callers: whenever a handle changes, tier is recomputed from the handles'
// follower counts via the pure domain function model.TierForFollowers. The
// banding policy lives and is tested in the domain; this layer only applies it,
// the same way the database trigger maintains updated_at.
package repository

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/getnyx/influaudit/backend/internal/influencer/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Pagination bounds applied by the service before a request reaches the
// repository; restated as the safety net the SQL relies on.
const listHardCap = 100

const (
	influencerColumns = "id::text, display_name, niche, tier, country, created_at, updated_at"
	handleColumns     = "id::text, influencer_id::text, platform, handle, platform_user_id, " +
		"follower_count, verified, last_seen_at, created_at, updated_at"
)

// querier is the subset of pgx used by this repository, satisfied by both
// *pgxpool.Pool and pgx.Tx so the same statement helpers run inside or outside a
// transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PostgresRepository is the pgx-backed InfluencerRepository.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

var _ InfluencerRepository = (*PostgresRepository)(nil)

// New builds a PostgresRepository over pool.
func New(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// CreateInfluencer inserts a new influencer. tier is left NULL: it is derived
// from handles, of which a new influencer has none.
func (r *PostgresRepository) CreateInfluencer(ctx context.Context, req model.CreateInfluencerRequest) (model.InfluencerResponse, error) {
	const q = "INSERT INTO influencer (display_name, niche, country) VALUES ($1, $2, $3) RETURNING " + influencerColumns

	inf, err := scanInfluencer(r.pool.QueryRow(ctx, q, req.DisplayName, req.Niche, req.Country))
	if err != nil {
		return model.InfluencerResponse{}, errs.Wrap(err, errs.KindInternal, "influencer.create_failed", "could not create influencer")
	}
	return toInfluencerResponse(inf), nil
}

// GetInfluencer returns the influencer identified by id together with its
// handles, or errs.KindNotFound when no such row exists.
func (r *PostgresRepository) GetInfluencer(ctx context.Context, id string) (model.InfluencerResponse, error) {
	const q = "SELECT " + influencerColumns + " FROM influencer WHERE id = $1"

	inf, err := scanInfluencer(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if notFound(err) {
			return model.InfluencerResponse{}, errInfluencerNotFound()
		}
		return model.InfluencerResponse{}, errs.Wrap(err, errs.KindInternal, "influencer.get_failed", "could not load influencer")
	}

	handles, err := r.handlesFor(ctx, r.pool, id)
	if err != nil {
		return model.InfluencerResponse{}, err
	}
	inf.Handles = handles

	return toInfluencerResponse(inf), nil
}

// ListInfluencers returns a keyset page of influencers filtered by the optional
// niche and tier, newest first, with the cursor for the following page.
func (r *PostgresRepository) ListInfluencers(ctx context.Context, req model.ListInfluencersRequest) (model.ListInfluencersResponse, error) {
	limit := req.Limit
	if limit <= 0 || limit > listHardCap {
		limit = listHardCap
	}

	query := "SELECT " + influencerColumns + " FROM influencer"
	args := make([]any, 0, 4)
	conds := make([]string, 0, 3)

	if req.Niche != nil {
		args = append(args, *req.Niche)
		conds = append(conds, "niche = $"+strconv.Itoa(len(args)))
	}
	if req.Tier != nil {
		args = append(args, *req.Tier)
		conds = append(conds, "tier = $"+strconv.Itoa(len(args)))
	}
	if req.Cursor != "" {
		cur, err := decodeCursor(req.Cursor)
		if err != nil {
			return model.ListInfluencersResponse{}, err
		}
		args = append(args, cur.createdAt, cur.id.String())
		conds = append(conds, "(created_at, id) < ($"+strconv.Itoa(len(args)-1)+", $"+strconv.Itoa(len(args))+")")
	}

	for i, c := range conds {
		if i == 0 {
			query += " WHERE " + c
			continue
		}
		query += " AND " + c
	}

	// Fetch one extra row to learn whether a further page exists without a
	// second COUNT query.
	args = append(args, limit+1)
	query += " ORDER BY created_at DESC, id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return model.ListInfluencersResponse{}, errs.Wrap(err, errs.KindInternal, "influencer.list_failed", "could not list influencers")
	}
	defer rows.Close()

	influencers := make([]model.Influencer, 0, limit+1)
	for rows.Next() {
		inf, scanErr := scanInfluencer(rows)
		if scanErr != nil {
			return model.ListInfluencersResponse{}, errs.Wrap(scanErr, errs.KindInternal, "influencer.list_failed", "could not list influencers")
		}
		influencers = append(influencers, inf)
	}
	if err := rows.Err(); err != nil {
		return model.ListInfluencersResponse{}, errs.Wrap(err, errs.KindInternal, "influencer.list_failed", "could not list influencers")
	}

	return buildListResponse(influencers, limit), nil
}

// UpdateInfluencer applies a partial update: a nil field in req leaves the
// corresponding column unchanged. tier is not touched here; it tracks handles.
func (r *PostgresRepository) UpdateInfluencer(ctx context.Context, id string, req model.UpdateInfluencerRequest) (model.InfluencerResponse, error) {
	const q = `UPDATE influencer
	SET display_name = COALESCE($2, display_name),
	    niche        = COALESCE($3, niche),
	    country      = COALESCE($4, country)
	WHERE id = $1
	RETURNING ` + influencerColumns

	inf, err := scanInfluencer(r.pool.QueryRow(ctx, q, id, req.DisplayName, req.Niche, req.Country))
	if err != nil {
		if notFound(err) {
			return model.InfluencerResponse{}, errInfluencerNotFound()
		}
		return model.InfluencerResponse{}, errs.Wrap(err, errs.KindInternal, "influencer.update_failed", "could not update influencer")
	}

	handles, err := r.handlesFor(ctx, r.pool, id)
	if err != nil {
		return model.InfluencerResponse{}, err
	}
	inf.Handles = handles

	return toInfluencerResponse(inf), nil
}

// AddHandle inserts a handle for the influencer and recomputes the influencer's
// tier from the resulting set of handles, both within one transaction. A
// unique-constraint violation surfaces as errs.KindConflict.
func (r *PostgresRepository) AddHandle(ctx context.Context, id string, req model.AddHandleRequest) (model.HandleResponse, error) {
	var handle model.Handle

	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT true FROM influencer WHERE id = $1", id).Scan(&exists); err != nil {
			if notFound(err) {
				return errInfluencerNotFound()
			}
			return errs.Wrap(err, errs.KindInternal, "influencer.handle_owner_check_failed", "could not verify influencer")
		}

		const insert = `INSERT INTO influencer_handle
		(influencer_id, platform, handle, platform_user_id, follower_count, verified)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + handleColumns

		h, scanErr := scanHandle(tx.QueryRow(ctx, insert,
			id, req.Platform, req.Handle, req.PlatformUserID, req.FollowerCount, req.Verified))
		if scanErr != nil {
			return mapHandleWriteError(scanErr)
		}
		handle = h

		return recomputeTier(ctx, tx, id)
	})
	if err != nil {
		return model.HandleResponse{}, err
	}

	return toHandleResponse(handle), nil
}

// DeleteHandle removes a handle owned by the influencer and recomputes the
// influencer's tier from the remaining handles, within one transaction. It
// returns errs.KindNotFound when no such handle exists for that influencer.
func (r *PostgresRepository) DeleteHandle(ctx context.Context, id string, handleID string) error {
	return db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM influencer_handle WHERE id = $1 AND influencer_id = $2", handleID, id)
		if err != nil {
			return errs.Wrap(err, errs.KindInternal, "influencer.handle_delete_failed", "could not delete handle")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.KindNotFound, "influencer.handle_not_found", "handle does not exist for this influencer")
		}
		return recomputeTier(ctx, tx, id)
	})
}

// handlesFor loads the handles belonging to an influencer, oldest first.
func (r *PostgresRepository) handlesFor(ctx context.Context, q querier, influencerID string) ([]model.Handle, error) {
	const query = "SELECT " + handleColumns + " FROM influencer_handle WHERE influencer_id = $1 ORDER BY created_at ASC, id ASC"

	rows, err := q.Query(ctx, query, influencerID)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "influencer.handles_load_failed", "could not load handles")
	}
	defer rows.Close()

	handles := make([]model.Handle, 0)
	for rows.Next() {
		h, scanErr := scanHandle(rows)
		if scanErr != nil {
			return nil, errs.Wrap(scanErr, errs.KindInternal, "influencer.handles_load_failed", "could not load handles")
		}
		handles = append(handles, h)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "influencer.handles_load_failed", "could not load handles")
	}
	return handles, nil
}

// recomputeTier resets the influencer's tier to the band implied by the largest
// follower count among its handles, or NULL when no handle reports one. It runs
// on the same querier as the change that triggered it so the update is atomic
// with the handle write.
func recomputeTier(ctx context.Context, q querier, influencerID string) error {
	var maxFollowers *int64
	if err := q.QueryRow(ctx, "SELECT MAX(follower_count) FROM influencer_handle WHERE influencer_id = $1", influencerID).Scan(&maxFollowers); err != nil {
		return errs.Wrap(err, errs.KindInternal, "influencer.tier_recompute_failed", "could not recompute tier")
	}

	var tier *string
	if maxFollowers != nil {
		t := string(model.TierForFollowers(*maxFollowers))
		tier = &t
	}

	if _, err := q.Exec(ctx, "UPDATE influencer SET tier = $1 WHERE id = $2", tier, influencerID); err != nil {
		return errs.Wrap(err, errs.KindInternal, "influencer.tier_recompute_failed", "could not recompute tier")
	}
	return nil
}

// errInfluencerNotFound is the single not-found error for the influencer
// resource, kept here so the code and message stay identical across methods.
func errInfluencerNotFound() error {
	return errs.New(errs.KindNotFound, "influencer.not_found", "influencer does not exist")
}
