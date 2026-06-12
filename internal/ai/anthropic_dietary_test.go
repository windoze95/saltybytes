package ai

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestExtractDietaryInterview_TextOnly_Incomplete(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "text", Text: "Do you have any food allergies?"},
		},
	}

	result, err := extractDietaryInterviewFromMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Complete {
		t.Error("Complete = true, want false for a text-only turn")
	}
	if result.Profile != nil {
		t.Errorf("Profile = %+v, want nil for a text-only turn", result.Profile)
	}
	if result.Response != "Do you have any food allergies?" {
		t.Errorf("Response = %q, want passthrough of the text content", result.Response)
	}
}

func TestExtractDietaryInterview_ToolCall_Complete(t *testing.T) {
	input := `{
		"allergies": [
			{"name": "peanuts", "severity": "severe", "sub_forms": ["raw peanuts", "peanut butter"], "notes": "carries an EpiPen"},
			{"name": "egg", "severity": "mild", "sub_forms": ["raw egg"], "notes": "baked egg is fine"}
		],
		"intolerances": ["lactose"],
		"restrictions": ["vegetarian"],
		"preferences": ["dislikes cilantro"],
		"medical_notes": "low sodium for hypertension"
	}`
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "text", Text: "All set! I've saved the profile."},
			{Type: "tool_use", Name: "save_dietary_profile", Input: json.RawMessage(input)},
		},
	}

	result, err := extractDietaryInterviewFromMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Complete {
		t.Fatal("Complete = false, want true for a tool-call turn")
	}
	if result.Response != "All set! I've saved the profile." {
		t.Errorf("Response = %q, want the wrap-up text", result.Response)
	}
	p := result.Profile
	if p == nil {
		t.Fatal("Profile = nil, want populated profile")
	}
	if len(p.Allergies) != 2 {
		t.Fatalf("len(Allergies) = %d, want 2", len(p.Allergies))
	}
	first := p.Allergies[0]
	if first.Name != "peanuts" || first.Severity != "severe" || first.Notes != "carries an EpiPen" {
		t.Errorf("Allergies[0] = %+v, want peanuts/severe/EpiPen note", first)
	}
	if len(first.SubForms) != 2 || first.SubForms[0] != "raw peanuts" || first.SubForms[1] != "peanut butter" {
		t.Errorf("Allergies[0].SubForms = %v, want [raw peanuts, peanut butter]", first.SubForms)
	}
	if len(p.Intolerances) != 1 || p.Intolerances[0] != "lactose" {
		t.Errorf("Intolerances = %v, want [lactose]", p.Intolerances)
	}
	if len(p.Restrictions) != 1 || p.Restrictions[0] != "vegetarian" {
		t.Errorf("Restrictions = %v, want [vegetarian]", p.Restrictions)
	}
	if len(p.Preferences) != 1 || p.Preferences[0] != "dislikes cilantro" {
		t.Errorf("Preferences = %v, want [dislikes cilantro]", p.Preferences)
	}
	if p.MedicalNotes != "low sodium for hypertension" {
		t.Errorf("MedicalNotes = %q, want medical note passthrough", p.MedicalNotes)
	}
}

func TestExtractDietaryInterview_ToolCallWithoutText_UsesFallbackWrapUp(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "tool_use", Name: "save_dietary_profile", Input: json.RawMessage(`{"allergies": [], "intolerances": [], "restrictions": [], "preferences": [], "medical_notes": ""}`)},
		},
	}

	result, err := extractDietaryInterviewFromMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Complete || result.Profile == nil {
		t.Fatal("want complete result with non-nil profile")
	}
	if result.Response != dietaryWrapUpFallback {
		t.Errorf("Response = %q, want fallback wrap-up text", result.Response)
	}
}

func TestExtractDietaryInterview_UnknownToolIgnored(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "text", Text: "Next question?"},
			{Type: "tool_use", Name: "some_other_tool", Input: json.RawMessage(`{}`)},
		},
	}

	result, err := extractDietaryInterviewFromMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Complete || result.Profile != nil {
		t.Error("unknown tool should not mark the interview complete")
	}
}

func TestExtractDietaryInterview_EmptyMessage_Error(t *testing.T) {
	msg := &anthropic.Message{}

	if _, err := extractDietaryInterviewFromMessage(msg); err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
}

func TestExtractDietaryInterview_MalformedToolInput_Error(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "tool_use", Name: "save_dietary_profile", Input: json.RawMessage(`{"allergies": "not-an-array"}`)},
		},
	}

	if _, err := extractDietaryInterviewFromMessage(msg); err == nil {
		t.Fatal("expected error for malformed tool input, got nil")
	}
}
