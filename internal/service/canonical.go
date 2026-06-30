package service

import (
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// MaterializeCanonical performs copy-on-write: if the recipe references a canonical
// and has not yet diverged, it copies the canonical's RecipeData into the recipe's
// own columns and marks it as diverged.
func MaterializeCanonical(recipe *models.Recipe, repo repository.RecipeRepo) error {
	if recipe.HasDiverged || recipe.Canonical == nil {
		return nil
	}

	data := recipe.Canonical.RecipeData
	if data.SourceURL == "" {
		data.SourceURL = recipe.SourceURL
	}

	// Detach the ingredient slice from the shared canonical struct, then
	// backfill normalized measurement fields. This lazily upgrades canonicals
	// materialized before the normalization fields existed.
	data.Ingredients = append(models.Ingredients(nil), data.Ingredients...)
	normalizeIngredients(&data)

	recipe.RecipeDef = data
	recipe.HasDiverged = true

	return repo.MaterializeRecipeFromCanonical(recipe.ID, data)
}
