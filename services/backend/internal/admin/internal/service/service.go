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
	// ListDecidedDisputes returns every resolved or rejected dispute, newest
	// decision first — including the ones decided on the heuristic alone. The
	// training-label export filters those out itself (ExportLabels); they stay in
	// the database because the dispute outcome is real and the customer is owed it.
	ListDecidedDisputes(ctx context.Context) ([]model.Dispute, error)
	// DisputeByID loads one dispute for the adjudicator's review read.
	DisputeByID(ctx context.Context, id uuid.UUID) (model.Dispute, error)
	// MarkScoreShown records that the heuristic's score was disclosed to the
	// adjudicator. It is the only writer of score_shown_to_admin, and it refuses a
	// dispute that is already decided (a conflict): the flag describes how a
	// decision was reached and cannot be back-dated onto one.
	MarkScoreShown(ctx context.Context, id uuid.UUID) (model.Dispute, error)
	// ResolveDispute records a decision, and the evidence it rests on, on an open
	// dispute. Resolving a dispute that does not exist is a not-found error;
	// resolving one already decided is a conflict.
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

// ReviewDispute returns one dispute for adjudication, EVIDENCE-BLIND: the
// response carries no heuristic score. An adjudicator shown the heuristic's own
// risk score and then asked whether the heuristic was right is not producing a
// label, they are ratifying one, and a model fit on ratifications learns only
// that humans agree with it.
//
// The score is disclosed only once RevealHeuristicScore has been called for this
// dispute — and once it has, the disclosure is on the row, so the read keeps
// showing it. Hiding it again would not un-see it; it would only hide the
// contamination. It is admin-only.
func (s *Service) ReviewDispute(ctx context.Context, id string) (model.DisputeReviewResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.DisputeReviewResponse{}, err
	}

	disputeID, err := uuid.Parse(id)
	if err != nil {
		return model.DisputeReviewResponse{}, errInvalidDisputeID()
	}

	dispute, err := s.repo.DisputeByID(ctx, disputeID)
	if err != nil {
		return model.DisputeReviewResponse{}, err
	}

	resp := model.DisputeReviewResponse{Dispute: model.ToDisputeResponse(dispute)}
	if !dispute.ScoreShownToAdmin {
		return resp, nil
	}

	// Already disclosed for this dispute, by an earlier explicit reveal that is on
	// the record. Attach it.
	score, err := s.heuristicScore(ctx, dispute.AuditJobID)
	if err != nil {
		return model.DisputeReviewResponse{}, err
	}
	resp.HeuristicScore = score
	return resp, nil
}

// RevealHeuristicScore discloses the heuristic's composite score and flags to the
// adjudicator, and RECORDS the disclosure on the dispute (score_shown_to_admin).
// It is the only way the score reaches a review screen, and the flag is written
// server-side — a client's assertion about what a human looked at is worth
// nothing as evidence.
//
// The disclosure is recorded only when there is actually a score to disclose: an
// audit that produced no fraud estimate has nothing to show, and stamping the row
// would enter a disclosure that never happened. Revealing against an
// already-decided dispute is a conflict (the repository refuses it): the flag
// describes how a decision was reached and cannot be back-dated onto one. It is
// admin-only.
func (s *Service) RevealHeuristicScore(ctx context.Context, id string) (model.DisputeReviewResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.DisputeReviewResponse{}, err
	}

	disputeID, err := uuid.Parse(id)
	if err != nil {
		return model.DisputeReviewResponse{}, errInvalidDisputeID()
	}

	dispute, err := s.repo.DisputeByID(ctx, disputeID)
	if err != nil {
		return model.DisputeReviewResponse{}, err
	}

	score, err := s.heuristicScore(ctx, dispute.AuditJobID)
	if err != nil {
		return model.DisputeReviewResponse{}, err
	}
	if score == nil {
		// Nothing was observed by the heuristic, so nothing is disclosed and the row
		// is left alone. The adjudicator adjudicates blind because there is nothing
		// to be blind to.
		return model.DisputeReviewResponse{Dispute: model.ToDisputeResponse(dispute)}, nil
	}

	stamped, err := s.repo.MarkScoreShown(ctx, disputeID)
	if err != nil {
		return model.DisputeReviewResponse{}, err
	}
	return model.DisputeReviewResponse{
		Dispute:        model.ToDisputeResponse(stamped),
		HeuristicScore: score,
	}, nil
}

