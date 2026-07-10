// Package model holds the audit module's domain types and the DTOs its HTTP
// surface exchanges. It has no behaviour beyond pure mapping and classification;
// persistence lives in the repository and orchestration in the service.
package model

import (
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of an audit_job, mirroring the audit_status
// enum declared in migration 000007. The values are stable: they are persisted
// and returned to clients, so they must never be renamed casually.
type Status string

const (
	// StatusQueued is the initial state: the job is created and its run task is
	// enqueued, but no work has started.
	StatusQueued Status = "queued"
	// StatusRunning is set when the worker picks the job up and begins fanning
	// out across the influencer's connected platforms.
	StatusRunning Status = "running"
	// StatusPartial is a terminal success: some connected platforms produced
	// data and some did not. A partial audit delivered value, so it consumes
	// quota.
	StatusPartial Status = "partial"
	// StatusSucceeded is a terminal success: every connected platform produced
	// data.
	StatusSucceeded Status = "succeeded"
	// StatusFailed is a terminal failure: no connected platform produced data.
	// Only this outcome releases the reserved quota unit.
	StatusFailed Status = "failed"
	// StatusCanceled is a terminal state reached by explicit cancellation. The
	// orchestrator never sets it; it exists so a canceled job is recognised as
	// terminal and never re-run.
	StatusCanceled Status = "canceled"
)

// Terminal reports whether s is a final state a run must never advance out of.
// The worker consults it to make re-delivery of the same task idempotent: a job
// already in a terminal state is left untouched.
func (s Status) Terminal() bool {
	switch s {
	case StatusPartial, StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

// Platform-result statuses, matching the CHECK constraint on
// audit_platform_result.status in migration 000007.
const (
	// ResultOK marks a platform that returned a complete snapshot.
	ResultOK = "ok"
	// ResultPartial marks a platform that returned a usable but incomplete
	// snapshot, or one that was rate-limited or over quota. A rate-limited or
	// quota-exhausted platform does not abort the audit.
	ResultPartial = "partial"
	// ResultSkipped marks a connected platform with no registered connector, so
	// no fetch was attempted.
	ResultSkipped = "skipped"
	// ResultError marks a platform whose fetch failed for a reason other than a
	// rate limit or exhausted quota.
	ResultError = "error"
)

// Job is the audit_job row in domain form.
type Job struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// InfluencerID is uuid.Nil when the influencer row was removed after the job
	// was created (the schema sets it NULL on influencer delete), in which case
	// no connections can be resolved and the run ends failed.
	InfluencerID       uuid.UUID
	IdempotencyKey     string
	Status             Status
	RequestedPlatforms []string
	ErrorCode          string
	ErrorMessage       string
	RequestedAt        time.Time
	StartedAt          *time.Time
	FinishedAt         *time.Time
}

// PlatformResult is an audit_platform_result row in domain form.
type PlatformResult struct {
	Platform     string
	Status       string
	ErrorCode    string
	ErrorMessage string
	FetchedAt    *time.Time
}

// CreateJobParams is the input to an idempotent job insert.
type CreateJobParams struct {
	UserID             uuid.UUID
	InfluencerID       uuid.UUID
	IdempotencyKey     string
	RequestedPlatforms []string
}

// ToAuditResponse projects a job and its per-platform results onto the wire DTO.
// It is a pure mapping: no field is invented, and error columns are surfaced
// only when set.
func ToAuditResponse(job Job, results []PlatformResult) AuditResponse {
	platforms := make([]PlatformResultResponse, 0, len(results))
	for _, r := range results {
		platforms = append(platforms, PlatformResultResponse(r))
	}

	resp := AuditResponse{
		ID:                 job.ID.String(),
		Status:             string(job.Status),
		RequestedPlatforms: job.RequestedPlatforms,
		Platforms:          platforms,
		ErrorCode:          job.ErrorCode,
		ErrorMessage:       job.ErrorMessage,
		RequestedAt:        job.RequestedAt,
		StartedAt:          job.StartedAt,
		FinishedAt:         job.FinishedAt,
	}
	if job.InfluencerID != uuid.Nil {
		resp.InfluencerID = job.InfluencerID.String()
	}
	if resp.RequestedPlatforms == nil {
		resp.RequestedPlatforms = []string{}
	}
	return resp
}
