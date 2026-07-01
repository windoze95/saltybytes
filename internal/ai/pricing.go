package ai

import "strings"

// ModelPrice is the USD price per 1M tokens for a model.
type ModelPrice struct {
	InputPerM      float64
	OutputPerM     float64
	CacheInputPerM float64 // price for cached/read input tokens (usually ~0.1x input)
}

// PricingTable maps a model ID to its price. Used to meter the actual cost of a
// call; the dashboard computes counterfactual ("what model X would have cost")
// pricing itself from the stored token counts + its own editable rate card.
type PricingTable map[string]ModelPrice

// DefaultPricing seeds known model prices (USD per 1M tokens, list prices as of
// 2026). Keys are matched by prefix so dated model IDs (…-20251001) resolve.
var DefaultPricing = PricingTable{
	"claude-sonnet-4":       {InputPerM: 3.00, OutputPerM: 15.00, CacheInputPerM: 0.30},
	"claude-haiku-4":        {InputPerM: 1.00, OutputPerM: 5.00, CacheInputPerM: 0.10},
	"claude-opus-4":         {InputPerM: 5.00, OutputPerM: 25.00, CacheInputPerM: 0.50},
	"gpt-4o-mini":           {InputPerM: 0.15, OutputPerM: 0.60, CacheInputPerM: 0.075},
	"gpt-4.1-mini":          {InputPerM: 0.40, OutputPerM: 1.60, CacheInputPerM: 0.10},
	"gpt-4.1-nano":          {InputPerM: 0.10, OutputPerM: 0.40, CacheInputPerM: 0.025},
	"gemini-2.0-flash":      {InputPerM: 0.10, OutputPerM: 0.40, CacheInputPerM: 0.025},
	"gemini-2.0-flash-lite": {InputPerM: 0.075, OutputPerM: 0.30, CacheInputPerM: 0.0},
	"gemini-2.5-flash":      {InputPerM: 0.30, OutputPerM: 2.50, CacheInputPerM: 0.075},
	"gemini-3.5-flash":      {InputPerM: 1.50, OutputPerM: 9.00, CacheInputPerM: 0.375},
	"deepseek-chat":         {InputPerM: 0.27, OutputPerM: 1.10, CacheInputPerM: 0.07},
}

// Lookup resolves a model's published price by longest-prefix match, returning
// false when the model is unknown. Exported so the model registry can seed an
// option's list prices from the default table.
func (p PricingTable) Lookup(model string) (ModelPrice, bool) {
	return p.priceFor(model)
}

// priceFor resolves a model's price, matching by longest prefix so dated IDs
// (e.g. "claude-haiku-4-5-20251001") map to their family price. Returns false
// when unknown.
func (p PricingTable) priceFor(model string) (ModelPrice, bool) {
	if mp, ok := p[model]; ok {
		return mp, true
	}
	var best string
	for key := range p {
		if strings.HasPrefix(model, key) && len(key) > len(best) {
			best = key
		}
	}
	if best == "" {
		return ModelPrice{}, false
	}
	return p[best], true
}

// Cost returns the USD cost of a call's token usage on the given model. Unknown
// models cost 0 (still recorded — the token counts let the dashboard price it).
func (p PricingTable) Cost(model string, u TokenUsage) float64 {
	mp, ok := p.priceFor(model)
	if !ok {
		return 0
	}
	return float64(u.InputTokens)/1e6*mp.InputPerM +
		float64(u.OutputTokens)/1e6*mp.OutputPerM +
		float64(u.CacheInputTokens)/1e6*mp.CacheInputPerM
}
