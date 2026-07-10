package service

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeRepo is an in-memory service.Repository. It models the idempotent create
// (a second create for the same key returns the existing job) and records the
// status transitions so a test can assert the state machine.
type fakeRepo struct {
	mu        sync.Mutex
	byID      map[uuid.UUID]model.Job
	byKey     map[string]model.Job
	results   map[uuid.UUID]map[string]model.PlatformResult
	statusLog map[uuid.UUID][]model.Status

	createCalls int
	deleteCalls int

	createErr error
	getErr    error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:      make(map[uuid.UUID]model.Job),
		byKey:     make(map[string]model.Job),
		results:   make(map[uuid.UUID]map[string]model.PlatformResult),
		statusLog: make(map[uuid.UUID][]model.Status),
	}
}

// seed inserts a job directly, bypassing CreateJob, for worker tests that start
// from an already-created job.
func (r *fakeRepo) seed(job model.Job) {
	r.byID[job.ID] = job
	r.byKey[job.IdempotencyKey] = job
	r.statusLog[job.ID] = append(r.statusLog[job.ID], job.Status)
}

func (r *fakeRepo) CreateJob(_ context.Context, params model.CreateJobParams) (model.Job, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.createCalls++
	if r.createErr != nil {
		return model.Job{}, false, r.createErr
	}
	if existing, ok := r.byKey[params.IdempotencyKey]; ok {
		if existing.UserID != params.UserID {
			return model.Job{}, false, errs.New(errs.KindConflict, "audit.idempotency_conflict", "idempotency key already in use")
		}
		return existing, false, nil
	}

	job := model.Job{
		ID:                 uuid.New(),
		UserID:             params.UserID,
		InfluencerID:       params.InfluencerID,
		IdempotencyKey:     params.IdempotencyKey,
		Status:             model.StatusQueued,
		RequestedPlatforms: params.RequestedPlatforms,
	}
	r.byID[job.ID] = job
	r.byKey[job.IdempotencyKey] = job
	r.statusLog[job.ID] = append(r.statusLog[job.ID], model.StatusQueued)
	return job, true, nil
}

func (r *fakeRepo) DeleteJob(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.deleteCalls++
	if job, ok := r.byID[id]; ok {
		delete(r.byKey, job.IdempotencyKey)
	}
	delete(r.byID, id)
	return nil
}

func (r *fakeRepo) GetJob(_ context.Context, id uuid.UUID) (model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.getErr != nil {
		return model.Job{}, r.getErr
	}
	job, ok := r.byID[id]
	if !ok {
		return model.Job{}, errs.New(errs.KindNotFound, "audit.not_found", "audit does not exist")
	}
	return job, nil
}

func (r *fakeRepo) GetJobForUser(_ context.Context, id, userID uuid.UUID) (model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	job, ok := r.byID[id]
	if !ok || job.UserID != userID {
		return model.Job{}, errs.New(errs.KindNotFound, "audit.not_found", "audit does not exist")
	}
	return job, nil
}

func (r *fakeRepo) ListJobsForUser(_ context.Context, userID uuid.UUID) ([]model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	jobs := make([]model.Job, 0)
	for _, job := range r.byID {
		if job.UserID == userID {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (r *fakeRepo) ListResults(_ context.Context, jobID uuid.UUID) ([]model.PlatformResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]model.PlatformResult, 0)
	for _, res := range r.results[jobID] {
		out = append(out, res)
	}
	return out, nil
}

func (r *fakeRepo) SetRunning(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	job := r.byID[id]
	job.Status = model.StatusRunning
	r.byID[id] = job
	r.statusLog[id] = append(r.statusLog[id], model.StatusRunning)
	return nil
}

func (r *fakeRepo) SetTerminal(_ context.Context, id uuid.UUID, status model.Status, code, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	job := r.byID[id]
	job.Status = status
	job.ErrorCode = code
	job.ErrorMessage = message
	r.byID[id] = job
	r.statusLog[id] = append(r.statusLog[id], status)
	return nil
}

func (r *fakeRepo) UpsertResult(_ context.Context, jobID uuid.UUID, result model.PlatformResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.results[jobID] == nil {
		r.results[jobID] = make(map[string]model.PlatformResult)
	}
	r.results[jobID][result.Platform] = result
	return nil
}

func (r *fakeRepo) statusesOf(id uuid.UUID) []model.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.Status(nil), r.statusLog[id]...)
}

func (r *fakeRepo) resultsOf(id uuid.UUID) map[string]model.PlatformResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]model.PlatformResult, len(r.results[id]))
	for k, v := range r.results[id] {
		out[k] = v
	}
	return out
}

