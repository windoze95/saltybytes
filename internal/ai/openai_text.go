package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// OpenAICompatProvider implements the text/reasoning portion of TextProvider
// against any OpenAI-compatible chat-completions endpoint (OpenAI, Gemini's
// OpenAI-compatible API, DeepSeek, ...). It backs the cheap "light" tier and
// only implements the three methods that tier actually calls
// (ExtractRecipeFromText, EstimatePortions, CookingQA); the remaining
// TextProvider methods are stubbed because they are only ever invoked on the
// main (Sonnet) provider.
//
// It deliberately reuses the schema (recipeProperties, portionProperties),
// parsing (recipeToolResult, portionToolResult and their converters) and
// validation (validateRecipeResult) helpers defined alongside the Anthropic
// provider so the two tiers stay byte-for-byte faithful.
type OpenAICompatProvider struct {
	client       *openai.Client
	model        string
	providerName string // "openai" | "gemini" | "deepseek" — used for cost attribution
	prompts      *config.Prompts
	middleware   AIMiddleware // nil means no middleware
}

// Compile-time assurance that the provider satisfies the full TextProvider
// interface (implemented methods + stubs).
var _ TextProvider = (*OpenAICompatProvider)(nil)

// NewOpenAICompatProvider creates a light-tier text provider talking to an
// OpenAI-compatible endpoint. An empty baseURL keeps the SDK default
// (api.openai.com); pass a vendor base URL for Gemini/DeepSeek. providerName is
// recorded against every call for cost attribution.
func NewOpenAICompatProvider(apiKey, baseURL, model, providerName string, prompts *config.Prompts) *OpenAICompatProvider {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	client := openai.NewClientWithConfig(cfg)
	return &OpenAICompatProvider{
		client:       client,
		model:        model,
		providerName: providerName,
		prompts:      prompts,
	}
}

// WithMiddleware sets the middleware chain for this provider.
func (p *OpenAICompatProvider) WithMiddleware(mw AIMiddleware) {
	p.middleware = mw
}

// combineSystemPrompt joins the static prefix and dynamic suffix the Anthropic
// provider would otherwise emit as two cached system blocks into a single
// system message (OpenAI-compatible APIs do not support Anthropic-style block
// caching).
func combineSystemPrompt(prefix, suffix string) string {
	switch {
	case prefix == "":
		return suffix
	case suffix == "":
		return prefix
	default:
		return prefix + "\n\n" + suffix
	}
}

// createChatCompletion issues a chat-completion request with the same retry
// policy as the sibling OpenAI providers (DALL-E/embeddings via
// classifyOpenAIError) and records token usage on every success so the cost
// middleware can meter it.
func (p *OpenAICompatProvider) createChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	const maxRetries = 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		resp, err := p.client.CreateChatCompletion(ctx, req)
		if err == nil {
			recordUsage(ctx, TokenUsage{
				InputTokens:  resp.Usage.PromptTokens,
				OutputTokens: resp.Usage.CompletionTokens,
			})
			return &resp, nil
		}

		lastErr = err
		shouldRetry, waitTime := classifyOpenAIError(err)
		if !shouldRetry {
			return nil, fmt.Errorf("%s chat completion error: %w", p.providerName, err)
		}

		logger.Get().Warn("OpenAI-compat chat completion error, retrying",
			zap.String("provider", p.providerName),
			zap.Error(err),
			zap.Int("attempt", i+1),
		)

		if i < maxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime * time.Duration(i+1)):
			}
		}
	}

	return nil, fmt.Errorf("%s chat completion: exhausted %d retries: %w", p.providerName, maxRetries, lastErr)
}

// schemaObject wraps a property set into a JSON-schema object so the reused
// *Properties helpers can serve as an OpenAI function's Parameters.
func schemaObject(properties map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
}

// structuredCompletion issues a chat completion constrained to the given JSON
// schema (response_format json_schema) and returns the raw JSON content.
// Forced FUNCTION calls are avoided deliberately on this provider: Gemini's
// OpenAI-compat function-call parser intermittently rejects large calls
// wholesale (finish_reason=MALFORMED_FUNCTION_CALL / "no tool call", zero
// output) — the exact failure #112 fixed for the finder's ranking, later seen
// in prod against create_recipe extraction too. Schema-constrained JSON
// sidesteps that parser; one resample covers residual empty responses.
//
// The schema-following instruction is appended to the first system message so
// callers keep prompts that were written for the tool-call era.
func (p *OpenAICompatProvider) structuredCompletion(ctx context.Context, name string, schema map[string]interface{}, maxTokens int, messages []openai.ChatCompletionMessage) (string, error) {
	msgs := make([]openai.ChatCompletionMessage, len(messages))
	copy(msgs, messages)
	for i := range msgs {
		if msgs[i].Role == openai.ChatMessageRoleSystem {
			msgs[i].Content += fmt.Sprintf("\n\nRespond with ONLY a JSON object matching the %s schema.", name)
			break
		}
	}

	chatReq := openai.ChatCompletionRequest{
		Model: p.model,
		// Generous budgets everywhere: Gemini 2.5 models spend completion
		// budget on internal thinking BEFORE the answer; billing is by actual
		// tokens, so headroom is free.
		MaxTokens: maxTokens,
		Messages:  msgs,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:   name,
				Schema: jsonSchemaMap(schema),
			},
		},
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := p.createChatCompletion(ctx, chatReq)
		if err != nil {
			return "", err // transport errors already retried downstream
		}
		content := ""
		finishReason := ""
		if len(resp.Choices) > 0 {
			content = stripCodeFences(resp.Choices[0].Message.Content)
			finishReason = string(resp.Choices[0].FinishReason)
		}
		if content != "" {
			return content, nil
		}
		lastErr = NewAIError(FailureContentEmpty,
			fmt.Errorf("empty %s JSON response (finish_reason=%s, completion_tokens=%d)",
				name, finishReason, resp.Usage.CompletionTokens),
			"empty response")
		logger.Get().Warn("structured completion empty, resampling",
			zap.String("provider", p.providerName),
			zap.String("schema", name),
			zap.Int("attempt", attempt+1),
		)
	}
	return "", lastErr
}

