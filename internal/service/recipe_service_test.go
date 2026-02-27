package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

func newTestRecipeService(repo repository.RecipeRepo) *RecipeService {
	return &RecipeService{
		Cfg:           &config.Config{},
		Repo:          repo,
		TextProvider:  &testutil.MockTextProvider{},
		ImageProvider: &testutil.MockImageProvider{},
	}
}

func TestToRecipeResponse_AllFields(t *testing.T) {
	now := time.Now()
	forkedID := uint(42)
	recipe := &models.Recipe{
		Model: gorm.Model{
			ID:        7,
			CreatedAt: now,
			UpdatedAt: now,
		},
		RecipeDef: testutil.TestRecipeDef(),
		ImageURL:  "https://example.com/img.jpg",
		Hashtags: []*models.Tag{
			{Hashtag: "breakfast"},
			{Hashtag: "easy"},
		},
		CreatedByID:  3,
		ForkedFromID: &forkedID,
	}

	svc := newTestRecipeService(testutil.NewMockRecipeRepo())
	resp := svc.ToRecipeResponse(recipe)

	if resp.ID != "7" {
		t.Errorf("ID = %q, want '7'", resp.ID)
	}
	if resp.Title != "Classic Pancakes" {
		t.Errorf("Title = %q, want 'Classic Pancakes'", resp.Title)
	}
	if resp.OwnerID != "3" {
		t.Errorf("OwnerID = %q, want '3'", resp.OwnerID)
	}
	if resp.ImageURL != "https://example.com/img.jpg" {
		t.Errorf("ImageURL = %q", resp.ImageURL)
	}
	if len(resp.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(resp.Tags))
	}
	if resp.CookTimeMinutes != 20 {
		t.Errorf("CookTimeMinutes = %d, want 20", resp.CookTimeMinutes)
	}
	if len(resp.Ingredients) != 4 {
		t.Errorf("Ingredients count = %d, want 4", len(resp.Ingredients))
	}
	if len(resp.Instructions) != 3 {
		t.Errorf("Instructions count = %d, want 3", len(resp.Instructions))
	}
	if resp.ParentRecipeID == nil || *resp.ParentRecipeID != "42" {
		t.Errorf("ParentRecipeID = %v, want '42'", resp.ParentRecipeID)
	}

	// Check date formatting
	expected := now.Format("2006-01-02T15:04:05Z")
	if resp.CreatedAt != expected {
		t.Errorf("CreatedAt = %q, want %q", resp.CreatedAt, expected)
	}
}

func TestToRecipeResponse_NoForkedFrom(t *testing.T) {
	recipe := &models.Recipe{
		Model:     gorm.Model{ID: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		RecipeDef: testutil.TestRecipeDef(),
	}

	svc := newTestRecipeService(testutil.NewMockRecipeRepo())
	resp := svc.ToRecipeResponse(recipe)

	if resp.ParentRecipeID != nil {
		t.Errorf("ParentRecipeID should be nil, got %v", resp.ParentRecipeID)
	}
}

func TestToRecipeResponse_EmptyTags(t *testing.T) {
	recipe := &models.Recipe{
		Model:     gorm.Model{ID: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		RecipeDef: testutil.TestRecipeDef(),
		Hashtags:  nil,
	}

	svc := newTestRecipeService(testutil.NewMockRecipeRepo())
	resp := svc.ToRecipeResponse(recipe)

	if len(resp.Tags) != 0 {
		t.Errorf("Tags should be empty, got %v", resp.Tags)
	}
}

func TestGetRecipeByID_Found(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := newTestRecipeService(repo)
	resp, err := svc.GetRecipeByID(recipe.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID error: %v", err)
	}
	if resp.ID != fmt.Sprintf("%d", recipe.ID) {
		t.Errorf("GetRecipeByID ID = %q, want %q", resp.ID, fmt.Sprintf("%d", recipe.ID))
	}
	if resp.Title != recipe.Title {
		t.Errorf("GetRecipeByID Title = %q, want %q", resp.Title, recipe.Title)
	}
}

func TestGetRecipeByID_NotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestRecipeService(repo)

	_, err := svc.GetRecipeByID(999)
	if err == nil {
		t.Fatal("GetRecipeByID should return error for missing recipe")
	}
	if _, ok := err.(repository.NotFoundError); !ok {
		t.Errorf("GetRecipeByID error type = %T, want NotFoundError", err)
	}
}

func TestGetUserRecipes_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := newTestRecipeService(repo)
	items, total, err := svc.GetUserRecipes(recipe.CreatedByID, 1, 10)
	if err != nil {
		t.Fatalf("GetUserRecipes error: %v", err)
	}
	if total != 1 {
		t.Errorf("GetUserRecipes total = %d, want 1", total)
	}
	if len(items) != 1 {
		t.Errorf("GetUserRecipes items count = %d, want 1", len(items))
	}
	if items[0].Title != recipe.Title {
		t.Errorf("GetUserRecipes item title = %q, want %q", items[0].Title, recipe.Title)
	}
}

func TestGetUserRecipes_Empty(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestRecipeService(repo)

	items, total, err := svc.GetUserRecipes(999, 1, 10)
	if err != nil {
		t.Fatalf("GetUserRecipes error: %v", err)
	}
	if total != 0 {
		t.Errorf("GetUserRecipes total = %d, want 0", total)
	}
	if len(items) != 0 {
		t.Errorf("GetUserRecipes items count = %d, want 0", len(items))
	}
}

func TestAssociateTagsWithRecipe_NewTags(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	svc := newTestRecipeService(repo)
	err := svc.AssociateTagsWithRecipe(recipe, []string{"#NewTag", "Existing"})
	if err != nil {
		t.Fatalf("AssociateTagsWithRecipe error: %v", err)
	}

	// Check that tags were created in the repo
	if len(repo.Tags) != 2 {
		t.Errorf("Tags created = %d, want 2", len(repo.Tags))
	}
}

func TestAssociateTagsWithRecipe_ExistingTag(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.CreatedAt = time.Now()
	recipe.UpdatedAt = time.Now()
	repo.Recipes[recipe.ID] = recipe

	// Pre-create a tag
	repo.Tags["breakfast"] = &models.Tag{Hashtag: "breakfast"}
	repo.Tags["breakfast"].ID = 1

	svc := newTestRecipeService(repo)
	err := svc.AssociateTagsWithRecipe(recipe, []string{"breakfast", "newtag"})
	if err != nil {
		t.Fatalf("AssociateTagsWithRecipe error: %v", err)
	}

	// Only 1 new tag should be created (breakfast already existed)
	if len(repo.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2 (1 existing + 1 new)", len(repo.Tags))
	}
}
