package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// RecipeHandler is the handler for recipe-related requests.
type RecipeHandler struct {
	Service *service.RecipeService
}

// NewRecipeHandler is the constructor function for initializing a new RecipeHandler.
func NewRecipeHandler(recipeService *service.RecipeService) *RecipeHandler {
	return &RecipeHandler{Service: recipeService}
}

// ListRecipes returns a paginated list of the authenticated user's recipes.
func (h *RecipeHandler) ListRecipes(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	page := 1
	pageSize := 20

	if p := c.Query("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	if ps := c.Query("page_size"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil && v > 0 && v <= 100 {
			pageSize = v
		}
	}

	recipes, total, err := h.Service.GetUserRecipes(user.ID, page, pageSize)
	if err != nil {
		logger.Get().Error("failed to list recipes", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list recipes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"recipes":  recipes,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// GetRecipe returns a recipe by ID.
func (h *RecipeHandler) GetRecipe(c *gin.Context) {
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	recipeResponse, err := h.Service.GetRecipeByID(recipeID)
	if err != nil {
		logger.Get().Error("failed to get recipe", zap.String("recipe_id", recipeIDStr), zap.Error(err))
		switch e := err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": e.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": e.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}

// GetRecipeHistory returns a recipe history by ID.
func (h *RecipeHandler) GetRecipeHistory(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	historyIDStr := c.Param("history_id")
	historyID, err := parseUintParam(historyIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe history ID"})
		return
	}

	// Verify ownership via the recipe that owns this history
	recipe, err := h.Service.Repo.GetRecipeByHistoryID(historyID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe history not found"})
		return
	}
	if recipe.CreatedByID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only view your own recipe history"})
		return
	}

	history, err := h.Service.GetRecipeHistoryByID(historyID)
	if err != nil {
		logger.Get().Error("failed to get recipe history", zap.String("history_id", historyIDStr), zap.Error(err))
		switch e := err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": e.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": e.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipeHistory": history})
}

// GenerateRecipe generates a new recipe with chat.
func (h *RecipeHandler) GenerateRecipe(c *gin.Context) {
	// Retrieve the user from the context
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		c.Abort()
		return
	}

	var request struct {
		UserPrompt string `json:"user_prompt"`
		GenImage   *bool  `json:"gen_image"`
	}

	// Parse the request body
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Check if GenImage was provided, if not, default to true
	var genImage bool
	if request.GenImage == nil {
		genImage = true
	} else {
		genImage = *request.GenImage
	}

	prompt := strings.TrimSpace(request.UserPrompt)
	recipeResponse, err := h.Service.InitGenerateRecipe(user, prompt, genImage)
	if err != nil {
		logger.Get().Error("failed to initialize recipe generation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred while initializing generation"})
		return
	}

	// go h.Service.FinishGenerateRecipe(recipe, user, request.UserPrompt)

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse, "message": "Generating recipe"})
}

// RegenerateRecipe regenerates a recipe with chat.
func (h *RecipeHandler) RegenerateRecipe(c *gin.Context) {
	// Retrieve the user from the context
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		c.Abort()
		return
	}

	// Use recipe_id from the URL path, not the request body
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	// Parse the request body for the user's prompt
	var request struct {
		UserPrompt string `json:"user_prompt"`
		GenImage   *bool  `json:"gen_image"`
	}

	// Parse the request body
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Check if GenImage was provided, if not, default to true
	var genImage bool
	if request.GenImage == nil {
		genImage = true
	} else {
		genImage = *request.GenImage
	}

	prompt := strings.TrimSpace(request.UserPrompt)
	err = h.Service.InitRegenerateRecipe(user, recipeID, prompt, genImage)
	if err != nil {
		logger.Get().Error("failed to initialize recipe regeneration", zap.Uint("recipe_id", recipeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred while initializing generation"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Regenerating recipe"})
}

// GenerateRecipeWithFork regenerates a recipe with a fork.
func (h *RecipeHandler) GenerateRecipeWithFork(c *gin.Context) {
	// Retrieve the user from the context
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		c.Abort()
		return
	}

	// Use recipe_id from the URL path, not the request body
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	// Parse the request body for the user's prompt
	var request struct {
		UserPrompt string `json:"user_prompt"`
		GenImage   *bool  `json:"gen_image"`
	}

	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Check if GenImage was provided, if not, default to true
	var genImage bool
	if request.GenImage == nil {
		genImage = true
	} else {
		genImage = *request.GenImage
	}

	prompt := strings.TrimSpace(request.UserPrompt)
	recipeResponse, err := h.Service.InitGenerateRecipeWithFork(user, recipeID, prompt, genImage)
	if err != nil {
		logger.Get().Error("failed to initialize recipe fork", zap.Uint("recipe_id", recipeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred while initializing generation"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse, "message": "Regenerating recipe"})
}

// DeleteRecipe deletes a recipe by its ID.
func (h *RecipeHandler) DeleteRecipe(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	// Verify ownership
	recipe, err := h.Service.GetRecipeByID(recipeID)
	if err != nil {
		logger.Get().Error("failed to get recipe for deletion", zap.String("recipe_id", recipeIDStr), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	if recipe.OwnerID != strconv.FormatUint(uint64(user.ID), 10) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only delete your own recipes"})
		return
	}

	if err := h.Service.DeleteRecipe(c.Request.Context(), recipeID); err != nil {
		logger.Get().Error("failed to delete recipe", zap.Uint("recipe_id", recipeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete recipe"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Recipe deleted successfully"})
}
