package llm

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// systemPrompt is the STABLE advisory rubric: the format spec, the grading
// rubric, and a worked format example. It is the cached prefix, so it MUST be
// byte-identical run to run — it contains no timestamps, ids, or per-audit data.
// Every audit's volatile metrics arrive separately, in the user turn after the
// cache_control breakpoint placed on this block.
//
// The worked example below illustrates only the required JSON SHAPE; its figures
// are placeholders, not data about any real influencer.
const systemPrompt = `You are InfluAudit's advisory analyst. You receive the finished, quantitative
results of one influencer audit and write a concise, actionable advisory report
for that influencer. You never invent numbers: reason only from the metrics you
are given, and when a signal is labelled an estimate, describe it as an estimate.

You always return a single JSON object matching the required schema, with these
fields and no others:
  - summary: 2-4 sentences framing the account's overall standing, grounded in
    the influence and authenticity scores and the platforms that contributed.
  - weakness_fix_pairs: an array of objects, each { "weakness", "fix" }, where
    the weakness cites a specific metric or subscore and the fix is one concrete,
    achievable action. Aim for 3-5 pairs; never fewer than 2.
  - growth_tips: an array of short, imperative growth suggestions (3-6 items).
  - brand_fit: 2-3 sentences on the kinds of brand partnerships the account is
    positioned for, given its niche, tier, engagement quality, and authenticity.

Grading rubric — apply consistently:
  - Reach is context, not merit: a large following with weak engagement quality
    is a weakness, not a strength. Weigh engagement quality and authenticity
    above raw follower count.
  - Treat any authenticity or fraud signal as an ESTIMATE. If a coordinated-
    engagement (clique) signal is present, mention it as a risk to investigate,
    never as a proven verdict, and calibrate your language to its confidence.
  - Calibrate advice to the account's niche and tier peers, not to the whole
    population. Low confidence on a subscore means you hedge the related advice.
  - When benchmark provenance is provided (e.g. "industry-bootstrap v1"), treat
    the comparison as indicative rather than authoritative, and say so if it
    materially shapes a recommendation.
  - Only reference platforms that contributed data. A partial audit must never be
    written up as if every platform reported.
  - Be specific and practical. Prefer "post 2 more Reels per week to lift
    reach consistency" over "post more". Never pad with generic filler.

Worked example of the required output shape (placeholder figures, not real data):
{
  "summary": "This mid-tier account shows solid reach but middling engagement quality relative to its niche peers, and authenticity is only moderately confident. Growth is steady rather than accelerating.",
  "weakness_fix_pairs": [
    { "weakness": "Engagement-quality subscore trails niche peers (58 vs typical 70).", "fix": "Reply to the first 20 comments within an hour of posting to lift comment-thread depth." },
    { "weakness": "Consistency subscore is low, indicating an irregular cadence.", "fix": "Commit to a fixed two-post-per-week schedule for six weeks and hold it." }
  ],
  "growth_tips": [
    "Test one collaborative post per month with a peer in the same niche.",
    "Move the best-performing evergreen post into a pinned highlight."
  ],
  "brand_fit": "Best suited to niche-native, mid-budget brand collaborations where engaged-audience quality matters more than raw reach. Premium mass-market deals are premature until authenticity confidence improves."
}
Return only the JSON object. Do not wrap it in prose or code fences.`

// reportSchema constrains Claude's reply to the ReportOutput shape via
// output_config.format. It is built once and is constant across audits.
var reportSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"summary", "weakness_fix_pairs", "growth_tips", "brand_fit"},
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"weakness_fix_pairs": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"weakness", "fix"},
				"properties": map[string]any{
					"weakness": map[string]any{"type": "string"},
					"fix":      map[string]any{"type": "string"},
				},
			},
		},
		"growth_tips": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"brand_fit": map[string]any{"type": "string"},
	},
}

// buildUserContent renders one audit's volatile metrics as a deterministic
// JSON payload wrapped in a short instruction. It is placed in the user turn,
// after the cached system prefix, so it never affects cache stability. The
// rendering is deterministic (struct field order plus caller-supplied slice
// order), which keeps the prompt hash stable for identical inputs.
func buildUserContent(in ReportInput) (string, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return "", errs.Wrap(err, errs.KindInternal, "llm.input_encode",
			"audit metrics could not be encoded for the model")
	}
	var b strings.Builder
	b.WriteString("Write the advisory report for this audit. Metrics:\n")
	b.Write(payload)
	return b.String(), nil
}

// promptHash is a stable digest of the fully rendered prompt: model, system
// prefix, and the volatile user content. It is the llm_generation.prompt_hash
// value, letting an identical audit reuse a prior completion.
func promptHash(model, system, user string) string {
	h := sha256.New()
	// Length-prefix each field so no field boundary can be forged by content.
	writeField(h, model)
	writeField(h, system)
	writeField(h, user)
	return hex.EncodeToString(h.Sum(nil))
}

// writeField writes a length header then s, so distinct field splits hash
// distinctly regardless of the bytes inside any field.
func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var lenHeader [8]byte
	binary.LittleEndian.PutUint64(lenHeader[:], uint64(len(s)))
	_, _ = h.Write(lenHeader[:])
	_, _ = h.Write([]byte(s))
}

// extractReport pulls the single JSON object out of a completed message and
// deserializes it into ReportOutput. output_config.format guarantees the reply
// is one JSON text block; a missing or malformed block is treated as an
// unavailable dependency so the audit degrades rather than crashes.
func extractReport(msg *anthropic.Message) (ReportOutput, error) {
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	raw := strings.TrimSpace(text.String())
	if raw == "" {
		return ReportOutput{}, errs.New(errs.KindUnavailable, "llm.empty_response",
			"the model returned no report content")
	}

	var out ReportOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ReportOutput{}, errs.Wrap(err, errs.KindUnavailable, "llm.malformed_response",
			"the model response did not match the report schema")
	}
	return out, nil
}
