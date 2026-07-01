package service

import (
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// newGenRecipeService builds a RecipeService with the given AI providers and
// embedding deps wired in (any may be nil for "not configured"). Shared by the
// fork and regenerate service tests.
func newGenRecipeService(repo repository.RecipeRepo, text ai.TextProvider, image ai.ImageProvider, embed ai.EmbeddingProvider, vector repository.VectorRepo) *RecipeService {
	return &RecipeService{
		Cfg:           &config.Config{},
		Repo:          repo,
		TextProvider:  text,
		ImageProvider: image,
		EmbedProvider: embed,
		VectorRepo:    vector,
	}
}
