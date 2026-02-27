package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// setupTestCookingHandler creates a CookingHandler with mock providers and a
// running Hub. Callers can configure the mock funcs before invoking handlers.
func setupTestCookingHandler() (*CookingHandler, *testutil.MockTextProvider, *testutil.MockSpeechProvider) {
	mockText := &testutil.MockTextProvider{}
	mockSpeech := &testutil.MockSpeechProvider{}
	cfg := &config.Config{}
	voiceService := service.NewVoiceService(cfg, mockText, mockSpeech)
	hub := NewHub()
	go hub.Run()
	mockRepo := testutil.NewMockRecipeRepo()
	return NewCookingHandler(hub, "test-secret", voiceService, mockRepo), mockText, mockSpeech
}

// newTestClient creates a Client with a buffered Send channel and no real
// websocket.Conn. This works because the handler methods write to client.Send
// rather than Conn directly.
func newTestClient(hub *Hub, roomID string, userID uint) *Client {
	return &Client{
		Hub:    hub,
		Send:   make(chan []byte, 256),
		RoomID: roomID,
		UserID: userID,
	}
}

// readMessage reads a single WSMessage from the client's Send channel with a
// short timeout to prevent tests from hanging.
func readMessage(t *testing.T, client *Client) WSMessage {
	t.Helper()
	select {
	case data := <-client.Send:
		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("failed to unmarshal message from Send channel: %v", err)
		}
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message on Send channel")
		return WSMessage{}
	}
}

// assertNoMoreMessages verifies nothing else is pending on the Send channel.
func assertNoMoreMessages(t *testing.T, client *Client) {
	t.Helper()
	select {
	case data := <-client.Send:
		t.Fatalf("unexpected extra message on Send channel: %s", string(data))
	case <-time.After(50 * time.Millisecond):
		// OK — nothing pending
	}
}

// --- handleChatMessage tests ---

func TestHandleChatMessage_Success(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.CookingQAFunc = func(ctx context.Context, question, recipeContext string) (string, error) {
		if question != "Can I substitute butter?" {
			t.Errorf("unexpected question: %q", question)
		}
		return "Yes, you can use margarine or oil.", nil
	}

	payload, _ := json.Marshal(ChatMessagePayload{
		Message:       "Can I substitute butter?",
		RecipeContext: "recipe with butter",
	})

	ch.handleChatMessage(client, payload)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeChatResponse {
		t.Fatalf("expected type %q, got %q", MsgTypeChatResponse, msg.Type)
	}
	var resp ChatResponsePayload
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("failed to unmarshal ChatResponsePayload: %v", err)
	}
	if resp.Message != "Yes, you can use margarine or oil." {
		t.Errorf("unexpected answer: %q", resp.Message)
	}
	assertNoMoreMessages(t, client)
}

