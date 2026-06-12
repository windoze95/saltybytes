package ai

import (
	"context"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestNewEmbeddingProvider_Defaults(t *testing.T) {
	p := NewEmbeddingProvider("test-key")
	if p.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want 'test-key'", p.apiKey)
	}
	if p.model != openai.SmallEmbedding3 {
		t.Errorf("model = %v, want SmallEmbedding3", p.model)
	}
}

func TestGenerateEmbedding_EmptyTextRejected(t *testing.T) {
	// The empty-input guard fires before any client or HTTP request is
	// constructed, so this stays fully offline.
	p := NewEmbeddingProvider("test-key")

	got, err := p.GenerateEmbedding(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	if got != nil {
		t.Errorf("expected nil embedding, got %v", got)
	}
	if !strings.Contains(err.Error(), "embedding text is empty") {
		t.Errorf("error = %q, want mention of empty embedding text", err.Error())
	}
}
