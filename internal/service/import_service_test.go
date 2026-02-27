package service

import (
	"context"
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

	resp, _, err := svc.createImportedRecipe(context.Background(), recipeDef, user, models.RecipeTypeManualEntry, "")
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

	_, _, err := svc.createImportedRecipe(context.Background(), recipeDef, user, models.RecipeTypeImportCopypasta, "")
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
