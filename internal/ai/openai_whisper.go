package ai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// WhisperProvider implements SpeechProvider using OpenAI Whisper.
type WhisperProvider struct {
	apiKey string
}

// NewWhisperProvider creates a new Whisper speech-to-text provider.
func NewWhisperProvider(apiKey string) *WhisperProvider {
	return &WhisperProvider{apiKey: apiKey}
}

// TranscribeAudio transcribes audio data to text using Whisper.
func (p *WhisperProvider) TranscribeAudio(ctx context.Context, audioData []byte) (string, error) {
	if len(audioData) == 0 {
		return "", errors.New("audio data is empty")
	}

	client := openai.NewClient(p.apiKey)
	const maxRetries = 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		resp, err := client.CreateTranscription(ctx, openai.AudioRequest{
			Model:    openai.Whisper1,
			Reader:   bytes.NewReader(audioData),
			FilePath: "audio.webm",
		})
		if err == nil {
			if resp.Text == "" {
				return "", errors.New("Whisper returned empty transcription")
			}
			return resp.Text, nil
		}

		lastErr = err
		shouldRetry, waitTime := classifyOpenAIError(err)
		if !shouldRetry {
			return "", fmt.Errorf("Whisper API error: %w", err)
		}

		logger.Get().Warn("Whisper API error, retrying",
			zap.Error(err),
			zap.Int("attempt", i+1),
		)

		if i < maxRetries-1 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(waitTime * time.Duration(i+1)):
			}
		}
	}

	return "", fmt.Errorf("Whisper API: exhausted %d retries: %w", maxRetries, lastErr)
}
