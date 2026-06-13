package ai

import "context"

// StreamEventType enumerates the SSE event types for streaming recipe generation.
type StreamEventType string

const (
	// StreamEventStarted indicates the recipe record has been created.
	StreamEventStarted StreamEventType = "recipe.started"
	// StreamEventGenerating indicates Claude is actively generating.
	StreamEventGenerating StreamEventType = "recipe.generating"
	// StreamEventProgress provides incremental token count updates.
	StreamEventProgress StreamEventType = "recipe.progress"
	// StreamEventComplete carries the finished recipe result.
	StreamEventComplete StreamEventType = "recipe.complete"
	// StreamEventError carries a classified error.
	StreamEventError StreamEventType = "recipe.error"
)

// StreamEvent is a single event emitted during streaming recipe generation.
type StreamEvent struct {
	Type        StreamEventType `json:"type"`
	RecipeID    uint            `json:"recipe_id,omitempty"`
	TokensSoFar int64           `json:"tokens_so_far,omitempty"`
	Result      *RecipeResult   `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	ErrorKind   string          `json:"error_kind,omitempty"`
}

// StreamingTextProvider is implemented by text providers that support
// streaming recipe generation with incremental progress events. Providers
// that do not implement it fall back to synchronous generation.
type StreamingTextProvider interface {
	TextProvider
	StreamGenerateRecipe(ctx context.Context, req RecipeRequest, events chan<- StreamEvent) (*RecipeResult, error)
}

// Compile-time check that the Anthropic provider supports streaming.
var _ StreamingTextProvider = (*AnthropicProvider)(nil)

// TrySendEvent sends an event to the channel, returning false if the context
// is cancelled (e.g. client disconnected). This prevents the producer goroutine
// from blocking forever on a full or unconsumed channel.
func TrySendEvent(ctx context.Context, events chan<- StreamEvent, event StreamEvent) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}
