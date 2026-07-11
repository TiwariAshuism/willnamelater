// Package repository is the admin module's data-access layer. It owns every SQL
// statement against the dispute table and maps rows to and from the module's
// domain types. It satisfies the service.Repository contract; the service
// depends only on that interface.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/getnyx/influaudit/backend/internal/admin/internal/model"
	"github.com/getnyx/influaudit/backend/internal/admin/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// disputeColumns is the dispute projection every read shares. uuid columns are
// cast to text so they scan into strings; the nullable actor columns scan into
// *string and the resolved timestamp into *time.Time.
const disputeColumns = "id::text, audit_job_id::text, raised_by::text, reason, " +
	"status, resolution, resolved_by::text, resolved_at, created_at, updated_at"

// rowScanner is the read surface shared by pgx.Row and pgx.Rows, letting one
// scan helper serve both single-row and multi-row queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// PostgresRepository is the pgx-backed service.Repository.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

var _ service.Repository = (*PostgresRepository)(nil)

// New builds a PostgresRepository over pool.
func New(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// CreateDispute files a new open dispute against an audit. A foreign-key
// violation on audit_job_id means the audit does not exist and surfaces as a
// not-found domain error rather than a generic 500, so a client cannot tell a
// missing audit apart from any other by the status code alone.
func (r *PostgresRepository) CreateDispute(ctx context.Context, params model.CreateDisputeParams) (model.Dispute, error) {
	const q = "INSERT INTO dispute (audit_job_id, raised_by, reason, status) " +
		"VALUES ($1, $2, $3, 'open') RETURNING " + disputeColumns

	d, err := scanDispute(r.pool.QueryRow(ctx, q, params.AuditJobID, params.RaisedBy, params.Reason))
	if err != nil {
		if isForeignKeyViolation(err) {
			return model.Dispute{}, errAuditNotFound()
		}
		return model.Dispute{}, errs.Wrap(err, errs.KindInternal, "admin.dispute_create_failed", "could not file dispute")
	}
	return d, nil
}

// ListOpenDisputes returns every open dispute oldest-first, so the review queue
// is worked in the order disputes were filed.
func (r *PostgresRepository) ListOpenDisputes(ctx context.Context) ([]model.Dispute, error) {
	const q = "SELECT " + disputeColumns + " FROM dispute WHERE status = 'open' " +
		"ORDER BY created_at ASC, id ASC"

	return r.queryDisputes(ctx, q)
}

// ListDecidedDisputes returns every resolved or rejected dispute, newest
// decision first. It backs the training-label export: each decided dispute is
// one labelled example.
func (r *PostgresRepository) ListDecidedDisputes(ctx context.Context) ([]model.Dispute, error) {
	const q = "SELECT " + disputeColumns + " FROM dispute " +
		"WHERE status IN ('resolved', 'rejected') " +
		"ORDER BY resolved_at DESC, id DESC"

	return r.queryDisputes(ctx, q)
}

// ResolveDispute records an admin's decision on an open dispute. The update is
// guarded by status = 'open' so a second resolve of the same dispute changes
// nothing; when no row is updated the dispute is disambiguated into a not-found
// or an already-resolved conflict rather than a silent no-op.
func (r *PostgresRepository) ResolveDispute(ctx context.Context, params model.ResolveDisputeParams) (model.Dispute, error) {
	const q = "UPDATE dispute SET status = $2, resolution = $3, resolved_by = $4, resolved_at = now() " +
		"WHERE id = $1 AND status = 'open' RETURNING " + disputeColumns

	d, err := scanDispute(r.pool.QueryRow(ctx, q, params.ID, string(params.Status), nullString(params.Resolution), params.ResolvedBy))
	if err == nil {
		return d, nil
	}
	if !notFound(err) {
		return model.Dispute{}, errs.Wrap(err, errs.KindInternal, "admin.dispute_resolve_failed", "could not resolve dispute")
	}

	// No open row was updated: tell a missing dispute apart from one already
	// decided so the caller gets a 404 or a 409 rather than a bare 500.
	exists, existErr := r.disputeExists(ctx, params.ID)
	if existErr != nil {
		return model.Dispute{}, existErr
	}
	if !exists {
		return model.Dispute{}, errDisputeNotFound()
	}
	return model.Dispute{}, errAlreadyResolved()
}

// disputeExists reports whether a dispute row with id is present.
func (r *PostgresRepository) disputeExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM dispute WHERE id = $1)", id).Scan(&exists)
	if err != nil {
		return false, errs.Wrap(err, errs.KindInternal, "admin.dispute_read_failed", "could not read dispute")
	}
	return exists, nil
}

// queryDisputes runs a disputeColumns query taking no parameters and scans the
// rows into domain disputes.
func (r *PostgresRepository) queryDisputes(ctx context.Context, q string) ([]model.Dispute, error) {
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "admin.dispute_list_failed", "could not list disputes")
	}
	defer rows.Close()

	disputes := make([]model.Dispute, 0)
	for rows.Next() {
		d, scanErr := scanDispute(rows)
		if scanErr != nil {
			return nil, errs.Wrap(scanErr, errs.KindInternal, "admin.dispute_list_failed", "could not list disputes")
		}
		disputes = append(disputes, d)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "admin.dispute_list_failed", "could not list disputes")
	}
	return disputes, nil
}

// scanDispute reads one dispute row into a domain Dispute. The nullable actor
// columns become uuid.Nil when SQL NULL (an account deleted, or an unresolved
// dispute's resolver), so the domain type never carries a bogus zero uuid it
// cannot distinguish from absence.
func scanDispute(row rowScanner) (model.Dispute, error) {
	var (
		id         string
		auditJobID string
		raisedBy   *string
		reason     string
		status     string
		resolution *string
		resolvedBy *string
		resolvedAt *time.Time
		createdAt  time.Time
		updatedAt  time.Time
	)

	if err := row.Scan(&id, &auditJobID, &raisedBy, &reason, &status,
		&resolution, &resolvedBy, &resolvedAt, &createdAt, &updatedAt); err != nil {
		return model.Dispute{}, err
	}

	disputeID, err := uuid.Parse(id)
	if err != nil {
		return model.Dispute{}, err
	}
	auditUUID, err := uuid.Parse(auditJobID)
	if err != nil {
		return model.Dispute{}, err
	}
	raisedUUID, err := parseNullableUUID(raisedBy)
	if err != nil {
		return model.Dispute{}, err
	}
	resolvedUUID, err := parseNullableUUID(resolvedBy)
	if err != nil {
		return model.Dispute{}, err
	}

	return model.Dispute{
		ID:         disputeID,
		AuditJobID: auditUUID,
		RaisedBy:   raisedUUID,
		Reason:     reason,
		Status:     model.Status(status),
		Resolution: deref(resolution),
		ResolvedBy: resolvedUUID,
		ResolvedAt: resolvedAt,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

// parseNullableUUID parses a text uuid pointer, returning uuid.Nil for a nil
// pointer (SQL NULL).
func parseNullableUUID(s *string) (uuid.UUID, error) {
	if s == nil {
		return uuid.Nil, nil
	}
	return uuid.Parse(*s)
}

// nullString maps an empty string to a nil *string so an unset resolution is
// stored as SQL NULL rather than an empty string.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// deref returns the pointed-to string, or "" when the pointer is nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
