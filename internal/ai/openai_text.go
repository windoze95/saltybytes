package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// firstToolCallArguments returns the JSON argument string of the first tool
// call in the response, guarding against zero choices / zero tool calls. fnName
// is used only for diagnostics; tool choice is forced so the first call is the
// requested function. The empty-tool-call error carries finish_reason + token
// usage: Gemini 2.5 models spend completion budget on thinking, so
// finish_reason=length with a large completion count means the budget was
// consumed before the tool call was emitted (raise MaxTokens).
func firstToolCallArguments(resp *openai.ChatCompletionResponse, fnName string) (string, error) {
	if resp == nil || len(resp.Choices) == 0 {
		return "", NewAIError(FailureContentEmpty, errors.New("no choices in chat completion response"), "no choices in response")
	}
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		return "", NewAIError(FailureContentEmpty,
			fmt.Errorf("no %s tool call in chat completion response (finish_reason=%s, completion_tokens=%d)",
				fnName, resp.Choices[0].FinishReason, resp.Usage.CompletionTokens),
			"no tool call in response")
	}
	return calls[0].Function.Arguments, nil
}

// schemaObject wraps a property set into a JSON-schema object so the reused
// *Properties helpers can serve as an OpenAI function's Parameters.
func schemaObject(properties map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
}

// ExtractRecipeFromText extracts a structured recipe from free-form text via a
// forced create_recipe function call. Mirrors AnthropicProvider.ExtractRecipeFromText.
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

		summaryDesc := p.prompts.Recipe.Summarize.Recipe

		req := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 4096,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: combineSystemPrompt(sysPrefix, sysSuffix)},
				{Role: openai.ChatMessageRoleUser, Content: text},
			},
			Tools: []openai.Tool{{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "create_recipe",
					Description: "Create a structured recipe definition with all required fields.",
					Parameters:  schemaObject(recipeProperties(summaryDesc)),
				},
			}},
			ToolChoice: openai.ToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ToolFunction{Name: "create_recipe"},
			},
		}

		resp, err := p.createChatCompletion(ctx, req)
		if err != nil {
			return nil, err
		}

		args, err := firstToolCallArguments(resp, "create_recipe")
		if err != nil {
			return nil, err
		}

		var tr recipeToolResult
		if err := json.Unmarshal([]byte(args), &tr); err != nil {
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

		req := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 256,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: "You are a culinary expert. Estimate the number of portions and portion size for the given recipe."},
				{Role: openai.ChatMessageRoleUser, Content: string(recipeJSON)},
			},
			Tools: []openai.Tool{{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "estimate_portions",
					Description: "Estimate the number of portions and portion size for a recipe.",
					Parameters:  schemaObject(portionProperties()),
				},
			}},
			ToolChoice: openai.ToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ToolFunction{Name: "estimate_portions"},
			},
		}

		resp, err := p.createChatCompletion(ctx, req)
		if err != nil {
			return nil, err
		}

		args, err := firstToolCallArguments(resp, "estimate_portions")
		if err != nil {
			return nil, err
		}

		var tr portionToolResult
		if err := json.Unmarshal([]byte(args), &tr); err != nil {
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
