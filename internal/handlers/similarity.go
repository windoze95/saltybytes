package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"go.uber.org/zap"
)

const (
	defaultSimilarLimit = 10
	maxSimilarLimit     = 25
)

// SimilarityHandler handles vector similarity search requests.
type SimilarityHandler struct {
	VectorRepo    repository.VectorRepo
	EmbedProvider ai.EmbeddingProvider
	RecipeService *service.RecipeService
}

// NewSimilarityHandler creates a new SimilarityHandler.
func NewSimilarityHandler(vectorRepo repository.VectorRepo, embedProvider ai.EmbeddingProvider, recipeService *service.RecipeService) *SimilarityHandler {
	return &SimilarityHandler{
		VectorRepo:    vectorRepo,
		EmbedProvider: embedProvider,
		RecipeService: recipeService,
	}
}

// FindSimilar handles GET /v1/recipes/similar/:recipe_id?limit=N
func (h *SimilarityHandler) FindSimilar(c *gin.Context) {
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	limit := defaultSimilarLimit
	if l := c.Query("limit"); l != "" {
		if v, convErr := strconv.Atoi(l); convErr == nil && v > 0 {
			limit = v
		}
	}
	if limit > maxSimilarLimit {
		limit = maxSimilarLimit
	}

	// Get the recipe for existence check and fallback embedding text
	recipe, err := h.RecipeService.GetRecipeByID(recipeID)
	if err != nil {
		logger.Get().Error("failed to get recipe for similarity", zap.String("recipe_id", recipeIDStr), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	// Use the stored embedding when present; only generate (and persist) one
	// when the recipe has no embedding yet.
	stored, err := h.VectorRepo.GetRecipeEmbedding(recipeID)
	if err != nil {
		logger.Get().Error("failed to read stored embedding", zap.Uint("recipe_id", recipeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find similar recipes"})
		return
	}

	var embeddingLiteral string
	if stored != nil && *stored != "" {
		embeddingLiteral = *stored
	} else {
		if h.EmbedProvider == nil {
			logger.Get().Error("no embedding provider configured", zap.Uint("recipe_id", recipeID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate embedding"})
			return
		}

		embeddingText := recipe.Title
		for _, ing := range recipe.Ingredients {
			embeddingText += " " + ing.Name
		}

		embedding, genErr := h.EmbedProvider.GenerateEmbedding(c.Request.Context(), embeddingText)
		if genErr != nil {
			logger.Get().Error("failed to generate embedding", zap.Uint("recipe_id", recipeID), zap.Error(genErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate embedding"})
			return
		}

		if storeErr := h.VectorRepo.UpdateEmbedding(recipeID, embedding); storeErr != nil {
			logger.Get().Warn("failed to store generated embedding", zap.Uint("recipe_id", recipeID), zap.Error(storeErr))
		}

		embeddingLiteral = repository.PgvectorLiteral(embedding)
	}

	similar, err := h.VectorRepo.FindSimilar(embeddingLiteral, recipeID, limit)
	if err != nil {
		logger.Get().Error("failed to find similar recipes", zap.Uint("recipe_id", recipeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find similar recipes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"similar_recipes": h.RecipeService.ToRecipeListItems(similar)})
}
