package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/connector"
)

// goodSnapshot is a usable, complete snapshot for a platform.
func goodSnapshot(p connector.Platform) connector.Snapshot {
	return connector.Snapshot{Platform: p, Handle: string(p) + "-handle", CapturedAt: time.Now().UTC()}
}

// connectionFor builds a connected platform with a dummy live token.
func connectionFor(p connector.Platform) port.Connection {
	return port.Connection{Platform: p, Handle: string(p) + "-handle", Token: &connector.OAuthToken{AccessToken: "tok"}}
}

// seedQueuedJob inserts a fresh queued job owned by the caller and returns it.
func seedQueuedJob(h *harness) model.Job {
	job := model.Job{
		ID:             uuid.New(),
		UserID:         h.caller,
		InfluencerID:   uuid.New(),
		IdempotencyKey: "job-" + uuid.NewString(),
		Status:         model.StatusQueued,
	}
	h.repo.seed(job)
	return job
}

// runTask drives ProcessRun for a job, mirroring what the worker does on
// delivery.
func runTask(t *testing.T, h *harness, jobID uuid.UUID) {
	t.Helper()
	payload, err := json.Marshal(runPayload{JobID: jobID, ReservationID: "res"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := h.svc.ProcessRun(context.Background(), asynq.NewTask(TaskAuditRun, payload)); err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
}

func TestRun_SucceededWhenAllPlatformsProduceData(t *testing.T) {
	registered := map[connector.Platform]connector.Connector{
		connector.PlatformYouTube:   fakeConnector{platform: connector.PlatformYouTube, snap: goodSnapshot(connector.PlatformYouTube)},
		connector.PlatformInstagram: fakeConnector{platform: connector.PlatformInstagram, snap: goodSnapshot(connector.PlatformInstagram)},
	}
	conns := []port.Connection{connectionFor(connector.PlatformYouTube), connectionFor(connector.PlatformInstagram)}
	h := newHarness(conns, registered)
	job := seedQueuedJob(h)

	runTask(t, h, job.ID)

	if got := h.repo.byID[job.ID].Status; got != model.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", got)
	}
	if h.quota.commitN != 1 || h.quota.releaseN != 0 {
		t.Fatalf("commit/release = %d/%d, want 1/0", h.quota.commitN, h.quota.releaseN)
	}
	results := h.repo.resultsOf(job.ID)
	if len(results) != 2 || results["youtube"].Status != model.ResultOK || results["instagram"].Status != model.ResultOK {
		t.Fatalf("results = %+v, want two ok rows", results)
	}
	if h.scorer.calls != 1 || len(h.scorer.scoredPlatforms()) != 2 {
		t.Fatalf("scorer calls=%d platforms=%v, want 1 call over 2 platforms", h.scorer.calls, h.scorer.scoredPlatforms())
	}
	if h.reporter.calls != 1 {
		t.Fatalf("reporter calls = %d, want 1", h.reporter.calls)
	}
	if h.ingester.calls != 2 {
		t.Fatalf("ingester calls = %d, want 2", h.ingester.calls)
	}
}

func TestRun_PartialWhenOnePlatformRateLimited(t *testing.T) {
	registered := map[connector.Platform]connector.Connector{
		connector.PlatformYouTube:   fakeConnector{platform: connector.PlatformYouTube, snap: goodSnapshot(connector.PlatformYouTube)},
		connector.PlatformInstagram: fakeConnector{platform: connector.PlatformInstagram, err: connector.NewRateLimitError(connector.PlatformInstagram, 0, nil)},
	}
	conns := []port.Connection{connectionFor(connector.PlatformYouTube), connectionFor(connector.PlatformInstagram)}
	h := newHarness(conns, registered)
	job := seedQueuedJob(h)

	runTask(t, h, job.ID)

	if got := h.repo.byID[job.ID].Status; got != model.StatusPartial {
		t.Fatalf("status = %q, want partial", got)
	}
	// A partial audit delivered value: the reservation is committed, not released.
	if h.quota.commitN != 1 || h.quota.releaseN != 0 {
		t.Fatalf("commit/release = %d/%d, want 1/0", h.quota.commitN, h.quota.releaseN)
	}
	results := h.repo.resultsOf(job.ID)
	if results["youtube"].Status != model.ResultOK {
		t.Fatalf("youtube status = %q, want ok", results["youtube"].Status)
	}
	if results["instagram"].Status != model.ResultPartial {
		t.Fatalf("instagram status = %q, want partial", results["instagram"].Status)
	}
	// Only the platform that produced data feeds the score.
	scored := h.scorer.scoredPlatforms()
	if len(scored) != 1 || scored[0] != connector.PlatformYouTube {
		t.Fatalf("scored platforms = %v, want [youtube]", scored)
	}
}

func TestRun_FailedWhenAllPlatformsError(t *testing.T) {
	registered := map[connector.Platform]connector.Connector{
		connector.PlatformYouTube:   fakeConnector{platform: connector.PlatformYouTube, err: errors.New("boom")},
		connector.PlatformInstagram: fakeConnector{platform: connector.PlatformInstagram, err: errors.New("boom")},
	}
	conns := []port.Connection{connectionFor(connector.PlatformYouTube), connectionFor(connector.PlatformInstagram)}
	h := newHarness(conns, registered)
	job := seedQueuedJob(h)

	runTask(t, h, job.ID)

	if got := h.repo.byID[job.ID].Status; got != model.StatusFailed {
		t.Fatalf("status = %q, want failed", got)
	}
	// No data produced: the reservation is released and never committed, so a
	// failed audit does not burn the caller's allowance.
	if h.quota.releaseN != 1 || h.quota.commitN != 0 {
		t.Fatalf("release/commit = %d/%d, want 1/0", h.quota.releaseN, h.quota.commitN)
	}
	if h.scorer.calls != 0 {
		t.Fatalf("scorer calls = %d, want 0", h.scorer.calls)
	}
	results := h.repo.resultsOf(job.ID)
	if results["youtube"].Status != model.ResultError || results["instagram"].Status != model.ResultError {
		t.Fatalf("results = %+v, want two error rows", results)
	}
}

func TestRun_FailedWhenNoConnections(t *testing.T) {
	h := newHarness(nil, nil)
	job := seedQueuedJob(h)

	runTask(t, h, job.ID)

	if got := h.repo.byID[job.ID].Status; got != model.StatusFailed {
		t.Fatalf("status = %q, want failed", got)
	}
	if h.quota.releaseN != 1 || h.quota.commitN != 0 {
		t.Fatalf("release/commit = %d/%d, want 1/0", h.quota.releaseN, h.quota.commitN)
	}
	if h.scorer.calls != 0 {
		t.Fatalf("scorer calls = %d, want 0", h.scorer.calls)
	}
}

func TestRun_MissingConnectorSkipsPlatform(t *testing.T) {
	// youtube is connected and produces data; instagram is connected but no
	// connector is registered for it, so it is skipped without failing the audit.
	registered := map[connector.Platform]connector.Connector{
		connector.PlatformYouTube: fakeConnector{platform: connector.PlatformYouTube, snap: goodSnapshot(connector.PlatformYouTube)},
	}
	conns := []port.Connection{connectionFor(connector.PlatformYouTube), connectionFor(connector.PlatformInstagram)}
	h := newHarness(conns, registered)
	job := seedQueuedJob(h)

	runTask(t, h, job.ID)

	if got := h.repo.byID[job.ID].Status; got != model.StatusPartial {
		t.Fatalf("status = %q, want partial", got)
	}
	results := h.repo.resultsOf(job.ID)
	if results["instagram"].Status != model.ResultSkipped {
		t.Fatalf("instagram status = %q, want skipped", results["instagram"].Status)
	}
	if h.quota.commitN != 1 {
		t.Fatalf("commitN = %d, want 1", h.quota.commitN)
	}
}

func TestRun_StateMachineTransitions(t *testing.T) {
	registered := map[connector.Platform]connector.Connector{
		connector.PlatformYouTube: fakeConnector{platform: connector.PlatformYouTube, snap: goodSnapshot(connector.PlatformYouTube)},
	}
	h := newHarness([]port.Connection{connectionFor(connector.PlatformYouTube)}, registered)
	job := seedQueuedJob(h)

	runTask(t, h, job.ID)

	got := h.repo.statusesOf(job.ID)
	want := []model.Status{model.StatusQueued, model.StatusRunning, model.StatusSucceeded}
	if len(got) != len(want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", got, want)
		}
	}
}

