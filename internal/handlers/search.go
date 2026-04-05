package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// SearchHandler handles recipe search requests.
type SearchHandler struct {
	Service       *service.SearchService
	MultiResolver *service.MultiRecipeResolver // nil-safe
}

// NewSearchHandler creates a new SearchHandler.
func NewSearchHandler(searchService *service.SearchService) *SearchHandler {
	return &SearchHandler{Service: searchService}
}

// SearchRecipes handles GET /v1/recipes/search?q=...&count=10
func (h *SearchHandler) SearchRecipes(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Check subscription limits
	if user.Subscription != nil && !user.Subscription.CanUseWebSearch() {
		c.JSON(http.StatusForbidden, gin.H{"error": "search limit reached; upgrade to premium for unlimited searches"})
		return
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query parameter 'q' is required"})
		return
	}

	count := 10
	if countStr := c.Query("count"); countStr != "" {
		parsed, err := strconv.Atoi(countStr)
		if err == nil && parsed > 0 && parsed <= 50 {
			count = parsed
		}
	}

	offset := 0
	if offsetStr := c.Query("offset"); offsetStr != "" {
		parsed, err := strconv.Atoi(offsetStr)
		if err != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid offset parameter"})
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		offset = parsed
	}

	result, err := h.Service.SearchRecipes(c.Request.Context(), query, count, offset)
	if err != nil {
		logger.Get().Error("failed to search recipes", zap.String("query", query), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to search recipes"})
		return
	}

	// Increment usage for every non-cached search (including pagination)
	if !result.FromCache && h.Service.SubService != nil {
		if err := h.Service.SubService.IncrementUsage(user.ID, "search"); err != nil {
			logger.Get().Error("failed to increment search usage", zap.Uint("user_id", user.ID), zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": result.Results, "has_more": result.HasMore})
}

// ResolveMultiRecipe handles GET /v1/recipes/search/resolve/:multi_id
// Returns the current state of a multi-recipe resolution.
func (h *SearchHandler) ResolveMultiRecipe(c *gin.Context) {
	if h.MultiResolver == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "multi-recipe resolution not available"})
		return
	}

	multiID := c.Param("multi_id")
	if multiID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multi_id is required"})
		return
	}

	entry := h.MultiResolver.Registry.GetByID(multiID)
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "multi-recipe entry not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"multi_id":   entry.ID,
		"source_url": entry.SourceURL,
		"status":     entry.GetStatus(),
		"recipes":    entry.GetCards(),
	})
}

// CheckMultiRecipe handles POST /v1/recipes/search/check-multi
// Late detection: checks if a URL contains multiple recipes.
// If it does, starts resolution and returns individual cards.
func (h *SearchHandler) CheckMultiRecipe(c *gin.Context) {
	if _, err := util.GetUserFromContext(c); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if h.MultiResolver == nil {
		c.JSON(http.StatusOK, gin.H{"is_multi": false})
		return
	}

	var request struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	url := strings.TrimSpace(request.URL)
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL is required"})
		return
	}

	// Validate URL before any network fetches to prevent SSRF
	if err := service.ValidateExternalURL(url); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid URL: " + err.Error()})
		return
	}

	// Check if already tracked
	if existing := h.MultiResolver.Registry.Get(url); existing != nil {
		c.JSON(http.StatusOK, gin.H{
			"is_multi": true,
			"multi_id": existing.ID,
			"status":   existing.GetStatus(),
			"recipes":  existing.GetCards(),
		})
		return
	}

	// Try to resolve — this fetches the page and checks for multiple recipes
	entry := h.MultiResolver.ResolveFromURL(c.Request.Context(), url)
	if entry == nil {
		c.JSON(http.StatusOK, gin.H{"is_multi": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"is_multi": true,
		"multi_id": entry.ID,
		"status":   entry.GetStatus(),
		"recipes":  entry.GetCards(),
	})
}
