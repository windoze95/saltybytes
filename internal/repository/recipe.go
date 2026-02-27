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

// GetHistoryByID retrieves a recipe history by its ID.
func (r *RecipeRepository) GetHistoryByID(historyID uint) (*models.RecipeHistory, error) {
	history := new(models.RecipeHistory)

	err := r.DB.Preload("Entries", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at ASC")
	}).First(history, historyID).Error
	if err != nil {
		return nil, err
	}

	return history, nil
}

// GetRecipeHistoryEntriesAfterID retrieves entries belonging to a specific RecipeHistory
// and having an ID greater than a given value.
func (r *RecipeRepository) GetRecipeHistoryEntriesAfterID(historyID uint, afterID uint) ([]models.RecipeHistoryEntry, error) {
	var entries []models.RecipeHistoryEntry

	result := r.DB.Where("recipe_chat_history_id = ? AND id > ?", historyID, afterID).
		Order("id ASC").Find(&entries)
	if result.Error != nil {
		return nil, result.Error
	}

	return entries, nil
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

// UpdateRecipeDef updates the core fields of a recipe and appends the new recipe history entry to the history.
//
// Core fields: "Title", "Ingredients", "Instructions", "CookTime", "LinkedSuggestions", "ImagePrompt"
// In service, on manual updates, we should check if the most recent
// Current? entry is of type "manual", if so, we should not create another,
//     trigger record touch instead, db.Model(&recipe).Update("updated_at", gorm.Expr("CURRENT_TIMESTAMP"))
// Otherwise, we need UpdateRecipeAndHistory to update the
//     recipe and the history with a new manual entry.

// Requirements should use a gpts similar approach,
// SYS: "You ask questions one at a time to gain insight into a user's dietary and health requirements, and then use that information to generate clear and concise guidelines that can be used to influence a recipe creation process. Collect insight on foods to avoid. If the user expresses a preferred food to choose more, only tell the user that you cannot add that unless they eat it exclusively, if they do eat that food exclusively, go ahead and reflect that, if not, tell them to ensure quality you cannot include that then ask shall we continue, if they say no, generate what you have so far. Ask questions in a way that is easy to understand and answer. At the end, provide only a list without any other unnecessary context or verbiage."
// You ask questions one at a time to gain insight into a user's dietary and health requirements, and then use that information to generate clear and concise guidelines that can be used to influence a recipe creation process. Collect insight on foods to avoid. If the user expresses a preferred food to choose more, only tell the user that you cannot add that unless they eat it exclusively, if they do eat that food exclusively, go ahead and reflect that, if not, tell them to ensure quality you cannot include that then ask shall we continue, if they say no, generate what you have so far. Ask questions in a way that is easy to understand and answer. Whenever you provide the list, only provide the list without any other context or verbiage.
// SYS - NEWEST VERSION: You ask questions one at a time to gain insight into a user's dietary and health requirements, and then use that information to generate clear and concise guidelines that can be used to influence a recipe creation process. Collect insight on foods to avoid. If the user expresses a preferred food they want, only tell the user that you cannot add that unless they eat it exclusively, if they do eat that food exclusively, go ahead and reflect that, if not, tell them to ensure quality you cannot include that then ask if we shall continue, if they say no, generate what you have so far. Ask questions in a way that is easy to understand and answer. Whenever you provide the list, only provide the list without any other context or verbiage. Ignore anything irrelevant or inappropriate. Remember that you cannot add food or cuisine type inclusions unless its exclusive, only express exclusions.
func (r *RecipeRepository) UpdateRecipeDef(recipe *models.Recipe, newRecipeHistoryEntry models.RecipeHistoryEntry) error {
	// Start a new transaction.
	tx := r.DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// Update core fields of the recipe.
	err := tx.Model(&models.Recipe{}).
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
		tx.Rollback()
		logger.Get().Error("failed to update recipe core fields", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
		return err
	}

	// Check if HistoryID is set in the Recipe
	if recipe.HistoryID == 0 {
		tx.Rollback()
		err = errors.New("recipe history ID not set in recipe")
		logger.Get().Error("recipe history ID not set", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
		return err
	}

	newRecipeHistoryEntry.HistoryID = recipe.HistoryID

	// Insert the new recipe history entry into the database
	err = tx.Create(&newRecipeHistoryEntry).Error
	if err != nil {
		tx.Rollback()
		logger.Get().Error("failed to create recipe history entry", zap.Error(err))
		return err
	}

	err = tx.Commit().Error
	if err != nil {
		logger.Get().Error("failed to commit transaction in UpdateRecipeDef", zap.Error(err))
		return err
	}

	return nil
}

// UpdateRecipeWithHistoryEntry sets a new active entry in RecipeHistory, uses its RecipeResponse to update the Recipe,
// and ensures the new active entry belongs to the correct RecipeHistory using the HistoryID field from Recipe.
// It performs no operation if the new active entry is the same as the current.
func (r *RecipeRepository) UpdateRecipeWithHistoryEntry(recipeID uint, newActiveEntryID uint, updatedResponse models.RecipeDef) error {
	// Begin a transaction
	tx := r.DB.Begin()

	// Fetch the Recipe along with its HistoryID
	var recipe models.Recipe
	if err := tx.First(&recipe, recipeID).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Fetch the RecipeHistory using Recipe's HistoryID to get the current ActiveEntryID
	var currentHistory models.RecipeHistory
	if err := tx.First(&currentHistory, recipe.HistoryID).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Check if the new active entry is the same as the current active entry
	if currentHistory.ActiveEntryID != nil && *currentHistory.ActiveEntryID == newActiveEntryID {
		return nil // No operation needed, return immediately
	}

	// Fetch the new active RecipeHistoryEntry and check if it belongs to the correct RecipeHistory
	var newActiveEntry models.RecipeHistoryEntry
	if err := tx.First(&newActiveEntry, newActiveEntryID).Error; err != nil {
		tx.Rollback()
		return err
	}
	if newActiveEntry.HistoryID != recipe.HistoryID {
		tx.Rollback()
		return errors.New("the new active entry does not belong to the correct recipe history")
	}

	// Fetch the RecipeResponse from the new active entry and update the Recipe
	recipe.RecipeDef = *newActiveEntry.Response

	// Update the Recipe
	if err := tx.Save(&recipe).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Update the RecipeHistoryEntry with the updated RecipeResponse
	if err := tx.Model(&models.RecipeHistoryEntry{}).Where("id = ?", *currentHistory.ActiveEntryID).Update("recipe_response", updatedResponse).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Update the RecipeHistory to reference the new active entry
	if err := tx.Model(&models.RecipeHistory{}).Where("id = ?", recipe.HistoryID).Update("active_entry_id", newActiveEntryID).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Clear the RecipeResponse of the new active entry
	if err := tx.Model(&newActiveEntry).Update("recipe_response", gorm.Expr("NULL")).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error // Commit the transaction
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