func TestHandleChatMessage_EmptyMessage(t *testing.T) {
	ch, _, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	payload, _ := json.Marshal(ChatMessagePayload{Message: ""})
	ch.handleChatMessage(client, payload)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeError {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var errPayload ErrorPayload
	if err := json.Unmarshal(msg.Payload, &errPayload); err != nil {
		t.Fatalf("failed to unmarshal ErrorPayload: %v", err)
	}
	if errPayload.Message != "message cannot be empty" {
		t.Errorf("unexpected error message: %q", errPayload.Message)
	}
}

func TestHandleChatMessage_AIError(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.CookingQAFunc = func(ctx context.Context, question, recipeContext string) (string, error) {
		return "", fmt.Errorf("API rate limit exceeded")
	}

	payload, _ := json.Marshal(ChatMessagePayload{Message: "What temp for chicken?"})
	ch.handleChatMessage(client, payload)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeError {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var errPayload ErrorPayload
	if err := json.Unmarshal(msg.Payload, &errPayload); err != nil {
		t.Fatalf("failed to unmarshal ErrorPayload: %v", err)
	}
	if errPayload.Message != "failed to get cooking answer" {
		t.Errorf("unexpected error message: %q", errPayload.Message)
	}
}

// --- handleVoiceTranscript tests ---

func TestHandleVoiceTranscript_WithAudioData(t *testing.T) {
	ch, mockText, mockSpeech := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockSpeech.TranscribeAudioFunc = func(ctx context.Context, audioData []byte) (string, error) {
		if string(audioData) != "fake-audio-bytes" {
			t.Errorf("unexpected audio data: %q", string(audioData))
		}
		return "scroll down please", nil
	}
	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		return &ai.VoiceIntent{
			Type:   "scroll_down",
			Amount: "large",
		}, nil
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "",
		AudioData:  []byte("fake-audio-bytes"),
	})
	ch.handleVoiceTranscript(client, payload)

	// First message: the raw VoiceIntent
	msg := readMessage(t, client)
	if msg.Type != MsgTypeVoiceIntent {
		t.Fatalf("expected type %q, got %q", MsgTypeVoiceIntent, msg.Type)
	}
	var intent VoiceIntentPayload
	if err := json.Unmarshal(msg.Payload, &intent); err != nil {
		t.Fatalf("failed to unmarshal VoiceIntentPayload: %v", err)
	}
	if intent.Type != "scroll_down" {
		t.Errorf("expected intent type scroll_down, got %q", intent.Type)
	}
	if intent.Amount != "large" {
		t.Errorf("expected amount large, got %q", intent.Amount)
	}

	// Second message: the mapped ScrollCommand
	msg2 := readMessage(t, client)
	if msg2.Type != MsgTypeScrollCommand {
		t.Fatalf("expected type %q, got %q", MsgTypeScrollCommand, msg2.Type)
	}
	var scroll ScrollCommandPayload
	if err := json.Unmarshal(msg2.Payload, &scroll); err != nil {
		t.Fatalf("failed to unmarshal ScrollCommandPayload: %v", err)
	}
	if scroll.Direction != "down" {
		t.Errorf("expected direction down, got %q", scroll.Direction)
	}
	if scroll.Amount != "large" {
		t.Errorf("expected amount large, got %q", scroll.Amount)
	}
	assertNoMoreMessages(t, client)
}

func TestHandleVoiceTranscript_TextOnly(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		if transcript != "go to ingredients" {
			t.Errorf("unexpected transcript: %q", transcript)
		}
		return &ai.VoiceIntent{
			Type:   "navigate",
			Target: "ingredients",
		}, nil
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "go to ingredients",
	})
	ch.handleVoiceTranscript(client, payload)

	// First message: VoiceIntent
	msg := readMessage(t, client)
	if msg.Type != MsgTypeVoiceIntent {
		t.Fatalf("expected type %q, got %q", MsgTypeVoiceIntent, msg.Type)
	}
	var intent VoiceIntentPayload
	if err := json.Unmarshal(msg.Payload, &intent); err != nil {
		t.Fatalf("failed to unmarshal VoiceIntentPayload: %v", err)
	}
	if intent.Type != "navigate" {
		t.Errorf("expected intent type navigate, got %q", intent.Type)
	}

	// Second message: NavigateCommand
	msg2 := readMessage(t, client)
	if msg2.Type != MsgTypeNavigateCommand {
		t.Fatalf("expected type %q, got %q", MsgTypeNavigateCommand, msg2.Type)
	}
	var nav NavigateCommandPayload
	if err := json.Unmarshal(msg2.Payload, &nav); err != nil {
		t.Fatalf("failed to unmarshal NavigateCommandPayload: %v", err)
	}
	if nav.Target != "ingredients" {
		t.Errorf("expected target ingredients, got %q", nav.Target)
	}
	assertNoMoreMessages(t, client)
}

func TestHandleVoiceTranscript_EmptyTranscript(t *testing.T) {
	ch, _, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "",
	})
	ch.handleVoiceTranscript(client, payload)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeError {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var errPayload ErrorPayload
	if err := json.Unmarshal(msg.Payload, &errPayload); err != nil {
		t.Fatalf("failed to unmarshal ErrorPayload: %v", err)
	}
	if errPayload.Message != "transcript or audio_data is required" {
		t.Errorf("unexpected error message: %q", errPayload.Message)
	}
}

