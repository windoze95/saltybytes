package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// newAllergenTestService wires an AllergenService with a recipe already in the
// repo and the given allergen repo / AI provider mocks.
func newAllergenTestService(allergenRepo *testutil.MockAllergenRepo, provider ai.TextProvider) (*AllergenService, *testutil.MockRecipeRepo) {
	recipeRepo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipeRepo.Recipes[recipe.ID] = recipe

	svc := NewAllergenService(&config.Config{}, allergenRepo, &testutil.MockFamilyRepo{}, recipeRepo, provider, nil)
	return svc, recipeRepo
}

func TestAnalyzeRecipe_NilProvider(t *testing.T) {
	svc, _ := newAllergenTestService(&testutil.MockAllergenRepo{}, nil)

	if _, err := svc.AnalyzeRecipe(context.Background(), 1, false); err == nil {
		t.Fatal("expected error when AI provider is not configured")
	}
}

func TestAnalyzeRecipe_RecipeNotFound(t *testing.T) {
	svc, _ := newAllergenTestService(&testutil.MockAllergenRepo{}, &testutil.MockTextProvider{})

	if _, err := svc.AnalyzeRecipe(context.Background(), 999, false); err == nil {
		t.Fatal("expected error for missing recipe")
	}
}

func TestAnalyzeRecipe_CachedAnalysis_SkipsAI(t *testing.T) {
	aiCalled := false
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			aiCalled = true
			return &ai.AllergenResult{}, nil
		},
	}
	allergenRepo := &testutil.MockAllergenRepo{
		GetAnalysisByRecipeIDFunc: func(recipeID uint) (*models.AllergenAnalysis, error) {
			return &models.AllergenAnalysis{
				ID:            42,
				RecipeID:      recipeID,
				PromptVersion: promptVersion,
				IsPremium:     false,
				ContainsNuts:  true,
			}, nil
		},
	}
	svc, _ := newAllergenTestService(allergenRepo, provider)

	resp, err := svc.AnalyzeRecipe(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("AnalyzeRecipe error: %v", err)
	}
	if aiCalled {
		t.Error("AI provider should not be called when a matching cached analysis exists")
	}
	if resp.ID != 42 || !resp.ContainsNuts {
		t.Errorf("cached analysis not returned: %+v", resp.AllergenAnalysis)
	}
	if resp.Disclaimer != AllergenDisclaimer {
		t.Errorf("Disclaimer = %q, want standard disclaimer", resp.Disclaimer)
	}
}

func TestAnalyzeRecipe_CacheBypassedOnPremiumMismatch(t *testing.T) {
	var updated *models.AllergenAnalysis
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			if !req.IsPremium {
				t.Error("AllergenRequest.IsPremium = false, want true")
			}
			return &ai.AllergenResult{Confidence: 0.95}, nil
		},
	}
	allergenRepo := &testutil.MockAllergenRepo{
		GetAnalysisByRecipeIDFunc: func(recipeID uint) (*models.AllergenAnalysis, error) {
			// Cached as a free-tier analysis; the premium request must re-run.
			return &models.AllergenAnalysis{ID: 42, RecipeID: recipeID, PromptVersion: promptVersion, IsPremium: false}, nil
		},
		UpdateAnalysisFunc: func(analysis *models.AllergenAnalysis) error {
			updated = analysis
			return nil
		},
		CreateAnalysisFunc: func(analysis *models.AllergenAnalysis) error {
			t.Error("CreateAnalysis called, want UpdateAnalysis for an existing row")
			return nil
		},
	}
	svc, _ := newAllergenTestService(allergenRepo, provider)

	resp, err := svc.AnalyzeRecipe(context.Background(), 1, true)
	if err != nil {
		t.Fatalf("AnalyzeRecipe error: %v", err)
	}
	if updated == nil {
		t.Fatal("UpdateAnalysis was not called")
	}
	if updated.ID != 42 {
		t.Errorf("updated.ID = %d, want existing row ID 42", updated.ID)
	}
	if !updated.IsPremium {
		t.Error("updated.IsPremium = false, want true")
	}
	if !resp.IsPremium {
		t.Error("response IsPremium = false, want true")
	}
}

