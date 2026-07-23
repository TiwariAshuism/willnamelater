package llm

import (
	"context"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// model is the Claude model this provider drives.
const model = anthropic.ModelClaudeOpus4_8

// maxTokens caps report generation. The report is a bounded JSON object, so a
// modest ceiling is sufficient and keeps the non-streaming request well under
// the SDK's HTTP timeout.
const maxTokens = 16000

// messagesClient is the small seam over Anthropic's Messages API. The real
// *anthropic.Client's Messages field satisfies it; tests supply a fake so no
// generation touches the network.
type messagesClient interface {
	New(ctx context.Context, params anthropic.MessageNewParams, opts ...option.RequestOption) (*anthropic.Message, error)
}

// anthropicProvider is the Anthropic-backed Provider implementation.
type anthropicProvider struct {
	client messagesClient
	now    func() time.Time
}

// New builds a network-backed Provider using apiKey. app wires it from
// cfg.Anthropic.APIKey.
func New(apiKey config.Secret) Provider {
	client := anthropic.NewClient(option.WithAPIKey(apiKey.Reveal()))
	return newProvider(&client.Messages)
}

// newProvider wires a provider over an injected messages client. It is the seam
// used by both New and the tests.
func newProvider(client messagesClient) *anthropicProvider {
	return &anthropicProvider{client: client, now: time.Now}
}

// GenerateReport renders the audit's metrics into the volatile user turn behind
// the cached rubric prefix, constrains the reply to the report schema, and
// deserializes it deterministically. The system prefix is byte-stable and
// carries a cache_control breakpoint, so the rubric caches across audits.
func (p *anthropicProvider) GenerateReport(ctx context.Context, in ReportInput) (ReportOutput, Usage, error) {
	userContent, err := buildUserContent(in)
	if err != nil {
		return ReportOutput{}, Usage{}, err
	}

	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
		Thinking: anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortHigh,
			Format: anthropic.JSONOutputFormatParam{Schema: reportSchema},
		},
		// The rubric is the stable prefix; the cache_control breakpoint on it
		// lets the ~3k-token block be served from cache on later audits.
		System: []anthropic.TextBlockParam{{
			Text:         systemPrompt,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userContent)),
		},
	}

	hash := promptHash(string(model), systemPrompt, userContent)

	start := p.now()
	msg, err := p.client.New(ctx, params)
	latencyMS := int(p.now().Sub(start).Milliseconds())
	if err != nil {
		return ReportOutput{}, Usage{}, errs.Wrap(err, errs.KindUnavailable, "llm.request_failed",
			"the advisory model could not be reached")
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return ReportOutput{}, Usage{}, errs.New(errs.KindUnavailable, "llm.refused",
			"the advisory model declined to produce a report")
	}

	out, err := extractReport(msg)
	if err != nil {
		return ReportOutput{}, Usage{}, err
	}

	return out, usageFrom(string(msg.Model), hash, msg.Usage, latencyMS), nil
}

// Chat is the phase-2 conversational surface. It is declared on the Provider
// seam but not yet wired, so it returns errs.ErrNotImplemented.
func (p *anthropicProvider) Chat(context.Context, ChatInput) (ChatOutput, Usage, error) {
	return ChatOutput{}, Usage{}, errs.ErrNotImplemented
}
