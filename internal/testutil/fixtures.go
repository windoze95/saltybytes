package testutil

import (
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
)

// TestUser creates a test user with all associated records populated.
func TestUser() *models.User {
	return &models.User{
		Model:     gorm.Model{ID: 1},
		Username:  "testuser",
		FirstName: "Test",
		Email:     "test@example.com",
		Auth: &models.UserAuth{
			Model:          gorm.Model{ID: 1},
			UserID:         1,
			HashedPassword: "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012",
			AuthType:       models.Standard,
		},
		Settings: &models.UserSettings{
			Model:           gorm.Model{ID: 1},
			UserID:          1,
			KeepScreenAwake: true,
		},
		Personalization: &models.Personalization{
			Model:        gorm.Model{ID: 1},
			UserID:       1,
			UnitSystem:   models.USCustomary,
			Requirements: "No peanuts",
			UID:          uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		},
	}
}

// TestRecipeDef creates a test RecipeDef with realistic fields.
func TestRecipeDef() models.RecipeDef {
	return models.RecipeDef{
		Title: "Classic Pancakes",
		Ingredients: models.Ingredients{
			{Name: "All-purpose flour", Unit: "cups", Amount: 1.5, OriginalText: "1.5 cups all-purpose flour"},
			{Name: "Milk", Unit: "cups", Amount: 1.25, OriginalText: "1 1/4 cups milk"},
			{Name: "Egg", Unit: "", Amount: 1, OriginalText: "1 egg"},
			{Name: "Butter", Unit: "tbsp", Amount: 3, OriginalText: "3 tbsp melted butter"},
		},
		Instructions:      pq.StringArray{"Mix dry ingredients", "Whisk wet ingredients", "Combine and cook on griddle"},
		CookTime:          20,
		ImagePrompt:       "A stack of fluffy golden pancakes with butter and maple syrup",
		Hashtags:          []string{"breakfast", "pancakes", "easy"},
		LinkedSuggestions: pq.StringArray{"Blueberry Pancakes", "Banana Pancakes"},
		Portions:          4,
		PortionSize:       "3 pancakes",
	}
}

// TestRecipe creates a test Recipe with a populated RecipeDef and associations.
func TestRecipe() *models.Recipe {
	recipeDef := TestRecipeDef()
	return &models.Recipe{
		Model:     gorm.Model{ID: 1},
		RecipeDef: recipeDef,
		ImageURL:  "https://example.com/pancakes.jpg",
		Hashtags: []*models.Tag{
			{Model: gorm.Model{ID: 1}, Hashtag: "breakfast"},
			{Model: gorm.Model{ID: 2}, Hashtag: "pancakes"},
		},
		CreatedByID:        1,
		PersonalizationUID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		UnitSystem:         models.USCustomary,
		HistoryID:          1,
		History: &models.RecipeHistory{
			Model: gorm.Model{ID: 1},
			Entries: []models.RecipeHistoryEntry{
				{
					Model:    gorm.Model{ID: 1},
					Prompt:   "Make pancakes",
					Response: &recipeDef,
					Summary:  "Classic pancake recipe",
					Type:     models.RecipeTypeChat,
					Order:    0,
				},
			},
		},
	}
}

// TestRecipeResult creates an ai.RecipeResult that matches TestRecipeDef fields.
func TestRecipeResult() *ai.RecipeResult {
	return &ai.RecipeResult{
		Title: "Classic Pancakes",
		Ingredients: []ai.IngredientResult{
			{Name: "All-purpose flour", Unit: "cups", Amount: 1.5, OriginalText: "1.5 cups all-purpose flour"},
			{Name: "Milk", Unit: "cups", Amount: 1.25, OriginalText: "1 1/4 cups milk"},
			{Name: "Egg", Unit: "", Amount: 1, OriginalText: "1 egg"},
			{Name: "Butter", Unit: "tbsp", Amount: 3, OriginalText: "3 tbsp melted butter"},
		},
		Instructions:      []string{"Mix dry ingredients", "Whisk wet ingredients", "Combine and cook on griddle"},
		CookTime:          20,
		ImagePrompt:       "A stack of fluffy golden pancakes with butter and maple syrup",
		Hashtags:          []string{"breakfast", "pancakes", "easy"},
		LinkedSuggestions: []string{"Blueberry Pancakes", "Banana Pancakes"},
		Summary:           "Classic pancake recipe",
		Portions:          4,
		PortionSize:       "3 pancakes",
	}
}
