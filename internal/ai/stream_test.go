package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// stubStreamingProvider compile-checks that any TextProvider plus a
// StreamGenerateRecipe method satisfies StreamingTextProvider.
type stubStreamingProvider struct {
	TextProvider
}

func (s *stubStreamingProvider) StreamGenerateRecipe(ctx context.Context, req RecipeRequest, events chan<- StreamEvent) (*RecipeResult, error) {
	return nil, nil
}

var _ StreamingTextProvider = (*stubStreamingProvider)(nil)

func TestStreamEventTypes_WireValues(t *testing.T) {
	// These string values are the SSE wire contract with the Flutter client;
	// changing them silently breaks streaming on deployed apps.
	tests := []struct {
		event StreamEventType
		want  string
	}{
		{StreamEventStarted, "recipe.started"},
		{StreamEventGenerating, "recipe.generating"},
		{StreamEventProgress, "recipe.progress"},
		{StreamEventComplete, "recipe.complete"},
		{StreamEventError, "recipe.error"},
	}
	for _, tc := range tests {
		if string(tc.event) != tc.want {
			t.Errorf("event type = %q, want %q", tc.event, tc.want)
		}
	}
}

func TestStreamEvent_JSONOmitsEmptyFields(t *testing.T) {
	raw, err := json.Marshal(StreamEvent{Type: StreamEventGenerating})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if got != `{"type":"recipe.generating"}` {
		t.Errorf("minimal event JSON = %s, want only the type field", got)
	}

	raw, err = json.Marshal(StreamEvent{Type: StreamEventError, Error: "boom", ErrorKind: "transient"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = string(raw)
	for _, want := range []string{`"error":"boom"`, `"error_kind":"transient"`} {
		if !strings.Contains(got, want) {
			t.Errorf("error event JSON = %s, want %s", got, want)
		}
	}
}

func TestTrySendEvent_DeliversWhenChannelHasCapacity(t *testing.T) {
	events := make(chan StreamEvent, 1)
	ok := TrySendEvent(context.Background(), events, StreamEvent{Type: StreamEventProgress, TokensSoFar: 128})
	if !ok {
		t.Fatal("TrySendEvent returned false with channel capacity available")
	}

	got := <-events
	if got.Type != StreamEventProgress {
		t.Errorf("received type %q, want %q", got.Type, StreamEventProgress)
	}
	if got.TokensSoFar != 128 {
		t.Errorf("received TokensSoFar = %d, want 128", got.TokensSoFar)
	}
}

func TestTrySendEvent_ReturnsFalseWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Unbuffered channel with no reader: without the ctx.Done() branch this
	// would block forever.
	events := make(chan StreamEvent)
	ok := TrySendEvent(ctx, events, StreamEvent{Type: StreamEventProgress})
	if ok {
		t.Error("TrySendEvent returned true despite a cancelled context and no consumer")
	}
}

func TestTrySendEvent_DeliversToWaitingConsumer(t *testing.T) {
	events := make(chan StreamEvent)
	received := make(chan StreamEvent, 1)
	go func() {
		received <- <-events
	}()

	ok := TrySendEvent(context.Background(), events, StreamEvent{Type: StreamEventComplete, RecipeID: 7})
	if !ok {
		t.Fatal("TrySendEvent returned false with an active consumer")
	}
	got := <-received
	if got.Type != StreamEventComplete || got.RecipeID != 7 {
		t.Errorf("consumer received %+v, want complete event for recipe 7", got)
	}
}
