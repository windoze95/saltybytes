package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// This file completes *OpenAICompatProvider so it satisfies the full
// TextProvider interface (not just the cheap "light" tier). Each method mirrors
// the matching AnthropicProvider method in anthropic.go field-for-field —
// identical prompt templates, template-data keys, tool schemas and result
// parsers — so the main tier can run on an OpenAI-compatible endpoint (e.g.
// Gemini 2.5 Pro) and produce byte-for-byte faithful requests/results. Only the
// transport differs: chat-completions function calls instead of Anthropic
// messages tool use.
//
// The three inline tool schemas below are hand-copied from the corresponding
// Anthropic tool constructors (analyzeAllergensTool, classifyVoiceIntentTool,
// saveDietaryProfileTool). The recipe schema is the shared recipeProperties
// helper, so it is reused directly rather than copied.

// messagesToOpenAIParams converts our Message slice into OpenAI chat messages,
// mirroring messagesToAnthropicParams: system-role turns are dropped from the
// message list (the Anthropic callers discard the separated system prompt and
// supply their own), and user/assistant turns are carried over in order.
func messagesToOpenAIParams(msgs []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: m.Content})
		case "assistant":
			out = append(out, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: m.Content})
		}
	}
	return out
}

// allergenProperties is the JSON-schema property set for the analyze_allergens
// tool, copied verbatim from analyzeAllergensTool().OfTool.InputSchema.Properties
// so both tiers request the identical schema.
func allergenProperties() map[string]interface{} {
	return map[string]interface{}{
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
	}
}

// voiceIntentProperties is the JSON-schema property set for the
// classify_voice_intent tool, copied verbatim from
// classifyVoiceIntentTool().OfTool.InputSchema.Properties.
func voiceIntentProperties() map[string]interface{} {
	return map[string]interface{}{
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
	}
}

// dietaryProfileProperties is the JSON-schema property set for the
// save_dietary_profile tool, copied verbatim from
// saveDietaryProfileTool().OfTool.InputSchema.Properties.
func dietaryProfileProperties() map[string]interface{} {
	return map[string]interface{}{
		"allergies": map[string]interface{}{
			"type":        "array",
			"description": "Food allergies. Empty array if none.",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":      map[string]interface{}{"type": "string", "description": "Name of the allergen (e.g. 'peanuts', 'shellfish')"},
					"severity":  map[string]interface{}{"type": "string", "description": "Severity of the allergy", "enum": []string{"mild", "moderate", "severe", "life_threatening"}},
					"sub_forms": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Specific forms or sub-ingredients that trigger the allergy (e.g. 'raw egg' but not 'baked egg'). Empty array if it applies to all forms."},
					"notes":     map[string]interface{}{"type": "string", "description": "Additional notes about the allergy (reactions, cross-contamination concerns, etc.)"},
				},
			},
		},
		"intolerances": map[string]interface{}{
			"type":        "array",
			"description": "Food intolerances (e.g. 'lactose', 'gluten'). Empty array if none.",
			"items":       map[string]interface{}{"type": "string"},
		},
		"restrictions": map[string]interface{}{
			"type":        "array",
			"description": "Dietary restrictions including lifestyle, cultural, or religious (e.g. 'vegetarian', 'halal', 'keto'). Empty array if none.",
			"items":       map[string]interface{}{"type": "string"},
		},
		"preferences": map[string]interface{}{
			"type":        "array",
			"description": "Food preferences and dislikes (e.g. 'dislikes cilantro', 'loves spicy food'). Empty array if none.",
			"items":       map[string]interface{}{"type": "string"},
		},
		"medical_notes": map[string]interface{}{
			"type":        "string",
			"description": "Medically relevant dietary notes (e.g. 'low sodium for hypertension'). Empty string if none.",
		},
	}
}

