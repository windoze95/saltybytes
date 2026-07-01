package ai

import (
	"context"
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// toolCallResponse builds a canned chat-completion response carrying a single
// forced tool call (fnName + JSON args). Mirrors the shape the real providers
// return so the offline tests exercise the same parse path.
func toolCallResponse(fnName, args string) openai.ChatCompletionResponse {
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ToolCall{{
					ID:       "call_1",
					Type:     openai.ToolTypeFunction,
					Function: openai.FunctionCall{Name: fnName, Arguments: args},
				}},
			},
			FinishReason: openai.FinishReasonToolCalls,
		}},
		Usage: openai.Usage{PromptTokens: 100, CompletionTokens: 60},
	}
}

func TestOpenAICompatProvider_GenerateRecipe(t *testing.T) {
	// Field names match recipeToolResult's json tags.
	recipeArgs := `{
		"title": "Weeknight Beef Stew",
		"ingredients": [
			{"name": "beef chuck", "unit": "lb", "amount": 2, "original_text": "2 lb beef chuck"},
			{"name": "carrot", "unit": "pieces", "amount": 3, "original_text": "3 carrots"}
		],
		"instructions": ["Brown the beef.", "Add vegetables and simmer for two hours."],
		"cook_time": 130,
		"recipe_summary": "A hearty braised beef stew.",
		"portions": 4,
		"portion_size": "1 bowl",
		"unit_system": "us_customary"
	}`

	srv := newMockOpenAIServer(t, toolCallResponse("create_recipe", recipeArgs))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gemini-2.5-pro", "gemini", testPrompts())

	result, err := p.GenerateRecipe(context.Background(), RecipeRequest{
		UserPrompt: "a hearty beef stew",
		UnitSystem: "us_customary",
	})
	if err != nil {
		t.Fatalf("GenerateRecipe returned error: %v", err)
	}
	if result.Title != "Weeknight Beef Stew" {
		t.Errorf("title = %q, want %q", result.Title, "Weeknight Beef Stew")
	}
	if len(result.Ingredients) != 2 {
		t.Fatalf("got %d ingredients, want 2", len(result.Ingredients))
	}
	if len(result.Instructions) != 2 {
		t.Errorf("got %d instructions, want 2", len(result.Instructions))
	}
	// PromptVersion must be stamped from the prompt set (mirrors Anthropic).
	if want := config.PromptVersion(testPrompts()); result.PromptVersion == "" || result.PromptVersion != want {
		t.Errorf("PromptVersion = %q, want %q", result.PromptVersion, want)
	}
}

func TestOpenAICompatProvider_GenerateRecipe_MissingTitleIsError(t *testing.T) {
	// A recipe with no title must fail validation, not silently succeed.
	badArgs := `{
		"ingredients": [{"name": "flour", "unit": "cup", "amount": 1}],
		"instructions": ["Mix."]
	}`
	srv := newMockOpenAIServer(t, toolCallResponse("create_recipe", badArgs))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gemini-2.5-pro", "gemini", testPrompts())

	if _, err := p.GenerateRecipe(context.Background(), RecipeRequest{UserPrompt: "x"}); err == nil {
		t.Error("expected validation error for recipe missing title, got nil")
	}
}

func TestOpenAICompatProvider_AnalyzeAllergens(t *testing.T) {
	// Field names match allergenToolResult / ingredientAnalysisToolRes json tags.
	allergenArgs := `{
		"ingredient_analyses": [
			{
				"ingredient_name": "peanut butter",
				"common_allergens": ["peanuts"],
				"possible_allergens": ["tree nuts"],
				"sub_ingredients": ["peanuts", "salt"],
				"seed_oil_risk": false,
				"confidence": 0.98
			}
		],
		"confidence": 0.95,
		"requires_review": false
	}`

	srv := newMockOpenAIServer(t, toolCallResponse("analyze_allergens", allergenArgs))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gemini-2.5-pro", "gemini", testPrompts())

	result, err := p.AnalyzeAllergens(context.Background(), AllergenRequest{
		Ingredients: []IngredientInput{{Name: "peanut butter", Unit: "tbsp", Amount: 2}},
		IsPremium:   true,
	})
	if err != nil {
		t.Fatalf("AnalyzeAllergens returned error: %v", err)
	}
	if len(result.IngredientAnalyses) != 1 {
		t.Fatalf("got %d analyses, want 1", len(result.IngredientAnalyses))
	}
	a := result.IngredientAnalyses[0]
	if a.IngredientName != "peanut butter" {
		t.Errorf("ingredient name = %q, want %q", a.IngredientName, "peanut butter")
	}
	if len(a.CommonAllergens) != 1 || a.CommonAllergens[0] != "peanuts" {
		t.Errorf("common allergens = %v, want [peanuts]", a.CommonAllergens)
	}
	if result.Confidence != 0.95 {
		t.Errorf("confidence = %v, want 0.95", result.Confidence)
	}
	if result.RequiresReview {
		t.Error("requires_review = true, want false")
	}
}