func TestHandleVoiceTranscript_QuestionIntent(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		return &ai.VoiceIntent{
			Type: "question",
			Text: "how long do I bake it",
		}, nil
	}
	mockText.CookingQAFunc = func(ctx context.Context, question, recipeContext string) (string, error) {
		if question != "how long do I bake it" {
			t.Errorf("unexpected question: %q", question)
		}
		return "Bake at 350F for 25 minutes.", nil
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "how long do I bake it",
	})
	ch.handleVoiceTranscript(client, payload)

	// First message: VoiceIntent
	msg := readMessage(t, client)
	if msg.Type != MsgTypeVoiceIntent {
		t.Fatalf("expected type %q, got %q", MsgTypeVoiceIntent, msg.Type)
	}
	var intent VoiceIntentPayload
	if err := json.Unmarshal(msg.Payload, &intent); err != nil {
		t.Fatalf("failed to unmarshal VoiceIntentPayload: %v", err)
	}
	if intent.Type != "question" {
		t.Errorf("expected intent type question, got %q", intent.Type)
	}

	// Second message: ChatResponse with the AI answer
	msg2 := readMessage(t, client)
	if msg2.Type != MsgTypeChatResponse {
		t.Fatalf("expected type %q, got %q", MsgTypeChatResponse, msg2.Type)
	}
	var resp ChatResponsePayload
	if err := json.Unmarshal(msg2.Payload, &resp); err != nil {
		t.Fatalf("failed to unmarshal ChatResponsePayload: %v", err)
	}
	if resp.Message != "Bake at 350F for 25 minutes." {
		t.Errorf("unexpected answer: %q", resp.Message)
	}
	assertNoMoreMessages(t, client)
}

func TestHandleVoiceTranscript_IgnoreIntent(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		return &ai.VoiceIntent{
			Type: "ignore",
		}, nil
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "um okay never mind",
	})
	ch.handleVoiceTranscript(client, payload)

	// Only the raw VoiceIntent should be sent, no follow-up command
	msg := readMessage(t, client)
	if msg.Type != MsgTypeVoiceIntent {
		t.Fatalf("expected type %q, got %q", MsgTypeVoiceIntent, msg.Type)
	}
	assertNoMoreMessages(t, client)
}

func TestHandleVoiceTranscript_ScrollUp(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		return &ai.VoiceIntent{
			Type:   "scroll_up",
			Amount: "small",
		}, nil
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "scroll up a little",
	})
	ch.handleVoiceTranscript(client, payload)

	// VoiceIntent
	msg := readMessage(t, client)
	if msg.Type != MsgTypeVoiceIntent {
		t.Fatalf("expected type %q, got %q", MsgTypeVoiceIntent, msg.Type)
	}

	// ScrollCommand with direction "up"
	msg2 := readMessage(t, client)
	if msg2.Type != MsgTypeScrollCommand {
		t.Fatalf("expected type %q, got %q", MsgTypeScrollCommand, msg2.Type)
	}
	var scroll ScrollCommandPayload
	if err := json.Unmarshal(msg2.Payload, &scroll); err != nil {
		t.Fatalf("failed to unmarshal ScrollCommandPayload: %v", err)
	}
	if scroll.Direction != "up" {
		t.Errorf("expected direction up, got %q", scroll.Direction)
	}
	if scroll.Amount != "small" {
		t.Errorf("expected amount small, got %q", scroll.Amount)
	}
	assertNoMoreMessages(t, client)
}

func TestHandleVoiceTranscript_ClassifyError(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		return nil, fmt.Errorf("classification failed")
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "something unintelligible",
	})
	ch.handleVoiceTranscript(client, payload)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeError {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var errPayload ErrorPayload
	if err := json.Unmarshal(msg.Payload, &errPayload); err != nil {
		t.Fatalf("failed to unmarshal ErrorPayload: %v", err)
	}
	if errPayload.Message != "failed to process voice command" {
		t.Errorf("unexpected error message: %q", errPayload.Message)
	}
}

