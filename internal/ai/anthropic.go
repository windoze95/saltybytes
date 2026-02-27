package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// AnthropicProvider implements TextProvider and VisionProvider using Claude.
type AnthropicProvider struct {
	client  anthropic.Client
	model   anthropic.Model
	prompts *config.Prompts
}

// NewAnthropicProvider creates a new AnthropicProvider with the given API key
// and prompt configuration.
func NewAnthropicProvider(apiKey string, prompts *config.Prompts) *AnthropicProvider {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{
		client: client,
		model:   anthropic.ModelClaude3_5Sonnet20241022,
		prompts: prompts,
	}
}

// NewAnthropicLightProvider creates an AnthropicProvider using the cheaper
// Haiku model. Suitable for preview/extraction tasks where cost matters more
// than maximum quality.
func NewAnthropicLightProvider(apiKey string, prompts *config.Prompts) *AnthropicProvider {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{
		client:  client,
		model:   anthropic.Model("claude-haiku-4-5-20251001"),
		prompts: prompts,
	}
}

// createRecipeTool builds the Claude tool definition that mirrors the existing
// OpenAI create_recipe function-call schema.
func createRecipeTool(summaryPrompt string) anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        "create_recipe",
			Description: anthropic.String("Create a structured recipe definition with all required fields."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Type: "object",
				Properties: map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Title of the recipe or meal",
					},
					"ingredients": map[string]interface{}{
						"type":        "array",
						"description": "List of ingredients used in the recipe",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"name":   map[string]interface{}{"type": "string", "description": "Name of the ingredient, do not include unit or amount in this field"},
								"unit":   map[string]interface{}{"type": "string", "description": "Unit for the ingredient, comply with UnitSystem specified.", "enum": []string{"pieces", "tsp", "tbsp", "fl oz", "cup", "pt", "qt", "gal", "oz", "lb", "mL", "L", "mg", "g", "kg", "pinch", "dash", "drop", "bushel"}},
								"amount": map[string]interface{}{"type": "number", "description": "Amount of the ingredient"},
							},
						},
					},
					"instructions": map[string]interface{}{
						"type":        "array",
						"description": "Steps to prepare the recipe (no numbering)",
						"items":       map[string]interface{}{"type": "string"},
					},
					"cook_time": map[string]interface{}{
						"type":        "number",
						"description": "Total time to prepare the recipe(s) in minutes",
					},
					"image_prompt": map[string]interface{}{
						"type":        "string",
						"description": "Prompt to generate an image for the recipe, this should be relevant to the recipe and not the user request",
					},
					"hashtags": map[string]interface{}{
						"type":        "array",
						"description": "Provide a lengthy and thorough list (ten or more) of hashtags relevant to the recipe, not the prompting. Alphanumeric characters only. No '#'. Exclude terms like 'recipe', 'homemade', 'DIY', or similar words. Use camelCase formatting if more than one word (first letter is always lowercase).",
						"items":       map[string]interface{}{"type": "string"},
					},
					"linked_recipe_suggestions": map[string]interface{}{
						"type":        "array",
						"description": "Provide a list of recipe suggestions (just the titles) based on: 1. Homemade versions of store-bought ingredients used in this recipe. 2. Something that would pair well with this recipe.",
						"items":       map[string]interface{}{"type": "string"},
					},
					"recipe_summary": map[string]interface{}{
						"type":        "string",
						"description": summaryPrompt,
					},
					"portions": map[string]interface{}{
						"type":        "number",
						"description": "Number of portions this recipe makes",
					},
					"portion_size": map[string]interface{}{
						"type":        "string",
						"description": "Description of a single portion size",
					},
				},
			},
		},
	}
}

// recipeToolResult is the JSON structure returned by the create_recipe tool call.
type recipeToolResult struct {
	Title                   string              `json:"title"`
	Ingredients             []ingredientToolRes `json:"ingredients"`
	Instructions            []string            `json:"instructions"`
	CookTime                int                 `json:"cook_time"`
	ImagePrompt             string              `json:"image_prompt"`
	Hashtags                []string            `json:"hashtags"`
	LinkedRecipeSuggestions []string            `json:"linked_recipe_suggestions"`
	RecipeSummary           string              `json:"recipe_summary"`
	Portions                int                 `json:"portions"`
	PortionSize             string              `json:"portion_size"`
}

