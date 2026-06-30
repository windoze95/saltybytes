package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
)

// Default endpoints and models per light-tier provider. Used when a spec leaves
// them blank. Centralised here so the env-default path (router) and the
// DB-driven live-switch (model manager) resolve identically.
const (
	geminiDefaultBaseURL   = "https://generativelanguage.googleapis.com/v1beta/openai"
	deepseekDefaultBaseURL = "https://api.deepseek.com"

	openAIDefaultModel   = "gpt-4o-mini"
	geminiDefaultModel   = "gemini-2.0-flash"
	deepseekDefaultModel = "deepseek-chat"
)

// LightProviderSpec identifies a light-tier model: which provider, which model
// id, and an optional endpoint override. The zero Provider ("") is treated as
// "anthropic" (the Haiku default).
type LightProviderSpec struct {
	Provider string `json:"provider"` // anthropic|openai|gemini|deepseek
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
}

// LightKeys carries the API keys (sourced from env/SSM, never the DB) used to
// build a light-tier provider, plus the Anthropic light-model default.
type LightKeys struct {
	AnthropicAPIKey     string
	AnthropicLightModel string
	OpenAIAPIKey        string
	GeminiAPIKey        string
	DeepSeekAPIKey      string
}

// BuildLightProvider constructs the cheap "light" tier TextProvider for a spec.
// Anthropic uses the Claude Haiku client; openai/gemini/deepseek use the shared
// OpenAI-compatible provider with the appropriate base URL. mw (cost metering +
// logging) is attached when non-nil. Returns an error when the required API key
// for the selected provider is missing or the provider name is unknown — so a
// misconfigured switch fails loudly instead of silently doing nothing.
func BuildLightProvider(spec LightProviderSpec, keys LightKeys, prompts *config.Prompts, mw AIMiddleware) (TextProvider, error) {
	switch spec.Provider {
	case "openai":
		if keys.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("light provider openai selected but OPENAI_API_KEY is not set")
		}
		model := spec.Model
		if model == "" {
			model = openAIDefaultModel
		}
		p := NewOpenAICompatProvider(keys.OpenAIAPIKey, spec.BaseURL, model, "openai", prompts)
		if mw != nil {
			p.WithMiddleware(mw)
		}
		return p, nil
	case "gemini":
		if keys.GeminiAPIKey == "" {
			return nil, fmt.Errorf("light provider gemini selected but GEMINI_API_KEY is not set")
		}
		baseURL := spec.BaseURL
		if baseURL == "" {
			baseURL = geminiDefaultBaseURL
		}
		model := spec.Model
		if model == "" {
			model = geminiDefaultModel
		}
		p := NewOpenAICompatProvider(keys.GeminiAPIKey, baseURL, model, "gemini", prompts)
		if mw != nil {
			p.WithMiddleware(mw)
		}
		return p, nil
	case "deepseek":
		if keys.DeepSeekAPIKey == "" {
			return nil, fmt.Errorf("light provider deepseek selected but DEEPSEEK_API_KEY is not set")
		}
		baseURL := spec.BaseURL
		if baseURL == "" {
			baseURL = deepseekDefaultBaseURL
		}
		model := spec.Model
		if model == "" {
			model = deepseekDefaultModel
		}
		p := NewOpenAICompatProvider(keys.DeepSeekAPIKey, baseURL, model, "deepseek", prompts)
		if mw != nil {
			p.WithMiddleware(mw)
		}
		return p, nil
	case "anthropic", "":
		model := spec.Model
		if model == "" {
			model = keys.AnthropicLightModel
		}
		p := NewAnthropicLightProvider(keys.AnthropicAPIKey, model, prompts)
		if mw != nil {
			p.WithMiddleware(mw)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown light provider %q", spec.Provider)
	}
}

// validationProbeText is a trivial recipe used by ValidateModel. It is small but
// complete enough that any working extraction model returns a structured recipe.
const validationProbeText = "Buttered toast.\nIngredients: 2 slices of bread, 1 tablespoon butter.\nSteps: 1. Toast the bread until golden. 2. Spread the butter evenly."

// ValidateModel runs a live validation probe against a spec: it builds the
// provider and performs one real minimal extraction. Success proves the model
// id exists, the API key works, the endpoint is reachable, and the model
// supports the forced function-calling the app relies on. A non-nil error means
// the model must NOT be activated (fail-closed). The probe is unmetered (no
// middleware) so it never pollutes production usage stats, and is bounded by a
// timeout so a hung endpoint can't block a switch indefinitely.
func ValidateModel(ctx context.Context, spec LightProviderSpec, keys LightKeys, prompts *config.Prompts) error {
	p, err := BuildLightProvider(spec, keys, prompts, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := p.ExtractRecipeFromText(ctx, validationProbeText, "metric"); err != nil {
		return fmt.Errorf("validation probe failed: %w", err)
	}
	return nil
}
