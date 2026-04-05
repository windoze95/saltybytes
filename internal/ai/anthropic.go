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
		client:  client,
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
								"amount":        map[string]interface{}{"type": "number", "description": "Amount of the ingredient"},
								"metric_unit":   map[string]interface{}{"type": "string", "description": "Metric equivalent unit. Always metric (g, kg, mL, L, mg). Duplicate primary if already metric.", "enum": []string{"mg", "g", "kg", "mL", "L"}},
								"metric_amount": map[string]interface{}{"type": "number", "description": "Metric equivalent amount. Use accurate cooking conversions (1 cup flour=120g, 1 cup butter=227g, 1 cup water=240mL). Round to practical amounts."},
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
					"unit_system": map[string]interface{}{
						"type":        "string",
						"description": "The measurement system used in the recipe",
						"enum":        []string{"us_customary", "metric"},
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
	UnitSystem              string              `json:"unit_system"`
}

type ingredientToolRes struct {
	Name         string  `json:"name"`
	Unit         string  `json:"unit"`
	Amount       float64 `json:"amount"`
	MetricUnit   string  `json:"metric_unit"`
	MetricAmount float64 `json:"metric_amount"`
}

// analyzeAllergensTool builds the Claude tool definition for allergen analysis.
func analyzeAllergensTool() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        "analyze_allergens",
			Description: anthropic.String("Analyze ingredients for allergen risks and return structured results."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Type: "object",
				Properties: map[string]interface{}{
					"ingredient_analyses": map[string]interface{}{
						"type":        "array",
						"description": "Analysis of each ingredient for allergens",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"ingredient_name":    map[string]interface{}{"type": "string", "description": "Name of the ingredient"},
								"common_allergens":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Known common allergens in this ingredient"},
								"possible_allergens": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Possible allergens that may be present"},
								"sub_ingredients":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Sub-ingredients that may contain allergens"},
								"seed_oil_risk":      map[string]interface{}{"type": "boolean", "description": "Whether this ingredient has seed oil risk"},
								"confidence":         map[string]interface{}{"type": "number", "description": "Confidence score from 0 to 1"},
							},
						},
					},
					"confidence": map[string]interface{}{
						"type":        "number",
						"description": "Overall confidence score from 0 to 1",
					},
					"requires_review": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether the analysis requires human review",
					},
				},
			},
		},
	}
}

// classifyVoiceIntentTool builds the Claude tool definition for voice intent classification.
func classifyVoiceIntentTool() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        "classify_voice_intent",
			Description: anthropic.String("Classify a voice transcript into an app intent."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Type: "object",
				Properties: map[string]interface{}{
					"type": map[string]interface{}{
						"type":        "string",
						"description": "The classified intent type",
						"enum":        []string{"scroll_up", "scroll_down", "navigate", "question", "ignore"},
					},
					"amount": map[string]interface{}{
						"type":        "string",
						"description": "Scroll amount",
						"enum":        []string{"small", "large"},
					},
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Navigation target section",
					},
					"text": map[string]interface{}{
						"type":        "string",
						"description": "The question text for question intents",
					},
				},
			},
		},
	}
}

// estimatePortionsTool builds the Claude tool definition for portion estimation.
func estimatePortionsTool() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        "estimate_portions",
			Description: anthropic.String("Estimate the number of portions and portion size for a recipe."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Type: "object",
				Properties: map[string]interface{}{
					"portions": map[string]interface{}{
						"type":        "integer",
						"description": "Number of portions this recipe makes",
					},
					"portion_size": map[string]interface{}{
						"type":        "string",
						"description": "Description of a single portion size",
					},
					"confidence": map[string]interface{}{
						"type":        "number",
						"description": "Confidence score from 0 to 1",
					},
				},
			},
		},
	}
}

// allergenToolResult is the JSON structure returned by the analyze_allergens tool call.
type allergenToolResult struct {
	IngredientAnalyses []ingredientAnalysisToolRes `json:"ingredient_analyses"`
	Confidence         float64                     `json:"confidence"`
	RequiresReview     bool                        `json:"requires_review"`
}

type ingredientAnalysisToolRes struct {
	IngredientName    string   `json:"ingredient_name"`
	CommonAllergens   []string `json:"common_allergens"`
	PossibleAllergens []string `json:"possible_allergens"`
	SubIngredients    []string `json:"sub_ingredients"`
	SeedOilRisk       bool     `json:"seed_oil_risk"`
	Confidence        float64  `json:"confidence"`
}

// voiceIntentToolResult is the JSON structure returned by the classify_voice_intent tool call.
type voiceIntentToolResult struct {
	Type   string `json:"type"`
	Amount string `json:"amount"`
	Target string `json:"target"`
	Text   string `json:"text"`
}

