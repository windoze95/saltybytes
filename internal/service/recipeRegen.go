package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
)

// InitRegenerateRecipe initializes recipe regeneration from conversation history.
func (s *RecipeService) InitRegenerateRecipe(user *models.User, recipeID uint, userPrompt string, genImage bool) error {
	if user.Personalization.ID == 0 {
		logger.Get().Warn("user personalization is nil", zap.Uint("user_id", user.ID))
		return errors.New("user's Personalization is nil")
	}

	recipe, err := s.Repo.GetRecipeByID(recipeID)
	if err != nil {
		return fmt.Errorf("failed to get existing recipe: %w", err)
	}

	if recipe.CreatedByID != user.ID {
		return errors.New("unauthorized: you do not own this recipe")
	}

	go s.FinishRegenerateRecipe(recipe, user, userPrompt, genImage)

	return nil
}

// FinishRegenerateRecipe finishes regenerating a recipe.
func (s *RecipeService) FinishRegenerateRecipe(recipe *models.Recipe, user *models.User, userPrompt string, genImage bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	recipeErrChan := make(chan error)
	imageErrChan := make(chan error)

	// Convert existing history entries to ai.Message format for conversation context
	var existingHistory []ai.Message
	if recipe.History != nil {
		existingHistory = historyEntriesToMessages(recipe.History.Entries, &recipe.RecipeDef)
	}

	req := ai.RegenerateRequest{
		RecipeRequest: ai.RecipeRequest{
			UserPrompt:   userPrompt,
			UnitSystem:   user.Personalization.GetUnitSystemText(),
			Requirements: user.Personalization.Requirements,
		},
		ExistingHistory: existingHistory,
	}

	// Goroutine to handle recipe generation
	go func(ctx context.Context, recipeErrChan chan<- error, imageErrChan chan<- error) {
		result, err := s.TextProvider.RegenerateRecipe(ctx, req)
		if err != nil {
			recipeErrChan <- err
			return
		}

		recipeDef := recipeResultToRecipeDef(result)

		// Goroutine to handle image generation and upload
		go func(ctx context.Context, imageErrChan chan<- error) {
			if genImage && result.ImagePrompt != "" {
				imageBytes, imgErr := s.ImageProvider.GenerateImage(ctx, result.ImagePrompt)
				if imgErr != nil {
					imageErrChan <- imgErr
					return
				}

				imageURL, uploadErr := uploadRecipeImage(ctx, recipe.ID, imageBytes, s.Cfg)
				if uploadErr != nil {
					imageErrChan <- uploadErr
					return
				}

				if dbErr := s.Repo.UpdateRecipeImageURL(recipe.ID, imageURL); dbErr != nil {
					imageErrChan <- dbErr
					return
				}
			}

			imageErrChan <- nil
		}(ctx, imageErrChan)

		historyEntry := models.RecipeHistoryEntry{
			Prompt:   userPrompt,
			Response: &recipeDef,
			Summary:  result.Summary,
			Type:     models.RecipeTypeChat,
		}

		if err := populateRecipeCoreFields(recipe, result, historyEntry); err != nil {
			recipeErrChan <- err
			return
		}

		if err := s.Repo.UpdateRecipeDef(recipe, historyEntry); err != nil {
			recipeErrChan <- err
			return
		}

		if err := s.AssociateTagsWithRecipe(recipe, result.Hashtags); err != nil {
			logger.Get().Error("failed to associate tags with recipe", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
		}

		// Add a new node to the recipe's tree if one exists
		if tree, treeErr := s.Repo.GetTreeByRecipeID(recipe.ID); treeErr == nil {
			activeNode, nodeErr := s.Repo.GetActiveNode(tree.ID)
			if nodeErr != nil {
				logger.Get().Error("failed to get active node for regen", zap.Uint("recipe_id", recipe.ID), zap.Error(nodeErr))
			} else {
				newNode := &models.RecipeNode{
					TreeID:      tree.ID,
					ParentID:    &activeNode.ID,
					Prompt:      userPrompt,
					Response:    &recipeDef,
					Summary:     result.Summary,
					Type:        models.RecipeTypeRegenChat,
					BranchName:  activeNode.BranchName,
					CreatedByID: recipe.CreatedByID,
				}
				if addErr := s.Repo.AddNodeToTree(newNode, true); addErr != nil {
					logger.Get().Error("failed to add regen node to tree", zap.Uint("recipe_id", recipe.ID), zap.Error(addErr))
				}
			}
		}

		recipeErrChan <- nil
	}(ctx, recipeErrChan, imageErrChan)

	// Wait for the recipe generation goroutine to finish or timeout
	select {
	case err := <-recipeErrChan:
		if err != nil {
			recipeID := recipe.ID
			logger.Get().Error("failed to finish recipe regeneration", zap.Uint("recipe_id", recipeID), zap.Error(err))
			e := s.DeleteRecipe(context.Background(), recipeID)
			if e != nil {
				logger.Get().Error("failed to delete recipe after regeneration error", zap.Uint("recipe_id", recipeID), zap.Error(e))
				return
			}
			logger.Get().Info("recipe deleted after regeneration error", zap.Uint("recipe_id", recipeID))
			return
		}
	case <-ctx.Done():
		err := errors.New("incomplete recipe generation: timed out after 5 minutes")
		recipeID := recipe.ID
		logger.Get().Error("recipe regeneration timed out", zap.Uint("recipe_id", recipeID), zap.Error(err))
		e := s.DeleteRecipe(context.Background(), recipeID)
		if e != nil {
			logger.Get().Error("failed to delete recipe after regeneration timeout", zap.Uint("recipe_id", recipeID), zap.Error(e))
			return
		}
		logger.Get().Info("recipe deleted after regeneration timeout", zap.Uint("recipe_id", recipeID))
		return
	}

	if !genImage {
		return
	}

	// Wait for the image generation goroutine to finish or timeout
	select {
	case err := <-imageErrChan:
		if err != nil {
			logger.Get().Error("failed to generate recipe image", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
			return
		}
	case <-ctx.Done():
		logger.Get().Error("recipe image generation timed out", zap.Uint("recipe_id", recipe.ID))
		return
	}
}

// historyEntriesToMessages converts recipe history entries into ai.Message slices
// for use as conversation context in regen/fork flows. The last entry's response
// is serialized from the current RecipeDef; earlier entries use summaries.
func historyEntriesToMessages(entries []models.RecipeHistoryEntry, currentDef *models.RecipeDef) []ai.Message {
	var messages []ai.Message
	length := len(entries)

	for i, entry := range entries {
		var userContent string
		var assistantContent string

		switch entry.Type {
		case models.RecipeTypeManualEntry:
			if i == length-1 {
				userContent = "The following response from you is the current revision of the recipe."
				defJSON, _ := json.Marshal(currentDef)
				assistantContent = string(defJSON)
			} else {
				continue
			}
		default:
			userContent = entry.Prompt
			if i == length-1 {
				defJSON, _ := json.Marshal(currentDef)
				assistantContent = string(defJSON)
			} else {
				assistantContent = entry.Summary
			}
		}

		messages = append(messages, ai.Message{Role: "user", Content: userContent})
		messages = append(messages, ai.Message{Role: "assistant", Content: assistantContent})
	}

	return messages
}
