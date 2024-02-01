package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
)

// RecipeHandler is the handler for recipe-related requests.
type RecipeHandler struct {
	Service *service.RecipeService
}

// NewRecipeHandler is the constructor function for initializing a new RecipeHandler.
func NewRecipeHandler(recipeService *service.RecipeService) *RecipeHandler {
	return &RecipeHandler{Service: recipeService}
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
		log.Printf("Error getting recipe: %v", err)
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
	historyIDStr := c.Param("history_id")
	historyID, err := parseUintParam(historyIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe history ID"})
		return
	}

	history, err := h.Service.GetRecipeHistoryByID(historyID)
	if err != nil {
		log.Printf("Error getting recipe history: %v", err)
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

// CreateRecipe creates a new recipe.
func (h *RecipeHandler) GenerateRecipeWithChat(c *gin.Context) {
	// Retrieve the user from the context
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		c.Abort()
		return
	}

	// Parse the request body for the user's prompt
	var request struct {
		UserPrompt string `json:"user_prompt"`
	}

	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	if request.UserPrompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User prompt is required"})
		return
	}

	recipeResponse, err := h.Service.InitGenerateRecipeWithChat(user, request.UserPrompt)
	if err != nil {
		log.Printf("error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred while initializing generation"})
		return
	}

	// go h.Service.FinishGenerateRecipeWithChat(recipe, user, request.UserPrompt)

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse, "message": "Generating recipe"})
}