// --- handleMessage routing tests ---

func TestHandleMessage_UnknownType(t *testing.T) {
	ch, _, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	data, _ := json.Marshal(WSMessage{
		Type:    "bogus_type",
		Payload: json.RawMessage(`{}`),
	})
	ch.handleMessage(client, data)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeError {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var errPayload ErrorPayload
	if err := json.Unmarshal(msg.Payload, &errPayload); err != nil {
		t.Fatalf("failed to unmarshal ErrorPayload: %v", err)
	}
	if errPayload.Message != "unknown message type: bogus_type" {
		t.Errorf("unexpected error message: %q", errPayload.Message)
	}
}

func TestHandleMessage_InvalidJSON(t *testing.T) {
	ch, _, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	ch.handleMessage(client, []byte(`{not valid json`))

	msg := readMessage(t, client)
	if msg.Type != MsgTypeError {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var errPayload ErrorPayload
	if err := json.Unmarshal(msg.Payload, &errPayload); err != nil {
		t.Fatalf("failed to unmarshal ErrorPayload: %v", err)
	}
	if errPayload.Message != "invalid message format" {
		t.Errorf("unexpected error message: %q", errPayload.Message)
	}
}

func TestHandleMessage_RoutesChatMessage(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.CookingQAFunc = func(ctx context.Context, question, recipeContext string) (string, error) {
		return "The answer is 42.", nil
	}

	payload, _ := json.Marshal(ChatMessagePayload{
		Message: "How many eggs?",
	})
	data, _ := json.Marshal(WSMessage{
		Type:    MsgTypeChatMessage,
		Payload: payload,
	})
	ch.handleMessage(client, data)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeChatResponse {
		t.Fatalf("expected type %q, got %q", MsgTypeChatResponse, msg.Type)
	}
}

func TestHandleMessage_RoutesVoiceTranscript(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		return &ai.VoiceIntent{Type: "ignore"}, nil
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "whatever",
	})
	data, _ := json.Marshal(WSMessage{
		Type:    MsgTypeVoiceTranscript,
		Payload: payload,
	})
	ch.handleMessage(client, data)

	msg := readMessage(t, client)
	if msg.Type != MsgTypeVoiceIntent {
		t.Fatalf("expected type %q, got %q", MsgTypeVoiceIntent, msg.Type)
	}
}

func TestHandleVoiceTranscript_QuestionIntent_AnswerError(t *testing.T) {
	ch, mockText, _ := setupTestCookingHandler()
	client := newTestClient(ch.Hub, "recipe-1", 42)

	mockText.ClassifyVoiceIntentFunc = func(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
		return &ai.VoiceIntent{
			Type: "question",
			Text: "how long?",
		}, nil
	}
	mockText.CookingQAFunc = func(ctx context.Context, question, recipeContext string) (string, error) {
		return "", fmt.Errorf("service unavailable")
	}

	payload, _ := json.Marshal(VoiceTranscriptPayload{
		Transcript: "how long?",
	})
	ch.handleVoiceTranscript(client, payload)

	// First: VoiceIntent is still sent
	msg := readMessage(t, client)
	if msg.Type != MsgTypeVoiceIntent {
		t.Fatalf("expected type %q, got %q", MsgTypeVoiceIntent, msg.Type)
	}

	// Second: error from CookingQA
	msg2 := readMessage(t, client)
	if msg2.Type != MsgTypeError {
		t.Fatalf("expected error type, got %q", msg2.Type)
	}
	var errPayload ErrorPayload
	if err := json.Unmarshal(msg2.Payload, &errPayload); err != nil {
		t.Fatalf("failed to unmarshal ErrorPayload: %v", err)
	}
	if errPayload.Message != "failed to get cooking answer" {
		t.Errorf("unexpected error message: %q", errPayload.Message)
	}
}