func TestAnalyzeRecipe_CreatesAnalysis_StampsPromptVersion(t *testing.T) {
	var created *models.AllergenAnalysis
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			// The test recipe has 4 ingredients; all must be forwarded.
			if len(req.Ingredients) != 4 {
				t.Errorf("len(Ingredients) = %d, want 4", len(req.Ingredients))
			}
			return &ai.AllergenResult{
				IngredientAnalyses: []ai.IngredientAnalysisResult{
					{IngredientName: "All-purpose flour", CommonAllergens: []string{"gluten", "wheat"}, Confidence: 0.97},
					{IngredientName: "Milk", CommonAllergens: []string{"dairy"}, Confidence: 0.99},
					{IngredientName: "Egg", CommonAllergens: []string{"egg"}, Confidence: 0.99},
					{IngredientName: "Vegetable oil", PossibleAllergens: []string{"soy"}, SeedOilRisk: true, Confidence: 0.7},
				},
				Confidence:     0.92,
				RequiresReview: false,
			}, nil
		},
	}
	allergenRepo := &testutil.MockAllergenRepo{
		CreateAnalysisFunc: func(analysis *models.AllergenAnalysis) error {
			analysis.ID = 7
			created = analysis
			return nil
		},
	}
	svc, _ := newAllergenTestService(allergenRepo, provider)

	resp, err := svc.AnalyzeRecipe(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("AnalyzeRecipe error: %v", err)
	}
	if created == nil {
		t.Fatal("CreateAnalysis was not called")
	}
	if created.PromptVersion != promptVersion {
		t.Errorf("PromptVersion = %q, want %q", created.PromptVersion, promptVersion)
	}
	if created.RecipeID != 1 {
		t.Errorf("RecipeID = %d, want 1", created.RecipeID)
	}
	if created.IsPremium {
		t.Error("IsPremium = true, want false")
	}
	if created.RequiresReview {
		t.Error("RequiresReview = true, want false for confidence 0.92")
	}
	// Aggregate flags derived from ingredient analyses.
	if !created.ContainsGluten || !created.ContainsDairy || !created.ContainsEggs {
		t.Errorf("aggregate flags wrong: gluten=%v dairy=%v eggs=%v", created.ContainsGluten, created.ContainsDairy, created.ContainsEggs)
	}
	if !created.ContainsSoy {
		t.Error("ContainsSoy = false; possible allergens must also set aggregate flags")
	}
	if !created.ContainsSeedOils {
		t.Error("ContainsSeedOils = false, want true when SeedOilRisk is set")
	}
	if created.ContainsNuts || created.ContainsShellfish {
		t.Errorf("unexpected flags: nuts=%v shellfish=%v", created.ContainsNuts, created.ContainsShellfish)
	}
	if resp.Disclaimer != AllergenDisclaimer {
		t.Errorf("Disclaimer = %q, want standard disclaimer", resp.Disclaimer)
	}
	if len(resp.IngredientAnalyses) != 4 {
		t.Errorf("len(IngredientAnalyses) = %d, want 4", len(resp.IngredientAnalyses))
	}
}

func TestAnalyzeRecipe_LowConfidence_RequiresReview(t *testing.T) {
	var created *models.AllergenAnalysis
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			return &ai.AllergenResult{Confidence: 0.5, RequiresReview: false}, nil
		},
	}
	allergenRepo := &testutil.MockAllergenRepo{
		CreateAnalysisFunc: func(analysis *models.AllergenAnalysis) error {
			created = analysis
			return nil
		},
	}
	svc, _ := newAllergenTestService(allergenRepo, provider)

	if _, err := svc.AnalyzeRecipe(context.Background(), 1, false); err != nil {
		t.Fatalf("AnalyzeRecipe error: %v", err)
	}
	if created == nil || !created.RequiresReview {
		t.Error("RequiresReview should be forced true when confidence < 0.8")
	}
}

func TestAnalyzeRecipe_AIError(t *testing.T) {
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			return nil, errors.New("model overloaded")
		},
	}
	svc, _ := newAllergenTestService(&testutil.MockAllergenRepo{}, provider)

	if _, err := svc.AnalyzeRecipe(context.Background(), 1, false); err == nil {
		t.Fatal("expected error when AI analysis fails")
	}
}

func TestAnalyzeRecipe_PersistFails(t *testing.T) {
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			return &ai.AllergenResult{Confidence: 0.9}, nil
		},
	}
	allergenRepo := &testutil.MockAllergenRepo{
		CreateAnalysisFunc: func(analysis *models.AllergenAnalysis) error {
			return errors.New("db down")
		},
	}
	svc, _ := newAllergenTestService(allergenRepo, provider)

	if _, err := svc.AnalyzeRecipe(context.Background(), 1, false); err == nil {
		t.Fatal("expected error when persisting the analysis fails")
	}
}

