package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mwCtxKey string

// recordingMiddleware appends to a shared call log and stamps the context in
// Before so call ordering and context threading can be asserted.
type recordingMiddleware struct {
	name  string
	calls *[]string

	gotResult *AIOperationResult
}

func (m *recordingMiddleware) Before(ctx context.Context, op AIOperation) context.Context {
	*m.calls = append(*m.calls, m.name+":before")
	return context.WithValue(ctx, mwCtxKey(m.name), op.Name)
}

func (m *recordingMiddleware) After(ctx context.Context, result AIOperationResult) {
	*m.calls = append(*m.calls, m.name+":after")
	r := result
	m.gotResult = &r
}

func TestMiddlewareChain_BeforeInOrderAfterReversed(t *testing.T) {
	var calls []string
	first := &recordingMiddleware{name: "first", calls: &calls}
	second := &recordingMiddleware{name: "second", calls: &calls}
	chain := NewMiddlewareChain(first, second)

	ctx := chain.Before(context.Background(), AIOperation{Name: "Op"})
	chain.After(ctx, AIOperationResult{Operation: AIOperation{Name: "Op"}})

	want := []string{"first:before", "second:before", "second:after", "first:after"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestMiddlewareChain_BeforeThreadsContext(t *testing.T) {
	var calls []string
	first := &recordingMiddleware{name: "first", calls: &calls}
	second := &recordingMiddleware{name: "second", calls: &calls}
	chain := NewMiddlewareChain(first, second)

	ctx := chain.Before(context.Background(), AIOperation{Name: "GenerateRecipe"})

	// Both middlewares must have contributed to the final context.
	if got := ctx.Value(mwCtxKey("first")); got != "GenerateRecipe" {
		t.Errorf("first middleware context value = %v, want 'GenerateRecipe'", got)
	}
	if got := ctx.Value(mwCtxKey("second")); got != "GenerateRecipe" {
		t.Errorf("second middleware context value = %v, want 'GenerateRecipe'", got)
	}
}

func TestRunWithMiddleware_NilMiddlewareRunsOperation(t *testing.T) {
	got, err := runWithMiddleware(context.Background(), nil, AIOperation{Name: "Op"}, func(ctx context.Context) (string, error) {
		return "result", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "result" {
		t.Errorf("result = %q, want 'result'", got)
	}
}

func TestRunWithMiddleware_PassesBeforeContextToOperation(t *testing.T) {
	var calls []string
	mw := &recordingMiddleware{name: "obs", calls: &calls}
	op := AIOperation{Name: "ClassifyVoiceIntent", StartTime: time.Now()}

	var seen interface{}
	_, err := runWithMiddleware(context.Background(), mw, op, func(ctx context.Context) (int, error) {
		seen = ctx.Value(mwCtxKey("obs"))
		return 1, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != "ClassifyVoiceIntent" {
		t.Errorf("operation saw context value %v, want 'ClassifyVoiceIntent'", seen)
	}

	want := []string{"obs:before", "obs:after"}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestRunWithMiddleware_ReportsErrorAndOperationInAfter(t *testing.T) {
	var calls []string
	mw := &recordingMiddleware{name: "obs", calls: &calls}
	op := AIOperation{Name: "GenerateRecipe", Provider: "anthropic", Model: "test-model", StartTime: time.Now()}
	opErr := errors.New("generation failed")

	got, err := runWithMiddleware(context.Background(), mw, op, func(ctx context.Context) (*RecipeResult, error) {
		return nil, opErr
	})
	if !errors.Is(err, opErr) {
		t.Fatalf("error = %v, want the operation error", err)
	}
	if got != nil {
		t.Errorf("result = %v, want nil on error", got)
	}

	if mw.gotResult == nil {
		t.Fatal("After was not called")
	}
	if !errors.Is(mw.gotResult.Err, opErr) {
		t.Errorf("After received Err = %v, want the operation error", mw.gotResult.Err)
	}
	if mw.gotResult.Operation.Name != "GenerateRecipe" {
		t.Errorf("After received operation %q, want 'GenerateRecipe'", mw.gotResult.Operation.Name)
	}
	if mw.gotResult.Operation.Provider != "anthropic" {
		t.Errorf("After received provider %q, want 'anthropic'", mw.gotResult.Operation.Provider)
	}
	if mw.gotResult.Duration < 0 {
		t.Errorf("After received negative duration %v", mw.gotResult.Duration)
	}
}

func TestLoggingMiddleware_BeforeAndAfterDoNotPanic(t *testing.T) {
	mw := &LoggingMiddleware{}
	op := AIOperation{Name: "Op", Provider: "anthropic", Model: "m", StartTime: time.Now()}

	ctx := mw.Before(context.Background(), op)
	if ctx == nil {
		t.Fatal("Before returned a nil context")
	}

	mw.After(ctx, AIOperationResult{Operation: op, Duration: time.Millisecond})
	mw.After(ctx, AIOperationResult{Operation: op, Duration: time.Millisecond, Err: errors.New("boom")})
}
