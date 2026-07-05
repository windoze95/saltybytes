package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/windoze95/saltybytes-api/internal/notify"
)

// ErrorAlertMiddleware pages the operator (ntfy) when an AI provider fails
// in a way that needs a human: authentication and billing errors take every
// AI feature down at once. It has happened — an exhausted Anthropic credit
// balance once 400'd the whole app until someone noticed organically. The
// alert links straight to the provider's billing console.
type ErrorAlertMiddleware struct{}

// Before implements AIMiddleware.
func (m *ErrorAlertMiddleware) Before(ctx context.Context, op AIOperation) context.Context {
	return ctx
}

// After inspects failures and alerts on credential/billing-class errors.
func (m *ErrorAlertMiddleware) After(ctx context.Context, result AIOperationResult) {
	if result.Err == nil {
		return
	}
	if !isCriticalProviderError(result.Err.Error()) {
		return
	}

	provider := result.Operation.Provider
	errText := result.Err.Error()
	if len(errText) > 300 {
		errText = errText[:300] + "…"
	}
	notify.Alert(
		"ai-provider-"+provider,
		fmt.Sprintf("SaltyBytes: %s API failing", provider),
		fmt.Sprintf("%s call %q failed: %s\n\nThis looks like a billing or credentials problem — every AI feature is affected until it's fixed.",
			provider, result.Operation.Name, errText),
		providerConsoleURL(provider),
		time.Hour,
	)
}

// isCriticalProviderError reports whether an AI provider error looks like a
// billing/credentials failure (page-worthy) rather than an ordinary flake.
func isCriticalProviderError(errText string) bool {
	msg := strings.ToLower(errText)
	for _, marker := range []string{
		"credit balance",
		"insufficient_quota",
		"insufficient credits",
		"billing",
		"payment",
		"invalid x-api-key",
		"invalid api key",
		"authentication_error",
		"permission_error",
		"account is not active",
		"401",
		"402",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// providerConsoleURL is the "go here to fix it" link per provider.
func providerConsoleURL(provider string) string {
	switch provider {
	case "anthropic":
		return "https://console.anthropic.com/settings/billing"
	case "openai":
		return "https://platform.openai.com/settings/organization/billing/overview"
	case "gemini":
		return "https://aistudio.google.com/app/apikey"
	case "deepseek":
		return "https://platform.deepseek.com/usage"
	default:
		return ""
	}
}
