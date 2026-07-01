package ai

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/sashabaranov/go-openai"
)

func TestNewAIError_RetryabilityTaxonomy(t *testing.T) {
	tests := []struct {
		name      string
		kind      AIFailureKind
		retryable bool
	}{
		{"transient is retryable", FailureTransient, true},
		{"auth is not retryable", FailureAuth, false},
		{"content empty is retryable", FailureContentEmpty, true},
		{"content parse is not retryable", FailureContentParse, false},
		{"content quality is not retryable", FailureContentQuality, false},
		{"vision ambiguous is not retryable", FailureVisionAmbiguous, false},
		{"quota exhausted is not retryable", FailureQuotaExhausted, false},
		{"unknown is not retryable", FailureUnknown, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			aiErr := NewAIError(tc.kind, errors.New("boom"), "detail")
			if aiErr.Retryable != tc.retryable {
				t.Errorf("Retryable = %v, want %v", aiErr.Retryable, tc.retryable)
			}
			if aiErr.Kind != tc.kind {
				t.Errorf("Kind = %v, want %v", aiErr.Kind, tc.kind)
			}
		})
	}
}

func TestAIError_ErrorStringIncludesKindAndDetail(t *testing.T) {
	tests := []struct {
		kind     AIFailureKind
		wantKind string
	}{
		{FailureTransient, "transient"},
		{FailureAuth, "auth"},
		{FailureContentEmpty, "content_empty"},
		{FailureContentParse, "content_parse"},
		{FailureContentQuality, "content_quality"},
		{FailureVisionAmbiguous, "vision_ambiguous"},
		{FailureQuotaExhausted, "quota_exhausted"},
		{FailureUnknown, "unknown"},
		{AIFailureKind(99), "unknown"}, // out-of-range kinds fall back to "unknown"
	}

	for _, tc := range tests {
		t.Run(tc.wantKind, func(t *testing.T) {
			aiErr := &AIError{Kind: tc.kind, Err: errors.New("boom"), Detail: "some detail"}
			msg := aiErr.Error()
			if !strings.Contains(msg, "["+tc.wantKind+"]") {
				t.Errorf("Error() = %q, want kind tag [%s]", msg, tc.wantKind)
			}
			if !strings.Contains(msg, "some detail") {
				t.Errorf("Error() = %q, want detail included", msg)
			}
			if !strings.Contains(msg, "boom") {
				t.Errorf("Error() = %q, want wrapped error included", msg)
			}
		})
	}
}

func TestAIError_UnwrapExposesInnerError(t *testing.T) {
	sentinel := errors.New("sentinel failure")
	aiErr := NewAIError(FailureTransient, sentinel, "wrapping")

	if !errors.Is(aiErr, sentinel) {
		t.Error("errors.Is did not find the wrapped sentinel error")
	}

	// And through a further layer of wrapping.
	wrapped := fmt.Errorf("outer: %w", aiErr)
	var got *AIError
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As did not find *AIError through an outer wrap")
	}
	if got.Kind != FailureTransient {
		t.Errorf("Kind = %v, want FailureTransient", got.Kind)
	}
}

func newAnthropicAPIError(t *testing.T, status int) *anthropic.Error {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	return &anthropic.Error{
		StatusCode: status,
		Request:    req,
		Response:   &http.Response{StatusCode: status},
	}
}