// GenerateRecipe creates a new recipe via a forced create_recipe function call.
// Mirrors AnthropicProvider.GenerateRecipe: it sends only the rendered system
// and user prompts (it does not use req.Messages, matching the Anthropic side).
func (p *OpenAICompatProvider) GenerateRecipe(ctx context.Context, req RecipeRequest) (*RecipeResult, error) {
	op := AIOperation{
		Name:      "GenerateRecipe",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*RecipeResult, error) {
		sysSuffix, err := config.RenderPrompt(p.prompts.Recipe.Generate.System, map[string]interface{}{
			"UnitSystem":     req.UnitSystem,
			"Requirements":   req.Requirements,
			"CookingContext": req.CookingContext,
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

		chatReq := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 4096,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: combineSystemPrompt(p.prompts.Recipe.Generate.SystemPrefix, sysSuffix)},
				{Role: openai.ChatMessageRoleUser, Content: userPrompt},
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

		return p.completeRecipe(ctx, chatReq)
	})
}

// RegenerateRecipe revises an existing recipe based on conversation history.
// Mirrors AnthropicProvider.RegenerateRecipe.
func (p *OpenAICompatProvider) RegenerateRecipe(ctx context.Context, req RegenerateRequest) (*RecipeResult, error) {
	op := AIOperation{
		Name:      "RegenerateRecipe",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*RecipeResult, error) {
		sysSuffix, err := config.RenderPrompt(p.prompts.Recipe.Regenerate.System, map[string]interface{}{
			"UnitSystem":     req.UnitSystem,
			"Requirements":   req.Requirements,
			"CookingContext": req.CookingContext,
		})
		if err != nil {
			return nil, fmt.Errorf("render system prompt: %w", err)
		}

		summaryDesc := p.prompts.Recipe.Summarize.Changes

		userPrompt, err := config.RenderPrompt(p.prompts.Recipe.Regenerate.User, map[string]interface{}{
			"Prompt": req.UserPrompt,
		})
		if err != nil {
			return nil, fmt.Errorf("render user prompt: %w", err)
		}

		// Build message list: system + existing history + new user prompt.
		messages := []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: combineSystemPrompt(p.prompts.Recipe.Regenerate.SystemPrefix, sysSuffix)},
		}
		messages = append(messages, messagesToOpenAIParams(req.ExistingHistory)...)
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: userPrompt})

		chatReq := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 4096,
			Messages:  messages,
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

		return p.completeRecipe(ctx, chatReq)
	})
}

// ForkRecipe creates a new recipe branched from an existing one.
// Mirrors AnthropicProvider.ForkRecipe.
func (p *OpenAICompatProvider) ForkRecipe(ctx context.Context, req ForkRequest) (*RecipeResult, error) {
	op := AIOperation{
		Name:      "ForkRecipe",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*RecipeResult, error) {
		sysSuffix, err := config.RenderPrompt(p.prompts.Recipe.Fork.System, map[string]interface{}{
			"UnitSystem":     req.UnitSystem,
			"Requirements":   req.Requirements,
			"CookingContext": req.CookingContext,
		})
		if err != nil {
			return nil, fmt.Errorf("render system prompt: %w", err)
		}

		summaryDesc := p.prompts.Recipe.Summarize.Recipe

		userPrompt, err := config.RenderPrompt(p.prompts.Recipe.Fork.User, map[string]interface{}{
			"Prompt": req.UserPrompt,
		})
		if err != nil {
			return nil, fmt.Errorf("render user prompt: %w", err)
		}

		messages := []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: combineSystemPrompt(p.prompts.Recipe.Fork.SystemPrefix, sysSuffix)},
		}
		messages = append(messages, messagesToOpenAIParams(req.ExistingHistory)...)
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: userPrompt})

		chatReq := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 4096,
			Messages:  messages,
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

		return p.completeRecipe(ctx, chatReq)
	})
}

// completeRecipe issues a forced create_recipe chat completion and parses,
// validates and stamps the resulting recipe. Shared by GenerateRecipe,
// RegenerateRecipe and ForkRecipe (their only difference is prompt/history).
func (p *OpenAICompatProvider) completeRecipe(ctx context.Context, chatReq openai.ChatCompletionRequest) (*RecipeResult, error) {
	resp, err := p.createChatCompletion(ctx, chatReq)
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
}

