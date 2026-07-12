package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"golang.org/x/sync/errgroup"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// TaskAuditRun is the asynq task type the submit path enqueues and the worker
// registers. It is stable: it is persisted in Redis, so renaming it would
// orphan in-flight tasks.
const TaskAuditRun = "audit:run"

const (
	// runUniqueTTL bounds the asynq uniqueness lock for a job's run task, so a
	// duplicate enqueue within the window is rejected as already-scheduled. It
	// comfortably exceeds the worst-case run time.
	runUniqueTTL = 6 * time.Hour
	// runTaskTimeout caps the whole run. The worker also bounds each platform
	// fetch individually; this is the outer ceiling.
	runTaskTimeout = 10 * time.Minute
	// runMaxRetry bounds retries of a run that returns an error (a transient
	// dependency failure). Platform-level failures do not return an error and so
	// never consume a retry.
	runMaxRetry = 3
	// perPlatformTimeout bounds a single platform fetch so one slow platform
	// cannot stall the whole audit.
	perPlatformTimeout = 90 * time.Second
	// maxPostsPerPlatform caps how many recent posts each connector returns,
	// keeping one audit within a platform's per-call quota budget.
	maxPostsPerPlatform = 50
)

// runPayload is the audit:run task body. The reservation id rides along so the
// worker commits or releases the same reservation the submit made.
type runPayload struct {
	JobID         uuid.UUID `json:"job_id"`
	ReservationID string    `json:"reservation_id"`
}

// ProcessRun is the asynq handler for TaskAuditRun. It decodes the payload and
// runs the orchestration. A malformed payload is unrecoverable, so it is not
// retried.
func (s *Service) ProcessRun(ctx context.Context, task *asynq.Task) error {
	var p runPayload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		// A payload that cannot be decoded will never decode; retrying wastes the
		// queue. SkipRetry archives it for inspection instead.
		return errors.Join(asynq.SkipRetry, errs.Wrap(err, errs.KindInvalid, "audit.bad_payload", "audit task payload is malformed"))
	}
	return s.run(ctx, p.JobID, port.ReservationID(p.ReservationID))
}

// run is the audit orchestration. It loads the job, fans out across the
// influencer's connected platforms, persists each platform's outcome, and then
// scores and reports over whatever succeeded — settling the job's terminal state
// and the quota reservation exactly once.
func (s *Service) run(ctx context.Context, jobID uuid.UUID, reservation port.ReservationID) error {
	job, err := s.repo.GetJob(ctx, jobID)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			// The job row is gone (a submit whose commit failed after the task was
			// enqueued, or a deleted job). There is nothing to run and nothing to
			// retry.
			return nil
		}
		return err
	}

	// Re-delivery of a task whose job already reached a terminal state must not
	// re-run it or touch the quota again.
	if job.Status.Terminal() {
		return nil
	}

	if err := s.repo.SetRunning(ctx, jobID); err != nil {
		return err
	}

	if job.InfluencerID == uuid.Nil {
		// The influencer was removed after the job was created; no connections can
		// be resolved. This is the zero-data case.
		return s.finishNoData(ctx, jobID, reservation, "audit.no_influencer", "the audited influencer no longer exists")
	}

	connections, err := s.connections.ListConnections(ctx, job.InfluencerID)
	if err != nil {
		return err
	}
	connections = filterConnections(connections, job.RequestedPlatforms)

	if len(connections) == 0 {
		return s.finishNoData(ctx, jobID, reservation, "audit.no_connections", "the influencer has no connected platforms to audit")
	}

	snapshots, allOK, err := s.fanOut(ctx, job, connections)
	if err != nil {
		// A system fault (a result write or ingest failed). Leave the job running
		// and let asynq retry; the per-platform upserts are idempotent.
		return err
	}

	if len(snapshots) == 0 {
		// Every platform failed. A failed audit must not burn the caller's quota.
		return s.finishNoData(ctx, jobID, reservation, "audit.all_platforms_failed", "no platform produced data")
	}

	if err := s.scoreAndReport(ctx, job, snapshots); err != nil {
		// Scoring is the core deliverable; a failure there is treated as transient
		// and retried with the job left running.
		return err
	}

	status := model.StatusPartial
	if allOK {
		status = model.StatusSucceeded
	}
	if err := s.repo.SetTerminal(ctx, jobID, status, "", ""); err != nil {
		return err
	}
	// A succeeded or partial audit delivered value, so the reserved unit is
	// committed.
	if err := s.quota.Commit(ctx, reservation); err != nil {
		return err
	}
	return nil
}

