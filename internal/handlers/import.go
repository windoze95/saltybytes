package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
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
	Service       *service.ImportService
	MultiResolver *service.MultiRecipeResolver // nil-safe; set for multi-recipe detection
	// SubService gates the premium video-import endpoint by subscription usage
	// when set (nil skips gating, e.g. in isolated tests).
	SubService *service.SubscriptionService
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
		var extractErr *service.ExtractionError
		if errors.As(err, &extractErr) {
			status := http.StatusInternalServerError
			switch extractErr.Code {
			case "site_blocked":
				status = http.StatusUnprocessableEntity
			case "fetch_failed":
				status = http.StatusBadGateway
			case "not_found":
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": extractErr.Message, "code": extractErr.Code})
			return
		}
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

// ImportFromFiles handles POST /v1/recipes/import/files — one or more images
// and/or PDF documents in a single request, each yielding one or more recipes.
func (h *ImportHandler) ImportFromFiles(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	const (
		maxFiles      = 10
		maxPerFile    = 10 * 1024 * 1024 // 10MB per file
		maxTotalBytes = 28 * 1024 * 1024 // keep the Claude request under its 32MB ceiling
	)

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid multipart form: " + err.Error()})
		return
	}
	headers := form.File["files"]
	if len(headers) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "At least one file is required (field 'files')"})
		return
	}
	if len(headers) > maxFiles {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Too many files (max %d)", maxFiles)})
		return
	}

	var files []service.FileInput
	var total int
	for _, fh := range headers {
		f, err := fh.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read an uploaded file"})
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, maxPerFile+1))
		f.Close()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read an uploaded file"})
			return
		}
		if len(data) == 0 {
			continue
		}
		if len(data) > maxPerFile {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("File %q exceeds the %dMB limit", fh.Filename, maxPerFile/(1024*1024))})
			return
		}
		total += len(data)
		if total > maxTotalBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Total upload size is too large"})
			return
		}
		files = append(files, service.FileInput{Data: data})
	}

	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No non-empty files provided"})
		return
	}

	recipes, err := h.Service.ImportFromFiles(c.Request.Context(), files, user)
	if err != nil {
		logger.Get().Error("failed to import recipes from files", zap.Error(err))
		var extractErr *service.ExtractionError
		if errors.As(err, &extractErr) {
			status := http.StatusUnprocessableEntity
			if extractErr.Code == "unsupported_file" || extractErr.Code == "no_files" {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"error": extractErr.Message, "code": extractErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to import recipes from files"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipes": recipes})
}

// ImportFromVoice handles POST /v1/recipes/import/voice — a spoken-audio recipe,
// transcribed via Whisper and extracted from the transcript.
func (h *ImportHandler) ImportFromVoice(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Audio file is required (field 'audio')"})
		return
	}
	defer file.Close()

	audioData, err := io.ReadAll(io.LimitReader(file, 25*1024*1024)) // 25MB (Whisper file limit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read audio data"})
		return
	}
	if len(audioData) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Audio file is empty"})
		return
	}

	format := strings.TrimSpace(c.PostForm("format"))
	if format == "" {
		format = filepath.Ext(header.Filename)
	}

	recipeResponse, err := h.Service.ImportFromVoice(c.Request.Context(), audioData, format, user)
	if err != nil {
		logger.Get().Error("failed to import recipe from voice", zap.Error(err))
		var extractErr *service.ExtractionError
		if errors.As(err, &extractErr) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": extractErr.Message, "code": extractErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to import recipe from voice"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}

// videoImportResponse serializes an async video-import job for the client.
func videoImportResponse(job *models.VideoImport) gin.H {
	resp := gin.H{
		"id":        job.ID,
		"status":    job.Status,
		"platform":  job.Platform,
		"cache_hit": job.CacheHit,
	}
	if job.RecipeID != nil {
		resp["recipe_id"] = *job.RecipeID
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	return resp
}

// ImportFromVideo handles POST /v1/recipes/import/video — a premium, paywalled
// import from a social/video link (TikTok, Instagram, YouTube, Facebook,
// Pinterest). It returns a queued job immediately; the client polls
// GetVideoImportStatus until the job is done and a recipe_id is present.
func (h *ImportHandler) ImportFromVideo(c *gin.Context) {
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

	// Subscription gate: free tier gets a small monthly allotment, premium more.
	if h.SubService != nil {
		allowed, err := h.SubService.CheckLimit(user.ID, "video_import")
		if err != nil {
			logger.Get().Error("failed to check video import limit", zap.Uint("user_id", user.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check subscription limits"})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Video import limit reached; upgrade to premium for more video imports",
				"code":  "video_limit_reached",
			})
			return
		}
	}

	job, err := h.Service.StartVideoImport(c.Request.Context(), url, user)
	if err != nil {
		logger.Get().Error("failed to start video import", zap.String("url", url), zap.Error(err))
		var extractErr *service.ExtractionError
		if errors.As(err, &extractErr) {
			status := http.StatusUnprocessableEntity
			switch extractErr.Code {
			case "unsupported_platform":
				status = http.StatusBadRequest
			case "video_unavailable":
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": extractErr.Message, "code": extractErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start video import"})
		return
	}

	// Count the accepted job against the user's monthly quota. Done after the
	// job is created so a rejected request (bad platform, feature off) is free.
	if h.SubService != nil {
		if err := h.SubService.IncrementUsage(user.ID, "video_import"); err != nil {
			logger.Get().Error("failed to increment video import usage", zap.Uint("user_id", user.ID), zap.Error(err))
		}
	}

	c.JSON(http.StatusAccepted, gin.H{"job": videoImportResponse(job)})
}

// GetVideoImportStatus handles GET /v1/recipes/import/video/:id — polling for an
// async video-import job. Only the job's owner may read it.
func (h *ImportHandler) GetVideoImportStatus(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job id"})
		return
	}

	job, err := h.Service.GetVideoImport(uint(id))
	// Return 404 both when the job is missing and when it belongs to another
	// user, so a caller cannot probe for others' job IDs.
	if err != nil || job == nil || job.UserID != user.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Video import job not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"job": videoImportResponse(job)})
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
	UnitSystem   string                  `json:"unit_system"`
	ImageURL     string                  `json:"image_url"`
}

