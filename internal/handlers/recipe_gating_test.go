package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// newGatedRecipeHandler builds a RecipeHandler with a SubscriptionService
// backed by the given user repo so AI-generation gating is exercised.
func newGatedRecipeHandler(userRepo *testutil.MockUserRepo) (*RecipeHandler, *testutil.MockRecipeRepo) {
	recipeRepo := testutil.NewMockRecipeRepo()
	svc := service.NewRecipeService(&config.Config{}, recipeRepo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	handler := NewRecipeHandler(svc)
	handler.SubService = service.NewSubscriptionService(&config.Config{}, userRepo)
	return handler, recipeRepo
}

func TestGenerateRecipe_FreeUserAtLimit_403(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:             gorm.Model{ID: 1},
		UserID:            user.ID,
		Tier:              models.TierFree,
		AIGenerationsUsed: 50, // free-tier limit
		MonthlyResetAt:    time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, recipeRepo := newGatedRecipeHandler(userRepo)

	r := gin.New()
	r.POST("/recipes/chat", setUser(user), handler.GenerateRecipe)

	req := httptest.NewRequest("POST", "/recipes/chat", strings.NewReader(`{"user_prompt": "pancakes", "gen_image": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
	if len(recipeRepo.Recipes) != 0 {
		t.Error("no recipe should be created when the user is over their AI generation limit")
	}
}

func TestGenerateRecipe_FreeUserUnderLimit_IncrementsUsage(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:             gorm.Model{ID: 1},
		UserID:            user.ID,
		Tier:              models.TierFree,
		AIGenerationsUsed: 3,
		MonthlyResetAt:    time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newGatedRecipeHandler(userRepo)

	r := gin.New()
	r.POST("/recipes/chat", setUser(user), handler.GenerateRecipe)

	req := httptest.NewRequest("POST", "/recipes/chat", strings.NewReader(`{"user_prompt": "pancakes", "gen_image": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := userRepo.Users[user.ID].Subscription.AIGenerationsUsed; got != 4 {
		t.Errorf("AIGenerationsUsed = %d, want 4 (incremented on success)", got)
	}
}

func TestGenerateRecipe_PremiumUserOverFreeLimit_Allowed(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:             gorm.Model{ID: 1},
		UserID:            user.ID,
		Tier:              models.TierPremium,
		AIGenerationsUsed: 999,
		MonthlyResetAt:    time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newGatedRecipeHandler(userRepo)

	r := gin.New()
	r.POST("/recipes/chat", setUser(user), handler.GenerateRecipe)

	req := httptest.NewRequest("POST", "/recipes/chat", strings.NewReader(`{"user_prompt": "pancakes", "gen_image": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestGenerateRecipe_NilSubscriptionUser_GatedWithFreeDefaults(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = nil
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newGatedRecipeHandler(userRepo)

	r := gin.New()
	r.POST("/recipes/chat", setUser(user), handler.GenerateRecipe)

	req := httptest.NewRequest("POST", "/recipes/chat", strings.NewReader(`{"user_prompt": "pancakes", "gen_image": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	sub := userRepo.Users[user.ID].Subscription
	if sub == nil {
		t.Fatal("a free-tier subscription row should have been created for gating")
	}
	if sub.Tier != models.TierFree {
		t.Errorf("Tier = %q, want %q", sub.Tier, models.TierFree)
	}
	if sub.AIGenerationsUsed != 1 {
		t.Errorf("AIGenerationsUsed = %d, want 1", sub.AIGenerationsUsed)
	}
}

func TestRegenerateRecipe_FreeUserAtLimit_403(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:             gorm.Model{ID: 1},
		UserID:            user.ID,
		Tier:              models.TierFree,
		AIGenerationsUsed: 50,
		MonthlyResetAt:    time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newGatedRecipeHandler(userRepo)

	r := gin.New()
	r.PUT("/recipes/:recipe_id/chat", setUser(user), handler.RegenerateRecipe)

	req := httptest.NewRequest("PUT", "/recipes/1/chat", strings.NewReader(`{"user_prompt": "less sugar", "gen_image": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestForkRecipe_FreeUserAtLimit_403(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:             gorm.Model{ID: 1},
		UserID:            user.ID,
		Tier:              models.TierFree,
		AIGenerationsUsed: 50,
		MonthlyResetAt:    time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	handler, _ := newGatedRecipeHandler(userRepo)

	r := gin.New()
	r.POST("/recipes/:recipe_id/fork", setUser(user), handler.GenerateRecipeWithFork)

	req := httptest.NewRequest("POST", "/recipes/1/fork", strings.NewReader(`{"user_prompt": "make it vegan", "gen_image": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}
