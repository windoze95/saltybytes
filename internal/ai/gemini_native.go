package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
)

// maxInlineVideoBytes caps the size of a video sent inline (base64) in a single
// Gemini generateContent request. Larger videos must be uploaded via the Files
// API or handled by frame sampling; the caller falls back on ErrVideoTooLarge.
const maxInlineVideoBytes = 18 << 20 // 18 MiB

// ErrVideoTooLarge signals that a video exceeds the inline size limit. The
// caller uses it to fall back to frame sampling instead of native ingestion.
var ErrVideoTooLarge = errors.New("video exceeds inline size limit")

// GeminiVideoProvider extracts a recipe from a whole short video by sending the
// video inline (base64) to Google's native Gemini generateContent API and
// forcing a create_recipe function call. It deliberately reuses the shared
// recipe schema (recipeProperties), parsing (recipeToolResult /
// toolResultToRecipeResult) and validation (validateRecipeResult) helpers so
// its output is byte-for-byte faithful to the other providers.
type GeminiVideoProvider struct {
	apiKey     string
	model      string
	baseURL    string
	prompts    *config.Prompts
	middleware AIMiddleware // nil means no middleware
	http       *http.Client
}

// Compile-time assurance the provider satisfies the VideoProvider interface.
var _ VideoProvider = (*GeminiVideoProvider)(nil)

