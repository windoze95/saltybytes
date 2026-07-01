package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// GeminiVisionProvider extracts structured recipes from images and PDF documents
// using Google's native Gemini generateContent API with forced function calling.
// It deliberately reuses the shared recipe schema (recipeProperties), parsing
// (recipeToolResult / multiRecipeToolResult / toolResultToRecipeResult) and
// validation (validateRecipeResult) helpers so its output is byte-for-byte
// faithful to the Anthropic vision provider.
type GeminiVisionProvider struct {
	apiKey     string
	model      string
	baseURL    string
	prompts    *config.Prompts
	middleware AIMiddleware // nil means no middleware
	http       *http.Client
}

// Compile-time assurance the provider satisfies the VisionProvider interface.
var _ VisionProvider = (*GeminiVisionProvider)(nil)

// NewGeminiVisionProvider creates a native Gemini vision-extraction provider. An
// empty model defaults to gemini-2.5-flash.
func NewGeminiVisionProvider(apiKey, model string, prompts *config.Prompts) *GeminiVisionProvider {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &GeminiVisionProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://generativelanguage.googleapis.com/v1beta",
		prompts: prompts,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// WithMiddleware sets the middleware chain for this provider.
func (p *GeminiVisionProvider) WithMiddleware(mw AIMiddleware) {
	p.middleware = mw
}

// httpClient returns the injected client or a default one.
func (p *GeminiVisionProvider) httpClient() *http.Client {
	if p.http != nil {
		return p.http
	}
	return http.DefaultClient
}

// visionSystemPrompt renders the shared vision system prompt (prefix + dynamic
// suffix) exactly as the Anthropic provider does.
func (p *GeminiVisionProvider) visionSystemPrompt(unitSystem, requirements string) (string, error) {
	sysSuffix, err := config.RenderPrompt(p.prompts.Recipe.Import.Vision.System, map[string]interface{}{
		"UnitSystem":   unitSystem,
		"Requirements": requirements,
	})
	if err != nil {
		return "", fmt.Errorf("render system prompt: %w", err)
	}
	return combineSystemPrompt(p.prompts.Recipe.Import.Vision.SystemPrefix, sysSuffix), nil
}

// ExtractRecipeFromImage extracts a single structured recipe from a photo via a
// forced create_recipe function call. Mirrors AnthropicProvider.ExtractRecipeFromImage.
func (p *GeminiVisionProvider) ExtractRecipeFromImage(ctx context.Context, imageData []byte, unitSystem string, requirements string) (*RecipeResult, error) {
	op := AIOperation{
		Name:      "ExtractRecipeFromImage",
		Provider:  "gemini",
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*RecipeResult, error) {
		systemPrompt, err := p.visionSystemPrompt(unitSystem, requirements)
		if err != nil {
			return nil, err
		}

		reqBody := geminiGenerateContentRequest{
			Contents: []geminiContent{{
				Role: "user",
				Parts: []geminiPart{
					{InlineData: &geminiInlineData{MimeType: detectImageMediaType(imageData), Data: base64.StdEncoding.EncodeToString(imageData)}},
					{Text: "Extract the recipe from this image. If the image shows a prepared dish, infer a reasonable recipe for it."},
				},
			}},
			Tools: []geminiTool{{
				FunctionDeclarations: []geminiFunctionDeclaration{{
					Name:        "create_recipe",
					Description: "Create a structured recipe definition with all required fields.",
					Parameters:  schemaObject(recipeProperties(p.prompts.Recipe.Summarize.Recipe)),
				}},
			}},
			ToolConfig: &geminiToolConfig{
				FunctionCallingConfig: geminiFunctionCallingConfig{
					Mode:                 "ANY",
					AllowedFunctionNames: []string{"create_recipe"},
				},
			},
		}
		if systemPrompt != "" {
			reqBody.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: systemPrompt}}}
		}

		resp, err := geminiGenerateContent(ctx, p.httpClient(), p.baseURL, p.apiKey, p.model, reqBody)
		if err != nil {
			return nil, err
		}

		recordUsage(ctx, TokenUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		})

		args, ok := firstFunctionCallArgs(resp, "create_recipe")
		if !ok {
			return nil, NewAIError(FailureContentEmpty, errors.New("no create_recipe function call in Gemini response"), "no recipe function call in response")
		}

		var tr recipeToolResult
		if err := json.Unmarshal(args, &tr); err != nil {
			return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal recipe: %w", err), "failed to parse recipe tool result")
		}

		result := toolResultToRecipeResult(&tr)
		if err := validateRecipeResult(result); err != nil {
			return nil, err
		}
		result.PromptVersion = config.PromptVersion(p.prompts)
		return result, nil
	})
}

