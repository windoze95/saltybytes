package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func TestNewVoiceService_WiresDependencies(t *testing.T) {
	cfg := &config.Config{}
	text := &testutil.MockTextProvider{}
	speech := &testutil.MockSpeechProvider{}

	svc := NewVoiceService(cfg, text, speech)
	if svc.Cfg != cfg {
		t.Error("NewVoiceService did not keep the config")
	}
	if svc.TextProvider != ai.TextProvider(text) {
		t.Error("NewVoiceService did not keep the text provider")
	}
	if svc.SpeechProvider != ai.SpeechProvider(speech) {
		t.Error("NewVoiceService did not keep the speech provider")
	}
}

func TestProcessVoiceCommand_TranscriptFlowsToClassifier(t *testing.T) {
	audio := []byte{0x1a, 0x45, 0xdf, 0xa3} // webm magic bytes
	var gotAudio []byte
	var gotFormat string
	var gotTranscript string

	speech := &testutil.MockSpeechProvider{
		TranscribeAudioFunc: func(ctx context.Context, audioData []byte, format string) (string, error) {
			gotAudio = audioData
			gotFormat = format
			return "scroll down a bit", nil
		},
	}
	text := &testutil.MockTextProvider{
		ClassifyVoiceIntentFunc: func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
			gotTranscript = transcript
			return &ai.VoiceIntent{Type: "scroll_down", Amount: "small"}, nil
		},
	}

	svc := NewVoiceService(&config.Config{}, text, speech)
	intent, err := svc.ProcessVoiceCommand(context.Background(), audio, "m4a")
	if err != nil {
		t.Fatalf("ProcessVoiceCommand error: %v", err)
	}

	if !bytes.Equal(gotAudio, audio) {
		t.Error("audio bytes were not passed to the speech provider unchanged")
	}
	if gotFormat != "m4a" {
		t.Errorf("speech provider format = %q, want 'm4a'", gotFormat)
	}
	if gotTranscript != "scroll down a bit" {
		t.Errorf("classifier transcript = %q, want the Whisper output", gotTranscript)
	}
	if intent.Type != "scroll_down" || intent.Amount != "small" {
		t.Errorf("intent = %+v, want scroll_down/small", intent)
	}
}

func TestProcessVoiceCommand_TranscribeError(t *testing.T) {
	classifierCalled := false
	speech := &testutil.MockSpeechProvider{
		TranscribeAudioFunc: func(ctx context.Context, audioData []byte, format string) (string, error) {
			return "", errors.New("whisper down")
		},
	}
	text := &testutil.MockTextProvider{
		ClassifyVoiceIntentFunc: func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
			classifierCalled = true
			return &ai.VoiceIntent{Type: "ignore"}, nil
		},
	}

	svc := NewVoiceService(&config.Config{}, text, speech)
	intent, err := svc.ProcessVoiceCommand(context.Background(), []byte("audio"), "")
	if err == nil {
		t.Fatal("ProcessVoiceCommand should fail when transcription fails")
	}
	if intent != nil {
		t.Errorf("intent = %+v, want nil on error", intent)
	}
	if !strings.Contains(err.Error(), "transcribe audio") {
		t.Errorf("error = %q, want it wrapped with 'transcribe audio'", err)
	}
	if !strings.Contains(err.Error(), "whisper down") {
		t.Errorf("error = %q, want the underlying cause preserved", err)
	}
	if classifierCalled {
		t.Error("intent classification ran even though transcription failed")
	}
}

func TestProcessVoiceCommand_ClassifyError(t *testing.T) {
	speech := &testutil.MockSpeechProvider{
		TranscribeAudioFunc: func(ctx context.Context, audioData []byte, format string) (string, error) {
			return "what temperature for the oven", nil
		},
	}
	text := &testutil.MockTextProvider{
		ClassifyVoiceIntentFunc: func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
			return nil, errors.New("claude down")
		},
	}

	svc := NewVoiceService(&config.Config{}, text, speech)
	intent, err := svc.ProcessVoiceCommand(context.Background(), []byte("audio"), "webm")
	if err == nil {
		t.Fatal("ProcessVoiceCommand should fail when classification fails")
	}
	if intent != nil {
		t.Errorf("intent = %+v, want nil on error", intent)
	}
	if !strings.Contains(err.Error(), "classify voice intent") {
		t.Errorf("error = %q, want it wrapped with 'classify voice intent'", err)
	}
}

func TestProcessVoiceCommand_EmptyTranscriptionStillClassified(t *testing.T) {
	// Documents current behavior: an empty (silent) transcription is not an
	// error in the service; it is forwarded to the classifier, which is
	// expected to return an "ignore" intent.
	var gotTranscript = "sentinel"
	speech := &testutil.MockSpeechProvider{
		TranscribeAudioFunc: func(ctx context.Context, audioData []byte, format string) (string, error) {
			return "", nil
		},
	}
	text := &testutil.MockTextProvider{
		ClassifyVoiceIntentFunc: func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
			gotTranscript = transcript
			return &ai.VoiceIntent{Type: "ignore"}, nil
		},
	}

	svc := NewVoiceService(&config.Config{}, text, speech)
	intent, err := svc.ProcessVoiceCommand(context.Background(), []byte("silence"), "webm")
	if err != nil {
		t.Fatalf("ProcessVoiceCommand error: %v", err)
	}
	if gotTranscript != "" {
		t.Errorf("classifier received %q, want the empty transcript", gotTranscript)
	}
	if intent.Type != "ignore" {
		t.Errorf("intent.Type = %q, want 'ignore'", intent.Type)
	}
}

func TestAnswerCookingQuestion_ThreadsQuestionAndContext(t *testing.T) {
	var gotQuestion, gotContext string
	text := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			gotQuestion = question
			gotContext = recipeContext
			return "Use 375F for 25 minutes.", nil
		},
	}

	svc := NewVoiceService(&config.Config{}, text, nil)
	recipeContext := "Roast Chicken recipe\n\nThe user is currently on step 3."
	answer, err := svc.AnswerCookingQuestion(context.Background(), "what temperature?", recipeContext)
	if err != nil {
		t.Fatalf("AnswerCookingQuestion error: %v", err)
	}
	if answer != "Use 375F for 25 minutes." {
		t.Errorf("answer = %q", answer)
	}
	if gotQuestion != "what temperature?" {
		t.Errorf("question passed to CookingQA = %q", gotQuestion)
	}
	if gotContext != recipeContext {
		t.Errorf("recipe context = %q, want the full context including the current-step note", gotContext)
	}
}

func TestAnswerCookingQuestion_ErrorPropagates(t *testing.T) {
	text := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return "", errors.New("qa unavailable")
		},
	}

	svc := NewVoiceService(&config.Config{}, text, nil)
	answer, err := svc.AnswerCookingQuestion(context.Background(), "how long?", "")
	if err == nil || !strings.Contains(err.Error(), "qa unavailable") {
		t.Errorf("err = %v, want the provider error to propagate", err)
	}
	if answer != "" {
		t.Errorf("answer = %q, want empty on error", answer)
	}
}
