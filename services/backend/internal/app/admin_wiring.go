package app

import (
	"context"

	"github.com/google/uuid"

	adminport "github.com/getnyx/influaudit/backend/internal/admin/port"
	"github.com/getnyx/influaudit/backend/internal/audit"
	"github.com/getnyx/influaudit/backend/internal/auth"
	"github.com/getnyx/influaudit/backend/internal/llm"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// This file is the admin module's wiring: the thin adapters that project each
// real collaborator onto the consumer-side ports the admin module declares in
// internal/admin/port. The admin module imports none of these; every arrow into
// it passes through an adapter here. The caller-identity port is satisfied by
// the shared auditCaller adapter (in audit_wiring.go), and the queue-inspector
// port is satisfied directly by *asynq.Inspector, so neither needs an adapter.

// --- AdminGuard: auth -> adminport.AdminGuard ----------------------------

// adminGuard adapts the auth module's context accessors onto the admin module's
// AdminGuard port, so admin never imports auth. It composes the two unforgeable
// context reads (identity present, and the role bit the middleware carried from
// the verified token) into the guard's three outcomes: an unauthenticated
// request is unauthorized, an authenticated non-admin is forbidden, and an admin
// yields their id to become a resolved dispute's resolved_by.
type adminGuard struct{}

func (adminGuard) RequireAdmin(ctx context.Context) (uuid.UUID, error) {
	id, ok := auth.UserID(ctx)
	if !ok {
		return uuid.Nil, errs.New(errs.KindUnauthorized, "app.unauthenticated",
			"this endpoint requires authentication")
	}
	role, _ := auth.Role(ctx)
	if role != "admin" {
		return uuid.Nil, errs.New(errs.KindForbidden, "app.forbidden",
			"this endpoint requires an admin account")
	}
	return id, nil
}

// --- FraudReader: audit.Module -> adminport.FraudReader ------------------

// adminFraudReader adapts audit.Module.FraudResultOf onto the admin module's
// FraudReader port, mapping the audit module's FraudView onto the admin port's
// identically-shaped one. The found bool is passed through so the label export
// can attach features only when a fraud row actually exists.
type adminFraudReader struct{ a *audit.Module }

func (r adminFraudReader) FraudResultOf(ctx context.Context, auditJobID uuid.UUID) (adminport.FraudView, bool, error) {
	fr, found, err := r.a.FraudResultOf(ctx, auditJobID)
	if err != nil || !found {
		return adminport.FraudView{}, found, err
	}
	return adminport.FraudView{
		Present:                  fr.Present,
		FakeFollowerRate:         fr.FakeFollowerRate,
		BotCommentRate:           fr.BotCommentRate,
		EngagementAnomaly:        fr.EngagementAnomaly,
		CliqueCount:              fr.CliqueCount,
		CliqueMembershipFraction: fr.CliqueMembershipFraction,
		Confidence:               fr.Confidence,
		ModelVersion:             fr.ModelVersion,
	}, true, nil
}

// --- CostReader: llm.Module -> adminport.CostReader ----------------------

// adminCostReader adapts llm.Module.CostSummary onto the admin module's
// CostReader port. The llm module owns the llm_generation table and computes the
// aggregate; the adapter only restates its shape onto the admin port so admin
// never imports llm.
type adminCostReader struct{ l *llm.Module }

func (r adminCostReader) CostSummary(ctx context.Context) (adminport.CostSummary, error) {
	cs, err := r.l.CostSummary(ctx)
	if err != nil {
		return adminport.CostSummary{}, err
	}
	out := adminport.CostSummary{
		TotalGenerations:  cs.TotalGenerations,
		TotalInputTokens:  cs.TotalInputTokens,
		TotalOutputTokens: cs.TotalOutputTokens,
		TotalCostMicros:   cs.TotalCostMicros,
		CachedGenerations: cs.CachedGenerations,
		ByModel:           make([]adminport.ModelCost, 0, len(cs.ByModel)),
	}
	for _, m := range cs.ByModel {
		out.ByModel = append(out.ByModel, adminport.ModelCost{
			Model:             m.Model,
			Generations:       m.Generations,
			InputTokens:       m.InputTokens,
			OutputTokens:      m.OutputTokens,
			CostMicros:        m.CostMicros,
			CachedGenerations: m.CachedGenerations,
		})
	}
	return out, nil
}