// portionToolResult is the JSON structure returned by the estimate_portions tool call.
type portionToolResult struct {
	Portions    int     `json:"portions"`
	PortionSize string  `json:"portion_size"`
	Confidence  float64 `json:"confidence"`
}

func toolResultToAllergenResult(tr *allergenToolResult) *AllergenResult {
	analyses := make([]IngredientAnalysisResult, len(tr.IngredientAnalyses))
	for i, a := range tr.IngredientAnalyses {
		analyses[i] = IngredientAnalysisResult{
			IngredientName:    a.IngredientName,
			CommonAllergens:   a.CommonAllergens,
			PossibleAllergens: a.PossibleAllergens,
			SubIngredients:    a.SubIngredients,
			SeedOilRisk:       a.SeedOilRisk,
			Confidence:        a.Confidence,
		}
	}
	return &AllergenResult{
		IngredientAnalyses: analyses,
		Confidence:         tr.Confidence,
		RequiresReview:     tr.RequiresReview,
	}
}

func toolResultToVoiceIntent(tr *voiceIntentToolResult) *VoiceIntent {
	return &VoiceIntent{
		Type:   tr.Type,
		Amount: tr.Amount,
		Target: tr.Target,
		Text:   tr.Text,
	}
}

func toolResultToPortionEstimate(tr *portionToolResult) *PortionEstimate {
	return &PortionEstimate{
		Portions:    tr.Portions,
		PortionSize: tr.PortionSize,
		Confidence:  tr.Confidence,
	}
}

func toolResultToRecipeResult(tr *recipeToolResult) *RecipeResult {
	ingredients := make([]IngredientResult, len(tr.Ingredients))
	for i, ing := range tr.Ingredients {
		ingredients[i] = IngredientResult{
			Name:         ing.Name,
			Unit:         ing.Unit,
			Amount:       ing.Amount,
			MetricUnit:   ing.MetricUnit,
			MetricAmount: ing.MetricAmount,
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
		UnitSystem:        tr.UnitSystem,
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
	var lastErr *AIError

	for i := 0; i < maxRetries; i++ {
		resp, err := p.client.Messages.New(ctx, params)
		if err == nil {
			return resp, nil
		}

		aiErr := classifyAnthropicError(err)
		lastErr = aiErr

		logger.Get().Warn("AI operation failed",
			zap.String("kind", aiErr.kindString()),
			zap.String("detail", aiErr.Detail),
			zap.Error(aiErr.Err),
			zap.Int("attempt", i+1),
		)

		if !aiErr.Retryable {
			return nil, aiErr
		}

		backoff := 2 * time.Second * time.Duration(i+1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return nil, fmt.Errorf("claude API: exhausted %d retries: %w", maxRetries, lastErr)
}

// classifyAnthropicError classifies an API error into the AI failure taxonomy.
func classifyAnthropicError(err error) *AIError {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			return NewAIError(FailureTransient, err, "rate limited")
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return NewAIError(FailureTransient, err, "server error")
		case http.StatusUnauthorized:
			return NewAIError(FailureAuth, err, "unauthorized")
		}
	}
	return NewAIError(FailureUnknown, err, "unknown API error")
}

// extractRecipeFromToolUse parses the tool-use content block returned by Claude.
func extractRecipeFromToolUse(msg *anthropic.Message) (*RecipeResult, error) {
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			raw, err := json.Marshal(block.Input)
			if err != nil {
				return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to marshal tool input: %w", err), "failed to parse recipe tool result")
			}
			var tr recipeToolResult
			if err := json.Unmarshal(raw, &tr); err != nil {
				return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal recipe: %w", err), "failed to parse recipe tool result")
			}
			return toolResultToRecipeResult(&tr), nil
		}
	}
	return nil, NewAIError(FailureContentEmpty, errors.New("no tool_use block found in Claude response"), "no tool_use block in response")
}

// extractAllergenFromToolUse parses the tool-use content block for allergen analysis.
func extractAllergenFromToolUse(msg *anthropic.Message) (*AllergenResult, error) {
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			raw, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool input: %w", err)
			}
			var tr allergenToolResult
			if err := json.Unmarshal(raw, &tr); err != nil {
				return nil, fmt.Errorf("failed to parse allergen tool result: %w", err)
			}
			return toolResultToAllergenResult(&tr), nil
		}
	}
	return nil, errors.New("no tool_use block found in Claude response")
}

