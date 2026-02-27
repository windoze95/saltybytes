package handlers

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// ImportHandler handles recipe import requests.
type ImportHandler struct {
	Service *service.ImportService
}

// NewImportHandler creates a new ImportHandler.
func NewImportHandler(importService *service.ImportService) *ImportHandler {
	return &ImportHandler{Service: importService}
}

// ImportFromURL handles POST /v1/recipes/import/url
func (h *ImportHandler) ImportFromURL(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	recipeResponse, err := h.Service.ImportFromURL(c.Request.Context(), url, user)
	if err != nil {
		logger.Get().Error("failed to import recipe from URL", zap.String("url", url), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to import recipe from URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}

// ImportFromPhoto handles POST /v1/recipes/import/photo
func (h *ImportHandler) ImportFromPhoto(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	file, _, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Image file is required"})
		return
	}
	defer file.Close()

	imageData, err := io.ReadAll(io.LimitReader(file, 10*1024*1024)) // 10MB limit
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read image data"})
		return
	}

	if len(imageData) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Image file is empty"})
		return
	}

	recipeResponse, err := h.Service.ImportFromPhoto(c.Request.Context(), imageData, user)
	if err != nil {
		logger.Get().Error("failed to import recipe from photo", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to import recipe from photo"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}

// ImportFromText handles POST /v1/recipes/import/text
func (h *ImportHandler) ImportFromText(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var request struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	text := strings.TrimSpace(request.Text)
	if text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Text is required"})
		return
	}

	recipeResponse, err := h.Service.ImportFromText(c.Request.Context(), text, user)
	if err != nil {
		logger.Get().Error("failed to import recipe from text", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to import recipe from text"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}

// manualImportRequest is the request body for manual recipe import.
type manualImportRequest struct {
	Title        string                  `json:"title" binding:"required"`
	Ingredients  []manualIngredientInput `json:"ingredients" binding:"required"`
	Instructions []string                `json:"instructions" binding:"required"`
	CookTime     int                     `json:"cook_time"`
	Portions     int                     `json:"portions"`
	PortionSize  string                  `json:"portion_size"`
	Hashtags     []string                `json:"hashtags"`
	SourceURL    string                  `json:"source_url"`
}

// manualIngredientInput represents an ingredient in the manual import request.
type manualIngredientInput struct {
	Name   string  `json:"name" binding:"required"`
	Unit   string  `json:"unit"`
	Amount float64 `json:"amount"`
}

// ImportManual handles POST /v1/recipes/import/manual
func (h *ImportHandler) ImportManual(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var request manualImportRequest
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Convert request to RecipeDef
	ingredients := make(models.Ingredients, len(request.Ingredients))
	for i, ing := range request.Ingredients {
		ingredients[i] = models.Ingredient{
			Name:   ing.Name,
			Unit:   ing.Unit,
			Amount: ing.Amount,
		}
	}

	recipeDef := &models.RecipeDef{
		Title:        request.Title,
		Ingredients:  ingredients,
		Instructions: pq.StringArray(request.Instructions),
		CookTime:     request.CookTime,
		Portions:     request.Portions,
		PortionSize:  request.PortionSize,
		Hashtags:     request.Hashtags,
		ImagePrompt:  "A photo of " + request.Title,
		SourceURL:    request.SourceURL,
	}

	recipeType := models.RecipeTypeManualEntry
	if request.SourceURL != "" {
		recipeType = models.RecipeTypeImportLink
	}

	recipeResponse, err := h.Service.ImportManual(c.Request.Context(), recipeDef, user, recipeType)
	if err != nil {
		logger.Get().Error("failed to import recipe manually", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create recipe"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}

// PreviewFromURL handles POST /v1/recipes/preview/url
func (h *ImportHandler) PreviewFromURL(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	unitSystem := user.Personalization.GetUnitSystemText()
	recipeDef, err := h.Service.PreviewFromURL(c.Request.Context(), url, unitSystem)
	if err != nil {
		logger.Get().Error("failed to preview recipe from URL", zap.String("url", url), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to preview recipe from URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeDef})
}
