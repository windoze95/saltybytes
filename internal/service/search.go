package service

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"go.uber.org/zap"
)

const cacheTTL = 24 * time.Hour

// SearchServiceResult wraps search results with a cache flag.
type SearchServiceResult struct {
	Results   []ai.SearchResult
	FromCache bool
	HasMore   bool
}

// SearchService handles web recipe search with caching.
type SearchService struct {
	Cfg            *config.Config
	SearchProvider ai.SearchProvider
	SubService     *SubscriptionService
	CacheRepo      repository.SearchCacheRepo
	EmbedProvider  ai.EmbeddingProvider
}

// NewSearchService creates a new SearchService.
func NewSearchService(cfg *config.Config, searchProvider ai.SearchProvider, subService *SubscriptionService, cacheRepo repository.SearchCacheRepo) *SearchService {
	return &SearchService{
		Cfg:            cfg,
		SearchProvider: searchProvider,
		SubService:     subService,
		CacheRepo:      cacheRepo,
	}
}

// SearchRecipes searches for recipes, checking cache first.
// Caching is only used for the first page (offset == 0); subsequent pages
// go directly to the search provider.
func (s *SearchService) SearchRecipes(ctx context.Context, query string, count int, offset int) (*SearchServiceResult, error) {
	normalized := normalizeQuery(query)

	// Paginated requests bypass cache entirely.
	if offset > 0 {
		results, err := s.SearchProvider.SearchRecipes(ctx, query, count, offset)
		if err != nil {
			return nil, err
		}
		return &SearchServiceResult{
			Results:   results,
			FromCache: false,
			HasMore:   len(results) == count,
		}, nil
	}

	// Phase 1: exact-match cache lookup
	if s.CacheRepo != nil {
		entry, err := s.CacheRepo.GetByNormalizedQuery(normalized)
		if err == nil && time.Since(entry.FetchedAt) < cacheTTL {
			go func() {
				if err := s.CacheRepo.IncrementHitCount(entry.ID); err != nil {
					logger.Get().Warn("failed to increment cache hit count", zap.Error(err))
				}
			}()
			results := cacheItemsToSearchResults(entry.Results)
			return &SearchServiceResult{
				Results:   results,
				FromCache: true,
				HasMore:   len(results) == count,
			}, nil
		}
	}

	// Phase 2: semantic/vector cache lookup
	if s.CacheRepo != nil && s.EmbedProvider != nil {
		embedding, err := s.EmbedProvider.GenerateEmbedding(ctx, normalized)
		if err == nil {
			similar, err := s.CacheRepo.FindSimilar(embedding, 0.92, 1)
			if err == nil && len(similar) > 0 && time.Since(similar[0].FetchedAt) < cacheTTL {
				go func() {
					if err := s.CacheRepo.IncrementHitCount(similar[0].ID); err != nil {
						logger.Get().Warn("failed to increment cache hit count", zap.Error(err))
					}
				}()
				results := cacheItemsToSearchResults(similar[0].Results)
				return &SearchServiceResult{
					Results:   results,
					FromCache: true,
					HasMore:   len(results) == count,
				}, nil
			}
		} else {
			logger.Get().Warn("failed to generate embedding for search query", zap.Error(err))
		}
	}

	// Cache miss — call search provider
	results, err := s.SearchProvider.SearchRecipes(ctx, query, count, 0)
	if err != nil {
		return nil, err
	}

	// Save to cache asynchronously
	if s.CacheRepo != nil {
		go s.saveToCache(normalized, results)
	}

	return &SearchServiceResult{
		Results:   results,
		FromCache: false,
		HasMore:   len(results) == count,
	}, nil
}

