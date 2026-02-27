package service

import (
	"context"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// SearchService handles web recipe search and import from search results.
type SearchService struct {
	Cfg            *config.Config
	SearchProvider ai.SearchProvider
}

// NewSearchService creates a new SearchService.
func NewSearchService(cfg *config.Config, searchProvider ai.SearchProvider) *SearchService {
	return &SearchService{
		Cfg:            cfg,
		SearchProvider: searchProvider,
	}
}

// SearchRecipes searches the web for recipes matching the query.
func (s *SearchService) SearchRecipes(ctx context.Context, query string, count int) ([]ai.SearchResult, error) {
	return s.SearchProvider.SearchRecipes(ctx, query, count)
}
