package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

func TestImportFromCanonical_Handler_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	canonical := testutil.TestCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByIDFunc: func(id uint) (*models.CanonicalRecipe, error) {
			if id == canonical.ID {
				return canonical, nil
			}
			return nil, fmt.Errorf("not found")
		},
	}

	importSvc := newImportService(repo, nil)
	importSvc.CanonicalRepo = canonicalRepo
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/canonical", setUser(user), handler.ImportFromCanonical)

	body := fmt.Sprintf(`{"canonical_id": %d}`, canonical.ID)
	req := httptest.NewRequest("POST", "/recipes/import/canonical", strings.NewReader(body))
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

func TestImportFromCanonical_Handler_MissingID(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/canonical", setUser(user), handler.ImportFromCanonical)

	body := `{}`
	req := httptest.NewRequest("POST", "/recipes/import/canonical", strings.NewReader(body))
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

func TestPreviewFromURL_Handler_SiteBlocked(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	importSvc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Forbidden"), 403, nil
	}
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/preview/url", setUser(user), handler.PreviewFromURL)

	body := `{"url": "https://example.com/recipe"}`
	req := httptest.NewRequest("POST", "/recipes/preview/url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "site_blocked" {
		t.Errorf("code = %v, want 'site_blocked'", resp["code"])
	}
}

func TestPreviewFromURL_Handler_NotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	importSvc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Not Found"), 404, nil
	}
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/preview/url", setUser(user), handler.PreviewFromURL)

	body := `{"url": "https://example.com/missing-recipe"}`
	req := httptest.NewRequest("POST", "/recipes/preview/url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "not_found" {
		t.Errorf("code = %v, want 'not_found'", resp["code"])
	}
}

func TestImportManual_Handler_RespectsProvidedUnitSystem(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	user.Personalization.UnitSystem = "us_customary" // request should win
	r := gin.New()
	r.POST("/recipes/import/manual", setUser(user), handler.ImportManual)

	body := `{
		"title": "Metric Bread",
		"unit_system": "metric",
		"image_url": "https://example.com/bread.jpg",
		"ingredients": [
			{"name": "flour", "unit": "g", "amount": 500, "metric_unit": "g", "metric_amount": 500, "original_text": "500 g strong bread flour"}
		],
		"instructions": ["Knead", "Bake"]
	}`
	req := httptest.NewRequest("POST", "/recipes/import/manual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if len(repo.Recipes) != 1 {
		t.Fatalf("recipes in repo = %d, want 1", len(repo.Recipes))
	}
	var recipe *models.Recipe
	for _, rec := range repo.Recipes {
		recipe = rec
	}

	if recipe.UnitSystem != "metric" {
		t.Errorf("UnitSystem = %q, want 'metric' (from request, not personalization)", recipe.UnitSystem)
	}
	if recipe.ImageURL != "https://example.com/bread.jpg" {
		t.Errorf("ImageURL = %q, want request image_url", recipe.ImageURL)
	}
	if len(recipe.Ingredients) != 1 {
		t.Fatalf("ingredients count = %d, want 1", len(recipe.Ingredients))
	}
	ing := recipe.Ingredients[0]
	if ing.OriginalText != "500 g strong bread flour" {
		t.Errorf("OriginalText = %q, want '500 g strong bread flour'", ing.OriginalText)
	}
	if ing.MetricUnit != "g" || ing.MetricAmount != 500 {
		t.Errorf("metric fields = %q/%v, want g/500", ing.MetricUnit, ing.MetricAmount)
	}
}

func TestImportManual_Handler_FallsBackToPersonalizationUnitSystem(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	user.Personalization.UnitSystem = "metric"
	r := gin.New()
	r.POST("/recipes/import/manual", setUser(user), handler.ImportManual)

	body := `{
		"title": "No Unit System",
		"ingredients": [{"name": "flour", "unit": "g", "amount": 500}],
		"instructions": ["Bake"]
	}`
	req := httptest.NewRequest("POST", "/recipes/import/manual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	for _, rec := range repo.Recipes {
		if rec.UnitSystem != "metric" {
			t.Errorf("UnitSystem = %q, want 'metric' (from personalization fallback)", rec.UnitSystem)
		}
	}
}

func TestImportManual_Handler_InvalidUnitSystem(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/manual", setUser(user), handler.ImportManual)

	body := `{
		"title": "Bad Units",
		"unit_system": "imperial",
		"ingredients": [{"name": "flour"}],
		"instructions": ["Bake"]
	}`
	req := httptest.NewRequest("POST", "/recipes/import/manual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if len(repo.Recipes) != 0 {
		t.Errorf("no recipe should be created for invalid unit_system")
	}
}

func TestImportFromFiles_Handler_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	importSvc.VisionProvider = &testutil.MockVisionProvider{
		ExtractRecipesFromMediaFunc: func(ctx context.Context, media []ai.MediaInput, unitSystem, requirements string) ([]*ai.RecipeResult, error) {
			r1 := testutil.TestRecipeResult()
			r2 := testutil.TestRecipeResult()
			r2.Title = "Second"
			return []*ai.RecipeResult{r1, r2}, nil
		},
	}
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/files", setUser(user), handler.ImportFromFiles)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	jpegPart, _ := mw.CreateFormFile("files", "a.jpg")
	jpegPart.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00})
	pdfPart, _ := mw.CreateFormFile("files", "b.pdf")
	pdfPart.Write([]byte("%PDF-1.7\nx"))
	mw.Close()

	req := httptest.NewRequest("POST", "/recipes/import/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp struct {
		Recipes []map[string]interface{} `json:"recipes"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Recipes) != 2 {
		t.Errorf("expected 2 recipes in response, got %d", len(resp.Recipes))
	}
	if len(repo.Recipes) != 2 {
		t.Errorf("expected 2 recipes saved, got %d", len(repo.Recipes))
	}
}

func TestImportFromFiles_Handler_NoFiles(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	importSvc.VisionProvider = &testutil.MockVisionProvider{}
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/files", setUser(user), handler.ImportFromFiles)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()
	req := httptest.NewRequest("POST", "/recipes/import/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestImportFromVoice_Handler_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	text := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, transcript, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	importSvc := newImportService(repo, text)
	importSvc.SpeechProvider = &testutil.MockSpeechProvider{
		TranscribeAudioFunc: func(ctx context.Context, audioData []byte, format string) (string, error) {
			return "recipe transcript text", nil
		},
	}
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/voice", setUser(user), handler.ImportFromVoice)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("audio", "note.m4a")
	part.Write([]byte("fake audio bytes"))
	mw.Close()

	req := httptest.NewRequest("POST", "/recipes/import/voice", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(repo.Recipes) != 1 {
		t.Errorf("expected 1 recipe saved, got %d", len(repo.Recipes))
	}
}

func TestImportFromVoice_Handler_MissingAudio(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	importSvc := newImportService(repo, nil)
	importSvc.SpeechProvider = &testutil.MockSpeechProvider{}
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/voice", setUser(user), handler.ImportFromVoice)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()
	req := httptest.NewRequest("POST", "/recipes/import/voice", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
