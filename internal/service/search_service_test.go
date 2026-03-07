package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

func testResults() []ai.SearchResult {
	return []ai.SearchResult{
		{Title: "Chicken Parmesan", URL: "https://example.com/chicken", Source: "example.com", Description: "Tasty"},
	}
}

func freshCacheEntry() *models.SearchCache {
	return &models.SearchCache{
		Model:           gorm.Model{ID: 1},
		NormalizedQuery: "chicken parmesan",
		Results: models.SearchResultList{
			{Title: "Chicken Parmesan", URL: "https://example.com/chicken", Source: "example.com", Description: "Tasty"},
		},
		ResultCount:    1,
		HitCount:       5,
		LastAccessedAt: time.Now(),
		FetchedAt:      time.Now().Add(-1 * time.Hour), // 1 hour ago = fresh
	}
}

func staleCacheEntry() *models.SearchCache {
	entry := freshCacheEntry()
	entry.FetchedAt = time.Now().Add(-25 * time.Hour) // 25 hours ago = stale
	return entry
}

// --- Phase 0 + 1: Exact-match cache tests ---

func TestSearchRecipes_CacheHit(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetByNormalizedQueryFunc: func(query string) (*models.SearchCache, error) {
			return freshCacheEntry(), nil
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	result, err := svc.SearchRecipes(context.Background(), "Chicken Parmesan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.FromCache {
		t.Error("expected FromCache=true for fresh cache hit")
	}
	if searchCalled {
		t.Error("search provider should not be called for cache hit")
	}
	if len(result.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Results))
	}
}

func TestSearchRecipes_CacheStale(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetByNormalizedQueryFunc: func(query string) (*models.SearchCache, error) {
			return staleCacheEntry(), nil
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	result, err := svc.SearchRecipes(context.Background(), "Chicken Parmesan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromCache {
		t.Error("expected FromCache=false for stale cache entry")
	}
	if !searchCalled {
		t.Error("search provider should be called for stale cache entry")
	}
}

func TestSearchRecipes_CacheMiss(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetByNormalizedQueryFunc: func(query string) (*models.SearchCache, error) {
			return nil, fmt.Errorf("not found")
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	result, err := svc.SearchRecipes(context.Background(), "Chicken Parmesan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromCache {
		t.Error("expected FromCache=false for cache miss")
	}
	if !searchCalled {
		t.Error("search provider should be called for cache miss")
	}
}

func TestSearchRecipes_NilCacheRepo(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, nil)
	result, err := svc.SearchRecipes(context.Background(), "Chicken Parmesan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromCache {
		t.Error("expected FromCache=false with nil cache repo")
	}
	if !searchCalled {
		t.Error("search provider should be called with nil cache repo")
	}
}

// --- Phase 2: Semantic/vector cache tests ---

func TestSearchRecipes_SemanticHit(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetByNormalizedQueryFunc: func(query string) (*models.SearchCache, error) {
			return nil, fmt.Errorf("not found") // no exact match
		},
		FindSimilarFunc: func(embedding []float32, threshold float64, limit int) ([]models.SearchCache, error) {
			return []models.SearchCache{*freshCacheEntry()}, nil // semantic match
		},
	}
	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	svc.EmbedProvider = embedProvider

	result, err := svc.SearchRecipes(context.Background(), "chicken parm", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.FromCache {
		t.Error("expected FromCache=true for semantic cache hit")
	}
	if searchCalled {
		t.Error("search provider should not be called for semantic cache hit")
	}
}

func TestSearchRecipes_SemanticMiss(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetByNormalizedQueryFunc: func(query string) (*models.SearchCache, error) {
			return nil, fmt.Errorf("not found")
		},
		FindSimilarFunc: func(embedding []float32, threshold float64, limit int) ([]models.SearchCache, error) {
			return nil, nil // no similar entries
		},
	}
	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	svc.EmbedProvider = embedProvider

	result, err := svc.SearchRecipes(context.Background(), "spaghetti bolognese", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromCache {
		t.Error("expected FromCache=false for semantic miss")
	}
	if !searchCalled {
		t.Error("search provider should be called for semantic miss")
	}
}

func TestSearchRecipes_EmbeddingFailure(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetByNormalizedQueryFunc: func(query string) (*models.SearchCache, error) {
			return nil, fmt.Errorf("not found")
		},
	}
	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return nil, fmt.Errorf("embedding API error")
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	svc.EmbedProvider = embedProvider

	result, err := svc.SearchRecipes(context.Background(), "chicken parmesan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromCache {
		t.Error("expected FromCache=false when embedding fails")
	}
	if !searchCalled {
		t.Error("search provider should be called when embedding fails")
	}
}

