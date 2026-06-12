package models

import (
	"encoding/json"
	"testing"
	"time"
)

// marshalToMap marshals v to JSON and unmarshals it into a map for key assertions.
func marshalToMap(t *testing.T, v interface{}) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	return m
}

// assertKeys checks that all want keys are present and all exclude keys are absent.
func assertKeys(t *testing.T, m map[string]json.RawMessage, want []string, exclude []string) {
	t.Helper()
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("expected JSON key %q to be present, got keys: %v", k, keysOf(m))
		}
	}
	for _, k := range exclude {
		if _, ok := m[k]; ok {
			t.Errorf("expected JSON key %q to be absent", k)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// --- RecipeNode / RecipeTree serialization (contract C2) ---

func TestRecipeNodeJSON_SnakeCaseKeys(t *testing.T) {
	parentID := uint(1)
	node := RecipeNode{
		ID:          2,
		CreatedAt:   time.Now(),
		TreeID:      3,
		ParentID:    &parentID,
		Prompt:      "make it spicier",
		Response:    &RecipeDef{Title: "Spicy Pancakes"},
		Summary:     "Added cayenne",
		Type:        RecipeTypeRegenChat,
		BranchName:  "original",
		CreatedByID: 1,
		IsActive:    true,
	}

	m := marshalToMap(t, node)

	assertKeys(t, m,
		[]string{"id", "tree_id", "parent_id", "branch_name", "is_active", "created_at", "user_prompt", "response", "summary", "type", "is_ephemeral", "created_by_id"},
		[]string{"ID", "TreeID", "ParentID", "BranchName", "IsActive", "CreatedAt", "Prompt", "Response", "DeletedAt", "Parent", "Children", "CreatedBy"},
	)

	// Response must keep the existing RecipeDef snake_case shape unchanged.
	var response map[string]json.RawMessage
	if err := json.Unmarshal(m["response"], &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if _, ok := response["title"]; !ok {
		t.Error("expected response.title key in RecipeDef serialization")
	}
}

func TestRecipeNodeJSON_RootHasNullParentID(t *testing.T) {
	node := RecipeNode{ID: 1, TreeID: 1}
	m := marshalToMap(t, node)
	if string(m["parent_id"]) != "null" {
		t.Errorf("parent_id for root node = %s, want null", m["parent_id"])
	}
}

func TestRecipeTreeJSON_SnakeCaseKeys(t *testing.T) {
	rootNodeID := uint(1)
	tree := RecipeTree{
		ID:         5,
		RecipeID:   10,
		RootNodeID: &rootNodeID,
		Nodes:      []RecipeNode{{ID: 1, TreeID: 5}},
	}

	m := marshalToMap(t, tree)

	assertKeys(t, m,
		[]string{"id", "recipe_id", "root_node_id", "nodes", "created_at"},
		[]string{"ID", "RecipeID", "RootNodeID", "RootNode", "DeletedAt"},
	)
}

// --- Family / FamilyMember / DietaryProfile serialization (contract C3) ---

func TestFamilyJSON_SnakeCaseKeys(t *testing.T) {
	family := Family{
		ID:      1,
		Name:    "The Does",
		OwnerID: 7,
		Owner:   &User{},
		Members: []FamilyMember{{ID: 2, FamilyID: 1, Name: "Jane", Relationship: "spouse"}},
	}

	m := marshalToMap(t, family)

	assertKeys(t, m,
		[]string{"id", "name", "owner_id", "members", "created_at"},
		[]string{"ID", "Name", "OwnerID", "Owner", "Members", "DeletedAt"},
	)
}

func TestFamilyMemberJSON_SnakeCaseKeys(t *testing.T) {
	userID := uint(3)
	member := FamilyMember{
		ID:           2,
		FamilyID:     1,
		Name:         "Jane",
		Relationship: "spouse",
		UserID:       &userID,
		User:         &User{},
		DietaryProfile: &DietaryProfile{
			ID:       4,
			MemberID: 2,
		},
	}

	m := marshalToMap(t, member)

	assertKeys(t, m,
		[]string{"id", "family_id", "name", "relationship", "user_id", "dietary_profile", "created_at"},
		[]string{"ID", "FamilyID", "Name", "Relationship", "UserID", "User", "DietaryProfile", "DeletedAt"},
	)
}

func TestDietaryProfileJSON_SnakeCaseKeys(t *testing.T) {
	profile := DietaryProfile{
		ID:       4,
		MemberID: 2,
		Allergies: AllergyList{
			{Name: "peanut", Severity: "severe", SubForms: []string{"peanut oil"}, Notes: "carries epipen"},
		},
		Intolerances: StringList{"lactose"},
		Restrictions: StringList{"halal"},
		Preferences:  StringList{"no cilantro"},
		MedicalNotes: "see allergist notes",
	}

	m := marshalToMap(t, profile)

	assertKeys(t, m,
		[]string{"id", "member_id", "allergies", "intolerances", "restrictions", "preferences", "medical_notes", "created_at"},
		[]string{"ID", "MemberID", "Allergies", "Intolerances", "Restrictions", "Preferences", "MedicalNotes", "DeletedAt"},
	)

	// Allergy entries keep their existing snake_case keys.
	var allergies []map[string]json.RawMessage
	if err := json.Unmarshal(m["allergies"], &allergies); err != nil {
		t.Fatalf("failed to unmarshal allergies: %v", err)
	}
	if len(allergies) != 1 {
		t.Fatalf("expected 1 allergy, got %d", len(allergies))
	}
	assertKeys(t, allergies[0], []string{"name", "severity", "sub_forms", "notes"}, nil)
}

// --- AllergenAnalysis serialization (contract C4) ---

func TestAllergenAnalysisJSON_SnakeCaseKeys(t *testing.T) {
	nodeID := uint(9)
	analysis := AllergenAnalysis{
		ID:       1,
		RecipeID: 2,
		Recipe:   &Recipe{},
		NodeID:   &nodeID,
		IngredientAnalyses: IngredientAnalysisList{
			{IngredientName: "peanut butter", CommonAllergens: []string{"peanut"}, Confidence: 0.95},
		},
		ContainsNuts:      true,
		SafeForProfiles:   UintList{1},
		UnsafeForProfiles: UintList{2},
		Confidence:        0.9,
		RequiresReview:    false,
		IsPremium:         true,
		PromptVersion:     "v1",
		Disclaimer:        "test disclaimer",
	}

	m := marshalToMap(t, analysis)

	assertKeys(t, m,
		[]string{
			"id", "created_at", "recipe_id", "node_id", "ingredient_analyses",
			"contains_nuts", "contains_dairy", "contains_gluten", "contains_soy",
			"contains_seed_oils", "contains_shellfish", "contains_eggs",
			"safe_for_profiles", "unsafe_for_profiles", "confidence",
			"requires_review", "is_premium", "prompt_version", "disclaimer",
		},
		[]string{"ID", "CreatedAt", "RecipeID", "Recipe", "NodeID", "IngredientAnalyses", "ContainsNuts", "DeletedAt"},
	)

	// Nested ingredient analyses keep their existing snake_case keys.
	var analyses []map[string]json.RawMessage
	if err := json.Unmarshal(m["ingredient_analyses"], &analyses); err != nil {
		t.Fatalf("failed to unmarshal ingredient_analyses: %v", err)
	}
	if len(analyses) != 1 {
		t.Fatalf("expected 1 ingredient analysis, got %d", len(analyses))
	}
	assertKeys(t, analyses[0],
		[]string{"ingredient_name", "common_allergens", "possible_allergens", "sub_ingredients", "seed_oil_risk", "confidence"},
		nil,
	)
}
