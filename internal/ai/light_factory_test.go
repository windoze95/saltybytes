package ai

import "testing"

func TestBuildLightProvider_RequiresKey(t *testing.T) {
	prompts := testPrompts()
	noKeys := LightKeys{}

	for _, provider := range []string{"openai", "gemini", "deepseek"} {
		if _, err := BuildLightProvider(LightProviderSpec{Provider: provider}, noKeys, prompts, nil); err == nil {
			t.Errorf("BuildLightProvider(%s) with no key: expected error, got nil", provider)
		}
	}
}

func TestBuildLightProvider_BuildsWithKeys(t *testing.T) {
	prompts := testPrompts()
	keys := LightKeys{
		AnthropicAPIKey: "k",
		OpenAIAPIKey:    "k",
		GeminiAPIKey:    "k",
		DeepSeekAPIKey:  "k",
	}

	for _, provider := range []string{"openai", "gemini", "deepseek", "anthropic", ""} {
		p, err := BuildLightProvider(LightProviderSpec{Provider: provider}, keys, prompts, nil)
		if err != nil {
			t.Errorf("BuildLightProvider(%q): unexpected error %v", provider, err)
			continue
		}
		if p == nil {
			t.Errorf("BuildLightProvider(%q): nil provider", provider)
		}
	}
}

func TestBuildLightProvider_UnknownProvider(t *testing.T) {
	if _, err := BuildLightProvider(LightProviderSpec{Provider: "bananas"}, LightKeys{}, testPrompts(), nil); err == nil {
		t.Error("BuildLightProvider(bananas): expected error for unknown provider")
	}
}