// stripCodeFences unwraps a ```json ... ``` fenced block some models emit
// around structured output; plain content passes through untouched.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ExtractRecipeFromText extracts a structured recipe from free-form text via a
// schema-constrained create_recipe completion. Mirrors
// AnthropicProvider.ExtractRecipeFromText (which keeps the tool path — the
// function-call rejection is specific to OpenAI-compat Gemini).
func (p *OpenAICompatProvider) ExtractRecipeFromText(ctx context.Context, text string, unitSystem string) (*RecipeResult, error) {
	op := AIOperation{
		Name:      "ExtractRecipeFromText",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*RecipeResult, error) {
		var sysPrefix string
		var promptTemplate string
		var templateData map[string]interface{}

		if unitSystem == UnitSystemPreserveSource {
			sysPrefix = p.prompts.Import.URL.SystemPrefix
			promptTemplate = p.prompts.Import.URL.System
			templateData = map[string]interface{}{
				"UnitSystem": "the original units from the source text. Do not convert measurements. Report which unit system is used via the unit_system field",
			}
		} else {
			sysPrefix = p.prompts.Import.Text.SystemPrefix
			promptTemplate = p.prompts.Import.Text.System
			templateData = map[string]interface{}{
				"UnitSystem": unitSystem,
			}
		}

		sysSuffix, err := config.RenderPrompt(promptTemplate, templateData)
		if err != nil {
			return nil, fmt.Errorf("render system prompt: %w", err)
		}

		return p.completeRecipe(ctx, p.prompts.Recipe.Summarize.Recipe, []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: combineSystemPrompt(sysPrefix, sysSuffix)},
			{Role: openai.ChatMessageRoleUser, Content: text},
		})
	})
}

// EstimatePortions estimates portion count and size for a recipe via a forced
// estimate_portions function call. Mirrors AnthropicProvider.EstimatePortions.
func (p *OpenAICompatProvider) EstimatePortions(ctx context.Context, recipeDef interface{}) (*PortionEstimate, error) {
	op := AIOperation{
		Name:      "EstimatePortions",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*PortionEstimate, error) {
		recipeJSON, err := json.Marshal(recipeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal recipe: %w", err)
		}

		content, err := p.structuredCompletion(ctx, "estimate_portions", schemaObject(portionProperties()), 2048, []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "You are a culinary expert. Estimate the number of portions and portion size for the given recipe."},
			{Role: openai.ChatMessageRoleUser, Content: string(recipeJSON)},
		})
		if err != nil {
			return nil, err
		}

		var tr portionToolResult
		if err := json.Unmarshal([]byte(content), &tr); err != nil {
			return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal portion estimate: %w", err), "failed to parse portion tool result")
		}
		return toolResultToPortionEstimate(&tr), nil
	})
}

// CookingQA answers a cooking question with optional recipe context via a plain
// completion. Mirrors AnthropicProvider.CookingQA.
func (p *OpenAICompatProvider) CookingQA(ctx context.Context, question string, recipeContext string) (string, error) {
	op := AIOperation{
		Name:      "CookingQA",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (string, error) {
		sysSuffix, err := config.RenderPrompt(p.prompts.CookingQA.System, map[string]interface{}{
			"RecipeContext": recipeContext,
		})
		if err != nil {
			return "", fmt.Errorf("render system prompt: %w", err)
		}

		req := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 1024,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: combineSystemPrompt(p.prompts.CookingQA.SystemPrefix, sysSuffix)},
				{Role: openai.ChatMessageRoleUser, Content: question},
			},
		}

		resp, err := p.createChatCompletion(ctx, req)
		if err != nil {
			return "", err
		}

		if len(resp.Choices) == 0 {
			return "", NewAIError(FailureContentEmpty, errors.New("no choices in chat completion response"), "no choices in response")
		}
		content := resp.Choices[0].Message.Content
		if content == "" {
			return "", NewAIError(FailureContentEmpty, errors.New("no text content in chat completion response"), "no text content in response")
		}
		return content, nil
	})
}

// The remaining TextProvider methods (GenerateRecipe, RegenerateRecipe,
// ForkRecipe, AnalyzeAllergens, ClassifyVoiceIntent, DietaryInterview) are
// implemented in openai_maintier.go, so this provider can serve the full main
// tier (e.g. Gemini 2.5 Pro) as well as the light tier.
