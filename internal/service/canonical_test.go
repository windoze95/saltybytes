package service

import (
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func TestMaterializeCanonical_AlreadyDiverged(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.HasDiverged = true
	recipe.Canonical = testutil.TestCanonicalRecipe()

	originalTitle := recipe.Title
	err := MaterializeCanonical(recipe, repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Title != originalTitle {
		t.Errorf("title changed from %q to %q; should be no-op", originalTitle, recipe.Title)
	}
}

func TestMaterializeCanonical_NoCanonical(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.HasDiverged = false
	recipe.Canonical = nil

	originalTitle := recipe.Title
	err := MaterializeCanonical(recipe, repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Title != originalTitle {
		t.Errorf("title changed from %q to %q; should be no-op", originalTitle, recipe.Title)
	}
}

func TestMaterializeCanonical_CopiesData(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()

	canonical := testutil.TestCanonicalRecipe()
	canonical.RecipeData.Title = "Updated Canonical Title"

	recipe := testutil.TestRecipe()
	recipe.HasDiverged = false
	canonicalID := canonical.ID
	recipe.CanonicalID = &canonicalID
	recipe.Canonical = canonical

	// Add to repo so MaterializeRecipeFromCanonical can find it
	repo.Recipes[recipe.ID] = recipe

	err := MaterializeCanonical(recipe, repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !recipe.HasDiverged {
		t.Error("expected HasDiverged to be true after materialization")
	}
	if recipe.Title != "Updated Canonical Title" {
		t.Errorf("title = %q, want 'Updated Canonical Title'", recipe.Title)
	}
}

func TestToRecipeResponse_ResolvesCanonical(t *testing.T) {
	svc := &RecipeService{Cfg: &config.Config{}, Repo: testutil.NewMockRecipeRepo()}
	recipe := testutil.TestCanonicalLinkedRecipe()

	resp := svc.ToRecipeResponse(recipe)
	if resp.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes' from canonical", resp.Title)
	}
	if len(resp.Ingredients) != 4 {
		t.Errorf("ingredients count = %d, want 4 from canonical", len(resp.Ingredients))
	}
}

func TestToRecipeResponse_DivergedUsesOwnData(t *testing.T) {
	svc := &RecipeService{Cfg: &config.Config{}, Repo: testutil.NewMockRecipeRepo()}
	recipe := testutil.TestCanonicalLinkedRecipe()
	recipe.HasDiverged = true
	recipe.RecipeDef = models.RecipeDef{
		Title:       "My Modified Recipe",
		Ingredients: models.Ingredients{{Name: "Salt"}},
	}

	resp := svc.ToRecipeResponse(recipe)
	if resp.Title != "My Modified Recipe" {
		t.Errorf("title = %q, want 'My Modified Recipe' (own data)", resp.Title)
	}
	if len(resp.Ingredients) != 1 {
		t.Errorf("ingredients count = %d, want 1 (own data)", len(resp.Ingredients))
	}
}
