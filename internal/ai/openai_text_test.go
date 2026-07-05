package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// testPrompts builds a minimal but renderable prompt set for the light-tier
// provider. The templates exercise the same {{.UnitSystem}} / {{.RecipeContext}}
// placeholders the real prompts use.
func testPrompts() *config.Prompts {
	p := &config.Prompts{}
	p.Recipe.Summarize.Recipe = "A short one-sentence summary of the recipe."
	p.Import.Text.SystemPrefix = "You extract structured recipes from free-form text."
	p.Import.Text.System = "Extract the recipe and call create_recipe. Use {{.UnitSystem}} units for any measurement you must infer; otherwise keep the source's units."
	p.Import.URL.SystemPrefix = "You extract structured recipes from web page text."
	p.Import.URL.System = "Extract the recipe and call create_recipe. Unit handling: {{.UnitSystem}}."
	p.CookingQA.SystemPrefix = "You are a helpful cooking assistant."
	p.CookingQA.System = "Answer the user's cooking question concisely. Recipe context: {{.RecipeContext}}"
	return p
}

// newMockOpenAIServer returns an httptest server that responds to every request
// with the JSON encoding of body. Point a provider's BaseURL at server.URL.
func newMockOpenAIServer(t *testing.T, body interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("failed to encode mock response: %v", err)
		}
	}))
}

func TestOpenAICompatProvider_ExtractRecipeFromText(t *testing.T) {
	recipeArgs := `{
		"title": "Test Pancakes",
		"ingredients": [
			{"name": "all-purpose flour", "unit": "cup", "amount": 2, "original_text": "2 cups all-purpose flour"},
			{"name": "milk", "unit": "cup", "amount": 1.5, "original_text": "1 1/2 cups milk"},
			{"name": "egg", "unit": "pieces", "amount": 2, "original_text": "2 eggs"}
		],
		"instructions": ["Mix the dry ingredients.", "Whisk in milk and eggs.", "Cook on a griddle."],
		"cook_time": 15,
		"portions": 4,
		"portion_size": "2 pancakes",
		"unit_system": "us_customary"
	}`

	srv := newMockOpenAIServer(t, structuredJSONResponse(recipeArgs))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gpt-4o-mini", "openai", testPrompts())

	result, err := p.ExtractRecipeFromText(context.Background(), "some recipe text", "us_customary")
	if err != nil {
		t.Fatalf("ExtractRecipeFromText returned error: %v", err)
	}
	if result.Title != "Test Pancakes" {
		t.Errorf("title = %q, want %q", result.Title, "Test Pancakes")
	}
	if len(result.Ingredients) != 3 {
		t.Fatalf("got %d ingredients, want 3", len(result.Ingredients))
	}
	if result.Ingredients[0].Name != "all-purpose flour" {
		t.Errorf("ingredient[0].Name = %q, want %q", result.Ingredients[0].Name, "all-purpose flour")
	}
	if result.Ingredients[0].Amount != 2 || result.Ingredients[0].Unit != "cup" {
		t.Errorf("ingredient[0] amount/unit = %v/%q, want 2/cup", result.Ingredients[0].Amount, result.Ingredients[0].Unit)
	}
	if len(result.Instructions) != 3 {
		t.Errorf("got %d instructions, want 3", len(result.Instructions))
	}
	if result.Portions != 4 {
		t.Errorf("portions = %d, want 4", result.Portions)
	}
	// PromptVersion must be stamped from the prompt set (mirrors Anthropic).
	if result.PromptVersion == "" || result.PromptVersion != config.PromptVersion(testPrompts()) {
		t.Errorf("PromptVersion = %q, want %q", result.PromptVersion, config.PromptVersion(testPrompts()))
	}
}

func TestOpenAICompatProvider_CookingQA(t *testing.T) {
	const answer = "Yes, you can substitute melted butter for the oil in equal amounts."
	canned := openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: answer,
			},
			FinishReason: openai.FinishReasonStop,
		}},
		Usage: openai.Usage{PromptTokens: 30, CompletionTokens: 18},
	}

	srv := newMockOpenAIServer(t, canned)
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gpt-4o-mini", "openai", testPrompts())

	got, err := p.CookingQA(context.Background(), "Can I use butter instead of oil?", "Pancake recipe")
	if err != nil {
		t.Fatalf("CookingQA returned error: %v", err)
	}
	if got != answer {
		t.Errorf("CookingQA = %q, want %q", got, answer)
	}
}