func TestRun_TerminalJobIsNotReRun(t *testing.T) {
	registered := map[connector.Platform]connector.Connector{
		connector.PlatformYouTube: fakeConnector{platform: connector.PlatformYouTube, snap: goodSnapshot(connector.PlatformYouTube)},
	}
	h := newHarness([]port.Connection{connectionFor(connector.PlatformYouTube)}, registered)
	job := seedQueuedJob(h)
	// Force the job terminal before delivery.
	_ = h.repo.SetTerminal(context.Background(), job.ID, model.StatusSucceeded, "", "")

	runTask(t, h, job.ID)

	// A re-delivered terminal job touches neither the quota nor the pipeline.
	if h.quota.commitN != 0 || h.quota.releaseN != 0 {
		t.Fatalf("commit/release = %d/%d, want 0/0 (terminal job skipped)", h.quota.commitN, h.quota.releaseN)
	}
	if h.scorer.calls != 0 {
		t.Fatalf("scorer calls = %d, want 0", h.scorer.calls)
	}
}

func TestRun_MissingJobIsNoOp(t *testing.T) {
	h := newHarness(nil, nil)
	// No job seeded; the payload references an id the repo does not hold.
	runTask(t, h, uuid.New())

	if h.quota.commitN != 0 || h.quota.releaseN != 0 {
		t.Fatalf("commit/release = %d/%d, want 0/0", h.quota.commitN, h.quota.releaseN)
	}
}

func TestRun_FraudUnavailableStillScores(t *testing.T) {
	registered := map[connector.Platform]connector.Connector{
		connector.PlatformYouTube: fakeConnector{platform: connector.PlatformYouTube, snap: goodSnapshot(connector.PlatformYouTube)},
	}
	h := newHarness([]port.Connection{connectionFor(connector.PlatformYouTube)}, registered)
	h.fraud.err = errors.New("ml unavailable")
	job := seedQueuedJob(h)

	runTask(t, h, job.ID)

	if got := h.repo.byID[job.ID].Status; got != model.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded despite fraud outage", got)
	}
	if h.scorer.calls != 1 {
		t.Fatalf("scorer calls = %d, want 1 (score without a fraud signal)", h.scorer.calls)
	}
}

func TestProcessRun_MalformedPayloadSkipsRetry(t *testing.T) {
	h := newHarness(nil, nil)
	err := h.svc.ProcessRun(context.Background(), asynq.NewTask(TaskAuditRun, []byte("{not json")))
	if err == nil {
		t.Fatal("expected an error for a malformed payload")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error does not wrap SkipRetry: %v", err)
	}
}