func TestClassifyAnthropicError(t *testing.T) {
	tests := []struct {
		name      string
		err       func(t *testing.T) error
		wantKind  AIFailureKind
		retryable bool
	}{
		{
			name:      "429 rate limited is transient",
			err:       func(t *testing.T) error { return newAnthropicAPIError(t, http.StatusTooManyRequests) },
			wantKind:  FailureTransient,
			retryable: true,
		},
		{
			name:      "500 server error is transient",
			err:       func(t *testing.T) error { return newAnthropicAPIError(t, http.StatusInternalServerError) },
			wantKind:  FailureTransient,
			retryable: true,
		},
		{
			name:      "502 bad gateway is transient",
			err:       func(t *testing.T) error { return newAnthropicAPIError(t, http.StatusBadGateway) },
			wantKind:  FailureTransient,
			retryable: true,
		},
		{
			name:      "503 unavailable is transient",
			err:       func(t *testing.T) error { return newAnthropicAPIError(t, http.StatusServiceUnavailable) },
			wantKind:  FailureTransient,
			retryable: true,
		},
		{
			name:      "401 unauthorized is auth, not retryable",
			err:       func(t *testing.T) error { return newAnthropicAPIError(t, http.StatusUnauthorized) },
			wantKind:  FailureAuth,
			retryable: false,
		},
		{
			name:      "400 bad request is unknown, not retryable",
			err:       func(t *testing.T) error { return newAnthropicAPIError(t, http.StatusBadRequest) },
			wantKind:  FailureUnknown,
			retryable: false,
		},
		{
			name:      "plain error is unknown, not retryable",
			err:       func(t *testing.T) error { return errors.New("connection reset") },
			wantKind:  FailureUnknown,
			retryable: false,
		},
		{
			name: "wrapped API error is still classified",
			err: func(t *testing.T) error {
				return fmt.Errorf("call failed: %w", newAnthropicAPIError(t, http.StatusTooManyRequests))
			},
			wantKind:  FailureTransient,
			retryable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			aiErr := classifyAnthropicError(tc.err(t))
			if aiErr.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", aiErr.Kind, tc.wantKind)
			}
			if aiErr.Retryable != tc.retryable {
				t.Errorf("Retryable = %v, want %v", aiErr.Retryable, tc.retryable)
			}
		})
	}
}

func TestClassifyOpenAIError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantRetry bool
		wantWait  time.Duration
	}{
		{"429 retries", &openai.APIError{HTTPStatusCode: 429}, true, 2 * time.Second},
		{"500 retries", &openai.APIError{HTTPStatusCode: 500}, true, 2 * time.Second},
		{"502 retries", &openai.APIError{HTTPStatusCode: 502}, true, 2 * time.Second},
		{"503 retries", &openai.APIError{HTTPStatusCode: 503}, true, 2 * time.Second},
		{"400 does not retry", &openai.APIError{HTTPStatusCode: 400}, false, 0},
		{"401 does not retry", &openai.APIError{HTTPStatusCode: 401}, false, 0},
		{"plain error does not retry", errors.New("dial tcp: timeout"), false, 0},
		{"wrapped API error still classified", fmt.Errorf("outer: %w", &openai.APIError{HTTPStatusCode: 429}), true, 2 * time.Second},
		// Gemini's OpenAI-compat endpoint wraps error bodies in a JSON ARRAY,
		// which go-openai can't unmarshal into ErrorResponse — those surface as
		// *openai.RequestError, not *openai.APIError. They must retry by HTTP
		// status too (a prod Gemini 429 on 2026-07-01 was returned terminal
		// because only APIError was recognized).
		{"RequestError 429 (Gemini array body) retries", &openai.RequestError{HTTPStatusCode: 429, Err: errors.New("json: cannot unmarshal array into Go value of type openai.ErrorResponse")}, true, 2 * time.Second},
		{"RequestError 500 retries", &openai.RequestError{HTTPStatusCode: 500, Err: errors.New("boom")}, true, 2 * time.Second},
		{"RequestError 401 does not retry", &openai.RequestError{HTTPStatusCode: 401, Err: errors.New("bad key")}, false, 0},
		{"wrapped RequestError 429 still classified", fmt.Errorf("gemini chat completion error: %w", &openai.RequestError{HTTPStatusCode: 429, Err: errors.New("quota")}), true, 2 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRetry, gotWait := classifyOpenAIError(tc.err)
			if gotRetry != tc.wantRetry {
				t.Errorf("shouldRetry = %v, want %v", gotRetry, tc.wantRetry)
			}
			if gotWait != tc.wantWait {
				t.Errorf("waitTime = %v, want %v", gotWait, tc.wantWait)
			}
		})
	}
}