// ExtractRecipesFromMedia extracts every distinct recipe found across the
// provided images and/or PDF documents in a single native Gemini request.
// Mirrors AnthropicProvider.ExtractRecipesFromMedia.
func (p *GeminiVisionProvider) ExtractRecipesFromMedia(ctx context.Context, media []MediaInput, contextText string, unitSystem string, requirements string) ([]*RecipeResult, error) {
	op := AIOperation{
		Name:      "ExtractRecipesFromMedia",
		Provider:  "gemini",
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) ([]*RecipeResult, error) {
		systemPrompt, err := p.visionSystemPrompt(unitSystem, requirements)
		if err != nil {
			return nil, err
		}

		parts := make([]geminiPart, 0, len(media)+2)
		for _, m := range media {
			mimeType := detectImageMediaType(m.Data)
			if m.Kind == MediaPDF {
				mimeType = "application/pdf"
			}
			parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(m.Data)}})
		}
		if contextText != "" {
			parts = append(parts, geminiPart{Text: "The following is the spoken transcript and caption from the source video. Treat it as a primary source of the ingredient quantities and step order; the images above are stills sampled from the same video and often show on-screen text, ingredients, and amounts not spoken aloud. Reconcile the two.\n\n" + contextText})
		}
		parts = append(parts, geminiPart{Text: "These images and/or documents contain one or more recipes. Extract EVERY distinct recipe you find across all of them, returning one entry per recipe. If an item shows a prepared dish with no written recipe, infer a reasonable recipe for it."})

		reqBody := geminiGenerateContentRequest{
			Contents: []geminiContent{{
				Role:  "user",
				Parts: parts,
			}},
			Tools: []geminiTool{{
				FunctionDeclarations: []geminiFunctionDeclaration{{
					Name:        "extract_recipes",
					Description: "Extract every distinct recipe found across the provided images and documents. Return one entry per recipe.",
					Parameters:  multiRecipeSchema(p.prompts.Recipe.Summarize.Recipe),
				}},
			}},
			ToolConfig: &geminiToolConfig{
				FunctionCallingConfig: geminiFunctionCallingConfig{
					Mode:                 "ANY",
					AllowedFunctionNames: []string{"extract_recipes"},
				},
			},
			GenerationConfig: &geminiGenerationConfig{MaxOutputTokens: 16000},
		}
		if systemPrompt != "" {
			reqBody.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: systemPrompt}}}
		}

		resp, err := geminiGenerateContent(ctx, p.httpClient(), p.baseURL, p.apiKey, p.model, reqBody)
		if err != nil {
			return nil, err
		}

		recordUsage(ctx, TokenUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		})

		args, ok := firstFunctionCallArgs(resp, "extract_recipes")
		if !ok {
			return nil, NewAIError(FailureContentEmpty, errors.New("no extract_recipes function call in Gemini response"), "no recipes function call in response")
		}

		var tr multiRecipeToolResult
		if err := json.Unmarshal(args, &tr); err != nil {
			return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal recipes: %w", err), "failed to parse recipes tool result")
		}

		pv := config.PromptVersion(p.prompts)
		valid := make([]*RecipeResult, 0, len(tr.Recipes))
		for i := range tr.Recipes {
			r := toolResultToRecipeResult(&tr.Recipes[i])
			if err := validateRecipeResult(r); err != nil {
				logger.Get().Warn("skipping invalid extracted recipe", zap.String("title", r.Title), zap.Error(err))
				continue
			}
			r.PromptVersion = pv
			valid = append(valid, r)
		}
		if len(valid) == 0 {
			return nil, NewAIError(FailureContentQuality, errors.New("no valid recipes extracted from media"), "no recipes found in the provided files")
		}
		return valid, nil
	})
}

// multiRecipeSchema builds the extract_recipes tool parameters: a JSON-schema
// object with a "recipes" array whose items reuse the shared recipeProperties
// schema. Mirrors createMultiRecipeTool's input schema.
func multiRecipeSchema(summaryPrompt string) map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"recipes": map[string]interface{}{
				"type":        "array",
				"description": "Every distinct recipe found across all provided inputs, one entry per recipe.",
				"items": map[string]interface{}{
					"type":       "object",
					"properties": recipeProperties(summaryPrompt),
				},
			},
		},
	}
}
