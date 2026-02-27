package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/s3"
	"go.uber.org/zap"
)

// ImportService handles recipe import from various sources.
type ImportService struct {
	Cfg             *config.Config
	RecipeRepo      repository.RecipeRepo
	RecipeService   *RecipeService
	TextProvider    ai.TextProvider
	VisionProvider  ai.VisionProvider
	PreviewProvider ai.TextProvider
}

// NewImportService creates a new ImportService.
func NewImportService(cfg *config.Config, recipeRepo repository.RecipeRepo, recipeService *RecipeService, textProvider ai.TextProvider, visionProvider ai.VisionProvider, previewProvider ai.TextProvider) *ImportService {
	return &ImportService{
		Cfg:             cfg,
		RecipeRepo:      recipeRepo,
		RecipeService:   recipeService,
		TextProvider:    textProvider,
		VisionProvider:  visionProvider,
		PreviewProvider: previewProvider,
	}
}

// ImportFromURL fetches a page, tries JSON-LD extraction first, falls back to AI.
func (s *ImportService) ImportFromURL(ctx context.Context, url string, user *models.User) (*RecipeResponse, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID), zap.String("source_url", url))

	// Fetch the URL content
	resp, err := http.Get(url)
	if err != nil {
		log.Error("failed to fetch URL", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("URL returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB limit
	if err != nil {
		log.Error("failed to read URL body", zap.Error(err))
		return nil, fmt.Errorf("failed to read URL body: %w", err)
	}
	html := string(body)

	// Try JSON-LD extraction first
	recipeDef, err := extractJSONLD(html)
	if err == nil && recipeDef != nil {
		log.Info("extracted recipe from JSON-LD")
		recipeDef.SourceURL = url
		resp, _, err := s.createImportedRecipe(ctx, recipeDef, user, models.RecipeTypeImportLink, url)
		return resp, err
	}

	// Fall back to AI text extraction
	if s.TextProvider == nil {
		return nil, fmt.Errorf("no AI text provider configured for fallback extraction")
	}

	unitSystem := user.Personalization.GetUnitSystemText()
	result, err := s.TextProvider.ExtractRecipeFromText(ctx, html, unitSystem)
	if err != nil {
		log.Error("AI text extraction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to extract recipe from URL: %w", err)
	}

	recipeDef = aiResultToRecipeDef(result)
	recipeDef.SourceURL = url
	recipeResp, _, createErr := s.createImportedRecipe(ctx, recipeDef, user, models.RecipeTypeImportLink, url)
	return recipeResp, createErr
}

// ImportFromPhoto sends an image to the VisionProvider for recipe extraction.
func (s *ImportService) ImportFromPhoto(ctx context.Context, imageData []byte, user *models.User) (*RecipeResponse, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID))

	if s.VisionProvider == nil {
		return nil, fmt.Errorf("no vision provider configured")
	}

	unitSystem := user.Personalization.GetUnitSystemText()
	requirements := user.Personalization.Requirements

	result, err := s.VisionProvider.ExtractRecipeFromImage(ctx, imageData, unitSystem, requirements)
	if err != nil {
		log.Error("vision extraction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to extract recipe from image: %w", err)
	}

	recipeDef := aiResultToRecipeDef(result)

	// Create the recipe first to get an ID for S3 upload
	recipeResponse, recipeID, err := s.createImportedRecipe(ctx, recipeDef, user, models.RecipeTypeImportVision, "")
	if err != nil {
		return nil, err
	}

	// Upload original image to S3
	s3Key := fmt.Sprintf("recipes/%d/images/original_import.jpg", recipeID)
	imageURL, err := s3.UploadRecipeImageToS3(ctx, s.Cfg, imageData, s3Key)
	if err != nil {
		log.Error("failed to upload original import image", zap.Uint("recipe_id", recipeID), zap.Error(err))
		// Non-fatal: recipe was still created
	} else {
		if err := s.RecipeRepo.UpdateRecipeImageURL(recipeID, imageURL); err != nil {
			log.Error("failed to update recipe with original image URL", zap.Uint("recipe_id", recipeID), zap.Error(err))
		} else {
			recipeResponse.ImageURL = imageURL
		}
	}

	return recipeResponse, nil
}

