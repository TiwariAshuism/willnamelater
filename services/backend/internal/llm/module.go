package llm

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Module is the wired llm layer: the advisory Provider plus the persistence of
// each generation's cost onto llm_generation (migration 000011). The provider
// stays pure — it only talks to Anthropic and computes a Usage record — so this
// module is the seam that owns the accounting write, keeping cost tracking in
// one place the composition root can reach.
type Module struct {
	provider Provider
	pool     *db.Pool
}

// NewModule builds the llm module over the shared pool. apiKey authenticates the
// Anthropic client; an empty key yields a provider whose GenerateReport fails at
// call time with a KindUnavailable error, which the audit path treats as an
// advisory-only degradation rather than a hard failure.
func NewModule(apiKey config.Secret, pool *db.Pool) *Module {
	return &Module{provider: New(apiKey), pool: pool}
}

// GenerateReport produces the advisory narrative and records the generation's
// token cost on an llm_generation row, returning the report, its usage, and the
// id of the accounting row.
//
// It deliberately does not persist the report *content*: that table is owned by
// the phase-2 report module and does not exist yet. This method exists now so
// that every generation's cost is accounted from the first live audit, and so
// the orchestrator has a real, persisted generation id to reference — not a
// fabricated handle. A failure to record the usage is surfaced, because a
// generation whose cost is silently lost is exactly the kind of quiet gap this
// product is built to expose.
func (m *Module) GenerateReport(ctx context.Context, in ReportInput) (ReportOutput, Usage, uuid.UUID, error) {
	out, usage, err := m.provider.GenerateReport(ctx, in)
	if err != nil {
		return ReportOutput{}, Usage{}, uuid.Nil, err
	}

	purpose := in.Purpose
	if purpose == "" {
		purpose = "summary"
	}

	// A nil audit_job_id is legal (the column is nullable ON DELETE SET NULL);
	// passing the zero uuid instead would violate the foreign key. The audit path
	// always supplies a real job id, but stay correct if it ever does not.
	var auditJobID any
	if in.AuditJobID != uuid.Nil {
		auditJobID = in.AuditJobID
	}

	// The report content and its cost land on the same row: a stored narrative can
	// never exist without its accounting, and a restated audit overwrites both.
	content, err := json.Marshal(out)
	if err != nil {
		return ReportOutput{}, Usage{}, uuid.Nil, errs.Wrap(err, errs.KindInternal,
			"llm.encode_content", "could not encode report content")
	}

	const q = `INSERT INTO llm_generation
		(audit_job_id, purpose, model, prompt_hash, input_tokens, output_tokens, cost_micros, cached, latency_ms, content_jsonb)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
		RETURNING id`

	var id uuid.UUID
	if err := m.pool.QueryRow(ctx, q,
		auditJobID, purpose, usage.Model, usage.PromptHash,
		usage.InputTokens, usage.OutputTokens, usage.CostMicros, usage.Cached, usage.LatencyMS, content,
	).Scan(&id); err != nil {
		return ReportOutput{}, Usage{}, uuid.Nil, errs.Wrap(err, errs.KindUnavailable,
			"llm.persist_generation", "could not record report generation usage")
	}

	return out, usage, id, nil
}

// NarrativeOf returns the most recent stored report narrative for an audit job,
// decoded from llm_generation.content_jsonb. The bool is false when the job has
// no stored narrative yet (the generation failed, or the ML/LLM step was
// skipped) — the report read treats that as "narrative pending", not an error.
// It selects the latest row for the job so a re-run's restated narrative wins.
func (m *Module) NarrativeOf(ctx context.Context, auditJobID uuid.UUID) (ReportOutput, bool, error) {
	const q = `SELECT content_jsonb
		FROM llm_generation
		WHERE audit_job_id = $1 AND purpose = 'summary' AND content_jsonb IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1`

	var raw []byte
	if err := m.pool.QueryRow(ctx, q, auditJobID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReportOutput{}, false, nil
		}
		return ReportOutput{}, false, errs.Wrap(err, errs.KindUnavailable,
			"llm.read_narrative", "could not read report narrative")
	}

	var out ReportOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return ReportOutput{}, false, errs.Wrap(err, errs.KindInternal,
			"llm.decode_narrative", "could not decode stored report narrative")
	}
	return out, true, nil
}
