package ai

import (
	"context"
	"testing"
)

// switchStub is a minimal TextProvider whose ExtractRecipeFromText returns its
// own name as the recipe title, so a test can tell which delegate handled a call.
type switchStub struct{ name string }

func (s switchStub) GenerateRecipe(context.Context, RecipeRequest) (*RecipeResult, error) {
	return &RecipeResult{Title: s.name}, nil
}
func (s switchStub) RegenerateRecipe(context.Context, RegenerateRequest) (*RecipeResult, error) {
	return &RecipeResult{Title: s.name}, nil
}
func (s switchStub) ForkRecipe(context.Context, ForkRequest) (*RecipeResult, error) {
	return &RecipeResult{Title: s.name}, nil
}
func (s switchStub) AnalyzeAllergens(context.Context, AllergenRequest) (*AllergenResult, error) {
	return &AllergenResult{}, nil
}
func (s switchStub) ClassifyVoiceIntent(context.Context, string) (*VoiceIntent, error) {
	return &VoiceIntent{}, nil
}
func (s switchStub) EstimatePortions(context.Context, interface{}) (*PortionEstimate, error) {
	return &PortionEstimate{}, nil
}
func (s switchStub) ExtractRecipeFromText(context.Context, string, string) (*RecipeResult, error) {
	return &RecipeResult{Title: s.name}, nil
}
func (s switchStub) CookingQA(context.Context, string, string) (string, error) {
	return s.name, nil
}
func (s switchStub) DietaryInterview(context.Context, []Message, string) (*DietaryInterviewResult, error) {
	return &DietaryInterviewResult{}, nil
}

func TestSwitchableTextProvider_Delegates(t *testing.T) {
	sw := NewSwitchableTextProvider(switchStub{name: "first"})

	got, err := sw.ExtractRecipeFromText(context.Background(), "x", "metric")
	if err != nil {
		t.Fatalf("ExtractRecipeFromText: %v", err)
	}
	if got.Title != "first" {
		t.Errorf("title = %q, want %q", got.Title, "first")
	}

	// Swap the underlying provider and confirm the next call routes to it.
	sw.Set(switchStub{name: "second"})
	got, err = sw.ExtractRecipeFromText(context.Background(), "x", "metric")
	if err != nil {
		t.Fatalf("ExtractRecipeFromText after Set: %v", err)
	}
	if got.Title != "second" {
		t.Errorf("after Set, title = %q, want %q", got.Title, "second")
	}
}

func TestSwitchableTextProvider_SetNilIgnored(t *testing.T) {
	sw := NewSwitchableTextProvider(switchStub{name: "keep"})
	sw.Set(nil) // must not blank the tier

	got, err := sw.CookingQA(context.Background(), "q", "")
	if err != nil {
		t.Fatalf("CookingQA: %v", err)
	}
	if got != "keep" {
		t.Errorf("after Set(nil), got %q, want %q", got, "keep")
	}
}
