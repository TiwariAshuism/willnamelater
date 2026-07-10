package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeMessages is a stand-in for Anthropic's Messages API. It records every
// request it receives and replays a canned message, so tests exercise the
// provider without any network call.
type fakeMessages struct {
	calls []anthropic.MessageNewParams
	reply *anthropic.Message
	err   error
}

func (f *fakeMessages) New(_ context.Context, params anthropic.MessageNewParams, _ ...option.RequestOption) (*anthropic.Message, error) {
	f.calls = append(f.calls, params)
	if f.err != nil {
		return nil, f.err
	}
	return f.reply, nil
}

// replyUsage bundles the token counts a fake reply reports.
type replyUsage struct {
	input, output, cacheRead, cacheWrite int64
}

// buildMessage constructs an *anthropic.Message from the API wire shape so that
// the SDK's own decoding populates each content block — the only reliable way to
// make a union block's typed accessor return text in a test.
func buildMessage(t *testing.T, bodyText, stopReason string, u replyUsage) *anthropic.Message {
	t.Helper()
	wire := map[string]any{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"model":       string(model),
		"stop_reason": stopReason,
		"content":     []any{map[string]any{"type": "text", "text": bodyText}},
		"usage": map[string]any{
			"input_tokens":                u.input,
			"output_tokens":               u.output,
			"cache_read_input_tokens":     u.cacheRead,
			"cache_creation_input_tokens": u.cacheWrite,
		},
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire message: %v", err)
	}
	var msg anthropic.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal into anthropic.Message: %v", err)
	}
	return &msg
}

// sampleReport is a schema-valid report body used as a fake reply. Its wording
// is a test fixture, not a claim about what any model produces.
const sampleReport = `{
  "summary": "s",
  "weakness_fix_pairs": [
    {"weakness": "w1", "fix": "f1"},
    {"weakness": "w2", "fix": "f2"}
  ],
  "growth_tips": ["t1", "t2", "t3"],
  "brand_fit": "b"
}`

func inputA() ReportInput {
	return ReportInput{
		Handle:         "creator_a",
		Niche:          "fitness",
		Tier:           "mid",
		Followers:      120000,
		Platforms:      []string{"youtube"},
		InfluenceScore: 61.5,
		Authenticity:   72.0,
		Subscores:      []Subscore{{Name: "engagement_quality", Value: 58, Confidence: 0.6}},
		Fraud:          FraudEstimate{CliqueCount: 3, Confidence: 0.4, Estimate: true},
		Metrics:        []Metric{{Name: "engagement_rate", Value: 0.031}},
		BenchmarkLabel: "industry-bootstrap v1",
	}
}

func inputB() ReportInput {
	return ReportInput{
		Handle:         "creator_b",
		Niche:          "beauty",
		Tier:           "macro",
		Followers:      2400000,
		Platforms:      []string{"youtube", "instagram"},
		InfluenceScore: 88.0,
		Authenticity:   40.0,
		Subscores:      []Subscore{{Name: "authenticity", Value: 40, Confidence: 0.9}},
		Fraud:          FraudEstimate{CliqueCount: 27, Confidence: 0.8, Estimate: true},
		Metrics:        []Metric{{Name: "engagement_rate", Value: 0.012}},
		BenchmarkLabel: "industry-bootstrap v1",
	}
}

// TestGenerateReport_ParsesStructuredOutput asserts the constrained JSON reply
// deserializes deterministically into the typed struct.
func TestGenerateReport_ParsesStructuredOutput(t *testing.T) {
	fake := &fakeMessages{reply: buildMessage(t, sampleReport, "end_turn", replyUsage{input: 100, output: 40})}
	p := newProvider(fake)

	out, _, err := p.GenerateReport(context.Background(), inputA())
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	want := ReportOutput{
		Summary: "s",
		WeaknessFixPairs: []WeaknessFix{
			{Weakness: "w1", Fix: "f1"},
			{Weakness: "w2", Fix: "f2"},
		},
		GrowthTips: []string{"t1", "t2", "t3"},
		BrandFit:   "b",
	}
	if len(out.WeaknessFixPairs) != 2 || len(out.GrowthTips) != 3 {
		t.Fatalf("unexpected shape: %+v", out)
	}
	if out.Summary != want.Summary || out.BrandFit != want.BrandFit {
		t.Fatalf("scalar fields did not round-trip: %+v", out)
	}
	for i, wf := range want.WeaknessFixPairs {
		if out.WeaknessFixPairs[i] != wf {
			t.Fatalf("weakness_fix_pair %d = %+v, want %+v", i, out.WeaknessFixPairs[i], wf)
		}
	}
}

