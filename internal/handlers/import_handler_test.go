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
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func newImportService(repo *testutil.MockRecipeRepo, textProvider ai.TextProvider) *service.ImportService {
	recipeService := service.NewRecipeService(&config.Config{}, repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{})
	return service.NewImportService(&config.Config{}, repo, recipeService, textProvider, nil, nil)
}

func TestImportFromText_Handler_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	mockText := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	importSvc := newImportService(repo, mockText)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/text", setUser(user), handler.ImportFromText)

	body := `{"text": "Here is my recipe for pancakes with flour, eggs, and milk."}`
	req := httptest.NewRequest("POST", "/recipes/import/text", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["recipe"] == nil {
		t.Error("response should contain 'recipe' field")
	}
}

func TestImportFromText_Handler_MissingText(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/text", setUser(user), handler.ImportFromText)

	body := `{}`
	req := httptest.NewRequest("POST", "/recipes/import/text", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestImportManual_Handler_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/manual", setUser(user), handler.ImportManual)

	body := `{
		"title": "Test Recipe",
		"ingredients": [{"name": "Flour", "unit": "cups", "amount": 2}],
		"instructions": ["Mix", "Bake"]
	}`
	req := httptest.NewRequest("POST", "/recipes/import/manual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestImportManual_Handler_MissingTitle(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/manual", setUser(user), handler.ImportManual)

	body := `{
		"ingredients": [{"name": "Flour"}],
		"instructions": ["Mix"]
	}`
	req := httptest.NewRequest("POST", "/recipes/import/manual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestImportURL_Handler_MissingURL(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/url", setUser(user), handler.ImportFromURL)

	body := `{}`
	req := httptest.NewRequest("POST", "/recipes/import/url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