// extractVoiceIntentFromToolUse parses the tool-use content block for voice intent.
func extractVoiceIntentFromToolUse(msg *anthropic.Message) (*VoiceIntent, error) {
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			raw, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool input: %w", err)
			}
			var tr voiceIntentToolResult
			if err := json.Unmarshal(raw, &tr); err != nil {
				return nil, fmt.Errorf("failed to parse voice intent tool result: %w", err)
			}
			return toolResultToVoiceIntent(&tr), nil
		}
	}
	return nil, errors.New("no tool_use block found in Claude response")
}

// extractPortionFromToolUse parses the tool-use content block for portion estimation.
func extractPortionFromToolUse(msg *anthropic.Message) (*PortionEstimate, error) {
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			raw, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool input: %w", err)
			}
			var tr portionToolResult
			if err := json.Unmarshal(raw, &tr); err != nil {
				return nil, fmt.Errorf("failed to parse portion tool result: %w", err)
			}
			return toolResultToPortionEstimate(&tr), nil
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
		return "", NewAIError(FailureContentEmpty, errors.New("no text content in Claude response"), "no text content in response")
	}
	return text, nil
}

// validateRecipeResult checks that a recipe has minimum required fields.
func validateRecipeResult(r *RecipeResult) error {
	if r.Title == "" {
		return NewAIError(FailureContentQuality, fmt.Errorf("recipe missing title"), "recipe quality check failed")
	}
	if len(r.Ingredients) == 0 {
		return NewAIError(FailureContentQuality, fmt.Errorf("recipe has no ingredients"), "recipe quality check failed")
	}
	if len(r.Instructions) == 0 {
		return NewAIError(FailureContentQuality, fmt.Errorf("recipe has no instructions"), "recipe quality check failed")
	}
	return nil
}

// extractAndValidateRecipe is a helper that extracts a recipe from a tool-use
// response and validates that it has the minimum required fields.
func extractAndValidateRecipe(msg *anthropic.Message) (*RecipeResult, error) {
	result, err := extractRecipeFromToolUse(msg)
	if err != nil {
		return nil, err
	}
	if err := validateRecipeResult(result); err != nil {
		aiErr := err.(*AIError)
		logger.Get().Warn("AI operation failed",
			zap.String("kind", aiErr.kindString()),
			zap.String("detail", aiErr.Detail),
			zap.Error(aiErr.Err),
		)
		return nil, err
	}
	return result, nil
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

	return extractAndValidateRecipe(resp)
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

	return extractAndValidateRecipe(resp)
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

	return extractAndValidateRecipe(resp)
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

	tool := analyzeAllergensTool()

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
				Name: "analyze_allergens",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractAllergenFromToolUse(resp)
}

// ClassifyVoiceIntent classifies a voice transcript into an app intent.
func (p *AnthropicProvider) ClassifyVoiceIntent(ctx context.Context, transcript string) (*VoiceIntent, error) {
	sysPrompt, err := config.RenderPrompt(p.prompts.Voice.Intent.System, nil)
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	tool := classifyVoiceIntentTool()

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: sysPrompt},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(transcript)),
		},
		Tools: []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: "classify_voice_intent",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractVoiceIntentFromToolUse(resp)
}

// EstimatePortions estimates portion count and sizes for a recipe.
func (p *AnthropicProvider) EstimatePortions(ctx context.Context, recipeDef interface{}) (*PortionEstimate, error) {
	recipeJSON, err := json.Marshal(recipeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal recipe: %w", err)
	}

	tool := estimatePortionsTool()

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: "You are a culinary expert. Estimate the number of portions and portion size for the given recipe."},
		},
		Messages: []anthropic.MessageParam{
			newUserMessage(anthropic.NewTextBlock(string(recipeJSON))),
		},
		Tools: []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: "estimate_portions",
			},
		},
	}

	resp, err := p.createMessageWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	return extractPortionFromToolUse(resp)
}

// ExtractRecipeFromText extracts a structured recipe from free-form text.
func (p *AnthropicProvider) ExtractRecipeFromText(ctx context.Context, text string, unitSystem string) (*RecipeResult, error) {
	var promptTemplate string
	var templateData map[string]interface{}

	if unitSystem == "preserve source" {
		promptTemplate = p.prompts.Import.URL.System
		templateData = map[string]interface{}{
			"UnitSystem": "the original units from the source text. Do not convert measurements. Report which unit system is used via the unit_system field",
		}
	} else {
		promptTemplate = p.prompts.Import.Text.System
		templateData = map[string]interface{}{
			"UnitSystem": unitSystem,
		}
	}

	sysPrompt, err := config.RenderPrompt(promptTemplate, templateData)
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

	return extractAndValidateRecipe(resp)
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

	return extractAndValidateRecipe(resp)
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
