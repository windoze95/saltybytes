package migrations

import (
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// MigrateRecipeHistoryToTree migrates existing RecipeHistory/RecipeHistoryEntry data
// into the new RecipeTree/RecipeNode structure. It converts linear history chains
// into tree structures where each history entry becomes a node in a linear chain
// (root -> child -> child ...) with the last node marked as active.
//
// This migration is idempotent: recipes that already have a TreeID are skipped.
func MigrateRecipeHistoryToTree(db *gorm.DB) error {
	// Find all recipes that have a history but no tree yet
	var recipes []models.Recipe
	if err := db.Where("history_id IS NOT NULL AND history_id != 0 AND (tree_id IS NULL OR tree_id = 0)").
		Find(&recipes).Error; err != nil {
		return err
	}

	if len(recipes) == 0 {
		logger.Get().Info("no recipes to migrate to tree structure")
		return nil
	}

	logger.Get().Info("migrating recipes to tree structure", zap.Int("count", len(recipes)))

	for _, recipe := range recipes {
		if err := migrateRecipeToTree(db, &recipe); err != nil {
			logger.Get().Error("failed to migrate recipe to tree",
				zap.Uint("recipe_id", recipe.ID),
				zap.Error(err))
			continue // skip failed recipes rather than aborting entire migration
		}
	}

	logger.Get().Info("recipe tree migration complete")
	return nil
}

func migrateRecipeToTree(db *gorm.DB, recipe *models.Recipe) error {
	// Load the history entries for this recipe
	var history models.RecipeHistory
	if err := db.Preload("Entries", func(db *gorm.DB) *gorm.DB {
		return db.Order("\"order\" ASC, created_at ASC")
	}).First(&history, recipe.HistoryID).Error; err != nil {
		return err
	}

	if len(history.Entries) == 0 {
		return nil // nothing to migrate
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// Create the tree
		tree := models.RecipeTree{
			RecipeID: recipe.ID,
		}
		if err := tx.Create(&tree).Error; err != nil {
			return err
		}

		var prevNodeID *uint
		var lastNodeID uint

		for _, entry := range history.Entries {
			node := models.RecipeNode{
				TreeID:      tree.ID,
				ParentID:    prevNodeID,
				Prompt:      entry.Prompt,
				Response:    entry.Response,
				Summary:     entry.Summary,
				Type:        entry.Type,
				BranchName:  "original",
				IsEphemeral: false,
				CreatedByID: recipe.CreatedByID,
				IsActive:    false,
			}
			if err := tx.Create(&node).Error; err != nil {
				return err
			}

			// Track the root node
			if prevNodeID == nil {
				if err := tx.Model(&tree).Update("root_node_id", node.ID).Error; err != nil {
					return err
				}
			}

			prevNodeID = &node.ID
			lastNodeID = node.ID
		}

		// Mark the last node as active
		if err := tx.Model(&models.RecipeNode{}).Where("id = ?", lastNodeID).
			Update("is_active", true).Error; err != nil {
			return err
		}

		// Link the tree to the recipe
		if err := tx.Model(&models.Recipe{}).Where("id = ?", recipe.ID).
			Update("tree_id", tree.ID).Error; err != nil {
			return err
		}

		return nil
	})
}