func TestOpenAICompatProvider_ClassifyVoiceIntent(t *testing.T) {
	// Field names match voiceIntentToolResult json tags.
	voiceArgs := `{"type": "scroll_down", "amount": "large", "target": "", "text": ""}`

	srv := newMockOpenAIServer(t, toolCallResponse("classify_voice_intent", voiceArgs))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gemini-2.5-pro", "gemini", testPrompts())

	intent, err := p.ClassifyVoiceIntent(context.Background(), "scroll down a lot")
	if err != nil {
		t.Fatalf("ClassifyVoiceIntent returned error: %v", err)
	}
	if intent.Type != "scroll_down" {
		t.Errorf("type = %q, want %q", intent.Type, "scroll_down")
	}
	if intent.Amount != "large" {
		t.Errorf("amount = %q, want %q", intent.Amount, "large")
	}
}

// DietaryInterview: a plain-text turn (no tool call) is the model asking its
// next question. Complete must be false, Profile nil, Response set.
func TestOpenAICompatProvider_DietaryInterview_TextTurn(t *testing.T) {
	const question = "Do you have any food allergies I should know about?"
	canned := openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: question,
			},
			FinishReason: openai.FinishReasonStop,
		}},
		Usage: openai.Usage{PromptTokens: 50, CompletionTokens: 15},
	}

	srv := newMockOpenAIServer(t, canned)
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gemini-2.5-pro", "gemini", testPrompts())

	result, err := p.DietaryInterview(context.Background(), []Message{
		{Role: "assistant", Content: "Hi! Let's set up your dietary profile."},
		{Role: "user", Content: "Sounds good."},
	}, "Alex")
	if err != nil {
		t.Fatalf("DietaryInterview returned error: %v", err)
	}
	if result.Complete {
		t.Error("Complete = true, want false for a plain-text interview turn")
	}
	if result.Profile != nil {
		t.Error("Profile should be nil for a plain-text interview turn")
	}
	if result.Response != question {
		t.Errorf("Response = %q, want %q", result.Response, question)
	}
}

// DietaryInterview: when the model calls save_dietary_profile the interview is
// complete, Profile is populated and Response carries the wrap-up text.
func TestOpenAICompatProvider_DietaryInterview_SaveProfile(t *testing.T) {
	// Field names match dietaryProfileToolResult / dietaryAllergyToolRes json tags.
	profileArgs := `{
		"allergies": [
			{"name": "peanuts", "severity": "severe", "sub_forms": ["raw"], "notes": "carries an epipen"}
		],
		"intolerances": ["lactose"],
		"restrictions": ["vegetarian"],
		"preferences": ["loves spicy food"],
		"medical_notes": "low sodium for hypertension"
	}`
	const wrapUp = "Great, I've saved your dietary profile!"

	canned := openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: wrapUp,
				ToolCalls: []openai.ToolCall{{
					ID:       "call_1",
					Type:     openai.ToolTypeFunction,
					Function: openai.FunctionCall{Name: "save_dietary_profile", Arguments: profileArgs},
				}},
			},
			FinishReason: openai.FinishReasonToolCalls,
		}},
		Usage: openai.Usage{PromptTokens: 200, CompletionTokens: 80},
	}

	srv := newMockOpenAIServer(t, canned)
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gemini-2.5-pro", "gemini", testPrompts())

	result, err := p.DietaryInterview(context.Background(), []Message{
		{Role: "user", Content: "I'm allergic to peanuts and I'm vegetarian."},
	}, "Alex")
	if err != nil {
		t.Fatalf("DietaryInterview returned error: %v", err)
	}
	if !result.Complete {
		t.Error("Complete = false, want true when save_dietary_profile is called")
	}
	if result.Profile == nil {
		t.Fatal("Profile is nil, want non-nil when save_dietary_profile is called")
	}
	if result.Response != wrapUp {
		t.Errorf("Response = %q, want %q", result.Response, wrapUp)
	}
	if len(result.Profile.Allergies) != 1 || result.Profile.Allergies[0].Name != "peanuts" {
		t.Errorf("allergies = %+v, want one entry named peanuts", result.Profile.Allergies)
	}
	if result.Profile.Allergies[0].Severity != "severe" {
		t.Errorf("severity = %q, want %q", result.Profile.Allergies[0].Severity, "severe")
	}
	if len(result.Profile.Restrictions) != 1 || result.Profile.Restrictions[0] != "vegetarian" {
		t.Errorf("restrictions = %v, want [vegetarian]", result.Profile.Restrictions)
	}
}

// DietaryInterview: when the model calls save_dietary_profile with no wrap-up
// text, the fallback wrap-up message is substituted.
func TestOpenAICompatProvider_DietaryInterview_SaveProfileNoText(t *testing.T) {
	profileArgs := `{"allergies": [], "intolerances": [], "restrictions": [], "preferences": [], "medical_notes": ""}`

	srv := newMockOpenAIServer(t, toolCallResponse("save_dietary_profile", profileArgs))
	defer srv.Close()

	p := NewOpenAICompatProvider("test-key", srv.URL, "gemini-2.5-pro", "gemini", testPrompts())

	result, err := p.DietaryInterview(context.Background(), []Message{{Role: "user", Content: "No restrictions."}}, "Alex")
	if err != nil {
		t.Fatalf("DietaryInterview returned error: %v", err)
	}
	if !result.Complete || result.Profile == nil {
		t.Fatalf("want Complete + Profile, got Complete=%v Profile=%v", result.Complete, result.Profile)
	}
	if result.Response != dietaryWrapUpFallback {
		t.Errorf("Response = %q, want fallback %q", result.Response, dietaryWrapUpFallback)
	}
}
