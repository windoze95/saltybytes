package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
)

// InitGenerateRecipeWithFork initializes a new recipe with fork.
func (s *RecipeService) InitGenerateRecipeWithFork(user *models.User, forkedRecipeID uint, userPrompt string, genImage bool) (*RecipeResponse, error) {
	if user.Personalization.ID == 0 {
		logger.Get().Warn("user personalization is nil", zap.Uint("user_id", user.ID))
		return nil, errors.New("user's Personalization is nil")
	}

	forkedRecipe, err := s.Repo.GetRecipeByID(forkedRecipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing recipe: %w", err)
	}

	// Populate initial fields of the Recipe struct
	recipe := &models.Recipe{
		CreatedBy:          user,
		ForkedFrom:         forkedRecipe,
		PersonalizationUID: user.Personalization.UID,
		Status:             "generating",
	}

	// Create a Recipe with the basic Recipe details
	err = s.Repo.CreateRecipe(recipe)
	if err != nil {
		return nil, fmt.Errorf("failed to save recipe record: %w", err)
	}

	recipeResponse := s.ToRecipeResponse(recipe)

	go s.FinishGenerateRecipeWithFork(recipe, forkedRecipe, user, userPrompt, genImage)

	return recipeResponse, nil
}

// FinishGenerateRecipeWithFork finishes generating a recipe with fork.
// sourceRecipe is the original recipe being forked (used for AI conversation context).
func (s *RecipeService) FinishGenerateRecipeWithFork(recipe *models.Recipe, sourceRecipe *models.Recipe, user *models.User, userPrompt string, genImage bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	recipeErrChan := make(chan error)
	imageErrChan := make(chan error, 1) // buffered to prevent goroutine leak when genImage is false

	// Resolve effective RecipeDef from canonical for fork context (read-only)
	effectiveDef := effectiveRecipeDef(sourceRecipe)

	// Load conversation context from source recipe's tree nodes
	var existingHistory []ai.Message
	if tree, treeErr := s.Repo.GetTreeByRecipeID(sourceRecipe.ID); treeErr == nil {
		if activeNode, nodeErr := s.Repo.GetActiveNode(tree.ID); nodeErr == nil {
			if ancestors, ancestorErr := s.Repo.GetNodeAncestors(activeNode.ID); ancestorErr == nil {
				existingHistory = compactNodeChain(ancestors, &effectiveDef, maxUncompactedNodes)
			} else {
				logger.Get().Warn("failed to load node ancestors for fork", zap.Uint("recipe_id", sourceRecipe.ID), zap.Error(ancestorErr))
			}
		}
	}

	req := ai.ForkRequest{
		RecipeRequest: ai.RecipeRequest{
			UserPrompt:     userPrompt,
			UnitSystem:     user.Personalization.UnitSystemText(),
			Requirements:   user.Personalization.Requirements,
			CookingContext: user.Personalization.CookingContextPrompt(),
		},
		ExistingHistory: existingHistory,
	}

	// Goroutine to handle recipe generation
	go func(ctx context.Context, recipeErrChan chan<- error, imageErrChan chan<- error) {
		result, err := s.TextProvider.ForkRecipe(ctx, req)
		if err != nil {
			recipeErrChan <- err
			return
		}

		if result.UnitSystem == "" {
			result.UnitSystem = user.Personalization.UnitSystem
		}
		recipeDef := recipeResultToRecipeDef(result)
		recipe.RecipeDef = recipeDef
		recipe.PromptVersion = result.PromptVersion

		if err := validateRecipeCoreFields(recipe); err != nil {
			recipeErrChan <- err
			return
		}

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

		if err := s.Repo.UpdateRecipeDef(recipe); err != nil {
			recipeErrChan <- err
			return
		}

		if err := s.AssociateTagsWithRecipe(recipe, result.Hashtags); err != nil {
			logger.Get().Error("failed to associate tags with recipe", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
		}

		// Create a tree for the forked recipe with the fork result as the root node
		rootNode := &models.RecipeNode{
			Prompt:      userPrompt,
			Response:    &recipeDef,
			Summary:     result.Summary,
			Type:        models.RecipeTypeFork,
			BranchName:  "original",
			CreatedByID: recipe.CreatedByID,
			IsActive:    true,
		}
		if _, err := s.Repo.CreateRecipeTree(recipe.ID, rootNode); err != nil {
			logger.Get().Error("failed to create recipe tree for fork", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
			// Non-fatal: the recipe was created successfully, tree is supplementary
		}

		s.generateAndStoreEmbedding(ctx, recipe.ID, &recipeDef)

		s.Repo.UpdateRecipeStatus(recipe.ID, "ready")

		recipeErrChan <- nil
	}(ctx, recipeErrChan, imageErrChan)

	// Wait for the recipe generation goroutine to finish or timeout
	select {
	case err := <-recipeErrChan:
		if err != nil {
			recipeID := recipe.ID
			logger.Get().Error("failed to finish recipe fork generation", zap.Uint("recipe_id", recipeID), zap.Error(err))
			s.Repo.UpdateRecipeStatus(recipeID, "failed")
			e := s.DeleteRecipe(context.Background(), recipeID)
			if e != nil {
				logger.Get().Error("failed to delete recipe after fork generation error", zap.Uint("recipe_id", recipeID), zap.Error(e))
				return
			}
			logger.Get().Info("recipe deleted after fork generation error", zap.Uint("recipe_id", recipeID))
			return
		}
	case <-ctx.Done():
		err := errors.New("incomplete recipe generation: timed out after 5 minutes")
		recipeID := recipe.ID
		logger.Get().Error("recipe fork generation timed out", zap.Uint("recipe_id", recipeID), zap.Error(err))
		s.Repo.UpdateRecipeStatus(recipeID, "failed")
		e := s.DeleteRecipe(context.Background(), recipeID)
		if e != nil {
			logger.Get().Error("failed to delete recipe after fork timeout", zap.Uint("recipe_id", recipeID), zap.Error(e))
			return
		}
		logger.Get().Info("recipe deleted after fork timeout", zap.Uint("recipe_id", recipeID))
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
