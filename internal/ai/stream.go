package ai

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
