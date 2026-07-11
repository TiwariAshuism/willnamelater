// Package repository is the audit module's data-access layer. It owns every SQL
// statement against audit_job and audit_platform_result and maps rows to and
// from the module's domain types. It satisfies the service.Repository contract;
// the service depends only on that interface.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/audit/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// jobColumns is the audit_job projection every job read shares. uuid and enum
// columns are cast to text so they scan into strings and typed values without a
// pgx type registration.
const jobColumns = "id::text, user_id::text, influencer_id::text, idempotency_key, " +
	"status::text, requested_platforms::text[], error_code, error_message, " +
	"requested_at, started_at, finished_at"

// resultColumns is the audit_platform_result projection.
const resultColumns = "platform::text, status, error_code, error_message, fetched_at"

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

// CreateJob inserts a queued job idempotently. The insert uses ON CONFLICT DO
// NOTHING on the idempotency key; when it inserts, the new row is returned with
// created=true. When the key already exists the existing job is loaded within
// the same transaction: it is returned with created=false if it belongs to the
// caller, or reported as a conflict if the key is held by another user, so a key
// can never attach a caller to someone else's job.
func (r *PostgresRepository) CreateJob(ctx context.Context, params model.CreateJobParams) (model.Job, bool, error) {
	const insert = "INSERT INTO audit_job (user_id, influencer_id, idempotency_key, requested_platforms, status) " +
		"VALUES ($1, $2, $3, $4::platform[], 'queued') " +
		"ON CONFLICT (idempotency_key) DO NOTHING " +
		"RETURNING " + jobColumns

	const selectExisting = "SELECT " + jobColumns + " FROM audit_job WHERE idempotency_key = $1"

	var (
		job     model.Job
		created bool
	)

	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		platforms := params.RequestedPlatforms
		if platforms == nil {
			platforms = []string{}
		}

		j, scanErr := scanJob(tx.QueryRow(ctx, insert, params.UserID, params.InfluencerID, params.IdempotencyKey, platforms))
		switch {
		case scanErr == nil:
			job = j
			created = true
			return nil
		case !notFound(scanErr):
			return errs.Wrap(scanErr, errs.KindInternal, "audit.create_failed", "could not create audit job")
		}

		// The insert conflicted on the idempotency key. Load the existing job.
		existing, getErr := scanJob(tx.QueryRow(ctx, selectExisting, params.IdempotencyKey))
		if getErr != nil {
			return errs.Wrap(getErr, errs.KindInternal, "audit.create_failed", "could not load existing audit job")
		}
		if existing.UserID != params.UserID {
			return errs.New(errs.KindConflict, "audit.idempotency_conflict", "idempotency key already in use")
		}
		job = existing
		created = false
		return nil
	})
	if err != nil {
		return model.Job{}, false, err
	}
	return job, created, nil
}

// DeleteJob removes a job by id.
func (r *PostgresRepository) DeleteJob(ctx context.Context, id uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, "DELETE FROM audit_job WHERE id = $1", id); err != nil {
		return errs.Wrap(err, errs.KindInternal, "audit.delete_failed", "could not delete audit job")
	}
	return nil
}

// GetJob returns a job by id regardless of owner.
func (r *PostgresRepository) GetJob(ctx context.Context, id uuid.UUID) (model.Job, error) {
	const q = "SELECT " + jobColumns + " FROM audit_job WHERE id = $1"

	job, err := scanJob(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if notFound(err) {
			return model.Job{}, errJobNotFound()
		}
		return model.Job{}, errs.Wrap(err, errs.KindInternal, "audit.get_failed", "could not load audit job")
	}
	return job, nil
}

// GetJobForUser returns the caller's job by id, or a not-found error when no job
// with that id belongs to the caller.
func (r *PostgresRepository) GetJobForUser(ctx context.Context, id, userID uuid.UUID) (model.Job, error) {
	const q = "SELECT " + jobColumns + " FROM audit_job WHERE id = $1 AND user_id = $2"

	job, err := scanJob(r.pool.QueryRow(ctx, q, id, userID))
	if err != nil {
		if notFound(err) {
			return model.Job{}, errJobNotFound()
		}
		return model.Job{}, errs.Wrap(err, errs.KindInternal, "audit.get_failed", "could not load audit job")
	}
	return job, nil
}