// AnalyzeAllergens analyses ingredients for allergen risks via a forced
// analyze_allergens function call. Mirrors AnthropicProvider.AnalyzeAllergens.
func (p *OpenAICompatProvider) AnalyzeAllergens(ctx context.Context, req AllergenRequest) (*AllergenResult, error) {
	op := AIOperation{
		Name:      "AnalyzeAllergens",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*AllergenResult, error) {
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

		chatReq := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 4096,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: sysPrompt},
				{Role: openai.ChatMessageRoleUser, Content: userPrompt},
			},
			Tools: []openai.Tool{{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "analyze_allergens",
					Description: "Analyze ingredients for allergen risks and return structured results.",
					Parameters:  schemaObject(allergenProperties()),
				},
			}},
			ToolChoice: openai.ToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ToolFunction{Name: "analyze_allergens"},
			},
		}

		resp, err := p.createChatCompletion(ctx, chatReq)
		if err != nil {
			return nil, err
		}

		args, err := firstToolCallArguments(resp, "analyze_allergens")
		if err != nil {
			return nil, err
		}

		var tr allergenToolResult
		if err := json.Unmarshal([]byte(args), &tr); err != nil {
			return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal allergen analysis: %w", err), "failed to parse allergen tool result")
		}
		return toolResultToAllergenResult(&tr), nil
	})
}

// ClassifyVoiceIntent classifies a voice transcript into an app intent via a
// forced classify_voice_intent function call. Mirrors
// AnthropicProvider.ClassifyVoiceIntent.
func (p *OpenAICompatProvider) ClassifyVoiceIntent(ctx context.Context, transcript string) (*VoiceIntent, error) {
	op := AIOperation{
		Name:      "ClassifyVoiceIntent",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*VoiceIntent, error) {
		sysPrompt, err := config.RenderPrompt(p.prompts.Voice.Intent.System, nil)
		if err != nil {
			return nil, fmt.Errorf("render system prompt: %w", err)
		}

		chatReq := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 256,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: sysPrompt},
				{Role: openai.ChatMessageRoleUser, Content: transcript},
			},
			Tools: []openai.Tool{{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "classify_voice_intent",
					Description: "Classify a voice transcript into an app intent.",
					Parameters:  schemaObject(voiceIntentProperties()),
				},
			}},
			ToolChoice: openai.ToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ToolFunction{Name: "classify_voice_intent"},
			},
		}

		resp, err := p.createChatCompletion(ctx, chatReq)
		if err != nil {
			return nil, err
		}

		args, err := firstToolCallArguments(resp, "classify_voice_intent")
		if err != nil {
			return nil, err
		}

		var tr voiceIntentToolResult
		if err := json.Unmarshal([]byte(args), &tr); err != nil {
			return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal voice intent: %w", err), "failed to parse voice intent tool result")
		}
		return toolResultToVoiceIntent(&tr), nil
	})
}

// ExpandAndRankRecipes runs the recipe finder's ranking call via a forced
// rank_recipes function call. Mirrors AnthropicProvider.ExpandAndRankRecipes —
// same inline system prompt, wire payload and tool schema — so the light Gemini
// tier that backs the finder produces a faithful request/result.
func (p *OpenAICompatProvider) ExpandAndRankRecipes(ctx context.Context, req FinderRankRequest) (*FinderRankResult, error) {
	op := AIOperation{
		Name:      "ExpandAndRankRecipes",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*FinderRankResult, error) {
		payload, err := json.Marshal(buildFinderRankPayload(req))
		if err != nil {
			return nil, fmt.Errorf("failed to marshal finder rank payload: %w", err)
		}

		// Require the top-level `ranked` array (nested index/expand are required
		// inside rankRecipesProperties) so the cheap light tier always emits the
		// expand flag instead of silently omitting it.
		rankSchema := schemaObject(rankRecipesProperties())
		rankSchema["required"] = []string{"ranked"}

		chatReq := openai.ChatCompletionRequest{
			Model: p.model,
			// 2048: the rank_recipes tool JSON for ~10 candidates with per-item
			// reason + safety[] + expand + broaden_queries runs ~1000-1500 output
			// tokens with a family profile; 512 truncated it into an unranked
			// fallback. Parsing is also truncation-tolerant as a backstop.
			MaxTokens: 2048,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: finderRankSystemPrompt},
				{Role: openai.ChatMessageRoleUser, Content: string(payload)},
			},
			Tools: []openai.Tool{{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "rank_recipes",
					Description: "Return the best-matching candidates by index, with rationales, per-member dietary safety, and broadened query suggestions.",
					Parameters:  rankSchema,
				},
			}},
			ToolChoice: openai.ToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ToolFunction{Name: "rank_recipes"},
			},
		}

		resp, err := p.createChatCompletion(ctx, chatReq)
		if err != nil {
			return nil, err
		}

		args, err := firstToolCallArguments(resp, "rank_recipes")
		if err != nil {
			return nil, err
		}

		tr, err := parseFinderRankToolArgs(args)
		if err != nil {
			return nil, err
		}
		return toolResultToFinderRankResult(tr), nil
	})
}

