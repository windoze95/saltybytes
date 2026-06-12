package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setUser is a test middleware that injects a user into the gin context.
func setUser(user *models.User) gin.HandlerFunc {
	return func(c *gin.Context) {
		if user != nil {
			c.Set("user", user)
		}
		c.Next()
	}
}

func newRecipeService(repo *testutil.MockRecipeRepo) *service.RecipeService {
	return service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
}

func TestGetRecipe_Valid(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	handler := NewRecipeHandler(svc)

	r := gin.New()
	r.GET("/recipes/:recipe_id", handler.GetRecipe)

	req := httptest.NewRequest("GET", "/recipes/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	recipeData, ok := body["recipe"].(map[string]interface{})
	if !ok {
		t.Fatal("response should contain 'recipe' field")
	}
	if recipeData["title"] != "Classic Pancakes" {
		t.Errorf("recipe title = %v, want 'Classic Pancakes'", recipeData["title"])
	}
}

func TestGetRecipe_InvalidID(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	handler := NewRecipeHandler(svc)

	r := gin.New()
	r.GET("/recipes/:recipe_id", handler.GetRecipe)

	req := httptest.NewRequest("GET", "/recipes/abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetRecipe_NotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	handler := NewRecipeHandler(svc)

	r := gin.New()
	r.GET("/recipes/:recipe_id", handler.GetRecipe)

	req := httptest.NewRequest("GET", "/recipes/999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestListRecipes_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	handler := NewRecipeHandler(svc)

	user := testutil.TestUser()
	r := gin.New()
	r.GET("/recipes", setUser(user), handler.ListRecipes)

	req := httptest.NewRequest("GET", "/recipes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["total"] == nil {
		t.Error("response should contain 'total' field")
	}
}

func TestListRecipes_Unauthorized(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	handler := NewRecipeHandler(svc)

	r := gin.New()
	// No setUser middleware — no user in context
	r.GET("/recipes", handler.ListRecipes)

	req := httptest.NewRequest("GET", "/recipes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestListRecipes_SemanticSearchMergesVectorAndTitleHits(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newRecipeService(repo)

	vectorHit := similarRecipe(1, "Classic Pancakes")
	titleHit := similarRecipe(2, "Pancake Casserole")
	duplicateOfVectorHit := similarRecipe(1, "Classic Pancakes")

	vectorRepo := &testutil.MockVectorRepo{
		SearchUserRecipesByEmbeddingFunc: func(userID uint, embeddingLiteral string, limit int) ([]models.Recipe, error) {
			return []models.Recipe{vectorHit}, nil
		},
		SearchUserRecipesByTitleFunc: func(userID uint, query string, onlyMissingEmbedding bool, limit int) ([]models.Recipe, error) {
			return []models.Recipe{duplicateOfVectorHit, titleHit}, nil
		},
	}
	svc.VectorRepo = vectorRepo
	svc.EmbedProvider = &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.1, 0.2}, nil
		},
	}

	handler := NewRecipeHandler(svc)
	user := testutil.TestUser()
	r := gin.New()
	r.GET("/recipes", setUser(user), handler.ListRecipes)

	req := httptest.NewRequest("GET", "/recipes?q=pancakes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	recipes, ok := body["recipes"].([]interface{})
	if !ok {
		t.Fatalf("response should contain 'recipes' array, body: %s", w.Body.String())
	}
	if len(recipes) != 2 {
		t.Fatalf("recipes len = %d, want 2 (deduped)", len(recipes))
	}
	first := recipes[0].(map[string]interface{})
	second := recipes[1].(map[string]interface{})
	if first["id"] != "1" {
		t.Errorf("first result id = %v, want \"1\" (vector hits rank first)", first["id"])
	}
	if second["id"] != "2" {
		t.Errorf("second result id = %v, want \"2\"", second["id"])
	}
	if body["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", body["total"])
	}

	// The title pass should only cover recipes lacking embeddings when the
	// vector search succeeded.
	if len(vectorRepo.SearchUserRecipesByTitleCalls) != 1 {
		t.Fatalf("title search calls = %d, want 1", len(vectorRepo.SearchUserRecipesByTitleCalls))
	}
	if !vectorRepo.SearchUserRecipesByTitleCalls[0].OnlyMissingEmbedding {
		t.Error("title search should be scoped to recipes missing embeddings after a vector hit")
	}
	if vectorRepo.SearchUserRecipesByTitleCalls[0].Query != "pancakes" {
		t.Errorf("title search query = %q, want \"pancakes\"", vectorRepo.SearchUserRecipesByTitleCalls[0].Query)
	}
}

func TestListRecipes_SearchFallsBackToTitleOnEmbedFailure(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newRecipeService(repo)

	vectorRepo := &testutil.MockVectorRepo{
		SearchUserRecipesByTitleFunc: func(userID uint, query string, onlyMissingEmbedding bool, limit int) ([]models.Recipe, error) {
			return []models.Recipe{similarRecipe(3, "Pancake Muffins")}, nil
		},
	}
	svc.VectorRepo = vectorRepo
	svc.EmbedProvider = &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return nil, fmt.Errorf("embedding service down")
		},
	}

	handler := NewRecipeHandler(svc)
	user := testutil.TestUser()
	r := gin.New()
	r.GET("/recipes", setUser(user), handler.ListRecipes)

	req := httptest.NewRequest("GET", "/recipes?q=pancakes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	recipes := body["recipes"].([]interface{})
	if len(recipes) != 1 {
		t.Fatalf("recipes len = %d, want 1", len(recipes))
	}
	if recipes[0].(map[string]interface{})["id"] != "3" {
		t.Errorf("result id = %v, want \"3\"", recipes[0].(map[string]interface{})["id"])
	}

	// Pure ILIKE fallback must NOT be scoped to missing-embedding rows.
	if len(vectorRepo.SearchUserRecipesByTitleCalls) != 1 {
		t.Fatalf("title search calls = %d, want 1", len(vectorRepo.SearchUserRecipesByTitleCalls))
	}
	if vectorRepo.SearchUserRecipesByTitleCalls[0].OnlyMissingEmbedding {
		t.Error("pure ILIKE fallback should search ALL of the user's recipes")
	}
}

