package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
)

// geminiMultiRecipeArgs is the extract_recipes function-call argument object the
// mock Gemini server returns for the multi-media test: two distinct recipes.
// Field names mirror recipeToolResult / multiRecipeToolResult exactly.
const geminiMultiRecipeArgs = `{
	"recipes": [
		{
			"title": "Skillet Cornbread",
			"ingredients": [{"name": "yellow cornmeal", "unit": "cup", "amount": 1, "original_text": "1 cup yellow cornmeal"}],
			"instructions": ["Mix the batter and bake at 425F for 20 minutes."],
			"cook_time": 20,
			"portions": 8,
			"portion_size": "1 wedge",
			"unit_system": "us_customary"
		},
		{
			"title": "Honey Butter",
			"ingredients": [{"name": "butter", "unit": "tbsp", "amount": 4, "original_text": "4 tbsp butter"}],
			"instructions": ["Whip the butter with honey until light and fluffy."],
			"cook_time": 5,
			"portions": 8,
			"portion_size": "1 tbsp",
			"unit_system": "us_customary"
		}
	]
}`

// geminiExtractRecipesResponse builds a native Gemini generateContent response
// body whose candidate emits an extract_recipes function call carrying args.
func geminiExtractRecipesResponse(args string) string {
	return fmt.Sprintf(`{
		"candidates": [
			{"content": {"role": "model", "parts": [
				{"functionCall": {"name": "extract_recipes", "args": %s}}
			]}}
		],
		"usageMetadata": {"promptTokenCount": 2400, "candidatesTokenCount": 480}
	}`, args)
}

func TestGeminiVisionProvider_ExtractRecipeFromImage(t *testing.T) {
	var sawPath, sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":generateContent") {
			sawPath = true
		}
		if r.Header.Get("x-goog-api-key") != "" {
			sawHeader = true
		}
		w.Header().Set("Content-Type", "application/json")
		// Reuse the shared single-recipe create_recipe response fixture.
		fmt.Fprint(w, geminiFunctionCallResponse(geminiRecipeArgs))
	}))
	defer srv.Close()

	p := NewGeminiVisionProvider("test-key", "gemini-2.5-flash", testPrompts())
	p.baseURL = srv.URL

	got, err := p.ExtractRecipeFromImage(context.Background(), []byte("fake-image-bytes"), "us_customary", "")
	if err != nil {
		t.Fatalf("ExtractRecipeFromImage returned error: %v", err)
	}
	if !sawPath {
		t.Error("request path did not end with :generateContent")
	}
	if !sawHeader {
		t.Error("x-goog-api-key header was not set on the request")
	}

	if got.Title != "Skillet Cornbread" {
		t.Errorf("title = %q, want %q", got.Title, "Skillet Cornbread")
	}
	if len(got.Ingredients) != 1 || got.Ingredients[0].Name != "yellow cornmeal" || got.Ingredients[0].Unit != "cup" || got.Ingredients[0].Amount != 1 {
		t.Fatalf("ingredients = %+v, want one {yellow cornmeal, cup, 1}", got.Ingredients)
	}
	if len(got.Instructions) != 1 {
		t.Errorf("got %d instructions, want 1", len(got.Instructions))
	}
	if got.Portions != 8 {
		t.Errorf("portions = %d, want 8", got.Portions)
	}
	if got.PromptVersion == "" || got.PromptVersion != config.PromptVersion(testPrompts()) {
		t.Errorf("PromptVersion = %q, want %q", got.PromptVersion, config.PromptVersion(testPrompts()))
	}
}

func TestGeminiVisionProvider_ExtractRecipesFromMedia(t *testing.T) {
	var sawPath, sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":generateContent") {
			sawPath = true
		}
		if r.Header.Get("x-goog-api-key") != "" {
			sawHeader = true
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, geminiExtractRecipesResponse(geminiMultiRecipeArgs))
	}))
	defer srv.Close()

	p := NewGeminiVisionProvider("test-key", "gemini-2.5-flash", testPrompts())
	p.baseURL = srv.URL

	media := []MediaInput{
		{Data: []byte("fake-image-bytes"), Kind: MediaImage},
		{Data: []byte("%PDF-1.4 fake"), Kind: MediaPDF},
	}
	results, err := p.ExtractRecipesFromMedia(context.Background(), media, "caption + transcript text", "us_customary", "")
	if err != nil {
		t.Fatalf("ExtractRecipesFromMedia returned error: %v", err)
	}
	if !sawPath {
		t.Error("request path did not end with :generateContent")
	}
	if !sawHeader {
		t.Error("x-goog-api-key header was not set on the request")
	}

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Skillet Cornbread" || results[1].Title != "Honey Butter" {
		t.Errorf("titles = %q, %q; want Skillet Cornbread, Honey Butter", results[0].Title, results[1].Title)
	}
	if len(results[1].Ingredients) != 1 || results[1].Ingredients[0].Name != "butter" {
		t.Errorf("results[1] ingredients = %+v, want one butter", results[1].Ingredients)
	}

	pv := config.PromptVersion(testPrompts())
	for i, r := range results {
		if r.PromptVersion == "" || r.PromptVersion != pv {
			t.Errorf("results[%d].PromptVersion = %q, want %q", i, r.PromptVersion, pv)
		}
	}
}

func TestGeminiVisionProvider_NoFunctionCallIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"I could not find a recipe."}]}}],"usageMetadata":{"promptTokenCount":40,"candidatesTokenCount":8}}`)
	}))
	defer srv.Close()

	p := NewGeminiVisionProvider("test-key", "gemini-2.5-flash", testPrompts())
	p.baseURL = srv.URL

	if _, err := p.ExtractRecipeFromImage(context.Background(), []byte("fake"), "metric", ""); err == nil {
		t.Error("expected an error when the response has no function call, got nil")
	}
}
