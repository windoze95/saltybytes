package ai

import (
	"context"
	"testing"
	"time"
)

func TestPricingTable_Cost(t *testing.T) {
	p := DefaultPricing
	oneM := TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	// A dated Haiku ID resolves to the claude-haiku-4 family ($1 in + $5 out).
	if got := p.Cost("claude-haiku-4-5-20251001", oneM); got != 6.00 {
		t.Errorf("haiku cost = %v, want 6.00", got)
	}
	// Exact match.
	if got := p.Cost("gpt-4o-mini", oneM); got != 0.75 {
		t.Errorf("gpt-4o-mini cost = %v, want 0.75", got)
	}
	// Unknown model → 0 (still recorded; tokens let the dashboard price it).
	if got := p.Cost("mystery-model", oneM); got != 0 {
		t.Errorf("unknown model cost = %v, want 0", got)
	}
}

func TestCostMiddleware_RecordsUsage(t *testing.T) {
	var got UsageRecord
	called := false
	mw := &CostMiddleware{
		Pricing: DefaultPricing,
		Sink:    func(r UsageRecord) { got = r; called = true },
	}
	mw.After(context.Background(), AIOperationResult{
		Operation: AIOperation{Name: "ExtractRecipeFromText", Provider: "anthropic", Model: "claude-haiku-4-5-20251001"},
		Duration:  100 * time.Millisecond,
		Usage:     TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
	})
	if !called {
		t.Fatal("sink was not called")
	}
	if got.Model != "claude-haiku-4-5-20251001" || got.Operation != "ExtractRecipeFromText" {
		t.Errorf("record = %+v", got)
	}
	if got.CostUSD != 6.00 {
		t.Errorf("cost = %v, want 6.00", got.CostUSD)
	}
	if got.DurationMS != 100 {
		t.Errorf("duration = %d ms, want 100", got.DurationMS)
	}
}

func TestCostMiddleware_SkipsZeroUsage(t *testing.T) {
	called := false
	mw := &CostMiddleware{Sink: func(UsageRecord) { called = true }}
	mw.After(context.Background(), AIOperationResult{
		Operation: AIOperation{Model: "claude-haiku-4-5"},
		Usage:     TokenUsage{}, // no tokens consumed (e.g. an early failure)
	})
	if called {
		t.Error("sink must not be called for a zero-usage call")
	}
}

type capturingMW struct{ captured AIOperationResult }

func (m *capturingMW) Before(ctx context.Context, op AIOperation) context.Context { return ctx }
func (m *capturingMW) After(ctx context.Context, r AIOperationResult)             { m.captured = r }

func TestRunWithMiddleware_FlowsUsageFromCall(t *testing.T) {
	mw := &capturingMW{}
	_, _ = runWithMiddleware(context.Background(), mw,
		AIOperation{Name: "X", StartTime: time.Now()},
		func(ctx context.Context) (string, error) {
			// A provider reports usage mid-call; it must reach After().
			recordUsage(ctx, TokenUsage{InputTokens: 42, OutputTokens: 7})
			return "ok", nil
		})
	if mw.captured.Usage.InputTokens != 42 || mw.captured.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v, want {42 7}", mw.captured.Usage)
	}
}
