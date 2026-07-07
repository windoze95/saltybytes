package ai

import (
	"context"
	"testing"
	"time"
)

// Metered calls made under a user-tagged context must attribute the spend
// to that user; untagged (system) work records user 0.
func TestCostMiddleware_AttributesUserFromContext(t *testing.T) {
	var got []UsageRecord
	mw := &CostMiddleware{Sink: func(rec UsageRecord) { got = append(got, rec) }}

	run := func(ctx context.Context) {
		_, _ = runWithMiddleware(ctx, mw, AIOperation{
			Name: "GenerateRecipe", Provider: "anthropic",
			Model: "claude-sonnet-4", StartTime: time.Now(),
		}, func(ctx context.Context) (string, error) {
			recordUsage(ctx, TokenUsage{InputTokens: 100, OutputTokens: 50})
			return "ok", nil
		})
	}

	run(WithUserID(context.Background(), 42))
	run(context.Background())

	if len(got) != 2 {
		t.Fatalf("recorded %d calls, want 2", len(got))
	}
	if got[0].UserID != 42 {
		t.Errorf("tagged call attributed to %d, want 42", got[0].UserID)
	}
	if got[1].UserID != 0 {
		t.Errorf("untagged call attributed to %d, want 0", got[1].UserID)
	}
}
