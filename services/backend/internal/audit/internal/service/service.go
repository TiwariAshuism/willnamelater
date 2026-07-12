// Package service implements the audit module's business logic: the submit path
// that creates and enqueues a job, the read paths that project a job for its
// owner, and the audit:run worker that orchestrates the fetch/score/report
// pipeline.
//
// Every collaborator is reached through a port declared in
// internal/audit/port, so this package imports no other business module. The
// repository is the sole data-access dependency and is declared here as a
// consumer-side interface the repository package satisfies.
package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// auditUnit is the quota unit an audit consumes. It is the string the billing
// quota service recognises for a single audit.
const auditUnit = "audit"

// Repository is the audit module's data-access contract. It is declared by the
// service (its consumer) and satisfied by the repository package. Every method
// is keyed on the audit job id so a re-run overwrites rather than duplicates.
type Repository interface {
	// CreateJob inserts a queued job idempotently. When a job with the same
	// idempotency key already exists for the caller it returns that job with
	// created=false and makes no change; a key held by a different user is a
	// conflict. created is true only when this call inserted the row.
	CreateJob(ctx context.Context, params model.CreateJobParams) (job model.Job, created bool, err error)
	// DeleteJob removes a job by id. It is the compensating action when a job
	// was created but its run task could not be enqueued.
	DeleteJob(ctx context.Context, id uuid.UUID) error
	// GetJob returns the job by id regardless of owner. The worker uses it; the
	// read routes use GetJobForUser instead.
	GetJob(ctx context.Context, id uuid.UUID) (model.Job, error)
	// GetJobForUser returns the caller's job, or a not-found error when no job
	// with that id belongs to the caller.
	GetJobForUser(ctx context.Context, id, userID uuid.UUID) (model.Job, error)
	// ListJobsForUser returns the caller's jobs, newest first.
	ListJobsForUser(ctx context.Context, userID uuid.UUID) ([]model.Job, error)
	// ListResults returns a job's per-platform results in a deterministic order.
	ListResults(ctx context.Context, jobID uuid.UUID) ([]model.PlatformResult, error)
	// SetRunning marks a job running and stamps started_at.
	SetRunning(ctx context.Context, id uuid.UUID) error
	// SetTerminal marks a job with a terminal status, stamps finished_at, and
	// records the error columns (empty strings clear them).
	SetTerminal(ctx context.Context, id uuid.UUID, status model.Status, errorCode, errorMessage string) error
	// UpsertResult writes one platform result, keyed on (job, platform), so a
	// re-run overwrites the previous attempt.
	UpsertResult(ctx context.Context, jobID uuid.UUID, result model.PlatformResult) error
	// UpsertFraudResult writes the per-audit fraud estimate keyed on the job id,
	// overwriting a prior run so a re-run never duplicates the row.
	UpsertFraudResult(ctx context.Context, jobID uuid.UUID, fr model.FraudResult) error
	// GetFraudResult returns the stored fraud estimate for a job. found is false
	// when no fraud row was written for it (a failed audit, or one that never
	// reached the fraud step).
	GetFraudResult(ctx context.Context, jobID uuid.UUID) (fr model.FraudResult, found bool, err error)
}