// ListJobsForUser returns the caller's jobs, newest first.
func (r *PostgresRepository) ListJobsForUser(ctx context.Context, userID uuid.UUID) ([]model.Job, error) {
	const q = "SELECT " + jobColumns + " FROM audit_job WHERE user_id = $1 ORDER BY requested_at DESC, id DESC"

	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "audit.list_failed", "could not list audits")
	}
	defer rows.Close()

	jobs := make([]model.Job, 0)
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, errs.Wrap(scanErr, errs.KindInternal, "audit.list_failed", "could not list audits")
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "audit.list_failed", "could not list audits")
	}
	return jobs, nil
}

// ListResults returns a job's per-platform results ordered by platform.
func (r *PostgresRepository) ListResults(ctx context.Context, jobID uuid.UUID) ([]model.PlatformResult, error) {
	const q = "SELECT " + resultColumns + " FROM audit_platform_result WHERE audit_job_id = $1 ORDER BY platform ASC"

	rows, err := r.pool.Query(ctx, q, jobID)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "audit.results_failed", "could not load platform results")
	}
	defer rows.Close()

	results := make([]model.PlatformResult, 0)
	for rows.Next() {
		res, scanErr := scanResult(rows)
		if scanErr != nil {
			return nil, errs.Wrap(scanErr, errs.KindInternal, "audit.results_failed", "could not load platform results")
		}
		results = append(results, res)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "audit.results_failed", "could not load platform results")
	}
	return results, nil
}

// SetRunning marks a job running, stamps started_at the first time, and counts
// the attempt.
func (r *PostgresRepository) SetRunning(ctx context.Context, id uuid.UUID) error {
	const q = "UPDATE audit_job SET status = 'running', " +
		"started_at = COALESCE(started_at, now()), attempts = attempts + 1 WHERE id = $1"

	if _, err := r.pool.Exec(ctx, q, id); err != nil {
		return errs.Wrap(err, errs.KindInternal, "audit.set_running_failed", "could not mark audit running")
	}
	return nil
}

// SetTerminal marks a job with a terminal status, stamps finished_at, and
// records the error columns (empty strings clear them to NULL).
func (r *PostgresRepository) SetTerminal(ctx context.Context, id uuid.UUID, status model.Status, errorCode, errorMessage string) error {
	const q = "UPDATE audit_job SET status = $2::audit_status, error_code = $3, " +
		"error_message = $4, finished_at = now() WHERE id = $1"

	if _, err := r.pool.Exec(ctx, q, id, string(status), nullString(errorCode), nullString(errorMessage)); err != nil {
		return errs.Wrap(err, errs.KindInternal, "audit.set_terminal_failed", "could not finalize audit")
	}
	return nil
}

// UpsertResult writes one platform result keyed on (job, platform), overwriting
// a prior attempt so a re-run never duplicates rows.
func (r *PostgresRepository) UpsertResult(ctx context.Context, jobID uuid.UUID, result model.PlatformResult) error {
	const q = "INSERT INTO audit_platform_result " +
		"(audit_job_id, platform, status, error_code, error_message, fetched_at) " +
		"VALUES ($1, $2::platform, $3, $4, $5, $6) " +
		"ON CONFLICT (audit_job_id, platform) DO UPDATE SET " +
		"status = EXCLUDED.status, error_code = EXCLUDED.error_code, " +
		"error_message = EXCLUDED.error_message, fetched_at = EXCLUDED.fetched_at"

	_, err := r.pool.Exec(ctx, q,
		jobID,
		result.Platform,
		result.Status,
		nullString(result.ErrorCode),
		nullString(result.ErrorMessage),
		result.FetchedAt,
	)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "audit.result_write_failed", "could not persist platform result")
	}
	return nil
}

// fraudColumns is the fraud_result projection, in the order UpsertFraudResult
// writes and GetFraudResult scans.
const fraudColumns = "present, fake_follower_rate, bot_comment_rate, engagement_anomaly, " +
	"clique_count, clique_membership_fraction, confidence, model_version"

