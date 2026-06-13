package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func newDietaryFamilyService(provider ai.TextProvider) *FamilyService {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 7, Name: "Joey"}, nil
		},
	}
	return NewFamilyService(&config.Config{}, repo, provider)
}

func TestDietaryInterview_IncompleteTurn(t *testing.T) {
	provider := &testutil.MockTextProvider{
		DietaryInterviewFunc: func(ctx context.Context, messages []ai.Message, memberName string) (*ai.DietaryInterviewResult, error) {
			if memberName != "Joey" {
				t.Errorf("memberName = %q, want Joey", memberName)
			}
			return &ai.DietaryInterviewResult{Response: "Do you have any food allergies?"}, nil
		},
	}
	svc := newDietaryFamilyService(provider)

	response, complete, profile, err := svc.DietaryInterview(context.Background(), 5, []ai.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response != "Do you have any food allergies?" {
		t.Errorf("response = %q, want passthrough of provider text", response)
	}
	if complete {
		t.Error("complete = true, want false")
	}
	if profile != nil {
		t.Errorf("profile = %+v, want nil", profile)
	}
}

func TestDietaryInterview_CompleteTurn_MapsProfile(t *testing.T) {
	provider := &testutil.MockTextProvider{
		DietaryInterviewFunc: func(ctx context.Context, messages []ai.Message, memberName string) (*ai.DietaryInterviewResult, error) {
			return &ai.DietaryInterviewResult{
				Response: "All saved!",
				Complete: true,
				Profile: &ai.DietaryProfileResult{
					Allergies: []ai.DietaryAllergyResult{
						{Name: "peanuts", Severity: "severe", SubForms: []string{"raw peanuts", "peanut butter"}, Notes: "carries an EpiPen"},
					},
					Intolerances: []string{"lactose"},
					Restrictions: []string{"vegetarian"},
					Preferences:  []string{"dislikes cilantro"},
					MedicalNotes: "low sodium",
				},
			}, nil
		},
	}
	svc := newDietaryFamilyService(provider)

	response, complete, profile, err := svc.DietaryInterview(context.Background(), 5, []ai.Message{{Role: "user", Content: "that's everything"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response != "All saved!" {
		t.Errorf("response = %q, want wrap-up text", response)
	}
	if !complete {
		t.Fatal("complete = false, want true")
	}
	if profile == nil {
		t.Fatal("profile = nil, want mapped profile")
	}
	if profile.ID != 0 || profile.MemberID != 0 {
		t.Errorf("profile IDs should be zero, got ID=%d MemberID=%d", profile.ID, profile.MemberID)
	}
	if len(profile.Allergies) != 1 {
		t.Fatalf("len(Allergies) = %d, want 1", len(profile.Allergies))
	}
	a := profile.Allergies[0]
	if a.Name != "peanuts" || a.Severity != "severe" || a.Notes != "carries an EpiPen" {
		t.Errorf("Allergies[0] = %+v, want peanuts/severe/EpiPen note", a)
	}
	if len(a.SubForms) != 2 || a.SubForms[0] != "raw peanuts" || a.SubForms[1] != "peanut butter" {
		t.Errorf("Allergies[0].SubForms = %v, want [raw peanuts, peanut butter]", a.SubForms)
	}
	if len(profile.Intolerances) != 1 || profile.Intolerances[0] != "lactose" {
		t.Errorf("Intolerances = %v, want [lactose]", profile.Intolerances)
	}
	if len(profile.Restrictions) != 1 || profile.Restrictions[0] != "vegetarian" {
		t.Errorf("Restrictions = %v, want [vegetarian]", profile.Restrictions)
	}
	if len(profile.Preferences) != 1 || profile.Preferences[0] != "dislikes cilantro" {
		t.Errorf("Preferences = %v, want [dislikes cilantro]", profile.Preferences)
	}
	if profile.MedicalNotes != "low sodium" {
		t.Errorf("MedicalNotes = %q, want 'low sodium'", profile.MedicalNotes)
	}
}

func TestDietaryInterview_NilProvider(t *testing.T) {
	svc := NewFamilyService(&config.Config{}, &testutil.MockFamilyRepo{}, nil)

	_, _, _, err := svc.DietaryInterview(context.Background(), 5, nil)
	if err == nil {
		t.Fatal("expected error when AI provider is not configured")
	}
}

func TestDietaryInterview_MemberNotFound(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return nil, fmt.Errorf("record not found")
		},
	}
	svc := NewFamilyService(&config.Config{}, repo, &testutil.MockTextProvider{})

	_, _, _, err := svc.DietaryInterview(context.Background(), 5, nil)
	if err == nil {
		t.Fatal("expected error when member lookup fails")
	}
}