func TestListRecipes_ShortQueryUsesPlainListing(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := newRecipeService(repo)
	vectorRepo := &testutil.MockVectorRepo{}
	svc.VectorRepo = vectorRepo
	svc.EmbedProvider = &testutil.MockEmbeddingProvider{}

	handler := NewRecipeHandler(svc)
	user := testutil.TestUser()
	r := gin.New()
	r.GET("/recipes", setUser(user), handler.ListRecipes)

	req := httptest.NewRequest("GET", "/recipes?q=a", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(vectorRepo.SearchUserRecipesByEmbeddingCalls) != 0 || len(vectorRepo.SearchUserRecipesByTitleCalls) != 0 {
		t.Error("a query under 2 chars should use the plain listing, not search")
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["total"].(float64) != 1 {
		t.Errorf("total = %v, want 1", body["total"])
	}
}

func TestDeleteRecipe_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := service.NewRecipeService(
		&config.Config{EnvVars: config.EnvVars{S3Bucket: "test-bucket", AWSRegion: "us-east-1"}},
		repo,
		&testutil.MockTextProvider{},
		&testutil.MockImageProvider{},
	)
	handler := NewRecipeHandler(svc)

	user := &models.User{Model: gorm.Model{ID: 1}} // Same as recipe.CreatedByID
	r := gin.New()
	r.DELETE("/recipes/:recipe_id", setUser(user), handler.DeleteRecipe)

	req := httptest.NewRequest("DELETE", "/recipes/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Note: This may fail due to S3 not being configured in tests.
	// The recipe is deleted from the mock repo, but S3 deletion will fail.
	// We verify the handler logic is correct by checking the recipe was removed from the repo.
	if _, err := repo.GetRecipeByID(1); err == nil {
		t.Error("Recipe should have been deleted from repo")
	}
}

func TestDeleteRecipe_Forbidden(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedByID = 1
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	handler := NewRecipeHandler(svc)

	otherUser := &models.User{Model: gorm.Model{ID: 999}} // Different user
	r := gin.New()
	r.DELETE("/recipes/:recipe_id", setUser(otherUser), handler.DeleteRecipe)

	req := httptest.NewRequest("DELETE", "/recipes/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}
