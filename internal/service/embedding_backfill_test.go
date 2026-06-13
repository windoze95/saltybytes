package service

import (
	"context"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// mockBackfillRepo is an in-memory EmbeddingBackfillRepo for backfill tests.
type mockBackfillRepo struct {
	recipes    []models.Recipe
	canonicals []models.CanonicalRecipe

	recipeEmbeds    map[uint][]float32
	canonicalEmbeds map[uint][]float32
}

func (m *mockBackfillRepo) ListRecipesMissingEmbedding(afterID uint, limit int) ([]models.Recipe, error) {
	var out []models.Recipe
	for _, r := range m.recipes {
		if r.ID > afterID && m.recipeEmbeds[r.ID] == nil {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *mockBackfillRepo) UpdateEmbedding(recipeID uint, embedding []float32) error {
	m.recipeEmbeds[recipeID] = embedding
	return nil
}

func (m *mockBackfillRepo) ListCanonicalsMissingEmbedding(afterID uint, limit int) ([]models.CanonicalRecipe, error) {
	var out []models.CanonicalRecipe
	for _, c := range m.canonicals {
		if c.ID > afterID && m.canonicalEmbeds[c.ID] == nil {
			out = append(out, c)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *mockBackfillRepo) UpdateCanonicalEmbedding(canonicalID uint, embedding []float32) error {
	m.canonicalEmbeds[canonicalID] = embedding
	return nil
}

func backfillRecipeFixture(id uint, title string) models.Recipe {
	return models.Recipe{
		Model:       gorm.Model{ID: id},
		RecipeDef:   recipeDefWithTitle(title),
		HasDiverged: true,
	}
}

// recipeDefWithTitle builds a minimal RecipeDef with the given title.
func recipeDefWithTitle(title string) models.RecipeDef {
	return models.RecipeDef{
		Title: title,
		Ingredients: models.Ingredients{
			{Name: "Flour"},
			{Name: "Milk"},
		},
	}
}

func TestBackfillRecipeEmbeddings_FillsMissingAndSkipsEmpty(t *testing.T) {
	origDelay := embeddingBackfillDelay
	embeddingBackfillDelay = 0
	defer func() { embeddingBackfillDelay = origDelay }()

	repo := &mockBackfillRepo{
		recipes: []models.Recipe{
			backfillRecipeFixture(1, "Pancakes"),
			{Model: gorm.Model{ID: 2}, HasDiverged: true}, // empty def — skipped
			backfillRecipeFixture(3, "Waffles"),
		},
		recipeEmbeds:    map[uint][]float32{},
		canonicalEmbeds: map[uint][]float32{},
	}

	var embeddedTexts []string
	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			embeddedTexts = append(embeddedTexts, text)
			return []float32{0.1}, nil
		},
	}

	backfillRecipeEmbeddings(repo, embedProvider)

	if len(repo.recipeEmbeds) != 2 {
		t.Fatalf("filled %d embeddings, want 2", len(repo.recipeEmbeds))
	}
	if repo.recipeEmbeds[1] == nil || repo.recipeEmbeds[3] == nil {
		t.Errorf("recipes 1 and 3 should be embedded, got %v", repo.recipeEmbeds)
	}
	if repo.recipeEmbeds[2] != nil {
		t.Error("recipe 2 (empty def) should be skipped")
	}
	if len(embeddedTexts) != 2 {
		t.Errorf("embed calls = %d, want 2", len(embeddedTexts))
	}
}

func TestBackfillCanonicalEmbeddings_FillsMissing(t *testing.T) {
	origDelay := embeddingBackfillDelay
	embeddingBackfillDelay = 0
	defer func() { embeddingBackfillDelay = origDelay }()

	repo := &mockBackfillRepo{
		canonicals: []models.CanonicalRecipe{
			{Model: gorm.Model{ID: 10}, RecipeData: recipeDefWithTitle("Pancakes")},
			{Model: gorm.Model{ID: 11}, RecipeData: recipeDefWithTitle("Waffles")},
		},
		recipeEmbeds:    map[uint][]float32{},
		canonicalEmbeds: map[uint][]float32{},
	}

	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.2}, nil
		},
	}

	backfillCanonicalEmbeddings(repo, embedProvider)

	if len(repo.canonicalEmbeds) != 2 {
		t.Fatalf("filled %d canonical embeddings, want 2", len(repo.canonicalEmbeds))
	}
	if repo.canonicalEmbeds[10] == nil || repo.canonicalEmbeds[11] == nil {
		t.Errorf("canonicals 10 and 11 should be embedded, got %v", repo.canonicalEmbeds)
	}
}

func TestBackfillRecipeEmbeddings_PerRowErrorIsSkipped(t *testing.T) {
	origDelay := embeddingBackfillDelay
	embeddingBackfillDelay = 0
	defer func() { embeddingBackfillDelay = origDelay }()

	repo := &mockBackfillRepo{
		recipes: []models.Recipe{
			backfillRecipeFixture(1, "Pancakes"),
			backfillRecipeFixture(2, "Waffles"),
		},
		recipeEmbeds:    map[uint][]float32{},
		canonicalEmbeds: map[uint][]float32{},
	}

	embedProvider := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			// Fail only the first recipe's row
			if strings.Contains(text, "Pancakes") {
				return nil, context.DeadlineExceeded
			}
			return []float32{0.3}, nil
		},
	}

	backfillRecipeEmbeddings(repo, embedProvider)

	if repo.recipeEmbeds[1] != nil {
		t.Error("recipe 1 should have been skipped after embed error")
	}
	if repo.recipeEmbeds[2] == nil {
		t.Error("recipe 2 should still be embedded despite recipe 1 failing")
	}
}