// DietaryInterview conducts a multi-turn dietary interview. The model is offered
// the save_dietary_profile tool with tool choice left on "auto": it keeps asking
// questions (plain-text turns → Complete=false) until it has gathered enough
// information, then calls the tool (Complete=true, Profile set, Response=wrap-up).
// Mirrors AnthropicProvider.DietaryInterview and extractDietaryInterviewFromMessage.
func (p *OpenAICompatProvider) DietaryInterview(ctx context.Context, messages []Message, memberName string) (*DietaryInterviewResult, error) {
	op := AIOperation{
		Name:      "DietaryInterview",
		Provider:  p.providerName,
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) (*DietaryInterviewResult, error) {
		sysSuffix, err := config.RenderPrompt(p.prompts.DietaryInterview.System, map[string]interface{}{
			"MemberName": memberName,
		})
		if err != nil {
			return nil, fmt.Errorf("render system prompt: %w", err)
		}

		chatMsgs := []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: combineSystemPrompt(p.prompts.DietaryInterview.SystemPrefix, sysSuffix)},
		}
		chatMsgs = append(chatMsgs, messagesToOpenAIParams(messages)...)

		// ToolChoice "auto" (not forced): let the model decide when the
		// interview has enough information to call save_dietary_profile.
		chatReq := openai.ChatCompletionRequest{
			Model:     p.model,
			MaxTokens: 1024,
			Messages:  chatMsgs,
			Tools: []openai.Tool{{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "save_dietary_profile",
					Description: "Save the completed dietary profile. Call this ONLY once the interview has gathered enough information to fill out the profile (allergies, intolerances, restrictions, and preferences have all been asked about).",
					Parameters:  schemaObject(dietaryProfileProperties()),
				},
			}},
			ToolChoice: "auto",
		}

		resp, err := p.createChatCompletion(ctx, chatReq)
		if err != nil {
			return nil, err
		}

		if resp == nil || len(resp.Choices) == 0 {
			return nil, NewAIError(FailureContentEmpty, errors.New("no choices in chat completion response"), "no choices in response")
		}

		msg := resp.Choices[0].Message
		text := msg.Content

		var profile *DietaryProfileResult
		for _, call := range msg.ToolCalls {
			if call.Function.Name != "save_dietary_profile" {
				continue
			}
			var tr dietaryProfileToolResult
			if err := json.Unmarshal([]byte(call.Function.Arguments), &tr); err != nil {
				return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal dietary profile: %w", err), "failed to parse dietary profile tool result")
			}
			profile = toolResultToDietaryProfile(&tr)
		}

		if profile != nil {
			if text == "" {
				text = dietaryWrapUpFallback
			}
			return &DietaryInterviewResult{Response: text, Complete: true, Profile: profile}, nil
		}

		if text == "" {
			return nil, NewAIError(FailureContentEmpty, errors.New("no text content in chat completion response"), "no text content in response")
		}
		return &DietaryInterviewResult{Response: text}, nil
	})
}
