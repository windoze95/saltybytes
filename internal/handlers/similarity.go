package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	CanonicalRepo repository.CanonicalRecipeRepo
	EmbedProvider ai.EmbeddingProvider
	RecipeService *service.RecipeService
}

// NewSimilarityHandler creates a new SimilarityHandler.
func NewSimilarityHandler(vectorRepo repository.VectorRepo, canonicalRepo repository.CanonicalRecipeRepo, embedProvider ai.EmbeddingProvider, recipeService *service.RecipeService) *SimilarityHandler {
	return &SimilarityHandler{
		VectorRepo:    vectorRepo,
		CanonicalRepo: canonicalRepo,
		EmbedProvider: embedProvider,
		RecipeService: recipeService,
	}
}

// similarWebRecipe is a lightweight card for a canonical (extracted) recipe:
// enough to render and to re-open its preview by source URL. Canonicals carry
// no image, so none is returned.
type similarWebRecipe struct {
	Title        string `json:"title"`
	SourceURL    string `json:"source_url"`
	SourceDomain string `json:"source_domain"`
}

// FindSimilarByURL handles GET /v1/recipes/similar-by-url?u=<url>&limit=N.
// It powers the preview screen's "similar recipes" strip: the previewed page
// isn't a saved recipe, so similarity is computed against the canonical
// extraction pool using the page's cached embedding (generated on demand and
// persisted when absent). A cache miss is not an error — it just yields an
// empty list so the section quietly hides.
func (h *SimilarityHandler) FindSimilarByURL(c *gin.Context) {
	rawURL := c.Query("u")
	if rawURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing url"})
		return
	}
	if h.CanonicalRepo == nil {
		c.JSON(http.StatusOK, gin.H{"similar_recipes": []similarWebRecipe{}})
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

	normalizedURL, err := service.NormalizeURL(rawURL)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"similar_recipes": []similarWebRecipe{}})
		return
	}
	canonical, err := h.CanonicalRepo.GetByNormalizedURL(normalizedURL)
	if err != nil || canonical == nil || canonical.IsMultiPage || canonical.RecipeData.Title == "" {
		c.JSON(http.StatusOK, gin.H{"similar_recipes": []similarWebRecipe{}})
		return
	}

	// Reuse the page's stored embedding; generate + persist one only when the
	// extraction hasn't been embedded yet (the backfill usually has).
	var embeddingLiteral string
	if canonical.Embedding != nil && *canonical.Embedding != "" {
		embeddingLiteral = *canonical.Embedding
	} else if h.EmbedProvider != nil {
		text := canonical.RecipeData.Title
		for _, ing := range canonical.RecipeData.Ingredients {
			text += " " + ing.Name
		}
		embedding, genErr := h.EmbedProvider.GenerateEmbedding(c.Request.Context(), text)
		if genErr != nil {
			logger.Get().Warn("similar-by-url: embedding generation failed", zap.Uint("canonical_id", canonical.ID), zap.Error(genErr))
			c.JSON(http.StatusOK, gin.H{"similar_recipes": []similarWebRecipe{}})
			return
		}
		if storeErr := h.VectorRepo.UpdateCanonicalEmbedding(canonical.ID, embedding); storeErr != nil {
			logger.Get().Warn("similar-by-url: failed to persist embedding", zap.Uint("canonical_id", canonical.ID), zap.Error(storeErr))
		}
		embeddingLiteral = repository.PgvectorLiteral(embedding)
	} else {
		c.JSON(http.StatusOK, gin.H{"similar_recipes": []similarWebRecipe{}})
		return
	}

	similar, err := h.VectorRepo.FindSimilarCanonicals(embeddingLiteral, canonical.ID, limit)
	if err != nil {
		logger.Get().Error("similar-by-url: query failed", zap.Uint("canonical_id", canonical.ID), zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"similar_recipes": []similarWebRecipe{}})
		return
	}

	items := make([]similarWebRecipe, 0, len(similar))
	for _, entry := range similar {
		source := entry.RecipeData.SourceURL
		if source == "" {
			source = entry.OriginalURL
		}
		items = append(items, similarWebRecipe{
			Title:        entry.RecipeData.Title,
			SourceURL:    source,
			SourceDomain: similarDomainOf(source),
		})
	}
	c.JSON(http.StatusOK, gin.H{"similar_recipes": items})
}

// similarDomainOf extracts a bare hostname for display.
func similarDomainOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.TrimPrefix(parsed.Host, "www.")
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
