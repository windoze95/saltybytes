package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// newSimilarityFixture builds a SimilarityHandler with a recipe (ID 1) in the
// repo, plus the given vector repo and embedding provider mocks.
func newSimilarityFixture(vectorRepo *testutil.MockVectorRepo, embedProvider *testutil.MockEmbeddingProvider) *SimilarityHandler {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	return NewSimilarityHandler(vectorRepo, nil, embedProvider, svc)
}

// newSimilarByURLFixture builds a SimilarityHandler wired with a canonical
// repo for the preview-screen similarity endpoint.
func newSimilarByURLFixture(vectorRepo *testutil.MockVectorRepo, canonicalRepo *testutil.MockCanonicalRecipeRepo, embedProvider *testutil.MockEmbeddingProvider) *SimilarityHandler {
	repo := testutil.NewMockRecipeRepo()
	svc := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	return NewSimilarityHandler(vectorRepo, canonicalRepo, embedProvider, svc)
}

func similarRecipe(id uint, title string) models.Recipe {
	r := testutil.TestRecipe()
	r.Model = gorm.Model{ID: id}
	r.Title = title
	r.CreatedAt = time.Now()
	r.UpdatedAt = time.Now()
	return *r
}

func TestFindSimilar_UsesStoredEmbedding(t *testing.T) {
	stored := "[0.1,0.2,0.3]"
	embedCalled := false

	vectorRepo := &testutil.MockVectorRepo{
		GetRecipeEmbeddingFunc: func(recipeID uint) (*string, error) {
			return &stored, nil
		},
		FindSimilarFunc: func(embeddingLiteral string, excludeRecipeID uint, limit int) ([]models.Recipe, error) {
			return []models.Recipe{similarRecipe(2, "Blueberry Pancakes")}, nil
		},
	}
	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			embedCalled = true
			return []float32{0.9}, nil
		},
	}

	handler := newSimilarityFixture(vectorRepo, embedProvider)
	r := gin.New()
	r.GET("/recipes/similar/:recipe_id", handler.FindSimilar)

	req := httptest.NewRequest("GET", "/recipes/similar/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if embedCalled {
		t.Error("embedding provider should NOT be called when a stored embedding exists")
	}
	if len(vectorRepo.UpdateEmbeddingCalls) != 0 {
		t.Errorf("UpdateEmbedding should not be called, got %d calls", len(vectorRepo.UpdateEmbeddingCalls))
	}
	if len(vectorRepo.FindSimilarCalls) != 1 {
		t.Fatalf("FindSimilar calls = %d, want 1", len(vectorRepo.FindSimilarCalls))
	}
	call := vectorRepo.FindSimilarCalls[0]
	if call.EmbeddingLiteral != stored {
		t.Errorf("FindSimilar embedding = %q, want stored %q", call.EmbeddingLiteral, stored)
	}
	if call.ExcludeRecipeID != 1 {
		t.Errorf("FindSimilar excludeRecipeID = %d, want 1", call.ExcludeRecipeID)
	}
	if call.Limit != 10 {
		t.Errorf("FindSimilar limit = %d, want default 10", call.Limit)
	}

	// Response must use the camelCase RecipeListItem DTO shape
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	items, ok := body["similar_recipes"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("response should contain 1 similar_recipes entry, body: %s", w.Body.String())
	}
	item := items[0].(map[string]interface{})
	if item["id"] != "2" {
		t.Errorf("item id = %v, want \"2\"", item["id"])
	}
	if item["title"] != "Blueberry Pancakes" {
		t.Errorf("item title = %v, want 'Blueberry Pancakes'", item["title"])
	}
	if _, ok := item["imageUrl"]; !ok {
		t.Error("item should have camelCase 'imageUrl' field")
	}
	if _, ok := item["ownerId"]; !ok {
		t.Error("item should have camelCase 'ownerId' field")
	}
}

func TestFindSimilar_GeneratesAndStoresOnNullEmbedding(t *testing.T) {
	embedCalls := 0

	vectorRepo := &testutil.MockVectorRepo{
		GetRecipeEmbeddingFunc: func(recipeID uint) (*string, error) {
			return nil, nil // no stored embedding
		},
		FindSimilarFunc: func(embeddingLiteral string, excludeRecipeID uint, limit int) ([]models.Recipe, error) {
			return []models.Recipe{}, nil
		},
	}
	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			embedCalls++
			return []float32{0.5, 0.25}, nil
		},
	}

	handler := newSimilarityFixture(vectorRepo, embedProvider)
	r := gin.New()
	r.GET("/recipes/similar/:recipe_id", handler.FindSimilar)

	req := httptest.NewRequest("GET", "/recipes/similar/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if embedCalls != 1 {
		t.Errorf("embedding provider calls = %d, want 1", embedCalls)
	}
	if len(vectorRepo.UpdateEmbeddingCalls) != 1 || vectorRepo.UpdateEmbeddingCalls[0] != 1 {
		t.Errorf("UpdateEmbedding should be called once for recipe 1, got %v", vectorRepo.UpdateEmbeddingCalls)
	}
	if len(vectorRepo.FindSimilarCalls) != 1 {
		t.Fatalf("FindSimilar calls = %d, want 1", len(vectorRepo.FindSimilarCalls))
	}
	wantLiteral := repository.PgvectorLiteral([]float32{0.5, 0.25})
	if vectorRepo.FindSimilarCalls[0].EmbeddingLiteral != wantLiteral {
		t.Errorf("FindSimilar embedding = %q, want %q", vectorRepo.FindSimilarCalls[0].EmbeddingLiteral, wantLiteral)
	}

	// Empty result still serializes as an array, not null
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["similar_recipes"].([]interface{}); !ok {
		t.Errorf("similar_recipes should be an array, body: %s", w.Body.String())
	}
}

