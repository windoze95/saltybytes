package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// AllergenHandler is the handler for allergen-related requests.
type AllergenHandler struct {
	Service *service.AllergenService
}

// NewAllergenHandler is the constructor function for initializing a new AllergenHandler.
func NewAllergenHandler(allergenService *service.AllergenService) *AllergenHandler {
	return &AllergenHandler{Service: allergenService}
}

// AnalyzeRecipe triggers allergen analysis for a recipe.
// POST /v1/recipes/:recipe_id/allergens/analyze
func (h *AllergenHandler) AnalyzeRecipe(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recipe ID"})
		return
	}

	// Check subscription tier for premium gating
	isPremium := false
	if user.Subscription != nil && user.Subscription.Tier == models.TierPremium {
		isPremium = true
	}

	// Check usage limits
	if user.Subscription != nil && !user.Subscription.CanUseAllergenAnalysis() {
		c.JSON(http.StatusForbidden, gin.H{"error": "allergen analysis limit reached; upgrade to premium for unlimited analyses"})
		return
	}

	result, err := h.Service.AnalyzeRecipe(c.Request.Context(), recipeID, isPremium)
	if err != nil {
		logger.Get().Error("allergen analysis failed", zap.String("recipe_id", recipeIDStr), zap.Error(err))
		switch err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "allergen analysis failed"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"analysis": result})
}

// GetAnalysis returns cached allergen analysis for a recipe.
// GET /v1/recipes/:recipe_id/allergens
func (h *AllergenHandler) GetAnalysis(c *gin.Context) {
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recipe ID"})
		return
	}

	result, err := h.Service.GetAnalysis(c.Request.Context(), recipeID)
	if err != nil {
		logger.Get().Error("failed to get allergen analysis", zap.String("recipe_id", recipeIDStr), zap.Error(err))
		switch err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get allergen analysis"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"analysis": result})
}

// CheckFamily cross-references allergen analysis with family dietary profiles.
// POST /v1/recipes/:recipe_id/allergens/check-family
func (h *AllergenHandler) CheckFamily(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recipe ID"})
		return
	}

	result, err := h.Service.CheckFamily(c.Request.Context(), recipeID, user.ID)
	if err != nil {
		logger.Get().Error("family allergen check failed", zap.String("recipe_id", recipeIDStr), zap.Uint("user_id", user.ID), zap.Error(err))
		switch err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "family allergen check failed"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"family_check": result})
}
