package repository

import (
	"errors"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// RecipeRepository is a repository for interacting with recipes.
type RecipeRepository struct {
	DB *gorm.DB
}

// NewRecipeRepository creates a new RecipeRepository.
func NewRecipeRepository(db *gorm.DB) *RecipeRepository {
	return &RecipeRepository{DB: db}
}

// GetUserRecipes retrieves a paginated list of recipes created by the given user.
func (r *RecipeRepository) GetUserRecipes(userID uint, page, pageSize int) ([]models.Recipe, int64, error) {
	var recipes []models.Recipe
	var total int64

	base := r.DB.Model(&models.Recipe{}).Where("created_by_id = ?", userID)

	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := r.DB.Preload("Hashtags").
		Preload("Canonical").
		Preload("CreatedBy", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID", "Username")
		}).
		Where("created_by_id = ?", userID).
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&recipes).Error
	if err != nil {
		return nil, 0, err
	}

	return recipes, total, nil
}

// GetRecipeByID retrieves a recipe by its ID.
func (r *RecipeRepository) GetRecipeByID(recipeID uint) (*models.Recipe, error) {
	var recipe models.Recipe

	err := r.DB.Preload("Hashtags").
		Preload("Canonical").
		Preload("CreatedBy", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID", "Username")
		}).
		Where("id = ?", recipeID).
		First(&recipe).Error
	if err != nil {
		logger.Get().Error("failed to retrieve recipe", zap.Uint("recipe_id", recipeID), zap.Error(err))

		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFoundError{message: "Recipe not found"}
		}

		return nil, err
	}

	// Manually load ForkedFrom to avoid self-referential GORM preload issues
	if recipe.ForkedFromID != nil && *recipe.ForkedFromID != 0 {
		var forked models.Recipe
		if err := r.DB.Select("ID", "Title").First(&forked, *recipe.ForkedFromID).Error; err == nil {
			recipe.ForkedFrom = &forked
		}
	}

	return &recipe, nil
}

// CreateRecipe creates a new recipe.
func (r *RecipeRepository) CreateRecipe(recipe *models.Recipe) error {
	// Start a new transaction
	tx := r.DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	err := tx.Create(recipe).Error
	if err != nil {
		tx.Rollback()
		logger.Get().Error("failed to create recipe", zap.Error(err))
		return err
	}

	return tx.Commit().Error
}

// DeleteRecipe deletes a recipe.
func (r *RecipeRepository) DeleteRecipe(recipeID uint) error {
	err := r.DB.Delete(&models.Recipe{}, recipeID).Error
	if err != nil {
		logger.Get().Error("failed to delete recipe", zap.Uint("recipe_id", recipeID), zap.Error(err))
	}
	return err
}

// UpdateRecipeTitle updates the title of a recipe.
func (r *RecipeRepository) UpdateRecipeTitle(recipe *models.Recipe, title string) error {
	err := r.DB.Model(recipe).
		Update("Title", title).Error
	if err != nil {
		logger.Get().Error("failed to update recipe title", zap.Error(err))
	}
	return err
}

// UpdateRecipeStatus updates the status of a recipe.
func (r *RecipeRepository) UpdateRecipeStatus(recipeID uint, status string) error {
	return r.DB.Model(&models.Recipe{}).
		Where("id = ?", recipeID).
		Update("Status", status).Error
}

// UpdateRecipeImageURL updates the image URL of a recipe.
func (r *RecipeRepository) UpdateRecipeImageURL(recipeID uint, imageURL string) error {
	err := r.DB.Model(&models.Recipe{}).
		Where("id = ?", recipeID).
		Update("ImageURL", imageURL).Error
	if err != nil {
		logger.Get().Error("failed to update recipe image URL", zap.Uint("recipe_id", recipeID), zap.Error(err))
	}
	return err
}

// UpdateRecipeDef updates the core fields of a recipe.
func (r *RecipeRepository) UpdateRecipeDef(recipe *models.Recipe) error {
	err := r.DB.Model(&models.Recipe{}).
		Where("id = ?", recipe.ID).
		Updates(map[string]interface{}{
			"Title":             recipe.Title,
			"Ingredients":       recipe.Ingredients,
			"Instructions":      recipe.Instructions,
			"CookTime":          recipe.CookTime,
			"LinkedSuggestions": recipe.LinkedSuggestions,
			"ImagePrompt":       recipe.ImagePrompt,
		}).Error
	if err != nil {
		logger.Get().Error("failed to update recipe core fields", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
	}
	return err
}

// FindTagByName finds a tag by its name.
func (r *RecipeRepository) FindTagByName(tagName string) (*models.Tag, error) {
	var tag models.Tag
	err := r.DB.Where("Hashtag = ?", tagName).
		First(&tag).Error
	if err != nil {
		return nil, err
	}
	return &tag, nil
}

// CreateTag creates a new tag.
func (r *RecipeRepository) CreateTag(tag *models.Tag) error {
	err := r.DB.Create(tag).Error
	if err != nil {
		logger.Get().Error("failed to create tag", zap.Error(err))
	}
	return err
}

// MaterializeRecipeFromCanonical copies canonical RecipeDef into the recipe's own
// columns and sets HasDiverged=true, completing copy-on-write.
func (r *RecipeRepository) MaterializeRecipeFromCanonical(recipeID uint, data models.RecipeDef) error {
	return r.DB.Model(&models.Recipe{}).
		Where("id = ?", recipeID).
		Updates(map[string]interface{}{
			"Title":             data.Title,
			"Ingredients":       data.Ingredients,
			"Instructions":      data.Instructions,
			"CookTime":          data.CookTime,
			"ImagePrompt":       data.ImagePrompt,
			"LinkedSuggestions": data.LinkedSuggestions,
			"Portions":          data.Portions,
			"PortionSize":       data.PortionSize,
			"SourceURL":         data.SourceURL,
			"HasDiverged":       true,
		}).Error
}

// UpdateRecipeTagsAssociation updates the tags associated with a recipe.
func (r *RecipeRepository) UpdateRecipeTagsAssociation(recipeID uint, newTags []models.Tag) error {
	var recipe models.Recipe
	result := r.DB.First(&recipe, recipeID)
	if result.Error != nil {
		return result.Error
	}

	// Replace existing associations with new tags
	if err := r.DB.Model(&recipe).Association("Hashtags").Replace(newTags); err != nil {
		return err
	}

	return nil
}