// saveToCache upserts search results into the cache.
func (s *SearchService) saveToCache(normalizedQuery string, results []ai.SearchResult) {
	now := time.Now()
	entry := &models.SearchCache{
		NormalizedQuery: normalizedQuery,
		Results:         searchResultsToCacheItems(results),
		ResultCount:     len(results),
		LastAccessedAt:  now,
		FetchedAt:       now,
	}

	// Generate embedding if provider is available
	if s.EmbedProvider != nil {
		embedding, err := s.EmbedProvider.GenerateEmbedding(context.Background(), normalizedQuery)
		if err == nil {
			literal := repository.PgvectorLiteral(embedding)
			entry.Embedding = &literal
		} else {
			logger.Get().Warn("failed to generate embedding for cache entry", zap.Error(err))
		}
	}

	if err := s.CacheRepo.Upsert(entry); err != nil {
		logger.Get().Warn("failed to save search cache", zap.Error(err))
	}
}

// StartBackgroundTasks launches periodic cache maintenance goroutines.
func (s *SearchService) StartBackgroundTasks() {
	if s.CacheRepo == nil {
		return
	}

	go func() {
		for range time.Tick(15 * time.Minute) {
			s.refreshHotQueries()
		}
	}()

	go func() {
		for range time.Tick(6 * time.Hour) {
			s.cleanupStaleEntries()
		}
	}()
}

// refreshHotQueries re-fetches popular queries approaching staleness.
func (s *SearchService) refreshHotQueries() {
	entries, err := s.CacheRepo.GetHotQueries(10, cacheTTL, 2*time.Hour)
	if err != nil {
		logger.Get().Warn("failed to get hot queries", zap.Error(err))
		return
	}

	for _, entry := range entries {
		results, err := s.SearchProvider.SearchRecipes(context.Background(), entry.NormalizedQuery, entry.ResultCount, 0)
		if err != nil {
			logger.Get().Warn("failed to refresh hot query", zap.String("query", entry.NormalizedQuery), zap.Error(err))
			continue
		}

		now := time.Now()
		entry.Results = searchResultsToCacheItems(results)
		entry.ResultCount = len(results)
		entry.FetchedAt = now
		entry.LastAccessedAt = now

		if err := s.CacheRepo.Upsert(&entry); err != nil {
			logger.Get().Warn("failed to update hot query cache", zap.String("query", entry.NormalizedQuery), zap.Error(err))
		}
	}
}

// cleanupStaleEntries removes cache entries not accessed in 30 days.
func (s *SearchService) cleanupStaleEntries() {
	deleted, err := s.CacheRepo.DeleteStale(30 * 24 * time.Hour)
	if err != nil {
		logger.Get().Warn("failed to cleanup stale cache entries", zap.Error(err))
		return
	}
	if deleted > 0 {
		logger.Get().Info("cleaned up stale search cache entries", zap.Int64("deleted", deleted))
	}
}

// normalizeQuery lowercases, trims, and collapses whitespace.
func normalizeQuery(query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	var b strings.Builder
	prevSpace := false
	for _, r := range query {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return b.String()
}

// searchResultsToCacheItems converts ai.SearchResult slice to models.SearchResultList.
func searchResultsToCacheItems(results []ai.SearchResult) models.SearchResultList {
	items := make(models.SearchResultList, len(results))
	for i, r := range results {
		items[i] = models.SearchResultItem{
			Title:       r.Title,
			URL:         r.URL,
			Source:      r.Source,
			Rating:      r.Rating,
			ImageURL:    r.ImageURL,
			Description: r.Description,
		}
	}
	return items
}

// cacheItemsToSearchResults converts models.SearchResultList to ai.SearchResult slice.
func cacheItemsToSearchResults(items models.SearchResultList) []ai.SearchResult {
	results := make([]ai.SearchResult, len(items))
	for i, item := range items {
		results[i] = ai.SearchResult{
			Title:       item.Title,
			URL:         item.URL,
			Source:      item.Source,
			Rating:      item.Rating,
			ImageURL:    item.ImageURL,
			Description: item.Description,
		}
	}
	return results
}