type ingredientToolRes struct {
	Name   string  `json:"name"`
	Unit   string  `json:"unit"`
	Amount float64 `json:"amount"`
}

func toolResultToRecipeResult(tr *recipeToolResult) *RecipeResult {
	ingredients := make([]IngredientResult, len(tr.Ingredients))
	for i, ing := range tr.Ingredients {
		ingredients[i] = IngredientResult{
			Name:   ing.Name,
			Unit:   ing.Unit,
			Amount: ing.Amount,
		}
	}
	return &RecipeResult{
		Title:             tr.Title,
		Ingredients:       ingredients,
		Instructions:      tr.Instructions,
		CookTime:          tr.CookTime,
		ImagePrompt:       tr.ImagePrompt,
		Hashtags:          tr.Hashtags,
		LinkedSuggestions: tr.LinkedRecipeSuggestions,
		Summary:           tr.RecipeSummary,
		Portions:          tr.Portions,
		PortionSize:       tr.PortionSize,
	}
}

// messagesToAnthropicParams converts our Message slice into Claude message params.
// System messages are separated out as they use a different field in the API.
func messagesToAnthropicParams(msgs []Message) (string, []anthropic.MessageParam) {
	var systemPrompt string
	var params []anthropic.MessageParam

	for _, m := range msgs {
		switch m.Role {
		case "system":
			if systemPrompt != "" {
				systemPrompt += "\n\n"
			}
			systemPrompt += m.Content
		case "user":
			params = append(params, anthropic.MessageParam{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock(m.Content),
				},
			})
		case "assistant":
			params = append(params, anthropic.MessageParam{
				Role: anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock(m.Content),
				},
			})
		}
	}
	return systemPrompt, params
}

// newUserMessage creates a user message param with the given content blocks.
func newUserMessage(blocks ...anthropic.ContentBlockParamUnion) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role:    anthropic.MessageParamRoleUser,
		Content: blocks,
	}
}

// createMessageWithRetry wraps the Claude API call with exponential backoff.
func (p *AnthropicProvider) createMessageWithRetry(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	const maxRetries = 5
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		resp, err := p.client.Messages.New(ctx, params)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		shouldRetry, waitTime := classifyAnthropicError(err)
		if !shouldRetry {
			return nil, fmt.Errorf("claude API error: %w", err)
		}

		logger.Get().Warn("claude API error, retrying",
			zap.Error(err),
			zap.Int("attempt", i+1),
		)

		backoff := waitTime * time.Duration(i+1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return nil, fmt.Errorf("claude API: exhausted %d retries: %w", maxRetries, lastErr)
}

// classifyAnthropicError determines whether to retry and the base wait duration.
func classifyAnthropicError(err error) (shouldRetry bool, waitTime time.Duration) {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			return true, 2 * time.Second
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return true, 2 * time.Second
		case http.StatusUnauthorized:
			return false, 0
		default:
			return false, 0
		}
	}
	return false, 0
}

// extractRecipeFromToolUse parses the tool-use content block returned by Claude.
func extractRecipeFromToolUse(msg *anthropic.Message) (*RecipeResult, error) {
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			raw, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool input: %w", err)
			}
			var tr recipeToolResult
			if err := json.Unmarshal(raw, &tr); err != nil {
				return nil, fmt.Errorf("failed to parse recipe tool result: %w", err)
			}
			return toolResultToRecipeResult(&tr), nil
		}
	}
	return nil, errors.New("no tool_use block found in Claude response")
}

// extractTextContent returns the concatenated text blocks from a Claude response.
func extractTextContent(msg *anthropic.Message) (string, error) {
	var text string
	for _, block := range msg.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if text == "" {
		return "", errors.New("no text content in Claude response")
	}
	return text, nil
}

// --- TextProvider implementation ---