// manualIngredientInput represents an ingredient in the manual import request.
type manualIngredientInput struct {
	Name         string  `json:"name" binding:"required"`
	Unit         string  `json:"unit"`
	Amount       float64 `json:"amount"`
	MetricUnit   string  `json:"metric_unit"`
	MetricAmount float64 `json:"metric_amount"`
	OriginalText string  `json:"original_text"`
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

	if request.UnitSystem != "" && request.UnitSystem != "us_customary" && request.UnitSystem != "metric" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unit_system must be 'us_customary' or 'metric'"})
		return
	}

	// image_url is stored verbatim and later served to any authenticated
	// viewer of the recipe, so reject anything that isn't a plain http(s)
	// URL (e.g. javascript:, data:, or garbage).
	if request.ImageURL != "" {
		u, err := url.Parse(request.ImageURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image_url must be a valid http(s) URL"})
			return
		}
	}

	// Convert request to RecipeDef
	ingredients := make(models.Ingredients, len(request.Ingredients))
	for i, ing := range request.Ingredients {
		ingredients[i] = models.Ingredient{
			Name:         ing.Name,
			Unit:         ing.Unit,
			Amount:       ing.Amount,
			MetricUnit:   ing.MetricUnit,
			MetricAmount: ing.MetricAmount,
			OriginalText: ing.OriginalText,
		}
	}

	// Use the unit system supplied by the client when present (e.g. preserved
	// from a preview extraction). Otherwise detect it from the entered
	// ingredients so a US recipe typed by a metric user is labeled correctly,
	// only falling back to the user's personalization when no unit markers
	// exist at all.
	unitSystem := request.UnitSystem
	if unitSystem == "" {
		if detected, hasMarker := service.DetectUnitSystemFromIngredients(ingredients); hasMarker {
			unitSystem = detected
		} else {
			unitSystem = user.Personalization.UnitSystem
		}
	}

	recipeDef := &models.RecipeDef{
		Title:        request.Title,
		Ingredients:  ingredients,
		Instructions: pq.StringArray(request.Instructions),
		CookTime:     request.CookTime,
		Portions:     request.Portions,
		PortionSize:  request.PortionSize,
		ImagePrompt:  "A photo of " + request.Title,
		SourceURL:    request.SourceURL,
		UnitSystem:   unitSystem,
	}

	recipeType := models.RecipeTypeManualEntry
	if request.SourceURL != "" {
		recipeType = models.RecipeTypeImportLink
	}

	recipeResponse, err := h.Service.ImportManual(c.Request.Context(), recipeDef, user, recipeType, request.Hashtags, request.ImageURL)
	if err != nil {
		logger.Get().Error("failed to import recipe manually", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create recipe"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}

// PreviewFromURL handles POST /v1/recipes/preview/url
func (h *ImportHandler) PreviewFromURL(c *gin.Context) {
	if _, err := util.GetUserFromContext(c); err != nil {
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

	result, err := h.Service.PreviewFromURLWithMultiCheck(c.Request.Context(), url, h.MultiResolver)
	if err != nil {
		logger.Get().Error("failed to preview recipe from URL", zap.String("url", url), zap.Error(err))
		var extractErr *service.ExtractionError
		if errors.As(err, &extractErr) {
			status := http.StatusInternalServerError
			switch extractErr.Code {
			case "site_blocked":
				status = http.StatusUnprocessableEntity
			case "fetch_failed":
				status = http.StatusBadGateway
			case "not_found":
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": extractErr.Message, "code": extractErr.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to preview recipe from URL"})
		return
	}

	if result.IsMulti {
		c.JSON(http.StatusOK, gin.H{
			"is_multi": true,
			"multi_id": result.MultiID,
			"recipes":  result.MultiCards,
		})
		return
	}

	response := gin.H{"recipe": result.Recipe}
	if result.CanonicalID != nil {
		response["canonical_id"] = *result.CanonicalID
	}
	if result.Recipe != nil && result.Recipe.UnitSystem != "" {
		response["unit_system"] = result.Recipe.UnitSystem
	}
	c.JSON(http.StatusOK, response)
}

// ImportFromCanonical handles POST /v1/recipes/import/canonical
func (h *ImportHandler) ImportFromCanonical(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var request struct {
		CanonicalID uint `json:"canonical_id" binding:"required"`
	}
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	if request.CanonicalID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "canonical_id is required"})
		return
	}

	recipeResponse, err := h.Service.ImportFromCanonical(c.Request.Context(), request.CanonicalID, user)
	if err != nil {
		logger.Get().Error("failed to import recipe from canonical", zap.Uint("canonical_id", request.CanonicalID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to import recipe"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipe": recipeResponse})
}
