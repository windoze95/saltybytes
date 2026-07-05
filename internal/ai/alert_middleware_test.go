package ai

import "testing"

func TestIsCriticalProviderError(t *testing.T) {
	critical := []string{
		"Your credit balance is too low to access the Anthropic API",
		"401 Unauthorized: invalid x-api-key",
		"authentication_error: invalid bearer token",
		"insufficient_quota: You exceeded your current quota, please check your plan and billing details",
		"402 Payment Required",
		"account is not active",
	}
	for _, msg := range critical {
		if !isCriticalProviderError(msg) {
			t.Errorf("isCriticalProviderError(%q) = false, want true", msg)
		}
	}

	ordinary := []string{
		"context deadline exceeded",
		"429 Too Many Requests: rate_limit_error",
		"500 Internal Server Error",
		"overloaded_error: the API is temporarily overloaded",
		"connection reset by peer",
	}
	for _, msg := range ordinary {
		if isCriticalProviderError(msg) {
			t.Errorf("isCriticalProviderError(%q) = true, want false (ordinary flake)", msg)
		}
	}
}

func TestProviderConsoleURL(t *testing.T) {
	for provider, wantNonEmpty := range map[string]bool{
		"anthropic": true,
		"openai":    true,
		"gemini":    true,
		"deepseek":  true,
		"someone":   false,
	} {
		got := providerConsoleURL(provider)
		if (got != "") != wantNonEmpty {
			t.Errorf("providerConsoleURL(%q) = %q", provider, got)
		}
	}
}
