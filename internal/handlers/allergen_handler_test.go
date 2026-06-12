package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// freeSubscription returns a fresh free-tier subscription with the given
// allergen usage and a reset date in the future.
func freeSubscription(userID uint, allergenUsed int) *models.Subscription {
	return &models.Subscription{
		Model:                gorm.Model{ID: 1},
		UserID:               userID,
		Tier:                 models.TierFree,
		AllergenAnalysesUsed: allergenUsed,
		MonthlyResetAt:       time.Now().Add(time.Hour),
	}
}

// newAllergenHandlerFixture wires an AllergenHandler whose recipe repo holds
// the standard test recipe (owned by user 1) and whose subscription service is
// backed by the given user repo.
func newAllergenHandlerFixture(allergenRepo *testutil.MockAllergenRepo, provider ai.TextProvider, userRepo *testutil.MockUserRepo) (*AllergenHandler, *testutil.MockRecipeRepo) {
	recipeRepo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipeRepo.Recipes[recipe.ID] = recipe

	subService := service.NewSubscriptionService(&config.Config{}, userRepo)
	svc := service.NewAllergenService(&config.Config{}, allergenRepo, &testutil.MockFamilyRepo{}, recipeRepo, provider, subService)
	return NewAllergenHandler(svc), recipeRepo
}

func allergenAIProvider() *testutil.MockTextProvider {
	return &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			return &ai.AllergenResult{
				IngredientAnalyses: []ai.IngredientAnalysisResult{
					{IngredientName: "Milk", CommonAllergens: []string{"dairy"}, Confidence: 0.99},
				},
				Confidence: 0.95,
			}, nil
		},
	}
}

