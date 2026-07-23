package llm

import (
	"math"

	"github.com/anthropics/anthropic-sdk-go"
)

// Per-million-token list prices for claude-opus-4-8, in USD. A cost in
// micro-dollars equals tokens*usdPerMTok, since dollars = tokens/1e6*usdPerMTok
// and micros = dollars*1e6.
const (
	inputUSDPerMTok  = 5.0
	outputUSDPerMTok = 25.0

	// Cache reads are billed at ~0.1x the base input rate; cache writes at
	// ~1.25x. These match Anthropic's ephemeral (5-minute) cache pricing.
	cacheReadMultiplier  = 0.1
	cacheWriteMultiplier = 1.25
)

// usageFrom converts an Anthropic usage report into the module's Usage record,
// computing cost_micros from the per-token price and flagging a cache hit.
//
// InputTokens is reported as the full prompt size: regular input plus tokens
// served from cache plus tokens written to cache. Cached reflects whether any
// of the prompt (the ~3k-token rubric prefix) was served from cache.
func usageFrom(model, promptHash string, u anthropic.Usage, latencyMS int) Usage {
	regular := float64(u.InputTokens)
	cacheRead := float64(u.CacheReadInputTokens)
	cacheWrite := float64(u.CacheCreationInputTokens)

	costMicros := regular*inputUSDPerMTok +
		cacheRead*inputUSDPerMTok*cacheReadMultiplier +
		cacheWrite*inputUSDPerMTok*cacheWriteMultiplier +
		float64(u.OutputTokens)*outputUSDPerMTok

	return Usage{
		Model:        model,
		PromptHash:   promptHash,
		InputTokens:  int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens),
		OutputTokens: int(u.OutputTokens),
		CostMicros:   int64(math.Round(costMicros)),
		Cached:       u.CacheReadInputTokens > 0,
		LatencyMS:    latencyMS,
	}
}