// fanOut fetches every connected platform concurrently, each under its own
// timeout, writing one audit_platform_result row per platform and collecting the
// usable snapshots. A rate limit or exhausted quota marks that platform and is
// not fatal; only a persistence or ingest fault aborts the fan-out. allOK
// reports whether every platform returned a complete snapshot.
func (s *Service) fanOut(ctx context.Context, job model.Job, connections []port.Connection) (snapshots []connector.Snapshot, allOK bool, err error) {
	g, gctx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	collected := make([]connector.Snapshot, 0, len(connections))
	okCount := 0

	for _, conn := range connections {
		conn := conn
		g.Go(func() error {
			result, snap, fatal := s.fetchPlatform(gctx, job, conn)
			if fatal != nil {
				return fatal
			}
			if writeErr := s.repo.UpsertResult(gctx, job.ID, result); writeErr != nil {
				return writeErr
			}
			if snap != nil {
				mu.Lock()
				collected = append(collected, *snap)
				if result.Status == model.ResultOK {
					okCount++
				}
				mu.Unlock()
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, false, err
	}

	return collected, okCount == len(connections), nil
}

// fetchPlatform fetches one platform and classifies the outcome into a result
// row. It returns a non-nil snapshot only when the fetch produced usable data.
// A non-nil fatal error is a system fault (not a platform failure) that should
// abort and retry the whole run; platform failures are folded into the returned
// result instead.
func (s *Service) fetchPlatform(ctx context.Context, job model.Job, conn port.Connection) (result model.PlatformResult, snap *connector.Snapshot, fatal error) {
	platform := string(conn.Platform)
	result = model.PlatformResult{Platform: platform}

	c, ok := s.connectors.Get(conn.Platform)
	if !ok {
		// No connector is registered for a platform the influencer connected. Skip
		// it rather than failing the audit.
		result.Status = model.ResultSkipped
		result.ErrorCode = "audit.connector_missing"
		result.ErrorMessage = "no connector registered for this platform"
		return result, nil, nil
	}

	pctx, cancel := context.WithTimeout(ctx, perPlatformTimeout)
	defer cancel()

	req := connector.FetchRequest{
		Handle:    conn.Handle,
		AccountID: conn.AccountID,
		Token:     conn.Token,
		Capabilities: []connector.Capability{
			connector.CapabilityProfile,
			connector.CapabilityMetrics,
			connector.CapabilityRecentPosts,
			connector.CapabilityComments,
		},
		MaxPosts: maxPostsPerPlatform,
	}

	fetched, err := c.Fetch(pctx, req)
	if err != nil {
		return classifyFetchError(result, err), nil, nil
	}

	// The fetch succeeded. Persist the snapshot for the influencer; a failure to
	// ingest is a system fault, not a platform failure.
	if err := s.ingester.Ingest(pctx, job.InfluencerID, job.ID, fetched); err != nil {
		return result, nil, err
	}

	fetchedAt := fetched.CapturedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	result.FetchedAt = &fetchedAt
	result.Status = model.ResultOK
	if fetched.Partial {
		// The connector deliberately omitted some requested data (for example it
		// hit a soft limit mid-fetch). The data is usable but the platform is
		// recorded as partial.
		result.Status = model.ResultPartial
	}
	return result, &fetched, nil
}

// classifyFetchError folds a fetch error into a platform result. A rate limit or
// exhausted quota is recorded as partial (the platform is temporarily
// unavailable, not broken); every other error is recorded as an error. Neither
// aborts the audit.
func classifyFetchError(result model.PlatformResult, err error) model.PlatformResult {
	var rl *connector.RateLimitError
	var qe *connector.QuotaExhaustedError
	switch {
	case errors.As(err, &rl):
		result.Status = model.ResultPartial
		result.ErrorCode = "audit.rate_limited"
		result.ErrorMessage = "platform rate limit reached"
	case errors.As(err, &qe):
		result.Status = model.ResultPartial
		result.ErrorCode = "audit.quota_exhausted"
		result.ErrorMessage = "platform quota exhausted"
	default:
		result.Status = model.ResultError
		result.ErrorCode = "audit.fetch_failed"
		result.ErrorMessage = "platform fetch failed"
	}
	return result
}

// scoreAndReport runs the fraud, scoring, and report steps over the usable
// snapshots. Scoring persists the score (it is keyed on the audit job id). The
// fraud and report steps are advisory: a failure there degrades gracefully
// rather than failing an audit that already collected data.
func (s *Service) scoreAndReport(ctx context.Context, job model.Job, snapshots []connector.Snapshot) error {
	fraud, err := s.fraud.ScoreFraud(ctx, snapshots)
	if err != nil {
		// The ml service was unavailable. Score without a fraud signal rather than
		// failing; the summary's Present flag records the absence.
		fraud = port.FraudSummary{Present: false}
	}

	// Persist the fraud estimate keyed on the job so the deliverable's
	// coordination headline and the dispute-labelling loop read a stored value
	// rather than re-running the models. A present=false row is written too: it
	// records that a fraud pass ran and found nothing, distinct from never having
	// run one. A write failure here is a system fault, so the run is retried (the
	// upsert is idempotent).
	if err := s.repo.UpsertFraudResult(ctx, job.ID, toFraudModel(fraud)); err != nil {
		return err
	}

	score, err := s.scorer.Score(ctx, job.ID, job.InfluencerID, snapshots, toFraudInput(fraud))
	if err != nil {
		return err
	}

	// Feed the ml feature store (the data flywheel). This is best-effort: the
	// score is already persisted, so a recorder failure is logged and ignored —
	// the audit's deliverable never depends on the training intake succeeding.
	s.recordFeatures(ctx, job, snapshots, fraud)

	if _, _, err := s.reporter.GenerateReport(ctx, port.ReportInput{
		AuditJobID:   job.ID,
		InfluencerID: job.InfluencerID,
		Score:        score,
		Fraud:        fraud,
		Snapshots:    snapshots,
	}); err != nil {
		// The narrative is advisory. The score is already persisted, so a report
		// failure does not fail the audit; the report route will surface its
		// absence.
		return nil
	}
	return nil
}

// finishNoData settles the zero-data outcome: the job is marked failed and the
// reserved quota unit is released, because no platform produced data and the
// caller's allowance must not be consumed.
func (s *Service) finishNoData(ctx context.Context, jobID uuid.UUID, reservation port.ReservationID, code, message string) error {
	if err := s.repo.SetTerminal(ctx, jobID, model.StatusFailed, code, message); err != nil {
		return err
	}
	if err := s.quota.Release(ctx, reservation); err != nil {
		return err
	}
	return nil
}

// recordFeatures captures the completed audit as an ml feature-store row through
// the optional FeatureRecorder port. A nil recorder is a no-op; any error is
// logged and swallowed so the training intake can never fail an audit that has
// already produced its deliverable.
func (s *Service) recordFeatures(ctx context.Context, job model.Job, snapshots []connector.Snapshot, fraud port.FraudSummary) {
	if s.features == nil {
		return
	}
	if err := s.features.RecordFeatures(ctx, port.FeatureRecord{
		AuditJobID:   job.ID,
		InfluencerID: job.InfluencerID,
		Snapshots:    snapshots,
		Fraud:        fraud,
	}); err != nil {
		slog.WarnContext(ctx, "ml feature-store intake failed (audit unaffected)",
			slog.String("audit_job_id", job.ID.String()), slog.Any("error", err))
	}
}

// toFraudInput maps the ml-agnostic fraud summary onto the scoring engine's
// fraud contribution. The clique signals are deliberately dropped: they are a
// reporting headline, not an input to the composite score, so FraudInput stays
// aligned with the scoring module's own type.
func toFraudInput(f port.FraudSummary) port.FraudInput {
	return port.FraudInput{
		Present:   f.Present,
		RiskScore: f.RiskScore,
		// The coordination fraction IS an input to the composite: it is an
		// independent measurement from a different model, blended (and renormalized)
		// with the risk score in the authenticity subscore. The clique COUNT stays a
		// reporting headline.
		CliqueMembershipFraction: f.CliqueMembershipFraction,
		Confidence:               f.Confidence,
		ModelVersion:             f.ModelVersion,
		RefinedScore:             f.RefinedScore,
	}
}

// toFraudModel maps the ml-agnostic fraud summary onto the persisted
// fraud_result row shape. An unmeasured signal persists as NULL, never as 0 —
// these rows feed the training feature store, and a zero-filled row teaches a
// model that "we didn't look" is a specific point in feature space.
func toFraudModel(f port.FraudSummary) model.FraudResult {
	return model.FraudResult{
		Present:                  f.Present,
		RiskScore:                f.RiskScore,
		EngagementAnomaly:        f.EngagementAnomaly,
		CliqueCount:              f.CliqueCount,
		CliqueMembershipFraction: f.CliqueMembershipFraction,
		Confidence:               f.Confidence,
		ModelVersion:             f.ModelVersion,
	}
}

// filterConnections restricts connections to the requested platforms. An empty
// request means every connected platform.
func filterConnections(connections []port.Connection, requested []string) []port.Connection {
	if len(requested) == 0 {
		return connections
	}
	wanted := make(map[string]struct{}, len(requested))
	for _, p := range requested {
		wanted[p] = struct{}{}
	}
	filtered := make([]port.Connection, 0, len(connections))
	for _, conn := range connections {
		if _, ok := wanted[string(conn.Platform)]; ok {
			filtered = append(filtered, conn)
		}
	}
	return filtered
}
