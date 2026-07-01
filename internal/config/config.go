package config

import (
	"fmt"
	"reflect"

	"github.com/caarlos0/env/v11"
)

// Config holds the application configuration.
type Config struct {
	EnvVars EnvVars  `json:"env"`
	Prompts *Prompts `json:"-"`
}

// EnvVars holds environment variables required by the application.
// Fields tagged `optional:"true"` are skipped by CheckConfigEnvFields.
type EnvVars struct {
	Port               string `env:"PORT" envDefault:"8080"`
	DatabaseUrl        string `env:"DATABASE_URL"`
	JwtSecretKey       string `env:"JWT_SECRET_KEY"`
	AWSRegion          string `env:"AWS_REGION"`
	AWSAccessKeyID     string `env:"AWS_ACCESS_KEY_ID" optional:"true"`
	AWSSecretAccessKey string `env:"AWS_SECRET_ACCESS_KEY" optional:"true"`
	S3Bucket           string `env:"S3_BUCKET"`
	IDHeader           string `env:"ID_HEADER"`
	AnthropicAPIKey    string `env:"ANTHROPIC_API_KEY"`
	// AnthropicModel and AnthropicLightModel override the Claude model IDs
	// used for full-quality and cheap preview/extraction tasks respectively.
	AnthropicModel      string `env:"ANTHROPIC_MODEL" envDefault:"claude-sonnet-4-6" optional:"true"`
	AnthropicLightModel string `env:"ANTHROPIC_LIGHT_MODEL" envDefault:"claude-haiku-4-5-20251001" optional:"true"`
	OpenAIAPIKey        string `env:"OPENAI_API_KEY"`
	// Light-tier provider selection. LightProvider defaults to "anthropic"
	// (Haiku); set to "openai"/"gemini"/"deepseek" to run a cheaper model for the
	// high-volume preview/extraction tasks. LightModel/LightBaseURL override the
	// model ID and endpoint; empty LightBaseURL uses the provider's default.
	// This is the startup default — the dashboard live-switch overrides it via DB.
	LightProvider string `env:"LIGHT_PROVIDER" envDefault:"anthropic" optional:"true"`
	LightModel    string `env:"LIGHT_MODEL" optional:"true"`
	LightBaseURL  string `env:"LIGHT_BASE_URL" optional:"true"`
	// Main-tier provider selection: the flagship reasoning tier (recipe
	// generation/regen/fork, allergens, dietary). Defaults to "anthropic" (Sonnet);
	// set to "gemini"/"openai"/"deepseek" to A/B a cheaper frontier model
	// (e.g. gemini-2.5-pro). MainModel/MainBaseURL override the model + endpoint.
	// Streaming generation gracefully falls back to non-streaming for non-Anthropic
	// providers. Behavior-preserving while unset.
	MainProvider   string `env:"MAIN_PROVIDER" envDefault:"anthropic" optional:"true"`
	MainModel      string `env:"MAIN_MODEL" optional:"true"`
	MainBaseURL    string `env:"MAIN_BASE_URL" optional:"true"`
	GeminiAPIKey   string `env:"GEMINI_API_KEY" optional:"true"`
	DeepSeekAPIKey string `env:"DEEPSEEK_API_KEY" optional:"true"`
	// AdminToken guards the admin API (the dashboard's live model-switch +
	// registry endpoints). When empty the admin API is disabled entirely, so a
	// deploy without the secret can never expose those endpoints.
	AdminToken string `env:"ADMIN_TOKEN" optional:"true"`
	// VideoNativeGemini routes video import through native Gemini video+audio
	// extraction (far cheaper than sampling frames onto Sonnet, and it reads the
	// narration natively). Requires GEMINI_API_KEY. Falls back to frame sampling
	// per-video when the clip is too large to inline or native extraction fails.
	VideoNativeGemini bool   `env:"VIDEO_NATIVE_GEMINI" optional:"true"`
	GeminiVideoModel  string `env:"GEMINI_VIDEO_MODEL" envDefault:"gemini-2.5-flash" optional:"true"`
	// VisionNativeGemini routes image/PDF recipe extraction (photo + files import,
	// and the video frame-sampling fallback) through Gemini instead of Sonnet.
	// Requires GEMINI_API_KEY. Falls back to the Sonnet vision provider when off.
	VisionNativeGemini bool   `env:"VISION_NATIVE_GEMINI" optional:"true"`
	GeminiVisionModel  string `env:"GEMINI_VISION_MODEL" envDefault:"gemini-2.5-flash" optional:"true"`
	// PromptsPath overrides the location of the prompts YAML file, which is
	// cwd-relative by default and only resolves from the Docker workdir.
	PromptsPath     string `env:"PROMPTS_PATH" envDefault:"configs/prompts.yaml" optional:"true"`
	GoogleSearchKey string `env:"GOOGLE_SEARCH_KEY" optional:"true"`
	GoogleSearchCX  string `env:"GOOGLE_SEARCH_CX" optional:"true"`
	BraveSearchKey  string `env:"BRAVE_SEARCH_KEY" optional:"true"`
	FirecrawlAPIKey string `env:"FIRECRAWL_API_KEY" optional:"true"`
	// ScrapeCreatorsAPIKey enables video-link import (TikTok/Instagram/YouTube/
	// Facebook/Pinterest). When empty, video import is disabled.
	ScrapeCreatorsAPIKey string `env:"SCRAPECREATORS_API_KEY" optional:"true"`
	// VideoImportDailyBudgetUSD is the global daily spend ceiling for fresh video
	// extractions; once the day's metered cost exceeds it, fresh extractions are
	// refused (cache hits still serve). The kill switch.
	VideoImportDailyBudgetUSD float64 `env:"VIDEO_IMPORT_DAILY_BUDGET_USD" envDefault:"25" optional:"true"`
	// RecipeWarmingConcurrency bounds how many search results extract in parallel
	// during proactive cache warming.
	RecipeWarmingConcurrency int `env:"RECIPE_WARMING_CONCURRENCY" envDefault:"6" optional:"true"`
	// RecipeWarmingDailyLimit is a daily kill-switch on the number of proactive
	// warm extractions (a runaway guard, intentionally high — not a normal cap;
	// JSON-LD/AI gap-fill runs freely under it). 0 disables the ceiling.
	RecipeWarmingDailyLimit int `env:"RECIPE_WARMING_DAILY_LIMIT" envDefault:"5000" optional:"true"`
}

// LoadConfig parses environment variables into the Config struct.
func LoadConfig() (*Config, error) {
	var config Config
	if err := env.Parse(&config.EnvVars); err != nil {
		return nil, err
	}
	return &config, nil
}

// CheckConfigEnvFields validates that all required EnvVars fields are set.
func (c *Config) CheckConfigEnvFields() error {
	return checkFieldsRecursive(reflect.ValueOf(c.EnvVars))
}

func checkFieldsRecursive(v reflect.Value) error {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := v.Type().Field(i)
		if fieldType.Tag.Get("optional") == "true" {
			continue
		}
		if isZeroValue(field) {
			return fmt.Errorf("$%s must be set", fieldType.Name)
		}
		if field.Kind() == reflect.Struct {
			if err := checkFieldsRecursive(field); err != nil {
				return err
			}
		}
	}
	return nil
}

func isZeroValue(v reflect.Value) bool {
	return v.Interface() == reflect.Zero(v.Type()).Interface()
}
