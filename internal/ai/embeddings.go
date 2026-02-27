package ai

import (
	"context"
	"errors"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// EmbeddingProviderImpl implements EmbeddingProvider using OpenAI embeddings.
type EmbeddingProviderImpl struct {
	apiKey string
	model  openai.EmbeddingModel
}

// NewEmbeddingProvider creates a new embedding provider using
// text-embedding-3-small by default.
func NewEmbeddingProvider(apiKey string) *EmbeddingProviderImpl {
	return &EmbeddingProviderImpl{
		apiKey: apiKey,
		model:  openai.SmallEmbedding3,
	}
}

// GenerateEmbedding produces a vector embedding for the given text,
// suitable for pgvector storage.
func (p *EmbeddingProviderImpl) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, errors.New("embedding text is empty")
	}

	client := openai.NewClient(p.apiKey)
	const maxRetries = 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		resp, err := client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
			Model: p.model,
			Input: []string{text},
		})
		if err == nil {
			if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
				return nil, errors.New("embedding API returned empty result")
			}
			return resp.Data[0].Embedding, nil
		}

		lastErr = err
		shouldRetry, waitTime := classifyOpenAIError(err)
		if !shouldRetry {
			return nil, fmt.Errorf("embedding API error: %w", err)
		}

		logger.Get().Warn("embedding API error, retrying",
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

	return nil, fmt.Errorf("embedding API: exhausted %d retries: %w", maxRetries, lastErr)
}