// --- CheckFamily ---

// checkFamilyFixture wires an AllergenService whose stored analysis flags
// peanuts as a common allergen and soy as a possible allergen, with the given
// family members.
func checkFamilyFixture(members []models.FamilyMember, onUpdate func(*models.AllergenAnalysis)) *AllergenService {
	allergenRepo := &testutil.MockAllergenRepo{
		GetAnalysisByRecipeIDFunc: func(recipeID uint) (*models.AllergenAnalysis, error) {
			return &models.AllergenAnalysis{
				ID:       1,
				RecipeID: recipeID,
				IngredientAnalyses: models.IngredientAnalysisList{
					{IngredientName: "Peanut butter", CommonAllergens: []string{"peanuts"}, PossibleAllergens: []string{"soy"}},
					{IngredientName: "Milk", CommonAllergens: []string{"dairy"}},
				},
			}, nil
		},
		UpdateAnalysisFunc: func(analysis *models.AllergenAnalysis) error {
			if onUpdate != nil {
				onUpdate(analysis)
			}
			return nil
		},
	}
	familyRepo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID, Members: members}, nil
		},
	}
	return NewAllergenService(&config.Config{}, allergenRepo, familyRepo, testutil.NewMockRecipeRepo(), &testutil.MockTextProvider{}, nil)
}

func TestCheckFamily_MemberStatusMapping(t *testing.T) {
	members := []models.FamilyMember{
		{ID: 1, FamilyID: 7, Name: "Alice", DietaryProfile: &models.DietaryProfile{
			Allergies: models.AllergyList{{Name: "peanuts", Severity: "severe"}},
		}},
		{ID: 2, FamilyID: 7, Name: "Bob", DietaryProfile: &models.DietaryProfile{
			Allergies: models.AllergyList{{Name: "soy"}},
		}},
		{ID: 3, FamilyID: 7, Name: "Carol", DietaryProfile: &models.DietaryProfile{
			Intolerances: models.StringList{"dairy"},
		}},
		{ID: 4, FamilyID: 7, Name: "NoProfile"},
		{ID: 5, FamilyID: 7, Name: "Eve", DietaryProfile: &models.DietaryProfile{
			Allergies: models.AllergyList{{Name: "shellfish"}},
		}},
	}
	var persisted *models.AllergenAnalysis
	svc := checkFamilyFixture(members, func(a *models.AllergenAnalysis) { persisted = a })

	resp, err := svc.CheckFamily(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("CheckFamily error: %v", err)
	}
	if resp.RecipeID != 1 {
		t.Errorf("RecipeID = %d, want 1", resp.RecipeID)
	}
	if resp.Disclaimer != AllergenDisclaimer {
		t.Errorf("Disclaimer = %q, want standard disclaimer", resp.Disclaimer)
	}
	if len(resp.MemberResults) != 5 {
		t.Fatalf("len(MemberResults) = %d, want 5", len(resp.MemberResults))
	}

	wantStatus := map[string]string{
		"Alice":     "unsafe",  // common allergen match
		"Bob":       "caution", // possible allergen match
		"Carol":     "caution", // intolerance match
		"NoProfile": "safe",    // no profile to check
		"Eve":       "safe",    // no matching allergen
	}
	for _, mr := range resp.MemberResults {
		if mr.Status != wantStatus[mr.MemberName] {
			t.Errorf("%s status = %q, want %q (warnings: %v)", mr.MemberName, mr.Status, wantStatus[mr.MemberName], mr.Warnings)
		}
	}

	// Alice (unsafe) must have a specific warning; safe members none.
	for _, mr := range resp.MemberResults {
		switch mr.MemberName {
		case "Alice":
			if len(mr.Warnings) == 0 {
				t.Error("Alice should have at least one warning")
			}
		case "NoProfile", "Eve":
			if len(mr.Warnings) != 0 {
				t.Errorf("%s warnings = %v, want none", mr.MemberName, mr.Warnings)
			}
		}
	}

	// Safe/unsafe profile lists are persisted back onto the analysis.
	if persisted == nil {
		t.Fatal("UpdateAnalysis was not called to persist profile results")
	}
	if len(persisted.UnsafeForProfiles) != 1 || persisted.UnsafeForProfiles[0] != 1 {
		t.Errorf("UnsafeForProfiles = %v, want [1]", persisted.UnsafeForProfiles)
	}
	// Caution counts as safe in the persisted lists (only unsafe is excluded).
	if len(persisted.SafeForProfiles) != 4 {
		t.Errorf("SafeForProfiles = %v, want the 4 non-unsafe member IDs", persisted.SafeForProfiles)
	}
}

