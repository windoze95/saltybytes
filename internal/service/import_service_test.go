package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func newTestImportService(repo *testutil.MockRecipeRepo, textProvider ai.TextProvider, previewProvider ai.TextProvider) *ImportService {
	recipeService := &RecipeService{
		Cfg:  &config.Config{},
		Repo: repo,
	}
	return &ImportService{
		Cfg:             &config.Config{},
		RecipeRepo:      repo,
		RecipeService:   recipeService,
		TextProvider:    textProvider,
		VisionProvider:  nil,
		PreviewProvider: previewProvider,
	}
}

func TestImportFromText_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	mockText := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newTestImportService(repo, mockText, nil)
	user := testutil.TestUser()

	resp, err := svc.ImportFromText(context.Background(), "Some recipe text", user)
	if err != nil {
		t.Fatalf("ImportFromText error: %v", err)
	}
	if resp == nil {
		t.Fatal("ImportFromText returned nil response")
	}
	if resp.Title != "Classic Pancakes" {
		t.Errorf("ImportFromText title = %q, want 'Classic Pancakes'", resp.Title)
	}
	if len(repo.Recipes) != 1 {
		t.Errorf("ImportFromText recipes in repo = %d, want 1", len(repo.Recipes))
	}
}

func TestImportManual_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title: "Manual Pancakes",
		Ingredients: models.Ingredients{
			{Name: "Flour", Unit: "cups", Amount: 2},
		},
		Instructions: []string{"Mix", "Cook"},
		ImagePrompt:  "A photo of pancakes",
	}

	resp, err := svc.ImportManual(context.Background(), recipeDef, user, models.RecipeTypeManualEntry)
	if err != nil {
		t.Fatalf("ImportManual error: %v", err)
	}
	if resp == nil {
		t.Fatal("ImportManual returned nil response")
	}
	if resp.Title != "Manual Pancakes" {
		t.Errorf("ImportManual title = %q, want 'Manual Pancakes'", resp.Title)
	}
}

func TestImportManual_EmptyTitle(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:       "",
		Ingredients: models.Ingredients{{Name: "Flour"}},
	}

	_, err := svc.ImportManual(context.Background(), recipeDef, user, models.RecipeTypeManualEntry)
	if err == nil {
		t.Fatal("ImportManual with empty title should return error")
	}
}

func TestImportManual_WithSourceURL(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:        "Linked Recipe",
		Ingredients:  models.Ingredients{{Name: "Water"}},
		Instructions: []string{"Boil"},
		ImagePrompt:  "A pot of water",
		SourceURL:    "https://example.com/recipe",
	}

	resp, err := svc.ImportManual(context.Background(), recipeDef, user, models.RecipeTypeImportLink)
	if err != nil {
		t.Fatalf("ImportManual with source URL error: %v", err)
	}
	if resp.SourceURL != "https://example.com/recipe" {
		t.Errorf("SourceURL = %q, want 'https://example.com/recipe'", resp.SourceURL)
	}
}

func TestImportFromText_NoProvider(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	_, err := svc.ImportFromText(context.Background(), "Some text", user)
	if err == nil {
		t.Fatal("ImportFromText with nil TextProvider should return error")
	}
}

func TestCreateImportedRecipe_AssociatesTags(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:        "Tagged Recipe",
		Ingredients:  models.Ingredients{{Name: "Flour"}},
		Instructions: []string{"Bake"},
		ImagePrompt:  "A baked thing",
		Hashtags:     []string{"baking", "easy"},
	}

	resp, _, err := svc.createImportedRecipe(context.Background(), recipeDef, user, models.RecipeTypeManualEntry, "", nil)
	if err != nil {
		t.Fatalf("createImportedRecipe error: %v", err)
	}
	if resp == nil {
		t.Fatal("createImportedRecipe returned nil")
	}

	// Tags should have been created
	if len(repo.Tags) < 2 {
		t.Errorf("Expected at least 2 tags, got %d", len(repo.Tags))
	}
}

