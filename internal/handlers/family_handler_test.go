package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// newDietaryInterviewRouter builds a router for the dietary interview
// endpoint with an authenticated user that owns family 7, which member 5
// belongs to.
func newDietaryInterviewRouter(provider *testutil.MockTextProvider) *gin.Engine {
	user := testutil.TestUser()
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 7, Name: "Joey"}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
	}
	svc := service.NewFamilyService(&config.Config{}, repo, provider)
	handler := NewFamilyHandler(svc)

	r := gin.New()
	r.POST("/family/members/:member_id/dietary/interview", setUser(user), handler.DietaryInterview)
	return r
}

func postDietaryInterview(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/family/members/5/dietary/interview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDietaryInterview_Handler_IncompleteEnvelope(t *testing.T) {
	provider := &testutil.MockTextProvider{
		DietaryInterviewFunc: func(ctx context.Context, messages []ai.Message, memberName string) (*ai.DietaryInterviewResult, error) {
			return &ai.DietaryInterviewResult{Response: "Do you have any food allergies?"}, nil
		},
	}
	r := newDietaryInterviewRouter(provider)

	w := postDietaryInterview(r, `{"messages": [{"role": "user", "content": "hi"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Assert the exact 3-key envelope.
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp) != 3 {
		t.Errorf("response has %d keys, want exactly 3 (response, complete, profile). body: %s", len(resp), w.Body.String())
	}

	var response string
	if err := json.Unmarshal(resp["response"], &response); err != nil || response != "Do you have any food allergies?" {
		t.Errorf("response = %q (err %v), want question text", response, err)
	}
	var complete bool
	if err := json.Unmarshal(resp["complete"], &complete); err != nil || complete {
		t.Errorf("complete = %v (err %v), want false", complete, err)
	}
	if profileRaw, ok := resp["profile"]; !ok || string(profileRaw) != "null" {
		t.Errorf("profile = %s, want JSON null", string(profileRaw))
	}
}

func TestDietaryInterview_Handler_CompleteProfileSnakeCase(t *testing.T) {
	provider := &testutil.MockTextProvider{
		DietaryInterviewFunc: func(ctx context.Context, messages []ai.Message, memberName string) (*ai.DietaryInterviewResult, error) {
			return &ai.DietaryInterviewResult{
				Response: "All saved!",
				Complete: true,
				Profile: &ai.DietaryProfileResult{
					Allergies: []ai.DietaryAllergyResult{
						{Name: "peanuts", Severity: "severe", SubForms: []string{"raw peanuts"}, Notes: "carries an EpiPen"},
					},
					Intolerances: []string{"lactose"},
					Restrictions: []string{"vegetarian"},
					Preferences:  []string{"dislikes cilantro"},
					MedicalNotes: "low sodium",
				},
			}, nil
		},
	}
	r := newDietaryInterviewRouter(provider)

	w := postDietaryInterview(r, `{"messages": [{"role": "user", "content": "that is everything"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["response"] != "All saved!" {
		t.Errorf("response = %v, want 'All saved!'", resp["response"])
	}
	if resp["complete"] != true {
		t.Errorf("complete = %v, want true", resp["complete"])
	}

	profile, ok := resp["profile"].(map[string]interface{})
	if !ok {
		t.Fatalf("profile is not an object: %v", resp["profile"])
	}

	// Assert snake_case keys per models.DietaryProfile json tags.
	allergies, ok := profile["allergies"].([]interface{})
	if !ok || len(allergies) != 1 {
		t.Fatalf("profile.allergies = %v, want array with 1 entry", profile["allergies"])
	}
	allergy, ok := allergies[0].(map[string]interface{})
	if !ok {
		t.Fatalf("allergies[0] is not an object: %v", allergies[0])
	}
	if allergy["name"] != "peanuts" {
		t.Errorf("allergy.name = %v, want 'peanuts'", allergy["name"])
	}
	if allergy["severity"] != "severe" {
		t.Errorf("allergy.severity = %v, want 'severe'", allergy["severity"])
	}
	subForms, ok := allergy["sub_forms"].([]interface{})
	if !ok || len(subForms) != 1 || subForms[0] != "raw peanuts" {
		t.Errorf("allergy.sub_forms = %v, want ['raw peanuts']", allergy["sub_forms"])
	}
	if allergy["notes"] != "carries an EpiPen" {
		t.Errorf("allergy.notes = %v, want EpiPen note", allergy["notes"])
	}

	intolerances, ok := profile["intolerances"].([]interface{})
	if !ok || len(intolerances) != 1 || intolerances[0] != "lactose" {
		t.Errorf("profile.intolerances = %v, want ['lactose']", profile["intolerances"])
	}
	restrictions, ok := profile["restrictions"].([]interface{})
	if !ok || len(restrictions) != 1 || restrictions[0] != "vegetarian" {
		t.Errorf("profile.restrictions = %v, want ['vegetarian']", profile["restrictions"])
	}
	preferences, ok := profile["preferences"].([]interface{})
	if !ok || len(preferences) != 1 || preferences[0] != "dislikes cilantro" {
		t.Errorf("profile.preferences = %v, want ['dislikes cilantro']", profile["preferences"])
	}
	if profile["medical_notes"] != "low sodium" {
		t.Errorf("profile.medical_notes = %v, want 'low sodium'", profile["medical_notes"])
	}
}

func TestDietaryInterview_Handler_MissingMessages(t *testing.T) {
	r := newDietaryInterviewRouter(&testutil.MockTextProvider{})

	w := postDietaryInterview(r, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDietaryInterview_Handler_NotOwnedMember(t *testing.T) {
	user := testutil.TestUser()
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 99, Name: "Joey"}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
	}
	svc := service.NewFamilyService(&config.Config{}, repo, &testutil.MockTextProvider{})
	handler := NewFamilyHandler(svc)

	r := gin.New()
	r.POST("/family/members/:member_id/dietary/interview", setUser(user), handler.DietaryInterview)

	w := postDietaryInterview(r, `{"messages": [{"role": "user", "content": "hi"}]}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
