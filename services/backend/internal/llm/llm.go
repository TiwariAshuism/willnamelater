// Package llm is the Claude advisory layer: it turns a finished audit's metrics
// into a structured, human-readable report through Anthropic's models.
//
// The package exposes a single seam, Provider, so the report/audit path depends
// on an interface rather than on Anthropic directly. A future OpenAI-backed
// implementation drops in behind the same interface, and tests exercise the
// audit path against a fake Provider that never touches the network.
//
// GenerateReport never parses prose. It constrains Claude to a JSON schema
// (output_config.format) and deserializes the reply into ReportOutput, which is
// exactly the object the caller persists into llm_generation.content_jsonb.
//
// Cost control is a first-class concern (PRD §11.4): the ~3k-token rubric is a
// byte-stable system prefix carrying a cache_control breakpoint, so it caches
// across audits while each audit's volatile metrics ride in the user turn after
// the breakpoint. Every generation returns a Usage record with token counts, a
// computed cost, and whether the prompt cache was hit.
package llm

import (
	"context"

	"github.com/google/uuid"
)

// Provider is the swappable advisory seam. The audit path calls only
// GenerateReport; Chat is declared for a phase-2 conversational surface and is
// not yet wired.
type Provider interface {
	// GenerateReport produces the structured advisory report for one audit and
	// returns the parsed output alongside a Usage accounting record. A non-nil
	// error means no usable report was produced.
	GenerateReport(ctx context.Context, in ReportInput) (ReportOutput, Usage, error)

	// Chat is the phase-2 conversational surface. It is part of the seam so the
	// contract is complete, but it is not implemented yet and returns
	// errs.ErrNotImplemented.
	Chat(ctx context.Context, in ChatInput) (ChatOutput, Usage, error)
}

// ReportInput is one audit's volatile metrics: the data that changes every run
// and therefore rides after the cached system prefix. It carries only primitive
// and local types so the llm module never imports another business module.
type ReportInput struct {
	// AuditJobID ties the generation's accounting row (llm_generation) back to
	// the audit that requested it. It never enters the prompt.
	AuditJobID uuid.UUID
	// Purpose labels the generation on its accounting row, e.g. "summary". It
	// never enters the prompt. An empty value defaults to "summary".
	Purpose string
	// Handle is the influencer's primary public handle, used to address the
	// report. It is not sensitive and is safe to include in the prompt.
	Handle string
	// Niche and Tier locate the influencer in the benchmark grid, so the model
	// can calibrate its advice to peers rather than to the whole population.
	Niche string
	Tier  string
	// Followers is the aggregate reach across contributing platforms.
	Followers int64
	// Platforms names the platforms that actually produced data for this audit,
	// so the report never implies coverage a partial audit did not have.
	Platforms []string
	// InfluenceScore and Authenticity are the composite outputs of the scoring
	// engine, on a 0..100 scale.
	InfluenceScore float64
	// Authenticity is nil when the subscore rests on no measurement. The narrative
	// prompt must not be handed a neutral 50 as though it were a finding — the LLM
	// would write prose around an invented number.
	Authenticity *float64
	// Subscores is the per-dimension breakdown behind InfluenceScore, each with
	// its own confidence.
	Subscores []Subscore
	// Fraud is the coordinated-engagement estimate from the ML layer.
	Fraud FraudEstimate
	// Metrics are supporting engagement figures (engagement rate, average likes,
	// comment ratio, ...) the model may reference in its advice.
	Metrics []Metric
	// BenchmarkLabel records the provenance of the benchmark cell used, e.g.
	// "industry-bootstrap v1", so the report can disclose it.
	BenchmarkLabel string
}

// Subscore is one weighted dimension of the composite influence score.
type Subscore struct {
	Name       string
	Value      float64 // 0..100
	Confidence float64 // 0..1
}

// FraudEstimate is the ML layer's coordinated-engagement finding. It is always
// an estimate; the report surfaces it as such rather than as a verdict.
type FraudEstimate struct {
	// CliqueCount is the number of maximal co-commenter cliques the ML layer
	// found — the headline coordination signal.
	CliqueCount int
	// Confidence is the model's confidence in the estimate, 0..1.
	Confidence float64
	// Estimate is always true; it is carried explicitly so the label survives
	// serialization into the prompt and cannot be silently dropped.
	Estimate bool
}

// Metric is one named supporting figure.
type Metric struct {
	Name  string
	Value float64
}

// ReportOutput is the deterministically deserialized advisory report. It is the
// object the caller persists into llm_generation.content_jsonb; its JSON tags
// define the schema Claude is constrained to.
type ReportOutput struct {
	Summary          string        `json:"summary"`
	WeaknessFixPairs []WeaknessFix `json:"weakness_fix_pairs"`
	GrowthTips       []string      `json:"growth_tips"`
	BrandFit         string        `json:"brand_fit"`
}

// WeaknessFix pairs an observed weakness with a concrete, actionable fix.
type WeaknessFix struct {
	Weakness string `json:"weakness"`
	Fix      string `json:"fix"`
}

// Usage is the accounting record for one generation. The caller writes these
// fields onto an llm_generation row (migration 000011).
type Usage struct {
	Model        string
	PromptHash   string
	InputTokens  int
	OutputTokens int
	// CostMicros is the computed cost in micro-dollars (millionths of USD),
	// derived from the model's per-token price with cache reads and writes
	// priced at their respective multipliers.
	CostMicros int64
	// Cached is true when the system prefix was served from the prompt cache.
	Cached bool
	// LatencyMS is the wall-clock time the model request took.
	LatencyMS int
}

// ChatInput is the phase-2 conversational request. It is declared so the
// Provider seam is complete; Chat is not yet implemented.
type ChatInput struct {
	System   string
	Messages []ChatMessage
}

// ChatMessage is one turn in a phase-2 conversation.
type ChatMessage struct {
	Role    string
	Content string
}

// ChatOutput is the phase-2 conversational reply.
type ChatOutput struct {
	Reply string
}