// GenerateRecipe creates a new recipe via Claude tool use.
func (p *AnthropicProvider) GenerateRecipe(ctx context.Context, req RecipeRequest) (*RecipeResult, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Recipe.Generate.System, map[string]interface{}{
		"UnitSystem":   req.UnitSystem,
		"Requirements": req.Requirements,
	})
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	userPrompt, err := config.RenderPrompt(p.prompts.Recipe.Generate.User, map[string]interface{}{
		"Prompt": req.UserPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}

	summaryDesc := p.prompts.Recipe.Summarize.Recipe
	tool := createRecipeTool(summaryDesc)

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
		Tools: []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: "create_recipe",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractRecipeFromToolUse(resp)
}

// RegenerateRecipe revises an existing recipe based on conversation history.
func (p *AnthropicProvider) RegenerateRecipe(ctx context.Context, req RegenerateRequest) (*RecipeResult, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Recipe.Regenerate.System, map[string]interface{}{
		"UnitSystem":   req.UnitSystem,
		"Requirements": req.Requirements,
	})
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	summaryDesc := p.prompts.Recipe.Summarize.Changes
	tool := createRecipeTool(summaryDesc)

	// Build message list: existing history + new user prompt
	_, historyParams := messagesToAnthropicParams(req.ExistingHistory)
	userPrompt, err := config.RenderPrompt(p.prompts.Recipe.Regenerate.User, map[string]interface{}{
		"Prompt": req.UserPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}
	historyParams = append(historyParams, newUserMessage(anthropic.NewTextBlock(userPrompt)))

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: historyParams,
		Tools:    []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: "create_recipe",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractRecipeFromToolUse(resp)
}

// ForkRecipe creates a new recipe branched from an existing one.
func (p *AnthropicProvider) ForkRecipe(ctx context.Context, req ForkRequest) (*RecipeResult, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Recipe.Fork.System, map[string]interface{}{
		"UnitSystem":   req.UnitSystem,
		"Requirements": req.Requirements,
	})
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	summaryDesc := p.prompts.Recipe.Summarize.Recipe
	tool := createRecipeTool(summaryDesc)

	_, historyParams := messagesToAnthropicParams(req.ExistingHistory)
	userPrompt, err := config.RenderPrompt(p.prompts.Recipe.Fork.User, map[string]interface{}{
		"Prompt": req.UserPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}
	historyParams = append(historyParams, newUserMessage(anthropic.NewTextBlock(userPrompt)))

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: historyParams,
		Tools:    []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: "create_recipe",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractRecipeFromToolUse(resp)
}

// AnalyzeAllergens analyses ingredients for allergen risks.
func (p *AnthropicProvider) AnalyzeAllergens(ctx context.Context, req AllergenRequest) (*AllergenResult, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Allergen.Analyze.System, nil)
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	ingredientList, _ := json.Marshal(req.Ingredients)
	userPrompt, err := config.RenderPrompt(p.prompts.Allergen.Analyze.User, map[string]interface{}{
		"Ingredients": string(ingredientList),
		"IsPremium":   req.IsPremium,
	})
	if err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	text, err := extractTextContent(resp)
	if err != nil {
		return nil, err
	}

	var result AllergenResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse allergen response: %w", err)
	}

	return &result, nil
}

// ClassifyVoiceIntent classifies a voice transcript into an app intent.
func (p *AnthropicProvider) ClassifyVoiceIntent(ctx context.Context, transcript string) (*VoiceIntent, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Voice.Intent.System, nil)
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(transcript)),
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	text, err := extractTextContent(resp)
	if err != nil {
		return nil, err
	}

	var intent VoiceIntent
	if err := json.Unmarshal([]byte(text), &intent); err != nil {
		return nil, fmt.Errorf("failed to parse voice intent: %w", err)
	}

	return &intent, nil
}

// NormalizeMeasurements normalises a list of ingredients to standard units.
func (p *AnthropicProvider) NormalizeMeasurements(ctx context.Context, ingredients []IngredientInput) ([]NormalizedIngredient, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Normalize.System, nil)
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	ingredientJSON, _ := json.Marshal(ingredients)

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 2048,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(string(ingredientJSON))),
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	text, err := extractTextContent(resp)
	if err != nil {
		return nil, err
	}

	var normalized []NormalizedIngredient
	if err := json.Unmarshal([]byte(text), &normalized); err != nil {
		return nil, fmt.Errorf("failed to parse normalized ingredients: %w", err)
	}

	return normalized, nil
}

