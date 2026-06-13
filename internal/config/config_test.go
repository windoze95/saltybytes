package config

import (
	"os"
	"testing"
)

// unsetenv removes an env var for the duration of the test, restoring it after.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	// t.Setenv registers the restore of the original value; Unsetenv then
	// clears it so envDefault applies during the test.
	t.Setenv(key, "")
	os.Unsetenv(key)
}

func TestLoadConfig_AnthropicModelDefaults(t *testing.T) {
	unsetenv(t, "ANTHROPIC_MODEL")
	unsetenv(t, "ANTHROPIC_LIGHT_MODEL")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got, want := cfg.EnvVars.AnthropicModel, "claude-sonnet-4-6"; got != want {
		t.Errorf("AnthropicModel = %q, want %q", got, want)
	}
	if got, want := cfg.EnvVars.AnthropicLightModel, "claude-haiku-4-5-20251001"; got != want {
		t.Errorf("AnthropicLightModel = %q, want %q", got, want)
	}
}

func TestLoadConfig_AnthropicModelOverrides(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "claude-opus-4-8")
	t.Setenv("ANTHROPIC_LIGHT_MODEL", "claude-haiku-4-5")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got, want := cfg.EnvVars.AnthropicModel, "claude-opus-4-8"; got != want {
		t.Errorf("AnthropicModel = %q, want %q", got, want)
	}
	if got, want := cfg.EnvVars.AnthropicLightModel, "claude-haiku-4-5"; got != want {
		t.Errorf("AnthropicLightModel = %q, want %q", got, want)
	}
}

func TestLoadConfig_PromptsPathDefaultAndOverride(t *testing.T) {
	unsetenv(t, "PROMPTS_PATH")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got, want := cfg.EnvVars.PromptsPath, "configs/prompts.yaml"; got != want {
		t.Errorf("PromptsPath default = %q, want %q", got, want)
	}

	t.Setenv("PROMPTS_PATH", "/etc/saltybytes/prompts.yaml")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got, want := cfg.EnvVars.PromptsPath, "/etc/saltybytes/prompts.yaml"; got != want {
		t.Errorf("PromptsPath override = %q, want %q", got, want)
	}
}

func TestCheckConfigEnvFields_OptionalModelFieldsSkipped(t *testing.T) {
	// A config with all required fields set must validate even when the
	// optional model/prompt fields are zero.
	cfg := &Config{EnvVars: EnvVars{
		Port:            "8080",
		DatabaseUrl:     "postgres://x",
		JwtSecretKey:    "secret",
		AWSRegion:       "us-east-2",
		S3Bucket:        "bucket",
		IDHeader:        "X-Id",
		AnthropicAPIKey: "key",
		OpenAIAPIKey:    "key",
	}}

	if err := cfg.CheckConfigEnvFields(); err != nil {
		t.Errorf("CheckConfigEnvFields() error = %v, want nil", err)
	}
}
