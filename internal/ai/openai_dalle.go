package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// DALLEProvider implements ImageProvider using OpenAI DALL-E 3.
type DALLEProvider struct {
	apiKey string
}

// NewDALLEProvider creates a new DALL-E image generation provider.
func NewDALLEProvider(apiKey string) *DALLEProvider {
	return &DALLEProvider{apiKey: apiKey}
}

// GenerateImage generates an image using DALL-E 3 and returns the raw bytes.
func (p *DALLEProvider) GenerateImage(ctx context.Context, prompt string) ([]byte, error) {
	if prompt == "" {
		return nil, errors.New("image prompt is empty")
	}

	client := openai.NewClient(p.apiKey)
	const maxRetries = 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		resp, err := client.CreateImage(ctx, openai.ImageRequest{
			Model:          openai.CreateImageModelDallE3,
			Prompt:         prompt,
			Size:           openai.CreateImageSize1024x1024,
			ResponseFormat: openai.CreateImageResponseFormatB64JSON,
			N:              1,
		})
		if err == nil {
			if len(resp.Data) == 0 || resp.Data[0].B64JSON == "" {
				return nil, errors.New("DALL-E API returned an empty image")
			}
			imgBytes, decErr := base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
			if decErr != nil {
				return nil, fmt.Errorf("base64 decode error: %w", decErr)
			}
			return imgBytes, nil
		}

		lastErr = err
		shouldRetry, waitTime := classifyOpenAIError(err)
		if !shouldRetry {
			return nil, fmt.Errorf("DALL-E API error: %w", err)
		}

		logger.Get().Warn("DALL-E API error, retrying",
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

	return nil, fmt.Errorf("DALL-E API: exhausted %d retries: %w", maxRetries, lastErr)
}

// classifyOpenAIError determines whether an OpenAI API error is retryable.
func classifyOpenAIError(err error) (shouldRetry bool, waitTime time.Duration) {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 429:
			return true, 2 * time.Second
		case 500, 502, 503:
			return true, 2 * time.Second
		default:
			return false, 0
		}
	}
	return false, 0
}
