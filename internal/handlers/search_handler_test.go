package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

func testSearchResults() []ai.SearchResult {
	return []ai.SearchResult{
		{Title: "Chicken Parmesan", URL: "https://example.com/chicken", Source: "example.com", Description: "Tasty chicken parm"},
	}
}

func newTestSearchService(searchFunc func(ctx context.Context, query string, count int) ([]ai.SearchResult, error)) *service.SearchService {
	mock := &testutil.MockSearchProvider{SearchRecipesFunc: searchFunc}
	return service.NewSearchService(&config.Config{}, mock, nil, nil)
}

func TestSearchRecipes_NoUser(t *testing.T) {
	svc := newTestSearchService(nil)
	handler := NewSearchHandler(svc)

	r := gin.New()
	r.GET("/recipes/search", handler.SearchRecipes) // no setUser middleware

	req := httptest.NewRequest("GET", "/recipes/search?q=chicken", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestSearchRecipes_LimitReached(t *testing.T) {
	svc := newTestSearchService(nil)
	handler := NewSearchHandler(svc)

	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:           gorm.Model{ID: 1},
		UserID:          user.ID,
		Tier:            models.TierFree,
		WebSearchesUsed: 20,
	}

	r := gin.New()
	r.GET("/recipes/search", setUser(user), handler.SearchRecipes)

	req := httptest.NewRequest("GET", "/recipes/search?q=chicken", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestSearchRecipes_PremiumUnlimited(t *testing.T) {
	svc := newTestSearchService(func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
		return testSearchResults(), nil
	})
	handler := NewSearchHandler(svc)

	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:           gorm.Model{ID: 1},
		UserID:          user.ID,
		Tier:            models.TierPremium,
		WebSearchesUsed: 999,
	}

	r := gin.New()
	r.GET("/recipes/search", setUser(user), handler.SearchRecipes)

	req := httptest.NewRequest("GET", "/recipes/search?q=chicken", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestSearchRecipes_Success(t *testing.T) {
	svc := newTestSearchService(func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
		return testSearchResults(), nil
	})
	handler := NewSearchHandler(svc)

	user := testutil.TestUser()
	// No subscription record → no limit check applies

	r := gin.New()
	r.GET("/recipes/search", setUser(user), handler.SearchRecipes)

	req := httptest.NewRequest("GET", "/recipes/search?q=chicken+parmesan", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	results, ok := body["results"].([]interface{})
	if !ok || len(results) == 0 {
		t.Error("response should contain non-empty 'results' array")
	}
}

func TestSearchRecipes_EmptyQuery(t *testing.T) {
	svc := newTestSearchService(nil)
	handler := NewSearchHandler(svc)

	user := testutil.TestUser()

	r := gin.New()
	r.GET("/recipes/search", setUser(user), handler.SearchRecipes)

	req := httptest.NewRequest("GET", "/recipes/search", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
