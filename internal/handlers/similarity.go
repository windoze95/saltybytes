package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"go.uber.org/zap"
)

// SimilarityHandler handles vector similarity search requests.
type SimilarityHandler struct {
	VectorRepo    *repository.VectorRepository
	EmbedProvider ai.EmbeddingProvider
	RecipeService *service.RecipeService
}

// NewSimilarityHandler creates a new SimilarityHandler.
func NewSimilarityHandler(vectorRepo *repository.VectorRepository, embedProvider ai.EmbeddingProvider, recipeService *service.RecipeService) *SimilarityHandler {
	return &SimilarityHandler{
		VectorRepo:    vectorRepo,
		EmbedProvider: embedProvider,
		RecipeService: recipeService,
	}
}

// FindSimilar handles GET /v1/recipes/similar/:recipe_id
func (h *SimilarityHandler) FindSimilar(c *gin.Context) {
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	// Get the recipe to build embedding text
	recipe, err := h.RecipeService.GetRecipeByID(recipeID)
	if err != nil {
		logger.Get().Error("failed to get recipe for similarity", zap.String("recipe_id", recipeIDStr), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	// Generate embedding from recipe title and ingredients
	embeddingText := recipe.Title
	for _, ing := range recipe.Ingredients {
		embeddingText += " " + ing.Name
	}

	embedding, err := h.EmbedProvider.GenerateEmbedding(c.Request.Context(), embeddingText)
	if err != nil {
		logger.Get().Error("failed to generate embedding", zap.Uint("recipe_id", recipeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate embedding"})
		return
	}

	similar, err := h.VectorRepo.FindSimilar(embedding, 10)
	if err != nil {
		logger.Get().Error("failed to find similar recipes", zap.Uint("recipe_id", recipeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find similar recipes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"similar_recipes": similar})
}
