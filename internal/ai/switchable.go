package ai

import (
	"context"
	"sync"
)

// SwitchableTextProvider is a TextProvider whose underlying implementation can
// be swapped at runtime, atomically and concurrency-safely. It backs the live
// model switch: the model manager builds a new light-tier provider (after a
// green validation probe) and calls Set to redirect all in-flight and future
// calls to it, with no restart. Every method simply delegates to the current
// provider under a read lock.
type SwitchableTextProvider struct {
	mu      sync.RWMutex
	current TextProvider
}

// Compile-time assurance it satisfies the full TextProvider interface.
var _ TextProvider = (*SwitchableTextProvider)(nil)

// NewSwitchableTextProvider wraps an initial provider.
func NewSwitchableTextProvider(initial TextProvider) *SwitchableTextProvider {
	return &SwitchableTextProvider{current: initial}
}

// Set atomically swaps the active provider. A nil provider is ignored so a
// caller can never accidentally blank the tier.
func (s *SwitchableTextProvider) Set(p TextProvider) {
	if p == nil {
		return
	}
	s.mu.Lock()
	s.current = p
	s.mu.Unlock()
}

func (s *SwitchableTextProvider) get() TextProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *SwitchableTextProvider) GenerateRecipe(ctx context.Context, req RecipeRequest) (*RecipeResult, error) {
	return s.get().GenerateRecipe(ctx, req)
}

func (s *SwitchableTextProvider) RegenerateRecipe(ctx context.Context, req RegenerateRequest) (*RecipeResult, error) {
	return s.get().RegenerateRecipe(ctx, req)
}

func (s *SwitchableTextProvider) ForkRecipe(ctx context.Context, req ForkRequest) (*RecipeResult, error) {
	return s.get().ForkRecipe(ctx, req)
}

func (s *SwitchableTextProvider) AnalyzeAllergens(ctx context.Context, req AllergenRequest) (*AllergenResult, error) {
	return s.get().AnalyzeAllergens(ctx, req)
}

func (s *SwitchableTextProvider) ClassifyVoiceIntent(ctx context.Context, transcript string) (*VoiceIntent, error) {
	return s.get().ClassifyVoiceIntent(ctx, transcript)
}

func (s *SwitchableTextProvider) EstimatePortions(ctx context.Context, recipeDef interface{}) (*PortionEstimate, error) {
	return s.get().EstimatePortions(ctx, recipeDef)
}

func (s *SwitchableTextProvider) ExtractRecipeFromText(ctx context.Context, text string, unitSystem string) (*RecipeResult, error) {
	return s.get().ExtractRecipeFromText(ctx, text, unitSystem)
}

func (s *SwitchableTextProvider) CookingQA(ctx context.Context, question string, recipeContext string) (string, error) {
	return s.get().CookingQA(ctx, question, recipeContext)
}

func (s *SwitchableTextProvider) DietaryInterview(ctx context.Context, messages []Message, memberName string) (*DietaryInterviewResult, error) {
	return s.get().DietaryInterview(ctx, messages, memberName)
}

func (s *SwitchableTextProvider) ExpandAndRankRecipes(ctx context.Context, req FinderRankRequest) (*FinderRankResult, error) {
	return s.get().ExpandAndRankRecipes(ctx, req)
}