func TestFindSimilar_LimitParam(t *testing.T) {
	stored := "[0.1]"
	vectorRepo := &testutil.MockVectorRepo{
		GetRecipeEmbeddingFunc: func(recipeID uint) (*string, error) { return &stored, nil },
	}
	handler := newSimilarityFixture(vectorRepo, &testutil.MockEmbeddingProvider{})

	r := gin.New()
	r.GET("/recipes/similar/:recipe_id", handler.FindSimilar)

	// limit above the max is capped at 25
	req := httptest.NewRequest("GET", "/recipes/similar/1?limit=100", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := vectorRepo.FindSimilarCalls[0].Limit; got != 25 {
		t.Errorf("limit = %d, want capped 25", got)
	}

	// explicit valid limit passes through
	req = httptest.NewRequest("GET", "/recipes/similar/1?limit=5", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := vectorRepo.FindSimilarCalls[1].Limit; got != 5 {
		t.Errorf("limit = %d, want 5", got)
	}
}

func TestFindSimilar_RecipeNotFound(t *testing.T) {
	handler := newSimilarityFixture(&testutil.MockVectorRepo{}, &testutil.MockEmbeddingProvider{})

	r := gin.New()
	r.GET("/recipes/similar/:recipe_id", handler.FindSimilar)

	req := httptest.NewRequest("GET", "/recipes/similar/999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestFindSimilar_InvalidID(t *testing.T) {
	handler := newSimilarityFixture(&testutil.MockVectorRepo{}, &testutil.MockEmbeddingProvider{})

	r := gin.New()
	r.GET("/recipes/similar/:recipe_id", handler.FindSimilar)

	req := httptest.NewRequest("GET", "/recipes/similar/abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestFindSimilarByURL_UsesStoredCanonicalEmbedding(t *testing.T) {
	stored := "[0.4,0.5,0.6]"
	src := &models.CanonicalRecipe{
		OriginalURL: "https://pinchofyum.com/bang-bang-salmon",
		Embedding:   &stored,
		RecipeData: models.RecipeDef{
			Title:     "Bang Bang Salmon",
			SourceURL: "https://pinchofyum.com/bang-bang-salmon",
		},
	}
	src.ID = 42

	match := models.CanonicalRecipe{
		OriginalURL: "https://pinchofyum.com/miso-ramen",
		RecipeData: models.RecipeDef{
			Title:     "Miso Peanut Ramen Bowls",
			SourceURL: "https://www.pinchofyum.com/miso-ramen",
		},
	}
	match.ID = 43

	embedCalled := false
	vectorRepo := &testutil.MockVectorRepo{
		FindSimilarCanonicalsFunc: func(lit string, exclude uint, limit int) ([]models.CanonicalRecipe, error) {
			if lit != stored {
				t.Errorf("used embedding %q, want stored %q", lit, stored)
			}
			if exclude != 42 {
				t.Errorf("excluded %d, want 42", exclude)
			}
			return []models.CanonicalRecipe{match}, nil
		},
	}
	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(string) (*models.CanonicalRecipe, error) { return src, nil },
	}
	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(context.Context, string) ([]float32, error) { embedCalled = true; return nil, nil },
	}

	handler := newSimilarByURLFixture(vectorRepo, canonicalRepo, embedProvider)
	r := gin.New()
	r.GET("/recipes/similar-by-url", handler.FindSimilarByURL)

	req := httptest.NewRequest("GET", "/recipes/similar-by-url?u=https://pinchofyum.com/bang-bang-salmon", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if embedCalled {
		t.Error("should not generate an embedding when one is stored")
	}
	var body struct {
		SimilarRecipes []struct {
			Title        string `json:"title"`
			SourceURL    string `json:"source_url"`
			SourceDomain string `json:"source_domain"`
		} `json:"similar_recipes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.SimilarRecipes) != 1 {
		t.Fatalf("got %d items, want 1", len(body.SimilarRecipes))
	}
	got := body.SimilarRecipes[0]
	if got.Title != "Miso Peanut Ramen Bowls" || got.SourceDomain != "pinchofyum.com" {
		t.Errorf("item = %+v", got)
	}
}

func TestFindSimilarByURL_CacheMissReturnsEmpty(t *testing.T) {
	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(string) (*models.CanonicalRecipe, error) {
			return nil, fmt.Errorf("not found")
		},
	}
	handler := newSimilarByURLFixture(&testutil.MockVectorRepo{}, canonicalRepo, &testutil.MockEmbeddingProvider{})
	r := gin.New()
	r.GET("/recipes/similar-by-url", handler.FindSimilarByURL)

	req := httptest.NewRequest("GET", "/recipes/similar-by-url?u=https://example.com/never-seen", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// A miss is not an error: the section just hides.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"similar_recipes":[]`) {
		t.Errorf("body = %s, want empty list", w.Body.String())
	}
}

func TestFindSimilarByURL_MissingURL(t *testing.T) {
	handler := newSimilarByURLFixture(&testutil.MockVectorRepo{}, &testutil.MockCanonicalRecipeRepo{}, &testutil.MockEmbeddingProvider{})
	r := gin.New()
	r.GET("/recipes/similar-by-url", handler.FindSimilarByURL)

	req := httptest.NewRequest("GET", "/recipes/similar-by-url", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