func TestSearchRecipes_NilEmbedProvider(t *testing.T) {
	searchCalled := false
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled = true
			return testResults(), nil
		},
	}
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetByNormalizedQueryFunc: func(query string) (*models.SearchCache, error) {
			return nil, fmt.Errorf("not found")
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	// EmbedProvider is nil — semantic step should be skipped

	result, err := svc.SearchRecipes(context.Background(), "chicken parmesan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromCache {
		t.Error("expected FromCache=false with nil embed provider")
	}
	if !searchCalled {
		t.Error("search provider should be called with nil embed provider")
	}
}

// --- normalizeQuery tests ---

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  Chicken   Parmesan  ", "chicken parmesan"},
		{"BEEF STEW", "beef stew"},
		{"chicken\tparm", "chicken parm"},
		{"  ", ""},
		{"pasta", "pasta"},
		{"Mixed  CASE   query", "mixed case query"},
		{"unicode café", "unicode café"},
	}

	for _, tc := range tests {
		got := normalizeQuery(tc.input)
		if got != tc.want {
			t.Errorf("normalizeQuery(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- Conversion round-trip test ---

func TestSearchResultConversion(t *testing.T) {
	original := []ai.SearchResult{
		{Title: "A", URL: "https://a.com", Source: "a.com", Rating: 4.5, ImageURL: "https://img.com/a.jpg", Description: "desc a"},
		{Title: "B", URL: "https://b.com", Source: "b.com", Rating: 0, ImageURL: "", Description: "desc b"},
	}

	items := searchResultsToCacheItems(original)
	roundTripped := cacheItemsToSearchResults(items)

	if len(roundTripped) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(roundTripped), len(original))
	}

	for i := range original {
		if roundTripped[i] != original[i] {
			t.Errorf("mismatch at index %d: got %+v, want %+v", i, roundTripped[i], original[i])
		}
	}
}

// --- Phase 3: Background task tests ---

func TestRefreshHotQueries(t *testing.T) {
	searchCalled := 0
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
			searchCalled++
			return testResults(), nil
		},
	}

	upsertCalled := 0
	cacheRepo := &testutil.MockSearchCacheRepo{
		GetHotQueriesFunc: func(minHits int, maxAge, refreshWindow time.Duration) ([]models.SearchCache, error) {
			return []models.SearchCache{*freshCacheEntry()}, nil
		},
		UpsertFunc: func(entry *models.SearchCache) error {
			upsertCalled++
			return nil
		},
	}

	svc := NewSearchService(&config.Config{}, searchProvider, nil, cacheRepo)
	svc.refreshHotQueries()

	if searchCalled != 1 {
		t.Errorf("search provider called %d times, want 1", searchCalled)
	}
	if upsertCalled != 1 {
		t.Errorf("upsert called %d times, want 1", upsertCalled)
	}
}

func TestCleanupStaleEntries(t *testing.T) {
	deleteStaleCalledWithDuration := time.Duration(0)
	cacheRepo := &testutil.MockSearchCacheRepo{
		DeleteStaleFunc: func(maxAge time.Duration) (int64, error) {
			deleteStaleCalledWithDuration = maxAge
			return 3, nil
		},
	}

	svc := NewSearchService(&config.Config{}, &testutil.MockSearchProvider{}, nil, cacheRepo)
	svc.cleanupStaleEntries()

	expected := 30 * 24 * time.Hour
	if deleteStaleCalledWithDuration != expected {
		t.Errorf("DeleteStale called with %v, want %v", deleteStaleCalledWithDuration, expected)
	}
}

func TestStartBackgroundTasks_NilCacheRepo(t *testing.T) {
	svc := NewSearchService(&config.Config{}, &testutil.MockSearchProvider{}, nil, nil)
	// Should not panic
	svc.StartBackgroundTasks()
}