// UpsertFraudResult writes the per-audit fraud estimate keyed on the job id,
// overwriting a prior run so a re-run never duplicates the row.
func (r *PostgresRepository) UpsertFraudResult(ctx context.Context, jobID uuid.UUID, fr model.FraudResult) error {
	const q = "INSERT INTO fraud_result " +
		"(audit_job_id, " + fraudColumns + ") " +
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) " +
		"ON CONFLICT (audit_job_id) DO UPDATE SET " +
		"present = EXCLUDED.present, fake_follower_rate = EXCLUDED.fake_follower_rate, " +
		"bot_comment_rate = EXCLUDED.bot_comment_rate, engagement_anomaly = EXCLUDED.engagement_anomaly, " +
		"clique_count = EXCLUDED.clique_count, clique_membership_fraction = EXCLUDED.clique_membership_fraction, " +
		"confidence = EXCLUDED.confidence, model_version = EXCLUDED.model_version"

	_, err := r.pool.Exec(ctx, q,
		jobID,
		fr.Present,
		fr.FakeFollowerRate,
		fr.BotCommentRate,
		fr.EngagementAnomaly,
		fr.CliqueCount,
		fr.CliqueMembershipFraction,
		fr.Confidence,
		fr.ModelVersion,
	)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "audit.fraud_write_failed", "could not persist fraud result")
	}
	return nil
}

// GetFraudResult returns the stored fraud estimate for a job. found is false,
// with no error, when no fraud row was written for it.
func (r *PostgresRepository) GetFraudResult(ctx context.Context, jobID uuid.UUID) (model.FraudResult, bool, error) {
	const q = "SELECT " + fraudColumns + " FROM fraud_result WHERE audit_job_id = $1"

	var fr model.FraudResult
	err := r.pool.QueryRow(ctx, q, jobID).Scan(
		&fr.Present,
		&fr.FakeFollowerRate,
		&fr.BotCommentRate,
		&fr.EngagementAnomaly,
		&fr.CliqueCount,
		&fr.CliqueMembershipFraction,
		&fr.Confidence,
		&fr.ModelVersion,
	)
	if err != nil {
		if notFound(err) {
			return model.FraudResult{}, false, nil
		}
		return model.FraudResult{}, false, errs.Wrap(err, errs.KindInternal, "audit.fraud_read_failed", "could not load fraud result")
	}
	return fr, true, nil
}

// scanJob reads one audit_job row into a domain Job.
func scanJob(row rowScanner) (model.Job, error) {
	var (
		id             string
		userID         string
		influencerID   *string
		idempotencyKey string
		status         string
		platforms      []string
		errorCode      *string
		errorMessage   *string
		requestedAt    time.Time
		startedAt      *time.Time
		finishedAt     *time.Time
	)

	if err := row.Scan(&id, &userID, &influencerID, &idempotencyKey, &status,
		&platforms, &errorCode, &errorMessage, &requestedAt, &startedAt, &finishedAt); err != nil {
		return model.Job{}, err
	}

	jobUUID, err := uuid.Parse(id)
	if err != nil {
		return model.Job{}, err
	}
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return model.Job{}, err
	}
	influencerUUID := uuid.Nil
	if influencerID != nil {
		influencerUUID, err = uuid.Parse(*influencerID)
		if err != nil {
			return model.Job{}, err
		}
	}
	if platforms == nil {
		platforms = []string{}
	}

	return model.Job{
		ID:                 jobUUID,
		UserID:             userUUID,
		InfluencerID:       influencerUUID,
		IdempotencyKey:     idempotencyKey,
		Status:             model.Status(status),
		RequestedPlatforms: platforms,
		ErrorCode:          deref(errorCode),
		ErrorMessage:       deref(errorMessage),
		RequestedAt:        requestedAt,
		StartedAt:          startedAt,
		FinishedAt:         finishedAt,
	}, nil
}

// scanResult reads one audit_platform_result row into a domain PlatformResult.
func scanResult(row rowScanner) (model.PlatformResult, error) {
	var (
		platform     string
		status       string
		errorCode    *string
		errorMessage *string
		fetchedAt    *time.Time
	)

	if err := row.Scan(&platform, &status, &errorCode, &errorMessage, &fetchedAt); err != nil {
		return model.PlatformResult{}, err
	}

	return model.PlatformResult{
		Platform:     platform,
		Status:       status,
		ErrorCode:    deref(errorCode),
		ErrorMessage: deref(errorMessage),
		FetchedAt:    fetchedAt,
	}, nil
}

// nullString maps an empty string to a nil *string so an unset error column is
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
