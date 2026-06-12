package testutil

import (
	"context"
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/ai"
)

// --- MockStreamingTextProvider ---

// MockStreamingTextProvider is a mock implementation of ai.StreamingTextProvider.
// It embeds MockTextProvider for the synchronous TextProvider surface and adds
// the streaming entry point. Use a plain MockTextProvider when a test needs a
// provider that does NOT support streaming (to exercise the sync fallback).
type MockStreamingTextProvider struct {
	MockTextProvider
	StreamGenerateRecipeFunc func(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error)
}

func (m *MockStreamingTextProvider) StreamGenerateRecipe(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error) {
	if m.StreamGenerateRecipeFunc != nil {
		return m.StreamGenerateRecipeFunc(ctx, req, events)
	}
	return nil, fmt.Errorf("StreamGenerateRecipe not configured")
}

// Compile-time interface check.
var _ ai.StreamingTextProvider = (*MockStreamingTextProvider)(nil)