// TestGenerateReport_CachedPrefixStableAcrossAudits asserts the system prefix is
// byte-identical across two audits with different data, that the breakpoint is
// on it, and that the volatile user content differs — the invariant that makes
// the rubric cacheable.
func TestGenerateReport_CachedPrefixStableAcrossAudits(t *testing.T) {
	fake := &fakeMessages{reply: buildMessage(t, sampleReport, "end_turn", replyUsage{input: 100, output: 40})}
	p := newProvider(fake)

	if _, _, err := p.GenerateReport(context.Background(), inputA()); err != nil {
		t.Fatalf("first GenerateReport: %v", err)
	}
	if _, _, err := p.GenerateReport(context.Background(), inputB()); err != nil {
		t.Fatalf("second GenerateReport: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(fake.calls))
	}

	sys := func(params anthropic.MessageNewParams) anthropic.TextBlockParam {
		if len(params.System) != 1 {
			t.Fatalf("expected 1 system block, got %d", len(params.System))
		}
		return params.System[0]
	}
	sysA, sysB := sys(fake.calls[0]), sys(fake.calls[1])

	if sysA.Text != sysB.Text {
		t.Fatal("system prefix differs across audits; the cache would miss")
	}
	if sysA.Text != systemPrompt {
		t.Fatal("system prefix is not the stable rubric constant")
	}
	if sysA.CacheControl.Type == "" {
		t.Fatal("system prefix carries no cache_control breakpoint")
	}

	userText := func(params anthropic.MessageNewParams) string {
		if len(params.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(params.Messages))
		}
		blocks := params.Messages[0].Content
		if len(blocks) != 1 || blocks[0].OfText == nil {
			t.Fatalf("expected a single user text block, got %+v", blocks)
		}
		return blocks[0].OfText.Text
	}
	if userText(fake.calls[0]) == userText(fake.calls[1]) {
		t.Fatal("volatile user content did not change with the audit data")
	}
}

// TestGenerateReport_RecordsUsageAndCost asserts token totals, the cache flag,
// and the computed cost are all recorded, for both an uncached and a cached run.
func TestGenerateReport_RecordsUsageAndCost(t *testing.T) {
	t.Run("uncached", func(t *testing.T) {
		fake := &fakeMessages{reply: buildMessage(t, sampleReport, "end_turn",
			replyUsage{input: 1000, output: 200, cacheRead: 0, cacheWrite: 3000})}
		p := newProvider(fake)

		_, usage, err := p.GenerateReport(context.Background(), inputA())
		if err != nil {
			t.Fatalf("GenerateReport: %v", err)
		}
		// input_tokens = regular + cache_read + cache_write.
		if usage.InputTokens != 4000 || usage.OutputTokens != 200 {
			t.Fatalf("tokens = in %d/out %d, want 4000/200", usage.InputTokens, usage.OutputTokens)
		}
		if usage.Cached {
			t.Fatal("Cached should be false with zero cache reads")
		}
		// 1000*5 + 3000*5*1.25 + 200*25 = 5000 + 18750 + 5000 = 28750.
		if usage.CostMicros != 28750 {
			t.Fatalf("CostMicros = %d, want 28750", usage.CostMicros)
		}
		if usage.Model != string(model) || usage.PromptHash == "" {
			t.Fatalf("model/prompt_hash not recorded: %+v", usage)
		}
	})

	t.Run("cached", func(t *testing.T) {
		fake := &fakeMessages{reply: buildMessage(t, sampleReport, "end_turn",
			replyUsage{input: 100, output: 200, cacheRead: 3000, cacheWrite: 0})}
		p := newProvider(fake)

		_, usage, err := p.GenerateReport(context.Background(), inputA())
		if err != nil {
			t.Fatalf("GenerateReport: %v", err)
		}
		if usage.InputTokens != 3100 {
			t.Fatalf("InputTokens = %d, want 3100", usage.InputTokens)
		}
		if !usage.Cached {
			t.Fatal("Cached should be true when cache reads are present")
		}
		// 100*5 + 3000*5*0.1 + 200*25 = 500 + 1500 + 5000 = 7000.
		if usage.CostMicros != 7000 {
			t.Fatalf("CostMicros = %d, want 7000", usage.CostMicros)
		}
	})
}

// TestGenerateReport_Refusal maps a refusal stop reason onto an unavailable
// dependency so the audit can degrade instead of crashing.
func TestGenerateReport_Refusal(t *testing.T) {
	fake := &fakeMessages{reply: buildMessage(t, "", "refusal", replyUsage{})}
	p := newProvider(fake)

	_, _, err := p.GenerateReport(context.Background(), inputA())
	if errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("refusal kind = %v, want KindUnavailable (%v)", errs.KindOf(err), err)
	}
}

// TestGenerateReport_MalformedResponse rejects a reply that is not schema-valid
// JSON rather than surfacing garbage as a report.
func TestGenerateReport_MalformedResponse(t *testing.T) {
	fake := &fakeMessages{reply: buildMessage(t, "not json", "end_turn", replyUsage{input: 10, output: 5})}
	p := newProvider(fake)

	_, _, err := p.GenerateReport(context.Background(), inputA())
	if errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("malformed kind = %v, want KindUnavailable", errs.KindOf(err))
	}
}

// TestGenerateReport_RequestError wraps a transport failure as unavailable.
func TestGenerateReport_RequestError(t *testing.T) {
	fake := &fakeMessages{err: errors.New("dial tcp: connection refused")}
	p := newProvider(fake)

	_, _, err := p.GenerateReport(context.Background(), inputA())
	if errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("request-error kind = %v, want KindUnavailable", errs.KindOf(err))
	}
}

// TestChat_NotImplemented confirms the phase-2 surface reports itself honestly.
func TestChat_NotImplemented(t *testing.T) {
	p := newProvider(&fakeMessages{})
	_, _, err := p.Chat(context.Background(), ChatInput{})
	if !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("Chat error = %v, want ErrNotImplemented", err)
	}
}

// Provider is satisfied by the concrete provider — a compile-time guard.
var _ Provider = (*anthropicProvider)(nil)