func TestAnalyzeRecipe_Handler_EnvelopeSnakeCase(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = freeSubscription(user.ID, 0)
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, allergenAIProvider(), userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/analyze", setUser(user), handler.AnalyzeRecipe)

	req := httptest.NewRequest("POST", "/recipes/1/allergens/analyze", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	analysisRaw, ok := resp["analysis"]
	if !ok {
		t.Fatalf("response missing 'analysis' envelope key. body: %s", w.Body.String())
	}

	var analysis map[string]interface{}
	if err := json.Unmarshal(analysisRaw, &analysis); err != nil {
		t.Fatalf("failed to parse analysis: %v", err)
	}
	// snake_case contract keys.
	if analysis["contains_dairy"] != true {
		t.Errorf("analysis.contains_dairy = %v, want true", analysis["contains_dairy"])
	}
	if analysis["prompt_version"] != "v1" {
		t.Errorf("analysis.prompt_version = %v, want 'v1'", analysis["prompt_version"])
	}
	if analysis["is_premium"] != false {
		t.Errorf("analysis.is_premium = %v, want false", analysis["is_premium"])
	}
	if _, ok := analysis["ingredient_analyses"]; !ok {
		t.Error("analysis missing 'ingredient_analyses' key")
	}
	disclaimer, _ := analysis["disclaimer"].(string)
	if disclaimer == "" {
		t.Error("analysis.disclaimer should be a non-empty string")
	}

	// Successful analysis increments the allergen usage counter.
	if got := userRepo.Users[user.ID].Subscription.AllergenAnalysesUsed; got != 1 {
		t.Errorf("AllergenAnalysesUsed = %d, want 1 after successful analysis", got)
	}
}

func TestAnalyzeRecipe_Handler_NotOwner_403(t *testing.T) {
	user := testutil.TestUser()
	user.ID = 2 // test recipe is owned by user 1
	user.Subscription = freeSubscription(user.ID, 0)
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, allergenAIProvider(), userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/analyze", setUser(user), handler.AnalyzeRecipe)

	req := httptest.NewRequest("POST", "/recipes/1/allergens/analyze", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestAnalyzeRecipe_Handler_FreeUserAtLimit_403(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = freeSubscription(user.ID, 5) // free-tier allergen limit
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	aiCalled := false
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			aiCalled = true
			return &ai.AllergenResult{}, nil
		},
	}
	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, provider, userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/analyze", setUser(user), handler.AnalyzeRecipe)

	req := httptest.NewRequest("POST", "/recipes/1/allergens/analyze", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
	if aiCalled {
		t.Error("AI provider must not be called when the user is over their allergen limit")
	}
	if got := userRepo.Users[user.ID].Subscription.AllergenAnalysesUsed; got != 5 {
		t.Errorf("AllergenAnalysesUsed = %d, want unchanged 5", got)
	}
}

func TestAnalyzeRecipe_Handler_PremiumBypassesLimit(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:                gorm.Model{ID: 1},
		UserID:               user.ID,
		Tier:                 models.TierPremium,
		AllergenAnalysesUsed: 100,
		MonthlyResetAt:       time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	var gotPremium bool
	provider := &testutil.MockTextProvider{
		AnalyzeAllergensFunc: func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
			gotPremium = req.IsPremium
			return &ai.AllergenResult{Confidence: 0.9}, nil
		},
	}
	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, provider, userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/analyze", setUser(user), handler.AnalyzeRecipe)

	req := httptest.NewRequest("POST", "/recipes/1/allergens/analyze", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if !gotPremium {
		t.Error("premium tier must be forwarded as IsPremium=true to the AI request")
	}
}

func TestAnalyzeRecipe_Handler_InvalidRecipeID_400(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = freeSubscription(user.ID, 0)
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, allergenAIProvider(), userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/analyze", setUser(user), handler.AnalyzeRecipe)

	req := httptest.NewRequest("POST", "/recipes/abc/allergens/analyze", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAnalyzeRecipe_Handler_RecipeNotFound_404(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = freeSubscription(user.ID, 0)
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, allergenAIProvider(), userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/analyze", setUser(user), handler.AnalyzeRecipe)

	req := httptest.NewRequest("POST", "/recipes/999/allergens/analyze", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

// --- GetAnalysis handler ---

func TestGetAnalysis_Handler_Envelope(t *testing.T) {
	user := testutil.TestUser()
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	allergenRepo := &testutil.MockAllergenRepo{
		GetAnalysisByRecipeIDFunc: func(recipeID uint) (*models.AllergenAnalysis, error) {
			return &models.AllergenAnalysis{ID: 3, RecipeID: recipeID, ContainsNuts: true, PromptVersion: "v1"}, nil
		},
	}
	handler, _ := newAllergenHandlerFixture(allergenRepo, &testutil.MockTextProvider{}, userRepo)

	r := gin.New()
	r.GET("/recipes/:recipe_id/allergens", setUser(user), handler.GetAnalysis)

	req := httptest.NewRequest("GET", "/recipes/1/allergens", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	analysis, ok := resp["analysis"]
	if !ok {
		t.Fatalf("response missing 'analysis' envelope key. body: %s", w.Body.String())
	}
	if analysis["contains_nuts"] != true {
		t.Errorf("analysis.contains_nuts = %v, want true", analysis["contains_nuts"])
	}
}

func TestGetAnalysis_Handler_NotFound_404(t *testing.T) {
	user := testutil.TestUser()
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, &testutil.MockTextProvider{}, userRepo)

	r := gin.New()
	r.GET("/recipes/:recipe_id/allergens", setUser(user), handler.GetAnalysis)

	req := httptest.NewRequest("GET", "/recipes/1/allergens", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestGetAnalysis_Handler_NotOwner_403(t *testing.T) {
	user := testutil.TestUser()
	user.ID = 2
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, &testutil.MockTextProvider{}, userRepo)

	r := gin.New()
	r.GET("/recipes/:recipe_id/allergens", setUser(user), handler.GetAnalysis)

	req := httptest.NewRequest("GET", "/recipes/1/allergens", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// --- CheckFamily handler ---

func TestCheckFamily_Handler_Envelope(t *testing.T) {
	user := testutil.TestUser()
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	recipeRepo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipeRepo.Recipes[recipe.ID] = recipe

	allergenRepo := &testutil.MockAllergenRepo{
		GetAnalysisByRecipeIDFunc: func(recipeID uint) (*models.AllergenAnalysis, error) {
			return &models.AllergenAnalysis{
				ID:       1,
				RecipeID: recipeID,
				IngredientAnalyses: models.IngredientAnalysisList{
					{IngredientName: "Peanut butter", CommonAllergens: []string{"peanuts"}},
				},
			}, nil
		},
	}
	familyRepo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID, Members: []models.FamilyMember{
				{ID: 5, FamilyID: 7, Name: "Joey", DietaryProfile: &models.DietaryProfile{
					Allergies: models.AllergyList{{Name: "peanuts"}},
				}},
			}}, nil
		},
	}
	subService := service.NewSubscriptionService(&config.Config{}, userRepo)
	svc := service.NewAllergenService(&config.Config{}, allergenRepo, familyRepo, recipeRepo, &testutil.MockTextProvider{}, subService)
	handler := NewAllergenHandler(svc)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/check-family", setUser(user), handler.CheckFamily)

	req := httptest.NewRequest("POST", "/recipes/1/allergens/check-family", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	checkRaw, ok := resp["family_check"]
	if !ok {
		t.Fatalf("response missing 'family_check' envelope key. body: %s", w.Body.String())
	}

	var check map[string]interface{}
	if err := json.Unmarshal(checkRaw, &check); err != nil {
		t.Fatalf("failed to parse family_check: %v", err)
	}
	if check["recipe_id"] != float64(1) {
		t.Errorf("family_check.recipe_id = %v, want 1", check["recipe_id"])
	}
	results, ok := check["member_results"].([]interface{})
	if !ok || len(results) != 1 {
		t.Fatalf("family_check.member_results = %v, want array of 1", check["member_results"])
	}
	mr, _ := results[0].(map[string]interface{})
	if mr["member_id"] != float64(5) || mr["member_name"] != "Joey" {
		t.Errorf("member_results[0] identity = %v/%v, want 5/Joey", mr["member_id"], mr["member_name"])
	}
	if mr["status"] != "unsafe" {
		t.Errorf("member_results[0].status = %v, want 'unsafe'", mr["status"])
	}
	if _, ok := mr["warnings"]; !ok {
		t.Error("member_results[0] missing 'warnings' key")
	}
	if disclaimer, _ := check["disclaimer"].(string); disclaimer == "" {
		t.Error("family_check.disclaimer should be a non-empty string")
	}
}

func TestCheckFamily_Handler_NotOwner_403(t *testing.T) {
	user := testutil.TestUser()
	user.ID = 2
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newAllergenHandlerFixture(&testutil.MockAllergenRepo{}, &testutil.MockTextProvider{}, userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/allergens/check-family", setUser(user), handler.CheckFamily)

	req := httptest.NewRequest("POST", "/recipes/1/allergens/check-family", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
