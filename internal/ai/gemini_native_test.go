package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
)

// geminiRecipeArgs is the create_recipe function-call argument object the mock
// Gemini server returns. Field names mirror recipeToolResult exactly.
const geminiRecipeArgs = `{
	"title": "Skillet Cornbread",
	"ingredients": [
		{"name": "yellow cornmeal", "unit": "cup", "amount": 1, "original_text": "1 cup yellow cornmeal"}
	],
	"instructions": ["Mix the batter and bake at 425F for 20 minutes."],
	"cook_time": 20,
	"portions": 8,
	"portion_size": "1 wedge",
	"unit_system": "us_customary"
}`

// geminiFunctionCallResponse builds a native Gemini generateContent response
// body whose candidate emits a create_recipe function call carrying args, plus
// usage metadata for cost metering.
func geminiFunctionCallResponse(args string) string {
	return fmt.Sprintf(`{
		"candidates": [
			{"content": {"role": "model", "parts": [
				{"functionCall": {"name": "create_recipe", "args": %s}}
			]}}
		],
		"usageMetadata": {"promptTokenCount": 1200, "candidatesTokenCount": 240}
	}`, args)
}

func TestGeminiVideoProvider_ExtractRecipesFromVideo(t *testing.T) {
	var sawPath, sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":generateContent") {
			sawPath = true
		}
		if r.Header.Get("x-goog-api-key") != "" {
			sawHeader = true
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, geminiFunctionCallResponse(geminiRecipeArgs))
	}))
	defer srv.Close()

	p := NewGeminiVideoProvider("test-key", "gemini-2.5-flash", testPrompts())
	p.baseURL = srv.URL

	results, err := p.ExtractRecipesFromVideo(context.Background(), []byte("fake-video-bytes"), "video/mp4", "caption + transcript text", "us_customary", "")
	if err != nil {
		t.Fatalf("ExtractRecipesFromVideo returned error: %v", err)
	}
	if !sawPath {
		t.Error("request path did not end with :generateContent")
	}
	if !sawHeader {
		t.Error("x-goog-api-key header was not set on the request")
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}

	got := results[0]
	if got.Title != "Skillet Cornbread" {
		t.Errorf("title = %q, want %q", got.Title, "Skillet Cornbread")
	}
	if len(got.Ingredients) != 1 {
		t.Fatalf("got %d ingredients, want 1", len(got.Ingredients))
	}
	if got.Ingredients[0].Name != "yellow cornmeal" || got.Ingredients[0].Unit != "cup" || got.Ingredients[0].Amount != 1 {
		t.Errorf("ingredient[0] = %+v, want name=yellow cornmeal unit=cup amount=1", got.Ingredients[0])
	}
	if len(got.Instructions) != 1 {
		t.Errorf("got %d instructions, want 1", len(got.Instructions))
	}
	if got.Portions != 8 {
		t.Errorf("portions = %d, want 8", got.Portions)
	}
	// PromptVersion must be stamped from the prompt set (mirrors the other providers).
	if got.PromptVersion == "" || got.PromptVersion != config.PromptVersion(testPrompts()) {
		t.Errorf("PromptVersion = %q, want %q", got.PromptVersion, config.PromptVersion(testPrompts()))
	}
}

func TestGeminiVideoProvider_ErrVideoTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("network must not be hit when the video exceeds the inline size limit")
	}))
	defer srv.Close()

	p := NewGeminiVideoProvider("test-key", "gemini-2.5-flash", testPrompts())
	p.baseURL = srv.URL

	_, err := p.ExtractRecipesFromVideo(context.Background(), make([]byte, maxInlineVideoBytes+1), "video/mp4", "", "us_customary", "")
	if !errors.Is(err, ErrVideoTooLarge) {
		t.Fatalf("err = %v, want ErrVideoTooLarge", err)
	}
}

func TestGeminiVideoProvider_NoFunctionCallIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"I could not find a recipe."}]}}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":10}}`)
	}))
	defer srv.Close()

	p := NewGeminiVideoProvider("test-key", "gemini-2.5-flash", testPrompts())
	p.baseURL = srv.URL

	if _, err := p.ExtractRecipesFromVideo(context.Background(), []byte("fake"), "video/mp4", "", "metric", ""); err == nil {
		t.Error("expected an error when the response has no function call, got nil")
	}
}

func TestGeminiVideoProvider_RetriesOn500(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"temporary"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, geminiFunctionCallResponse(geminiRecipeArgs))
	}))
	defer srv.Close()

	p := NewGeminiVideoProvider("test-key", "gemini-2.5-flash", testPrompts())
	p.baseURL = srv.URL

	results, err := p.ExtractRecipesFromVideo(context.Background(), []byte("fake"), "video/mp4", "", "us_customary", "")
	if err != nil {
		t.Fatalf("ExtractRecipesFromVideo returned error after retry: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Skillet Cornbread" {
		t.Fatalf("unexpected results after retry: %+v", results)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("server was called %d times, want 2 (one 500, then one 200)", got)
	}
}