// heuristicScore loads the audit's stored fraud estimate, or nil when the audit
// produced none. It is the heuristic's OWN output, which is why every path that
// hands it to a human goes through the recorded reveal.
func (s *Service) heuristicScore(ctx context.Context, auditJobID uuid.UUID) (*model.FraudFeatures, error) {
	view, found, err := s.fraud.FraudResultOf(ctx, auditJobID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	features := model.ToFraudFeatures(view)
	return &features, nil
}

// ResolveDispute records an admin's decision on an open dispute, together with
// the evidence the decision actually rests on. The decision alone is not a label:
// the dispute exists only because the heuristic flagged the account, so a bare
// "rejected" says no more than "an admin declined to overturn the flag". What
// makes it a label — or refuses to — is LabelEvidence, so it is required and
// validated against the closed set here. It is admin-only.
func (s *Service) ResolveDispute(ctx context.Context, id string, req model.ResolveDisputeRequest) (model.DisputeResponse, error) {
	adminID, err := s.guard.RequireAdmin(ctx)
	if err != nil {
		return model.DisputeResponse{}, err
	}

	disputeID, err := uuid.Parse(id)
	if err != nil {
		return model.DisputeResponse{}, errInvalidDisputeID()
	}

	decision := model.Decision(req.Decision)
	if !decision.Valid() {
		return model.DisputeResponse{}, errs.New(errs.KindInvalid, "admin.invalid_decision", "decision must be 'upheld' or 'rejected'")
	}

	// An unknown kind, and an omitted one, are both rejected. "I did not say" is
	// not an evidence kind: an adjudicator who observed nothing beyond the flag has
	// a first-class, honest answer available (none_reviewed_heuristic_only), and
	// saying so keeps the row out of the training-label export instead of letting
	// silence smuggle it in.
	evidence := model.LabelEvidence(req.LabelEvidence)
	if !evidence.Valid() {
		return model.DisputeResponse{}, errs.New(errs.KindInvalid, "admin.invalid_label_evidence",
			"label_evidence must state what was observed: one of platform_enforcement_action, "+
				"creator_admission, purchase_receipt_or_panel_invoice, brand_campaign_conversion_data, "+
				"manual_follower_sample_audit, none_reviewed_heuristic_only")
	}

	dispute, err := s.repo.ResolveDispute(ctx, model.ResolveDisputeParams{
		ID:            disputeID,
		Status:        decision.Status(),
		Resolution:    req.Notes,
		ResolvedBy:    adminID,
		LabelEvidence: evidence,
	})
	if err != nil {
		return model.DisputeResponse{}, err
	}

	// Backfill the supervised fraud label onto the audit's feature-store row (the
	// ml labelling loop). The evidence travels with it: mlops, not this module,
	// owns what enters a fold, and it cannot make that call from the bool alone.
	// Best-effort: the decision is already recorded, so a sink failure is logged
	// and never fails the resolution.
	s.recordLabel(ctx, dispute.AuditJobID, decision.FraudLabel(), evidence)

	return model.ToDisputeResponse(dispute), nil
}

// recordLabel backfills the dispute's supervised fraud label, and the evidence it
// rests on, through the optional TrainingLabelSink. A nil sink is a no-op; any
// error is logged and swallowed.
func (s *Service) recordLabel(ctx context.Context, auditJobID uuid.UUID, fraudulent bool, evidence model.LabelEvidence) {
	if s.labels == nil {
		return
	}
	if err := s.labels.RecordDisputeLabel(ctx, auditJobID, fraudulent, evidence); err != nil {
		slog.WarnContext(ctx, "ml training-label backfill failed (dispute resolution unaffected)",
			slog.String("audit_job_id", auditJobID.String()),
			slog.String("label_evidence", string(evidence)), slog.Any("error", err))
	}
}

// errInvalidDisputeID is the single invalid-input error for a dispute id that is
// not a uuid.
func errInvalidDisputeID() error {
	return errs.New(errs.KindInvalid, "admin.invalid_dispute_id", "dispute id is not a valid uuid")
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

// ExportLabels projects decided disputes into supervised training examples for
// services/ml/training. Each example carries the admin's decision as its label,
// the evidence that decision rests on, and — when the disputed audit produced a
// stored fraud estimate — that estimate as its features, never a fabricated
// all-zero vector when the estimate is absent.
//
// A dispute decided WITHOUT an observation is not exported. Its evidence kind
// (none_reviewed_heuristic_only, and equally an evidence nobody ever recorded)
// says the adjudicator saw nothing the heuristic had not already computed, so the
// "label" is the heuristic's own output handed back to it. Training on it teaches
// the model to agree with itself, and no downstream gate can see the problem: they
// all check the model against the labels and assume the labels are real. The rows
// stay in the database — the dispute outcome is a real decision the customer is
// owed — they simply never leave here as y.
//
// It is admin-only.
func (s *Service) ExportLabels(ctx context.Context) (model.LabelExportResponse, error) {
	if _, err := s.guard.RequireAdmin(ctx); err != nil {
		return model.LabelExportResponse{}, err
	}

	disputes, err := s.repo.ListDecidedDisputes(ctx)
	if err != nil {
		return model.LabelExportResponse{}, err
	}

	labels := make([]model.TrainingLabel, 0, len(disputes))
	excluded := 0
	for _, d := range disputes {
		if !d.LabelEvidence.Observable() {
			excluded++
			continue
		}

		label := model.TrainingLabel{
			DisputeID:         d.ID.String(),
			AuditJobID:        d.AuditJobID.String(),
			Label:             d.Status == model.StatusRejected,
			LabelEvidence:     string(d.LabelEvidence),
			ScoreShownToAdmin: d.ScoreShownToAdmin,
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
			label.Features = model.ToFraudFeatures(view)
		}
		labels = append(labels, label)
	}

	return model.LabelExportResponse{Count: len(labels), Excluded: excluded, Labels: labels}, nil
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