// EstimatePortions estimates portion count and sizes for a recipe.
func (p *AnthropicProvider) EstimatePortions(ctx context.Context, recipeDef interface{}) (*PortionEstimate, error) {
	recipeJSON, err := json.Marshal(recipeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal recipe: %w", err)
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: "You are a culinary expert. Estimate the number of portions and portion size for the given recipe. Respond with JSON: {\"portions\": <int>, \"portion_size\": \"<string>\", \"confidence\": <float>}"},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(string(recipeJSON))),
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	text, err := extractTextContent(resp)
	if err != nil {
		return nil, err
	}

	var estimate PortionEstimate
	if err := json.Unmarshal([]byte(text), &estimate); err != nil {
		return nil, fmt.Errorf("failed to parse portion estimate: %w", err)
	}

	return &estimate, nil
}

// ExtractRecipeFromText extracts a structured recipe from free-form text.
func (p *AnthropicProvider) ExtractRecipeFromText(ctx context.Context, text string, unitSystem string) (*RecipeResult, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Import.Text.System, map[string]interface{}{
		"UnitSystem": unitSystem,
	})
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	summaryDesc := p.prompts.Recipe.Summarize.Recipe
	tool := createRecipeTool(summaryDesc)

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(text)),
		},
		Tools: []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: "create_recipe",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractRecipeFromToolUse(resp)
}

// CookingQA answers a cooking question with optional recipe context.
func (p *AnthropicProvider) CookingQA(ctx context.Context, question string, recipeContext string) (string, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.CookingQA.System, map[string]interface{}{
		"RecipeContext": recipeContext,
	})
	if err != nil {
		return "", fmt.Errorf("render system prompt: %w", err)
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(question)),
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return "", err
	}

	return extractTextContent(resp)
}

// DietaryInterview conducts a multi-turn dietary interview.
func (p *AnthropicProvider) DietaryInterview(ctx context.Context, messages []Message, memberName string) (string, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.DietaryInterview.System, map[string]interface{}{
		"MemberName": memberName,
	})
	if err != nil {
		return "", fmt.Errorf("render system prompt: %w", err)
	}

	_, msgParams := messagesToAnthropicParams(messages)

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: msgParams,
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return "", err
	}

	return extractTextContent(resp)
}

// --- VisionProvider implementation ---

// ExtractRecipeFromImage extracts a structured recipe from a photo.
func (p *AnthropicProvider) ExtractRecipeFromImage(ctx context.Context, imageData []byte, unitSystem string, requirements string) (*RecipeResult, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Recipe.Import.Vision.System, map[string]interface{}{
		"UnitSystem":   unitSystem,
		"Requirements": requirements,
	})
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(imageData)
	mediaType := detectImageMediaType(imageData)

	summaryDesc := p.prompts.Recipe.Summarize.Recipe
	tool := createRecipeTool(summaryDesc)

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(
				anthropic.ContentBlockParamUnion{
					OfRequestImageBlock: &anthropic.ImageBlockParam{
						Source: anthropic.ImageBlockParamSourceUnion{
							OfBase64ImageSource: &anthropic.Base64ImageSourceParam{
								MediaType: anthropic.Base64ImageSourceMediaType(mediaType),
								Data:      b64,
							},
						},
					},
				},
				anthropic.NewTextBlock("Extract the recipe from this image. If the image shows a prepared dish, infer a reasonable recipe for it."),
			),
		},
		Tools: []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: "create_recipe",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractRecipeFromToolUse(resp)
}

// detectImageMediaType returns the MIME type based on magic bytes.
func detectImageMediaType(data []byte) string {
	if len(data) < 4 {
		return "image/jpeg"
	}
	// PNG magic bytes
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}
	// GIF
	if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
		return "image/gif"
	}
	// WebP
	if len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return "image/webp"
	}
	return "image/jpeg"
}
