package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"go.uber.org/zap"
)

// SearchHandler handles recipe search requests.
type SearchHandler struct {
	Service *service.SearchService
}

// NewSearchHandler creates a new SearchHandler.
func NewSearchHandler(searchService *service.SearchService) *SearchHandler {
	return &SearchHandler{Service: searchService}
}

// SearchRecipes handles GET /v1/recipes/search?q=...&count=10
func (h *SearchHandler) SearchRecipes(c *gin.Context) {
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

	results, err := h.Service.SearchRecipes(c.Request.Context(), query, count)
	if err != nil {
		logger.Get().Error("failed to search recipes", zap.String("query", query), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to search recipes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}
