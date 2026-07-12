// Package service implements the admin module's business logic: the dispute
// review loop (file, queue, resolve-with-label), the training-label export that
// turns a resolved dispute into a supervised example, the API-cost dashboard
// over the llm generation aggregate, and the asynq job monitor.
//
// Every collaborator is reached through a port declared in internal/admin/port,
// so this package imports no other business module. The repository is the sole
// data-access dependency and is declared here as a consumer-side interface the
// repository package satisfies. Filing a dispute is open to any authenticated
// caller; every other operation is gated by the AdminGuard port.
package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/admin/internal/model"
	"github.com/getnyx/influaudit/backend/internal/admin/port"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// microsPerUSD is the number of cost micros (millionths of a dollar) in one US
// dollar. cost_micros on llm_generation is stored in these units.
const microsPerUSD = 1_000_000.0

// Repository is the admin module's data-access contract. It is declared by the
// service (its consumer) and satisfied by the repository package.
type Repository interface {
	// CreateDispute files a new open dispute. A dispute against a non-existent
	// audit is a not-found error, surfaced by the foreign-key constraint.
	CreateDispute(ctx context.Context, params model.CreateDisputeParams) (model.Dispute, error)
	// ListOpenDisputes returns the review queue: every open dispute, oldest first.
	ListOpenDisputes(ctx context.Context) ([]model.Dispute, error)
	// ListDecidedDisputes returns every resolved or rejected dispute for the
	// training-label export, newest decision first.
	ListDecidedDisputes(ctx context.Context) ([]model.Dispute, error)
	// ResolveDispute records a decision on an open dispute. Resolving a dispute
	// that does not exist is a not-found error; resolving one already decided is a
	// conflict.
	ResolveDispute(ctx context.Context, params model.ResolveDisputeParams) (model.Dispute, error)
}

// Service is the wired admin service. It satisfies the generated AdminService.
type Service struct {
	repo   Repository
	caller port.CallerID
	guard  port.AdminGuard
	fraud  port.FraudReader
	cost   port.CostReader
	queues port.QueueInspector
	// labels is the optional ml training-label sink. It may be nil (dispute
	// resolution is unchanged); when set, a resolved dispute best-effort backfills
	// the supervised fraud label onto the audit's feature-store row.
	labels port.TrainingLabelSink
}

var _ AdminService = (*Service)(nil)

// New builds the admin service over its repository and every collaborator port.
// labels is optional (nil disables the ml training-label backfill).
func New(
	repo Repository,
	caller port.CallerID,
	guard port.AdminGuard,
	fraud port.FraudReader,
	cost port.CostReader,
	queues port.QueueInspector,
	labels port.TrainingLabelSink,
) *Service {
	return &Service{
		repo:   repo,
		caller: caller,
		guard:  guard,
		fraud:  fraud,
		cost:   cost,
		queues: queues,
		labels: labels,
	}
}

// FileDispute files a dispute against an audit on behalf of the authenticated
// caller. The caller becomes the dispute's raised_by; filing is open to any
// signed-in user, since the audited party is who contests a result.
func (s *Service) FileDispute(ctx context.Context, id string, req model.FileDisputeRequest) (model.DisputeResponse, error) {
	userID, err := s.caller.CallerID(ctx)
	if err != nil {
		return model.DisputeResponse{}, err
	}

	auditJobID, err := uuid.Parse(id)
	if err != nil {
		return model.DisputeResponse{}, errs.New(errs.KindInvalid, "admin.invalid_audit_id", "audit id is not a valid uuid")
	}

	dispute, err := s.repo.CreateDispute(ctx, model.CreateDisputeParams{
		AuditJobID: auditJobID,
		RaisedBy:   userID,
		Reason:     req.Reason,
	})
	if err != nil {
		return model.DisputeResponse{}, err
	}
	return model.ToDisputeResponse(dispute), nil
}