// ImportFromText sends raw text to AI for structured extraction.
func (s *ImportService) ImportFromText(ctx context.Context, text string, user *models.User) (*RecipeResponse, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID))

	if s.TextProvider == nil {
		return nil, fmt.Errorf("no AI text provider configured")
	}

	unitSystem := user.Personalization.GetUnitSystemText()
	result, err := s.TextProvider.ExtractRecipeFromText(ctx, text, unitSystem)
	if err != nil {
		log.Error("text extraction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to extract recipe from text: %w", err)
	}

	recipeDef := aiResultToRecipeDef(result)
	resp, _, err := s.createImportedRecipe(ctx, recipeDef, user, models.RecipeTypeImportCopypasta, "")
	return resp, err
}

// ImportManual creates a recipe from structured form input.
func (s *ImportService) ImportManual(ctx context.Context, recipeDef *models.RecipeDef, user *models.User, recipeType models.RecipeType) (*RecipeResponse, error) {
	resp, _, err := s.createImportedRecipe(ctx, recipeDef, user, recipeType, "")
	return resp, err
}

// PreviewFromURL fetches a page and extracts recipe data without saving.
// Uses JSON-LD first (free), then falls back to the cheap PreviewProvider.
func (s *ImportService) PreviewFromURL(ctx context.Context, url string, unitSystem string) (*models.RecipeDef, error) {
	log := logger.Get().With(zap.String("source_url", url))

	resp, err := http.Get(url)
	if err != nil {
		log.Error("failed to fetch URL for preview", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("URL returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		log.Error("failed to read URL body for preview", zap.Error(err))
		return nil, fmt.Errorf("failed to read URL body: %w", err)
	}
	html := string(body)

	// Try JSON-LD extraction first (free)
	recipeDef, err := extractJSONLD(html)
	if err == nil && recipeDef != nil {
		log.Info("preview extracted recipe from JSON-LD")
		recipeDef.SourceURL = url
		return recipeDef, nil
	}

	// Fall back to cheap AI extraction
	if s.PreviewProvider == nil {
		return nil, fmt.Errorf("no preview provider configured for fallback extraction")
	}

	result, err := s.PreviewProvider.ExtractRecipeFromText(ctx, html, unitSystem)
	if err != nil {
		log.Error("preview AI extraction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to extract recipe preview: %w", err)
	}

	recipeDef = aiResultToRecipeDef(result)
	recipeDef.SourceURL = url
	return recipeDef, nil
}

// createImportedRecipe creates a recipe in the DB from a RecipeDef.
// Returns the RecipeResponse and the raw DB recipe ID.
func (s *ImportService) createImportedRecipe(ctx context.Context, recipeDef *models.RecipeDef, user *models.User, recipeType models.RecipeType, sourcePrompt string) (*RecipeResponse, uint, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID), zap.String("type", string(recipeType)))

	if recipeDef.Title == "" {
		return nil, 0, fmt.Errorf("recipe title is required")
	}

	historyEntry := models.RecipeHistoryEntry{
		Prompt:   sourcePrompt,
		Response: recipeDef,
		Summary:  fmt.Sprintf("Imported: %s", recipeDef.Title),
		Type:     recipeType,
		Order:    0,
	}

	recipe := &models.Recipe{
		RecipeDef:          *recipeDef,
		UnitSystem:         user.Personalization.UnitSystem,
		CreatedBy:          user,
		PersonalizationUID: user.Personalization.UID,
		History: &models.RecipeHistory{
			Entries: []models.RecipeHistoryEntry{historyEntry},
		},
	}

	if err := s.RecipeRepo.CreateRecipe(recipe); err != nil {
		log.Error("failed to create imported recipe", zap.Error(err))
		return nil, 0, fmt.Errorf("failed to save imported recipe: %w", err)
	}

	// Associate tags if present
	if len(recipeDef.Hashtags) > 0 {
		if err := s.RecipeService.AssociateTagsWithRecipe(recipe, recipeDef.Hashtags); err != nil {
			log.Error("failed to associate tags with imported recipe", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
		}
	}

	// Create a recipe tree with the import as the root node
	rootNode := &models.RecipeNode{
		Prompt:      sourcePrompt,
		Response:    recipeDef,
		Summary:     fmt.Sprintf("Imported: %s", recipeDef.Title),
		Type:        recipeType,
		BranchName:  "original",
		CreatedByID: user.ID,
		IsActive:    true,
	}
	if _, err := s.RecipeRepo.CreateRecipeTree(recipe.ID, rootNode); err != nil {
		log.Error("failed to create recipe tree for import", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
	}

	log.Info("recipe imported successfully", zap.Uint("recipe_id", recipe.ID), zap.String("title", recipeDef.Title))

	recipeResponse := s.RecipeService.ToRecipeResponse(recipe)
	return recipeResponse, recipe.ID, nil
}

// aiResultToRecipeDef converts an ai.RecipeResult to a models.RecipeDef.
func aiResultToRecipeDef(result *ai.RecipeResult) *models.RecipeDef {
	ingredients := make(models.Ingredients, len(result.Ingredients))
	for i, ing := range result.Ingredients {
		ingredients[i] = models.Ingredient{
			Name:             ing.Name,
			Unit:             ing.Unit,
			Amount:           ing.Amount,
			OriginalText:     ing.OriginalText,
			NormalizedAmount: ing.NormalizedAmount,
			NormalizedUnit:   ing.NormalizedUnit,
			IsEstimated:      ing.IsEstimated,
		}
	}

	return &models.RecipeDef{
		Title:             result.Title,
		Ingredients:       ingredients,
		Instructions:      result.Instructions,
		CookTime:          result.CookTime,
		ImagePrompt:       result.ImagePrompt,
		Hashtags:          result.Hashtags,
		LinkedSuggestions: result.LinkedSuggestions,
		Portions:          result.Portions,
		PortionSize:       result.PortionSize,
		SourceURL:         result.SourceURL,
	}
}

// jsonLDRecipe represents the JSON-LD Recipe schema (subset of fields we care about).
type jsonLDRecipe struct {
	Context      interface{} `json:"@context"`
	Type         interface{} `json:"@type"`
	Name         string      `json:"name"`
	Ingredients  []string    `json:"recipeIngredient"`
	Instructions interface{} `json:"recipeInstructions"`
	CookTime     string      `json:"cookTime"`
	TotalTime    string      `json:"totalTime"`
	Yield        interface{} `json:"recipeYield"`
	Image        interface{} `json:"image"`
	Keywords     interface{} `json:"keywords"`
}

// extractJSONLD tries to find and parse JSON-LD recipe data from HTML.
func extractJSONLD(html string) (*models.RecipeDef, error) {
	re := regexp.MustCompile(`(?s)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	matches := re.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		jsonStr := strings.TrimSpace(match[1])

		// Try parsing as a single object
		recipeDef, err := tryParseJSONLDObject(jsonStr)
		if err == nil && recipeDef != nil {
			return recipeDef, nil
		}

		// Try parsing as an array
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &arr); err == nil {
			for _, item := range arr {
				recipeDef, err := tryParseJSONLDObject(string(item))
				if err == nil && recipeDef != nil {
					return recipeDef, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no JSON-LD recipe found")
}

// tryParseJSONLDObject attempts to parse a JSON string as a JSON-LD Recipe.
func tryParseJSONLDObject(jsonStr string) (*models.RecipeDef, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return nil, err
	}

	// Check if this is a @graph container
	if graph, ok := obj["@graph"]; ok {
		if graphArr, ok := graph.([]interface{}); ok {
			for _, item := range graphArr {
				itemBytes, err := json.Marshal(item)
				if err != nil {
					continue
				}
				recipeDef, err := tryParseJSONLDObject(string(itemBytes))
				if err == nil && recipeDef != nil {
					return recipeDef, nil
				}
			}
		}
		return nil, fmt.Errorf("no recipe found in @graph")
	}

	// Check @type
	if !isRecipeType(obj["@type"]) {
		return nil, fmt.Errorf("not a Recipe type")
	}

	var recipe jsonLDRecipe
	if err := json.Unmarshal([]byte(jsonStr), &recipe); err != nil {
		return nil, err
	}

	return jsonLDToRecipeDef(&recipe)
}

// isRecipeType checks if the @type field indicates a Recipe.
func isRecipeType(typeField interface{}) bool {
	switch v := typeField.(type) {
	case string:
		return v == "Recipe" || strings.HasSuffix(v, "/Recipe")
	case []interface{}:
		for _, t := range v {
			if s, ok := t.(string); ok {
				if s == "Recipe" || strings.HasSuffix(s, "/Recipe") {
					return true
				}
			}
		}
	}
	return false
}

// jsonLDToRecipeDef converts a parsed JSON-LD recipe to a RecipeDef.
func jsonLDToRecipeDef(recipe *jsonLDRecipe) (*models.RecipeDef, error) {
	if recipe.Name == "" {
		return nil, fmt.Errorf("recipe name is empty")
	}

	// Parse ingredients
	ingredients := make(models.Ingredients, len(recipe.Ingredients))
	for i, ingStr := range recipe.Ingredients {
		ingredients[i] = models.Ingredient{
			Name:         ingStr,
			OriginalText: ingStr,
		}
	}

	// Parse instructions
	instructions := parseJSONLDInstructions(recipe.Instructions)

	// Parse cook time from ISO 8601 duration
	cookTime := parseISO8601Duration(recipe.CookTime)
	if cookTime == 0 {
		cookTime = parseISO8601Duration(recipe.TotalTime)
	}

	// Parse portions from yield
	portions := parseYield(recipe.Yield)

	// Parse keywords into hashtags
	hashtags := parseKeywords(recipe.Keywords)

	return &models.RecipeDef{
		Title:        recipe.Name,
		Ingredients:  ingredients,
		Instructions: instructions,
		CookTime:     cookTime,
		Portions:     portions,
		Hashtags:     hashtags,
		ImagePrompt:  fmt.Sprintf("A photo of %s", recipe.Name),
	}, nil
}

// parseJSONLDInstructions extracts instruction strings from various JSON-LD formats.
func parseJSONLDInstructions(instructions interface{}) []string {
	if instructions == nil {
		return nil
	}

	switch v := instructions.(type) {
	case string:
		return []string{v}
	case []interface{}:
		var result []string
		for _, item := range v {
			switch step := item.(type) {
			case string:
				result = append(result, step)
			case map[string]interface{}:
				// HowToStep or HowToSection
				if text, ok := step["text"].(string); ok {
					result = append(result, text)
				} else if items, ok := step["itemListElement"].([]interface{}); ok {
					// HowToSection with nested steps
					for _, subItem := range items {
						if subStep, ok := subItem.(map[string]interface{}); ok {
							if text, ok := subStep["text"].(string); ok {
								result = append(result, text)
							}
						}
					}
				}
			}
		}
		return result
	}
	return nil
}

// parseISO8601Duration parses an ISO 8601 duration string (e.g., "PT30M") into minutes.
func parseISO8601Duration(duration string) int {
	if duration == "" {
		return 0
	}

	re := regexp.MustCompile(`PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?`)
	matches := re.FindStringSubmatch(strings.ToUpper(duration))
	if matches == nil {
		return 0
	}

	var total int
	if matches[1] != "" {
		var hours int
		fmt.Sscanf(matches[1], "%d", &hours)
		total += hours * 60
	}
	if matches[2] != "" {
		var minutes int
		fmt.Sscanf(matches[2], "%d", &minutes)
		total += minutes
	}
	if matches[3] != "" {
		var seconds int
		fmt.Sscanf(matches[3], "%d", &seconds)
		if seconds >= 30 {
			total++
		}
	}
	return total
}

// parseYield extracts a portion count from the recipeYield field.
func parseYield(yield interface{}) int {
	switch v := yield.(type) {
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	case float64:
		return int(v)
	case []interface{}:
		if len(v) > 0 {
			return parseYield(v[0])
		}
	}
	return 0
}

// parseKeywords extracts hashtag strings from a keywords field.
func parseKeywords(keywords interface{}) []string {
	switch v := keywords.(type) {
	case string:
		parts := strings.Split(v, ",")
		var result []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	case []interface{}:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