// taskEnqueuer is the slice of *asynq.Client the submit path needs. Declaring
// it as an interface lets the worker be tested without a live Redis while the
// composition root injects the real client.
type taskEnqueuer interface {
	EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// Service is the wired audit service. It satisfies the generated AuditService
// (the HTTP surface) and additionally exposes ProcessRun, the worker task
// handler.
type Service struct {
	repo        Repository
	enqueuer    taskEnqueuer
	quota       port.Quota
	ingester    port.Ingester
	scorer      port.Scorer
	fraud       port.FraudClient
	reporter    port.Reporter
	connectors  port.Connectors
	connections port.Connections
	caller      port.CallerID
	// features is the optional ml feature-store intake. It may be nil (the audit
	// runs identically without it); when set, each completed audit is recorded
	// best-effort as a training row.
	features port.FeatureRecorder
}

var _ AuditService = (*Service)(nil)

// New builds the audit service over its repository, the asynq client, and every
// collaborator port. features is optional (nil disables the ml feature-store
// intake) so the module builds and tests without it.
func New(
	repo Repository,
	enqueuer taskEnqueuer,
	quota port.Quota,
	ingester port.Ingester,
	scorer port.Scorer,
	fraud port.FraudClient,
	reporter port.Reporter,
	connectors port.Connectors,
	connections port.Connections,
	caller port.CallerID,
	features port.FeatureRecorder,
) *Service {
	return &Service{
		repo:        repo,
		enqueuer:    enqueuer,
		quota:       quota,
		ingester:    ingester,
		scorer:      scorer,
		fraud:       fraud,
		reporter:    reporter,
		connectors:  connectors,
		connections: connections,
		caller:      caller,
		features:    features,
	}
}

// SubmitAudit reserves quota, creates the job idempotently, and enqueues its run
// task.
//
// The quota unit is consumed at reserve time, before any job exists, so an
// over-quota caller (KindQuotaExceeded, rendered 402) never has a job created.
// A retried submit carrying an idempotency key that already produced a job is a
// no-op: the existing job is returned and the just-made reservation is released,
// so a retry never double-charges. If the job is created but its run task cannot
// be enqueued, the job is deleted and the reservation released so the caller can
// retry cleanly.
func (s *Service) SubmitAudit(ctx context.Context, req model.SubmitAuditRequest) (model.AuditResponse, error) {
	userID, err := s.caller.CallerID(ctx)
	if err != nil {
		return model.AuditResponse{}, err
	}

	influencerID, err := uuid.Parse(req.InfluencerID)
	if err != nil {
		return model.AuditResponse{}, errs.New(errs.KindInvalid, "audit.invalid_influencer", "influencer id is not a valid uuid")
	}
	if req.IdempotencyKey == "" {
		return model.AuditResponse{}, errs.New(errs.KindInvalid, "audit.missing_idempotency_key", "idempotency key is required")
	}

	reservation, err := s.quota.Reserve(ctx, userID, auditUnit)
	if err != nil {
		// KindQuotaExceeded flows straight through to a 402; no job is created.
		return model.AuditResponse{}, err
	}

	job, created, err := s.repo.CreateJob(ctx, model.CreateJobParams{
		UserID:             userID,
		InfluencerID:       influencerID,
		IdempotencyKey:     req.IdempotencyKey,
		RequestedPlatforms: req.RequestedPlatforms,
	})
	if err != nil {
		// The job was not created, so the reserved unit must go back.
		s.releaseQuiet(ctx, reservation)
		return model.AuditResponse{}, err
	}

	if !created {
		// Idempotent replay: the job already existed, so this submit reserved a
		// unit it must not keep.
		s.releaseQuiet(ctx, reservation)
		return s.respondWithResults(ctx, job)
	}

	if err := s.enqueueRun(ctx, job.ID, reservation); err != nil {
		// The job exists but nothing will run it. Undo both so the caller can
		// resubmit with the same key.
		s.deleteQuiet(ctx, job.ID)
		s.releaseQuiet(ctx, reservation)
		return model.AuditResponse{}, err
	}

	return model.ToAuditResponse(job, nil), nil
}

// GetAudit returns the caller's job by id, projecting its status and per-platform
// results. A job that exists but belongs to another caller is reported as
// not-found so ownership is never leaked.
func (s *Service) GetAudit(ctx context.Context, id string) (model.AuditResponse, error) {
	userID, err := s.caller.CallerID(ctx)
	if err != nil {
		return model.AuditResponse{}, err
	}

	jobID, err := uuid.Parse(id)
	if err != nil {
		return model.AuditResponse{}, errs.New(errs.KindInvalid, "audit.invalid_id", "audit id is not a valid uuid")
	}

	job, err := s.repo.GetJobForUser(ctx, jobID, userID)
	if err != nil {
		return model.AuditResponse{}, err
	}

	return s.respondWithResults(ctx, job)
}

// ListAudits returns the caller's audits, newest first, without their
// per-platform results — a listing stays cheap and the detail route carries the
// breakdown.
func (s *Service) ListAudits(ctx context.Context) ([]model.AuditResponse, error) {
	userID, err := s.caller.CallerID(ctx)
	if err != nil {
		return nil, err
	}

	jobs, err := s.repo.ListJobsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	resp := make([]model.AuditResponse, 0, len(jobs))
	for _, job := range jobs {
		resp = append(resp, model.ToAuditResponse(job, nil))
	}
	return resp, nil
}

// FraudResultOf returns the stored per-audit fraud estimate for a job. found is
// false when no fraud pass was recorded for it. It is not caller-scoped: the
// report and admin modules authorize the audit before reading its fraud row, so
// this read carries no identity check of its own.
func (s *Service) FraudResultOf(ctx context.Context, jobID uuid.UUID) (model.FraudResult, bool, error) {
	return s.repo.GetFraudResult(ctx, jobID)
}

// respondWithResults loads a job's platform results and projects the full
// detail DTO.
func (s *Service) respondWithResults(ctx context.Context, job model.Job) (model.AuditResponse, error) {
	results, err := s.repo.ListResults(ctx, job.ID)
	if err != nil {
		return model.AuditResponse{}, err
	}
	return model.ToAuditResponse(job, results), nil
}

// enqueueRun enqueues the audit:run task for a job. The reservation id rides in
// the payload so the worker can commit or release the same reservation the
// submit made. asynq.Unique(runUniqueTTL) and a job-scoped task id make a second
// enqueue for the same job a no-op.
func (s *Service) enqueueRun(ctx context.Context, jobID uuid.UUID, reservation port.ReservationID) error {
	payload, err := json.Marshal(runPayload{JobID: jobID, ReservationID: string(reservation)})
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "audit.enqueue_encode", "could not encode audit task")
	}

	task := asynq.NewTask(TaskAuditRun, payload)
	_, err = s.enqueuer.EnqueueContext(ctx, task,
		asynq.TaskID(jobID.String()),
		asynq.Unique(runUniqueTTL),
		asynq.MaxRetry(runMaxRetry),
		asynq.Timeout(runTaskTimeout),
	)
	if err != nil {
		// A duplicate task for the same job id is the idempotent, already-enqueued
		// case, not a failure: the job's run is already scheduled.
		if errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil
		}
		return errs.Wrap(err, errs.KindUnavailable, "audit.enqueue_failed", "could not enqueue audit task")
	}
	return nil
}

// releaseQuiet releases a reservation, discarding the error: the caller is
// already on an error or idempotent-replay path, and a failed release must not
// mask that outcome. The billing service logs its own failures.
func (s *Service) releaseQuiet(ctx context.Context, reservation port.ReservationID) {
	_ = s.quota.Release(ctx, reservation)
}

// deleteQuiet deletes a job, discarding the error for the same reason as
// releaseQuiet: it is a best-effort compensation on an already-failing path.
func (s *Service) deleteQuiet(ctx context.Context, id uuid.UUID) {
	_ = s.repo.DeleteJob(ctx, id)
}