func TestPreviewFromURL_CanonicalCacheHit(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	canonical := testutil.TestCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(normalizedURL string) (*models.CanonicalRecipe, error) {
			return canonical, nil
		},
	}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo

	recipeDef, canonicalID, err := svc.PreviewFromURL(context.Background(), "https://example.com/classic-pancakes", "US customary")
	if err != nil {
		t.Fatalf("PreviewFromURL error: %v", err)
	}
	if recipeDef == nil {
		t.Fatal("PreviewFromURL returned nil recipeDef")
	}
	if recipeDef.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", recipeDef.Title)
	}
	if canonicalID == nil {
		t.Fatal("expected canonical_id to be set")
	}
	if *canonicalID != canonical.ID {
		t.Errorf("canonical_id = %d, want %d", *canonicalID, canonical.ID)
	}
}

func TestPreviewFromURL_StaleCanonicalSkipped(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	stale := testutil.TestStaleCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(normalizedURL string) (*models.CanonicalRecipe, error) {
			return stale, nil
		},
	}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo

	// With a stale canonical and no real server to fetch from, this should fail
	// during extraction — proving the stale cache was skipped.
	_, _, err := svc.PreviewFromURL(context.Background(), "https://example.com/classic-pancakes", "US customary")
	if err == nil {
		t.Fatal("expected error when stale canonical is skipped and extraction fails")
	}
}

func TestImportFromURL_StaleCanonicalSkipped(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	stale := testutil.TestStaleCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(normalizedURL string) (*models.CanonicalRecipe, error) {
			return stale, nil
		},
	}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo
	user := testutil.TestUser()

	// With a stale canonical and no real server to fetch from, this should fail
	// during extraction — proving the stale cache was skipped.
	_, err := svc.ImportFromURL(context.Background(), "https://example.com/classic-pancakes", user)
	if err == nil {
		t.Fatal("expected error when stale canonical is skipped and extraction fails")
	}
}

func TestImportFromCanonical_Success(t *testing.T) {
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

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo
	user := testutil.TestUser()

	resp, err := svc.ImportFromCanonical(context.Background(), canonical.ID, user)
	if err != nil {
		t.Fatalf("ImportFromCanonical error: %v", err)
	}
	if resp == nil {
		t.Fatal("ImportFromCanonical returned nil response")
	}
	if resp.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", resp.Title)
	}
	if len(repo.Recipes) != 1 {
		t.Errorf("recipes in repo = %d, want 1", len(repo.Recipes))
	}
	// Verify the recipe has canonical link
	for _, r := range repo.Recipes {
		if r.CanonicalID == nil {
			t.Error("expected recipe to have CanonicalID set")
		}
		if r.HasDiverged {
			t.Error("expected recipe HasDiverged to be false")
		}
	}
}

func TestImportFromCanonical_NotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	canonicalRepo := &testutil.MockCanonicalRecipeRepo{}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo
	user := testutil.TestUser()

	_, err := svc.ImportFromCanonical(context.Background(), 999, user)
	if err == nil {
		t.Fatal("ImportFromCanonical with invalid ID should return error")
	}
}

func TestCreateImportedRecipe_CreatesTree(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:        "Tree Recipe",
		Ingredients:  models.Ingredients{{Name: "Salt"}},
		Instructions: []string{"Add salt"},
		ImagePrompt:  "Salty",
	}

	_, _, err := svc.createImportedRecipe(context.Background(), recipeDef, user, models.RecipeTypeImportCopypasta, "", nil)
	if err != nil {
		t.Fatalf("createImportedRecipe error: %v", err)
	}

	// A tree should have been created
	if len(repo.Trees) != 1 {
		t.Errorf("Expected 1 tree, got %d", len(repo.Trees))
	}
	if len(repo.Nodes) != 1 {
		t.Errorf("Expected 1 node (root), got %d", len(repo.Nodes))
	}
}
