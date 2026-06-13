package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// newFamilyCrudRouter wires the full family route set against the given repo
// with an authenticated test user.
func newFamilyCrudRouter(repo *testutil.MockFamilyRepo) (*gin.Engine, *models.User) {
	user := testutil.TestUser()
	svc := service.NewFamilyService(&config.Config{}, repo, &testutil.MockTextProvider{})
	handler := NewFamilyHandler(svc)

	r := gin.New()
	r.POST("/family", setUser(user), handler.CreateFamily)
	r.GET("/family", setUser(user), handler.GetFamily)
	r.POST("/family/members", setUser(user), handler.AddMember)
	r.PUT("/family/members/:member_id", setUser(user), handler.UpdateMember)
	r.PUT("/family/members/:member_id/dietary", setUser(user), handler.UpdateDietaryProfile)
	return r, user
}

func doJSON(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- CreateFamily ---

func TestCreateFamily_Handler_Envelope(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		CreateFamilyFunc: func(family *models.Family) error {
			family.ID = 7
			return nil
		},
	}
	r, user := newFamilyCrudRouter(repo)

	w := doJSON(r, "POST", "/family", `{"name": "The Does"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	family, ok := resp["family"]
	if !ok {
		t.Fatalf("response missing 'family' envelope key. body: %s", w.Body.String())
	}
	if family["name"] != "The Does" {
		t.Errorf("family.name = %v, want 'The Does'", family["name"])
	}
	// snake_case contract key for the owner.
	if family["owner_id"] != float64(user.ID) {
		t.Errorf("family.owner_id = %v, want %d", family["owner_id"], user.ID)
	}
}

func TestCreateFamily_Handler_MissingName_400(t *testing.T) {
	r, _ := newFamilyCrudRouter(&testutil.MockFamilyRepo{})

	w := doJSON(r, "POST", "/family", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- GetFamily ---

func TestGetFamily_Handler_NullWhenNotFound(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	w := doJSON(r, "GET", "/family", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (no family is not an error). body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if familyRaw, ok := resp["family"]; !ok || string(familyRaw) != "null" {
		t.Errorf("family = %s, want JSON null", string(familyRaw))
	}
}

func TestGetFamily_Handler_Envelope(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID, Name: "The Does", Members: []models.FamilyMember{
				{ID: 5, FamilyID: 7, Name: "Joey"},
			}}, nil
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	w := doJSON(r, "GET", "/family", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	family := resp["family"]
	members, ok := family["members"].([]interface{})
	if !ok || len(members) != 1 {
		t.Fatalf("family.members = %v, want array of 1", family["members"])
	}
	member, _ := members[0].(map[string]interface{})
	if member["family_id"] != float64(7) {
		t.Errorf("member.family_id = %v, want 7 (snake_case)", member["family_id"])
	}
}

// --- AddMember ---

func TestAddMember_Handler_Envelope(t *testing.T) {
	var created *models.FamilyMember
	repo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
		CreateFamilyMemberFunc: func(member *models.FamilyMember) error {
			member.ID = 5
			created = member
			return nil
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	w := doJSON(r, "POST", "/family/members", `{"name": "Joey", "relationship": "son", "user_id": 33}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	if created == nil {
		t.Fatal("member was not created")
	}
	if created.FamilyID != 7 {
		t.Errorf("created.FamilyID = %d, want owner's family 7", created.FamilyID)
	}
	if created.UserID == nil || *created.UserID != 33 {
		t.Errorf("created.UserID = %v, want 33 (snake_case user_id binding)", created.UserID)
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	member, ok := resp["member"]
	if !ok {
		t.Fatalf("response missing 'member' envelope key. body: %s", w.Body.String())
	}
	if member["name"] != "Joey" || member["relationship"] != "son" {
		t.Errorf("member = %v, want Joey/son", member)
	}
}

func TestAddMember_Handler_MissingName_400(t *testing.T) {
	r, _ := newFamilyCrudRouter(&testutil.MockFamilyRepo{})

	w := doJSON(r, "POST", "/family/members", `{"relationship": "son"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAddMember_Handler_NoFamily_404(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	w := doJSON(r, "POST", "/family/members", `{"name": "Joey"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- UpdateMember ---

func TestUpdateMember_Handler_Envelope(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 7, Name: "Old"}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	w := doJSON(r, "PUT", "/family/members/5", `{"name": "New", "relationship": "daughter"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	member, ok := resp["member"]
	if !ok {
		t.Fatalf("response missing 'member' envelope key. body: %s", w.Body.String())
	}
	if member["name"] != "New" || member["relationship"] != "daughter" {
		t.Errorf("member = %v, want New/daughter", member)
	}
}

func TestUpdateMember_Handler_NotOwner_403(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 99}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	w := doJSON(r, "PUT", "/family/members/5", `{"name": "New"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// --- UpdateDietaryProfile ---

func TestUpdateDietaryProfile_Handler_SnakeCaseBinding(t *testing.T) {
	var saved *models.DietaryProfile
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 7}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
		GetOrCreateDietaryProfileFunc: func(memberID uint) (*models.DietaryProfile, error) {
			return &models.DietaryProfile{ID: 3, MemberID: memberID}, nil
		},
		UpdateDietaryProfileFunc: func(profile *models.DietaryProfile) error {
			saved = profile
			return nil
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	body := `{
		"allergies": [{"name": "peanuts", "severity": "severe", "sub_forms": ["peanut butter", "peanut oil"], "notes": "EpiPen"}],
		"intolerances": ["lactose"],
		"restrictions": ["halal"],
		"preferences": ["spicy"],
		"medical_notes": "type 1 diabetes"
	}`
	w := doJSON(r, "PUT", "/family/members/5/dietary", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["message"] == nil {
		t.Error("response should contain 'message'")
	}

	if saved == nil {
		t.Fatal("dietary profile was not persisted")
	}
	// snake_case body keys must bind into the model.
	if saved.MedicalNotes != "type 1 diabetes" {
		t.Errorf("MedicalNotes = %q, want 'type 1 diabetes' (medical_notes binding)", saved.MedicalNotes)
	}
	if len(saved.Allergies) != 1 {
		t.Fatalf("len(Allergies) = %d, want 1", len(saved.Allergies))
	}
	a := saved.Allergies[0]
	if a.Name != "peanuts" || a.Severity != "severe" || a.Notes != "EpiPen" {
		t.Errorf("Allergies[0] = %+v, want peanuts/severe/EpiPen", a)
	}
	if len(a.SubForms) != 2 || a.SubForms[0] != "peanut butter" || a.SubForms[1] != "peanut oil" {
		t.Errorf("Allergies[0].SubForms = %v, want sub_forms binding", a.SubForms)
	}
	if len(saved.Intolerances) != 1 || saved.Intolerances[0] != "lactose" {
		t.Errorf("Intolerances = %v, want [lactose]", saved.Intolerances)
	}
	if len(saved.Restrictions) != 1 || len(saved.Preferences) != 1 {
		t.Errorf("Restrictions/Preferences = %v/%v, want both bound", saved.Restrictions, saved.Preferences)
	}
}

func TestUpdateDietaryProfile_Handler_NotOwner_403(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 99}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
	}
	r, _ := newFamilyCrudRouter(repo)

	w := doJSON(r, "PUT", "/family/members/5/dietary", `{"medical_notes": "x"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestUpdateDietaryProfile_Handler_InvalidMemberID_400(t *testing.T) {
	r, _ := newFamilyCrudRouter(&testutil.MockFamilyRepo{})

	w := doJSON(r, "PUT", "/family/members/abc/dietary", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