// ListDisputeQueue returns the admin review queue: every open dispute. It is
// admin-only.
func (s *Service) ListDisputeQueue(ctx context.Context) ([]model.DisputeResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return nil, err
	}

	disputes, err := s.repo.ListOpenDisputes(ctx)
	if err != nil {
		return nil, err
	}

	resp := make([]model.DisputeResponse, 0, len(disputes))
	for _, d := range disputes {
		resp = append(resp, model.ToDisputeResponse(d))
	}
	return resp, nil
}

// ResolveDispute records an admin's decision on an open dispute. The decision is
// the labelling act: it becomes the dispute's terminal status and, downstream,
// the supervised label the training export carries. It is admin-only.
func (s *Service) ResolveDispute(ctx context.Context, id string, req model.ResolveDisputeRequest) (model.DisputeResponse, error) {
	adminID, err := s.guard.RequireAdmin(ctx)
	if err != nil {
		return model.DisputeResponse{}, err
	}

	disputeID, err := uuid.Parse(id)
	if err != nil {
		return model.DisputeResponse{}, errs.New(errs.KindInvalid, "admin.invalid_dispute_id", "dispute id is not a valid uuid")
	}

	decision := model.Decision(req.Decision)
	if !decision.Valid() {
		return model.DisputeResponse{}, errs.New(errs.KindInvalid, "admin.invalid_decision", "decision must be 'upheld' or 'rejected'")
	}

	dispute, err := s.repo.ResolveDispute(ctx, model.ResolveDisputeParams{
		ID:         disputeID,
		Status:     decision.Status(),
		Resolution: req.Notes,
		ResolvedBy: adminID,
	})
	if err != nil {
		return model.DisputeResponse{}, err
	}

	// Backfill the supervised fraud label onto the audit's feature-store row (the
	// ml labelling loop). Best-effort: the decision is already recorded, so a sink
	// failure is logged and never fails the resolution.
	s.recordLabel(ctx, dispute.AuditJobID, decision.FraudLabel())

	return model.ToDisputeResponse(dispute), nil
}

// recordLabel backfills the dispute's supervised fraud label through the optional
// TrainingLabelSink. A nil sink is a no-op; any error is logged and swallowed.
func (s *Service) recordLabel(ctx context.Context, auditJobID uuid.UUID, fraudulent bool) {
	if s.labels == nil {
		return
	}
	if err := s.labels.RecordDisputeLabel(ctx, auditJobID, fraudulent); err != nil {
		slog.WarnContext(ctx, "ml training-label backfill failed (dispute resolution unaffected)",
			slog.String("audit_job_id", auditJobID.String()), slog.Any("error", err))
	}
}

// CostDashboard returns the aggregate LLM generation cost, with the USD and
// cache-hit-rate figures computed for display. It is admin-only.
func (s *Service) CostDashboard(ctx context.Context) (model.CostDashboardResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.CostDashboardResponse{}, err
	}

	summary, err := s.cost.CostSummary(ctx)
	if err != nil {
		return model.CostDashboardResponse{}, err
	}
	return toCostDashboard(summary), nil
}

// QueueMonitor returns the live state of every asynq queue. A failure to reach
// the queue backend surfaces as an unavailable error (the inspector talks to
// Redis), keeping a transient outage distinct from a client mistake. It is
// admin-only.
func (s *Service) QueueMonitor(ctx context.Context) (model.QueueMonitorResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.QueueMonitorResponse{}, err
	}

	names, err := s.queues.Queues()
	if err != nil {
		return model.QueueMonitorResponse{}, errs.Wrap(err, errs.KindUnavailable, "admin.queues_unavailable", "could not read queue state")
	}

	snapshots := make([]model.QueueSnapshot, 0, len(names))
	for _, name := range names {
		info, infoErr := s.queues.GetQueueInfo(name)
		if infoErr != nil {
			return model.QueueMonitorResponse{}, errs.Wrap(infoErr, errs.KindUnavailable, "admin.queues_unavailable", "could not read queue state")
		}
		snapshots = append(snapshots, model.QueueSnapshot{
			Queue:     info.Queue,
			Size:      info.Size,
			Pending:   info.Pending,
			Active:    info.Active,
			Scheduled: info.Scheduled,
			Retry:     info.Retry,
			Archived:  info.Archived,
			Completed: info.Completed,
			Processed: info.Processed,
			Failed:    info.Failed,
			Paused:    info.Paused,
			LatencyMs: info.Latency.Milliseconds(),
		})
	}
	return model.QueueMonitorResponse{Queues: snapshots}, nil
}

