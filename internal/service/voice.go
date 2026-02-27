package service

import (
	"context"
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// VoiceService handles voice command processing for cooking mode.
type VoiceService struct {
	Cfg            *config.Config
	TextProvider   ai.TextProvider
	SpeechProvider ai.SpeechProvider
}

// NewVoiceService creates a new VoiceService.
func NewVoiceService(cfg *config.Config, textProvider ai.TextProvider, speechProvider ai.SpeechProvider) *VoiceService {
	return &VoiceService{
		Cfg:            cfg,
		TextProvider:   textProvider,
		SpeechProvider: speechProvider,
	}
}

// ProcessVoiceCommand transcribes audio and classifies the user's intent.
func (s *VoiceService) ProcessVoiceCommand(ctx context.Context, audioData []byte) (*ai.VoiceIntent, error) {
	transcript, err := s.SpeechProvider.TranscribeAudio(ctx, audioData)
	if err != nil {
		return nil, fmt.Errorf("transcribe audio: %w", err)
	}

	intent, err := s.TextProvider.ClassifyVoiceIntent(ctx, transcript)
	if err != nil {
		return nil, fmt.Errorf("classify voice intent: %w", err)
	}

	return intent, nil
}

// AnswerCookingQuestion uses the AI to answer a cooking Q&A question.
func (s *VoiceService) AnswerCookingQuestion(ctx context.Context, question string, recipeContext string) (string, error) {
	return s.TextProvider.CookingQA(ctx, question, recipeContext)
}