// NewGeminiVideoProvider creates a native Gemini video-extraction provider. An
// empty model defaults to gemini-2.5-flash.
func NewGeminiVideoProvider(apiKey, model string, prompts *config.Prompts) *GeminiVideoProvider {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &GeminiVideoProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://generativelanguage.googleapis.com/v1beta",
		prompts: prompts,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// WithMiddleware sets the middleware chain for this provider.
func (p *GeminiVideoProvider) WithMiddleware(mw AIMiddleware) {
	p.middleware = mw
}

// --- Native Gemini generateContent wire types (camelCase JSON names) ---

// geminiInlineData carries base64-encoded media inline in a request part.
type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// geminiFunctionCall is a model-emitted function call in a response part.
// Gemini returns args as a JSON object; capture it raw for later unmarshalling.
type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// geminiPart is one heterogeneous element of a content's parts list. A request
// part carries text or inlineData; a response part may carry a functionCall.
type geminiPart struct {
	Text         string              `json:"text,omitempty"`
	InlineData   *geminiInlineData   `json:"inlineData,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
}

// geminiContent is a role-tagged list of parts (used for user content, the
// system instruction, and response candidates).
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

// geminiFunctionDeclaration declares a callable tool function and its JSON
// schema parameters.
type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// geminiTool groups function declarations exposed to the model.
type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

// geminiFunctionCallingConfig forces / constrains function calling. Mode "ANY"
// with a single allowed name makes the call mandatory.
type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// geminiToolConfig wraps the function-calling configuration.
type geminiToolConfig struct {
	FunctionCallingConfig geminiFunctionCallingConfig `json:"functionCallingConfig"`
}

// geminiGenerationConfig holds generation parameters.
type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// geminiGenerateContentRequest is the request body for :generateContent.
type geminiGenerateContentRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

// geminiUsageMetadata reports token consumption for cost metering.
type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

// geminiCandidate is one generated candidate.
type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

// geminiGenerateContentResponse is the response body for :generateContent.
type geminiGenerateContentResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

// ExtractRecipesFromVideo sends a whole short video inline to Gemini and returns
// the single recipe extracted via a forced create_recipe function call. It
// returns ErrVideoTooLarge (without hitting the network) when the video exceeds
// the inline size limit so the caller can fall back to frame sampling.
func (p *GeminiVideoProvider) ExtractRecipesFromVideo(ctx context.Context, videoData []byte, mimeType, contextText, unitSystem, requirements string) ([]*RecipeResult, error) {
	if len(videoData) == 0 {
		return nil, fmt.Errorf("gemini: empty video data")
	}
	if len(videoData) > maxInlineVideoBytes {
		return nil, ErrVideoTooLarge
	}
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	op := AIOperation{
		Name:      "ExtractRecipesFromVideo",
		Provider:  "gemini",
		Model:     p.model,
		StartTime: time.Now(),
	}

	return runWithMiddleware(ctx, p.middleware, op, func(ctx context.Context) ([]*RecipeResult, error) {
		// Reuse the exact vision prompt fields the frame-sampling path uses so
		// native ingestion stays consistent with the other providers.
		sysSuffix, err := config.RenderPrompt(p.prompts.Recipe.Import.Vision.System, map[string]interface{}{
			"UnitSystem":   unitSystem,
			"Requirements": requirements,
		})
		if err != nil {
			return nil, fmt.Errorf("render system prompt: %w", err)
		}
		systemPrompt := combineSystemPrompt(p.prompts.Recipe.Import.Vision.SystemPrefix, sysSuffix)

		// User parts: the inline video followed by the framing text. Mirrors the
		// context framing used by ExtractRecipesFromMedia.
		framing := ""
		if contextText != "" {
			framing = "The following is the spoken transcript and caption from the source video. Treat it as a primary source of the ingredient quantities and step order; the video shows on-screen text and amounts too — reconcile them.\n\n" + contextText + "\n\n"
		}
		framing += "This video contains a recipe. Extract it and call create_recipe. If it shows a prepared dish with no written recipe, infer a reasonable one."

		reqBody := geminiGenerateContentRequest{
			Contents: []geminiContent{{
				Role: "user",
				Parts: []geminiPart{
					{InlineData: &geminiInlineData{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(videoData)}},
					{Text: framing},
				},
			}},
			Tools: []geminiTool{{
				FunctionDeclarations: []geminiFunctionDeclaration{{
					Name:        "create_recipe",
					Description: "Create a structured recipe definition with all required fields.",
					Parameters:  schemaObject(recipeProperties(p.prompts.Recipe.Summarize.Recipe)),
				}},
			}},
			ToolConfig: &geminiToolConfig{
				FunctionCallingConfig: geminiFunctionCallingConfig{
					Mode:                 "ANY",
					AllowedFunctionNames: []string{"create_recipe"},
				},
			},
			GenerationConfig: &geminiGenerationConfig{MaxOutputTokens: 8192},
		}
		if systemPrompt != "" {
			reqBody.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: systemPrompt}}}
		}

		resp, err := p.generateContent(ctx, reqBody)
		if err != nil {
			return nil, err
		}

		recordUsage(ctx, TokenUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		})

		args, ok := firstFunctionCallArgs(resp, "create_recipe")
		if !ok {
			return nil, NewAIError(FailureContentEmpty, errors.New("no create_recipe function call in Gemini response"), "no recipe function call in response")
		}

		var tr recipeToolResult
		if err := json.Unmarshal(args, &tr); err != nil {
			return nil, NewAIError(FailureContentParse, fmt.Errorf("failed to unmarshal recipe: %w", err), "failed to parse recipe tool result")
		}

		result := toolResultToRecipeResult(&tr)
		if err := validateRecipeResult(result); err != nil {
			return nil, err
		}
		result.PromptVersion = config.PromptVersion(p.prompts)
		return []*RecipeResult{result}, nil
	})
}

// firstFunctionCallArgs returns the raw args of the first response part carrying
// a function call with the given name.
func firstFunctionCallArgs(resp *geminiGenerateContentResponse, name string) (json.RawMessage, bool) {
	if resp == nil {
		return nil, false
	}
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil && part.FunctionCall.Name == name {
				return part.FunctionCall.Args, true
			}
		}
	}
	return nil, false
}

// generateContent POSTs the request to {baseURL}/models/{model}:generateContent
// and returns the parsed response. It retries a bounded number of times on HTTP
// 429 / 5xx (and transport errors) with a short, context-honoring backoff.
func (p *GeminiVideoProvider) generateContent(ctx context.Context, reqBody geminiGenerateContentRequest) (*geminiGenerateContentResponse, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/models/%s:generateContent", p.baseURL, p.model)

	const maxAttempts = 3
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := backoffWait(ctx, attempt); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", p.apiKey)

		resp, err := p.httpClient().Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gemini request failed: %w", err)
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read gemini response: %w", readErr)
			continue
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			var out geminiGenerateContentResponse
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, NewAIError(FailureContentParse, fmt.Errorf("unmarshal gemini response: %w", err), "failed to parse gemini response")
			}
			return &out, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("gemini generateContent returned status %d: %s", resp.StatusCode, truncateBody(body))
			continue
		default:
			return nil, fmt.Errorf("gemini generateContent returned status %d: %s", resp.StatusCode, truncateBody(body))
		}
	}

	return nil, fmt.Errorf("gemini generateContent: exhausted %d attempts: %w", maxAttempts, lastErr)
}

// httpClient returns the injected client or a default one.
func (p *GeminiVideoProvider) httpClient() *http.Client {
	if p.http != nil {
		return p.http
	}
	return http.DefaultClient
}

// backoffWait sleeps for a short, attempt-scaled interval, returning early if
// the context is cancelled.
func backoffWait(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt) * 250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncateBody renders a response body for error messages, capping its length.
func truncateBody(b []byte) string {
	const max = 512
	if len(b) > max {
		return string(b[:max]) + "...(truncated)"
	}
	return string(b)
}