// ExportLabels projects every decided dispute into a supervised training example
// for services/ml/training. Each example carries the admin's decision as its
// label and, when the disputed audit produced a stored fraud estimate, that
// estimate as its features — never a fabricated all-zero vector when the estimate
// is absent. It is admin-only.
func (s *Service) ExportLabels(ctx context.Context) (model.LabelExportResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.LabelExportResponse{}, err
	}

	disputes, err := s.repo.ListDecidedDisputes(ctx)
	if err != nil {
		return model.LabelExportResponse{}, err
	}

	labels := make([]model.TrainingLabel, 0, len(disputes))
	for _, d := range disputes {
		label := model.TrainingLabel{
			DisputeID:  d.ID.String(),
			AuditJobID: d.AuditJobID.String(),
			Label:      d.Status == model.StatusRejected,
		}
		if d.ResolvedAt != nil {
			label.ResolvedAt = *d.ResolvedAt
		}

		view, found, fraudErr := s.fraud.FraudResultOf(ctx, d.AuditJobID)
		if fraudErr != nil {
			return model.LabelExportResponse{}, fraudErr
		}
		if found {
			label.HasFeatures = true
			label.Features = model.FraudFeatures{
				Present:                  view.Present,
				RiskScore:                view.RiskScore,
				EngagementAnomaly:        view.EngagementAnomaly,
				CliqueCount:              view.CliqueCount,
				CliqueMembershipFraction: view.CliqueMembershipFraction,
				Confidence:               view.Confidence,
				ModelVersion:             view.ModelVersion,
			}
		}
		labels = append(labels, label)
	}

	return model.LabelExportResponse{Count: len(labels), Labels: labels}, nil
}

// toCostDashboard maps the port-side cost aggregate onto the dashboard DTO,
// computing the USD and cache-hit-rate figures so the frontend renders no
// arithmetic.
func toCostDashboard(s port.CostSummary) model.CostDashboardResponse {
	resp := model.CostDashboardResponse{
		TotalGenerations:  s.TotalGenerations,
		TotalInputTokens:  s.TotalInputTokens,
		TotalOutputTokens: s.TotalOutputTokens,
		TotalCostMicros:   s.TotalCostMicros,
		TotalCostUSD:      microsToUSD(s.TotalCostMicros),
		CachedGenerations: s.CachedGenerations,
		CacheHitRate:      hitRate(s.CachedGenerations, s.TotalGenerations),
		ByModel:           make([]model.CostResponse, 0, len(s.ByModel)),
	}
	for _, m := range s.ByModel {
		resp.ByModel = append(resp.ByModel, model.CostResponse{
			Model:             m.Model,
			Generations:       m.Generations,
			InputTokens:       m.InputTokens,
			OutputTokens:      m.OutputTokens,
			CostMicros:        m.CostMicros,
			CostUSD:           microsToUSD(m.CostMicros),
			CachedGenerations: m.CachedGenerations,
		})
	}
	return resp
}

// microsToUSD converts cost micros to US dollars.
func microsToUSD(micros int64) float64 {
	return float64(micros) / microsPerUSD
}

// hitRate returns cached/total as a fraction, and 0 when there were no
// generations, avoiding a divide-by-zero on an empty dashboard.
func hitRate(cached, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(cached) / float64(total)
}