func TestCheckFamily_SubFormMatch_Unsafe(t *testing.T) {
	members := []models.FamilyMember{
		{ID: 1, FamilyID: 7, Name: "Dana", DietaryProfile: &models.DietaryProfile{
			Allergies: models.AllergyList{{Name: "legumes", SubForms: []string{"peanuts"}}},
		}},
	}
	svc := checkFamilyFixture(members, nil)

	resp, err := svc.CheckFamily(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("CheckFamily error: %v", err)
	}
	if resp.MemberResults[0].Status != "unsafe" {
		t.Errorf("status = %q, want unsafe via allergy sub-form match", resp.MemberResults[0].Status)
	}
}

func TestCheckFamily_UnsafeNotDowngradedByCaution(t *testing.T) {
	// A member allergic to peanuts (unsafe) AND soy (caution) stays unsafe.
	members := []models.FamilyMember{
		{ID: 1, FamilyID: 7, Name: "Frank", DietaryProfile: &models.DietaryProfile{
			Allergies: models.AllergyList{{Name: "peanuts"}, {Name: "soy"}},
		}},
	}
	svc := checkFamilyFixture(members, nil)

	resp, err := svc.CheckFamily(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("CheckFamily error: %v", err)
	}
	if resp.MemberResults[0].Status != "unsafe" {
		t.Errorf("status = %q, want unsafe (caution must not downgrade)", resp.MemberResults[0].Status)
	}
}

func TestCheckFamily_NoAnalysis(t *testing.T) {
	svc := NewAllergenService(&config.Config{}, &testutil.MockAllergenRepo{}, &testutil.MockFamilyRepo{}, testutil.NewMockRecipeRepo(), &testutil.MockTextProvider{}, nil)

	if _, err := svc.CheckFamily(context.Background(), 1, 10); err == nil {
		t.Fatal("expected error when no analysis exists for the recipe")
	}
}

func TestCheckFamily_FamilyLookupFails(t *testing.T) {
	allergenRepo := &testutil.MockAllergenRepo{
		GetAnalysisByRecipeIDFunc: func(recipeID uint) (*models.AllergenAnalysis, error) {
			return &models.AllergenAnalysis{ID: 1, RecipeID: recipeID}, nil
		},
	}
	familyRepo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return nil, fmt.Errorf("record not found")
		},
	}
	svc := NewAllergenService(&config.Config{}, allergenRepo, familyRepo, testutil.NewMockRecipeRepo(), &testutil.MockTextProvider{}, nil)

	if _, err := svc.CheckFamily(context.Background(), 1, 10); err == nil {
		t.Fatal("expected error when family lookup fails")
	}
}

// --- GetAnalysis ---

func TestGetAnalysis_Success(t *testing.T) {
	allergenRepo := &testutil.MockAllergenRepo{
		GetAnalysisByRecipeIDFunc: func(recipeID uint) (*models.AllergenAnalysis, error) {
			return &models.AllergenAnalysis{ID: 9, RecipeID: recipeID, ContainsDairy: true}, nil
		},
	}
	svc, _ := newAllergenTestService(allergenRepo, &testutil.MockTextProvider{})

	resp, err := svc.GetAnalysis(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAnalysis error: %v", err)
	}
	if resp.ID != 9 || !resp.ContainsDairy {
		t.Errorf("analysis = %+v, want stored analysis", resp.AllergenAnalysis)
	}
	if resp.Disclaimer != AllergenDisclaimer {
		t.Errorf("Disclaimer = %q, want standard disclaimer", resp.Disclaimer)
	}
}

func TestGetAnalysis_NotFound(t *testing.T) {
	svc, _ := newAllergenTestService(&testutil.MockAllergenRepo{}, &testutil.MockTextProvider{})

	if _, err := svc.GetAnalysis(context.Background(), 1); err == nil {
		t.Fatal("expected error when no analysis exists")
	}
}