func TestOpenAICompatProvider_EstimatePortions(t *testing.T) {
	srv := newMockOpenAIServer(t, structuredJSONResponse(`{"portions": 6, "portion_size": "1 bowl", "confidence": 0.9}`))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gpt-4o-mini", "openai", testPrompts())

	est, err := p.EstimatePortions(context.Background(), map[string]interface{}{"title": "Soup"})
	if err != nil {
		t.Fatalf("EstimatePortions returned error: %v", err)
	}
	if est.Portions != 6 {
		t.Errorf("portions = %d, want 6", est.Portions)
	}
	if est.PortionSize != "1 bowl" {
		t.Errorf("portion size = %q, want %q", est.PortionSize, "1 bowl")
	}
	if est.Confidence != 0.9 {
		t.Errorf("confidence = %v, want 0.9", est.Confidence)
	}
}

func TestOpenAICompatProvider_NonJSONContentIsError(t *testing.T) {
	// A refusal / non-JSON body must surface a clear parse error, not a panic
	// or a silent zero-value recipe.
	srv := newMockOpenAIServer(t, structuredJSONResponse("I cannot help."))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gpt-4o-mini", "openai", testPrompts())

	if _, err := p.ExtractRecipeFromText(context.Background(), "text", "metric"); err == nil {
		t.Error("expected error for non-JSON structured response, got nil")
	}
}

func TestOpenAICompatProvider_EmptyContentIsError(t *testing.T) {
	// An empty body (e.g. the thinking budget consumed everything) must error
	// after the resample, not succeed silently.
	srv := newMockOpenAIServer(t, structuredJSONResponse(""))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gpt-4o-mini", "openai", testPrompts())

	if _, err := p.ExtractRecipeFromText(context.Background(), "text", "metric"); err == nil {
		t.Error("expected error for empty structured response, got nil")
	}
}

func TestStripCodeFences(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{"  {\"a\":1}  ", `{"a":1}`},
	}
	for _, c := range cases {
		if got := stripCodeFences(c.in); got != c.want {
			t.Errorf("stripCodeFences(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOpenAICompatProvider_ExtractRecipeFromText_Live hits a real
// OpenAI-compatible endpoint. Gated behind OPENAI_LIVE_TEST so it never runs
// in CI / the offline suite. Defaults to OpenAI gpt-4o-mini; set
// LIGHT_API_KEY/LIGHT_BASE_URL/LIGHT_MODEL to run it against the prod light
// tier (Gemini) — the config whose function-call parser motivated the
// json_schema output path this test now covers.
func TestOpenAICompatProvider_ExtractRecipeFromText_Live(t *testing.T) {
	if os.Getenv("OPENAI_LIVE_TEST") == "" {
		t.Skip("set OPENAI_LIVE_TEST=1 (and an API key) to run the live extraction test")
	}
	apiKey := firstNonEmptyEnv("LIGHT_API_KEY", "GEMINI_API_KEY", "OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("no API key (LIGHT_API_KEY / GEMINI_API_KEY / OPENAI_API_KEY) set; skipping live test")
	}
	model := os.Getenv("LIGHT_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}

	p := NewOpenAICompatProvider(apiKey, os.Getenv("LIGHT_BASE_URL"), model, "gemini", testPrompts())

	const text = `Classic Guacamole

Ingredients:
- 3 ripe avocados
- 1 lime, juiced
- 1/2 teaspoon salt
- 1/2 cup diced onion
- 2 tablespoons chopped fresh cilantro

Instructions:
1. Halve and pit the avocados, then scoop the flesh into a bowl and mash.
2. Mix in the lime juice and salt.
3. Stir in the onion and cilantro and serve.`

	result, err := p.ExtractRecipeFromText(context.Background(), text, "us_customary")
	if err != nil {
		t.Fatalf("live ExtractRecipeFromText failed: %v", err)
	}
	if strings.TrimSpace(result.Title) == "" {
		t.Error("expected a non-empty title from live extraction")
	}
	if len(result.Ingredients) < 3 {
		t.Errorf("expected >=3 ingredients from live extraction, got %d", len(result.Ingredients))
	}
}