// fakeQuota is a call-counting port.Quota. It lets a test assert the reserve /
// commit / release lifecycle without any billing dependency.
type fakeQuota struct {
	mu         sync.Mutex
	reserveN   int
	commitN    int
	releaseN   int
	reserveErr error
}

func (q *fakeQuota) Reserve(_ context.Context, _ uuid.UUID, _ string) (port.ReservationID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reserveN++
	if q.reserveErr != nil {
		return "", q.reserveErr
	}
	return port.ReservationID("res"), nil
}

func (q *fakeQuota) Commit(_ context.Context, _ port.ReservationID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.commitN++
	return nil
}

func (q *fakeQuota) Release(_ context.Context, _ port.ReservationID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.releaseN++
	return nil
}

// fakeEnqueuer is a taskEnqueuer that records enqueued tasks and can be made to
// fail.
type fakeEnqueuer struct {
	tasks []*asynq.Task
	err   error
}

func (e *fakeEnqueuer) EnqueueContext(_ context.Context, task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if e.err != nil {
		return nil, e.err
	}
	e.tasks = append(e.tasks, task)
	return &asynq.TaskInfo{}, nil
}

// fakeCaller is a port.CallerID returning a fixed identity or an error.
type fakeCaller struct {
	id  uuid.UUID
	err error
}

func (c fakeCaller) CallerID(context.Context) (uuid.UUID, error) {
	if c.err != nil {
		return uuid.Nil, c.err
	}
	return c.id, nil
}

// fakeIngester records the snapshots ingested.
type fakeIngester struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (i *fakeIngester) Ingest(context.Context, uuid.UUID, uuid.UUID, connector.Snapshot) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	return i.err
}

// fakeScorer captures the snapshots it was scored over, so a test can assert
// which platforms fed the number.
type fakeScorer struct {
	mu        sync.Mutex
	calls     int
	snapshots []connector.Snapshot
	err       error
}

func (s *fakeScorer) Score(_ context.Context, _, _ uuid.UUID, snapshots []connector.Snapshot, _ port.FraudInput) (port.ScoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.snapshots = append([]connector.Snapshot(nil), snapshots...)
	if s.err != nil {
		return port.ScoreResult{}, s.err
	}
	platforms := make([]connector.Platform, 0, len(snapshots))
	for _, snap := range snapshots {
		platforms = append(platforms, snap.Platform)
	}
	return port.ScoreResult{Overall: 1, ContributingPlatforms: platforms}, nil
}

func (s *fakeScorer) scoredPlatforms() []connector.Platform {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]connector.Platform, 0, len(s.snapshots))
	for _, snap := range s.snapshots {
		out = append(out, snap.Platform)
	}
	return out
}

// fakeFraud is a port.FraudClient.
type fakeFraud struct {
	calls int
	err   error
}

func (f *fakeFraud) ScoreFraud(context.Context, []connector.Snapshot) (port.FraudSummary, error) {
	f.calls++
	if f.err != nil {
		return port.FraudSummary{}, f.err
	}
	return port.FraudSummary{Present: true, ModelVersion: "test"}, nil
}

// fakeReporter is a port.Reporter.
type fakeReporter struct {
	calls int
	err   error
}

func (r *fakeReporter) GenerateReport(context.Context, port.ReportInput) (port.ReportOutput, port.Usage, error) {
	r.calls++
	if r.err != nil {
		return port.ReportOutput{}, port.Usage{}, r.err
	}
	return port.ReportOutput{}, port.Usage{}, nil
}

// fakeConnectors is a port.Connectors over an in-memory map.
type fakeConnectors struct {
	byPlatform map[connector.Platform]connector.Connector
}

func (c fakeConnectors) Get(p connector.Platform) (connector.Connector, bool) {
	conn, ok := c.byPlatform[p]
	return conn, ok
}

// fakeConnections is a port.Connections.
type fakeConnections struct {
	conns []port.Connection
	err   error
}

func (c fakeConnections) ListConnections(context.Context, uuid.UUID) ([]port.Connection, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.conns, nil
}

// fakeConnector is a connector.Connector that returns a preset snapshot or
// error, letting a test drive success, rate-limit, and hard-failure paths.
type fakeConnector struct {
	platform connector.Platform
	snap     connector.Snapshot
	err      error
}

func (c fakeConnector) Platform() connector.Platform { return c.platform }

func (c fakeConnector) Capabilities() []connector.Capability {
	return []connector.Capability{
		connector.CapabilityProfile,
		connector.CapabilityMetrics,
		connector.CapabilityRecentPosts,
		connector.CapabilityComments,
	}
}

func (c fakeConnector) Fetch(context.Context, connector.FetchRequest) (connector.Snapshot, error) {
	if c.err != nil {
		return connector.Snapshot{}, c.err
	}
	return c.snap, nil
}
