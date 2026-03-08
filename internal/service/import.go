package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/s3"
	"go.uber.org/zap"
)

// ExtractionError is a typed error returned by extraction functions.
// Code is a machine-readable identifier for the failure reason.
type ExtractionError struct {
	Code    string // e.g. "site_blocked", "not_found", "extraction_failed"
	Message string
}

func (e *ExtractionError) Error() string {
	return e.Message
}

const defaultUserAgent = "Mozilla/5.0 (compatible; SaltyBytesBot/1.0; +https://saltybytes.ai)"

// ImportService handles recipe import from various sources.
type ImportService struct {
	Cfg             *config.Config
	RecipeRepo      repository.RecipeRepo
	RecipeService   *RecipeService
	TextProvider    ai.TextProvider
	VisionProvider  ai.VisionProvider
	PreviewProvider ai.TextProvider
	CanonicalRepo   repository.CanonicalRecipeRepo

	// Test seams — nil in production, set in tests to bypass real HTTP/Firecrawl calls
	HTTPFetchOverride      func(ctx context.Context, url string) (body []byte, statusCode int, err error)
	FirecrawlFetchOverride func(ctx context.Context, url string) (html string, err error)
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

// validateExternalURL checks that a user-supplied URL is safe to fetch.
// It blocks private/internal IPs and non-HTTP(S) schemes to prevent SSRF.
func validateExternalURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow http and https
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q: only http and https are allowed", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no hostname")
	}

	// Resolve hostname to IPs and block internal/private ranges
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname %q: %w", host, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL resolves to a blocked address: %s", ipStr)
		}
	}

	return nil
}

// safeHTTPClient returns an HTTP client that re-validates each redirect hop
// against the SSRF blocklist, preventing open-redirect attacks that bounce
// through a public URL into a private/internal address.
var safeHTTPClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return validateExternalURL(req.URL.String())
	},
}

// ImportFromURL fetches a page, tries JSON-LD extraction first, falls back to AI.
// When a CanonicalRepo is configured, it checks the canonical cache first and
// saves extractions for future deduplication.
func (s *ImportService) ImportFromURL(ctx context.Context, rawURL string, user *models.User) (*RecipeResponse, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID), zap.String("source_url", rawURL))

	if err := validateExternalURL(rawURL); err != nil {
		return nil, fmt.Errorf("URL validation failed: %w", err)
	}

	// Check canonical cache if available
	if s.CanonicalRepo != nil {
		normalizedURL, normErr := NormalizeURL(rawURL)
		if normErr == nil {
			if canonical, err := s.CanonicalRepo.GetByNormalizedURL(normalizedURL); err == nil {
				if time.Since(canonical.FetchedAt) < canonicalTTL {
					log.Info("import from canonical cache hit")
					go s.CanonicalRepo.IncrementHitCount(canonical.ID)
					canonicalID := canonical.ID
					recipeResp, _, createErr := s.createImportedRecipe(ctx, &canonical.RecipeData, user, models.RecipeTypeImportLink, rawURL, &canonicalID, nil)
					return recipeResp, createErr
				}
			}
		}
	}

	recipeDef, hashtags, method, err := s.extractFromURL(ctx, rawURL)
	if err != nil {
		log.Error("extraction failed", zap.Error(err))
		return nil, err
	}

	// Save to canonical cache
	var canonicalID *uint
	if s.CanonicalRepo != nil {
		if normalizedURL, normErr := NormalizeURL(rawURL); normErr == nil {
			now := time.Now()
			entry := &models.CanonicalRecipe{
				NormalizedURL:    normalizedURL,
				OriginalURL:      rawURL,
				RecipeData:       *recipeDef,
				ExtractionMethod: method,
				FetchedAt:        now,
				LastAccessedAt:   now,
			}
			if upsertErr := s.CanonicalRepo.Upsert(entry); upsertErr == nil {
				canonicalID = &entry.ID
			} else {
				log.Warn("failed to upsert canonical", zap.Error(upsertErr))
			}
		}
	}

	recipeResp, _, createErr := s.createImportedRecipe(ctx, recipeDef, user, models.RecipeTypeImportLink, rawURL, canonicalID, hashtags)
	return recipeResp, createErr
}

// ImportFromCanonical creates a recipe as a thin reference to a canonical entry.
func (s *ImportService) ImportFromCanonical(ctx context.Context, canonicalID uint, user *models.User) (*RecipeResponse, error) {
	if s.CanonicalRepo == nil {
		return nil, fmt.Errorf("canonical repository not configured")
	}

	canonical, err := s.CanonicalRepo.GetByID(canonicalID)
	if err != nil {
		return nil, fmt.Errorf("canonical recipe not found: %w", err)
	}

	go s.CanonicalRepo.IncrementHitCount(canonical.ID)

	cID := canonical.ID
	resp, _, createErr := s.createImportedRecipe(ctx, &canonical.RecipeData, user, models.RecipeTypeImportLink, canonical.OriginalURL, &cID, nil)
	return resp, createErr
}

// isBotBlockStatus returns true for HTTP status codes that indicate bot protection.
func isBotBlockStatus(code int) bool {
	return code == http.StatusPaymentRequired || // 402
		code == http.StatusForbidden || // 403
		code == http.StatusServiceUnavailable // 503
}

// isCloudflareChallenge checks if HTML content is a Cloudflare challenge page.
func isCloudflareChallenge(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "<title>Just a moment...</title>") ||
		strings.Contains(s, "challenge-platform")
}

// fetchViaFirecrawl scrapes a URL using the Firecrawl API as a fallback
// when direct HTTP fetch is blocked by bot protection.
func (s *ImportService) fetchViaFirecrawl(ctx context.Context, rawURL string) (string, error) {
	if s.FirecrawlFetchOverride != nil {
		return s.FirecrawlFetchOverride(ctx, rawURL)
	}

	apiKey := s.Cfg.EnvVars.FirecrawlAPIKey
	if apiKey == "" {
		return "", fmt.Errorf("firecrawl API key not configured")
	}

	payload, err := json.Marshal(map[string]interface{}{
		"url":     rawURL,
		"formats": []string{"html"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal firecrawl request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.firecrawl.dev/v1/scrape", strings.NewReader(string(payload)))
	if err != nil {
		return "", fmt.Errorf("failed to create firecrawl request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("firecrawl request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read firecrawl response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("firecrawl returned status %d", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			HTML string `json:"html"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse firecrawl response: %w", err)
	}
	if !result.Success || result.Data.HTML == "" {
		return "", fmt.Errorf("firecrawl returned no HTML")
	}

	return result.Data.HTML, nil
}

// extractFromURL fetches a URL and extracts recipe data via JSON-LD or AI fallback.
// Returns the recipe definition, raw hashtag strings, and the extraction method used.
// Shared by PreviewFromURL, ImportFromURL, and background refresh.
func (s *ImportService) extractFromURL(ctx context.Context, rawURL string) (*models.RecipeDef, []string, models.ExtractionMethod, error) {
	log := logger.Get().With(zap.String("url", rawURL))

	// Phase 1: Fetch HTML (with Firecrawl fallback for blocked sites)
	var html string
	var usedFirecrawl bool

	if s.HTTPFetchOverride != nil {
		body, statusCode, err := s.HTTPFetchOverride(ctx, rawURL)
		if err != nil {
			return nil, nil, "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to fetch URL: %v", err)}
		}
		if statusCode == http.StatusNotFound {
			return nil, nil, "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
		}
		if isBotBlockStatus(statusCode) || isCloudflareChallenge(body) {
			log.Info("direct fetch blocked, trying firecrawl", zap.Int("status", statusCode))
			fcHTML, fcErr := s.fetchViaFirecrawl(ctx, rawURL)
			if fcErr != nil {
				return nil, nil, "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
			}
			html = fcHTML
			usedFirecrawl = true
		} else if statusCode != http.StatusOK {
			return nil, nil, "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("URL returned status %d", statusCode)}
		} else {
			html = string(body)
		}
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to create request: %v", err)}
		}
		req.Header.Set("User-Agent", defaultUserAgent)

		resp, err := safeHTTPClient.Do(req)
		if err != nil {
			return nil, nil, "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to fetch URL: %v", err)}
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if err != nil {
			return nil, nil, "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to read URL body: %v", err)}
		}

		if resp.StatusCode == http.StatusNotFound {
			return nil, nil, "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
		}

		if isBotBlockStatus(resp.StatusCode) || isCloudflareChallenge(body) {
			log.Info("direct fetch blocked, trying firecrawl", zap.Int("status", resp.StatusCode))
			fcHTML, fcErr := s.fetchViaFirecrawl(ctx, rawURL)
			if fcErr != nil {
				return nil, nil, "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
			}
			html = fcHTML
			usedFirecrawl = true
		} else if resp.StatusCode != http.StatusOK {
			return nil, nil, "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("URL returned status %d", resp.StatusCode)}
		} else {
			html = string(body)
		}
	}

	// Phase 2: Extract recipe from HTML
	recipeDef, hashtags, jsonLDErr := extractJSONLD(html)
	if jsonLDErr == nil && recipeDef != nil {
		recipeDef.SourceURL = rawURL
		method := models.ExtractionJSONLD
		if usedFirecrawl {
			method = models.ExtractionFirecrawlJSONLD
		}
		return recipeDef, hashtags, method, nil
	}

	// Fall back to AI extraction
	provider := s.PreviewProvider
	if provider == nil {
		provider = s.TextProvider
	}
	if provider == nil {
		return nil, nil, "", fmt.Errorf("no AI text provider configured for fallback extraction")
	}

	result, err := provider.ExtractRecipeFromText(ctx, html, "preserve source")
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to extract recipe from URL: %w", err)
	}

	def := recipeResultToRecipeDef(result)
	def.SourceURL = rawURL
	method := models.ExtractionHaiku
	if usedFirecrawl {
		method = models.ExtractionFirecrawlHaiku
	}
	return &def, result.Hashtags, method, nil
}

// ImportFromPhoto sends an image to the VisionProvider for recipe extraction.
func (s *ImportService) ImportFromPhoto(ctx context.Context, imageData []byte, user *models.User) (*RecipeResponse, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID))

	if s.VisionProvider == nil {
		return nil, fmt.Errorf("no vision provider configured")
	}

	unitSystem := user.Personalization.UnitSystemText()
	requirements := user.Personalization.Requirements

	result, err := s.VisionProvider.ExtractRecipeFromImage(ctx, imageData, unitSystem, requirements)
	if err != nil {
		log.Error("vision extraction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to extract recipe from image: %w", err)
	}

	def := recipeResultToRecipeDef(result)
	if def.UnitSystem == "" {
		def.UnitSystem = user.Personalization.UnitSystem
	}

	// Create the recipe first to get an ID for S3 upload
	recipeResponse, recipeID, err := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportVision, "", nil, result.Hashtags)
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

	unitSystem := user.Personalization.UnitSystemText()
	result, err := s.TextProvider.ExtractRecipeFromText(ctx, text, unitSystem)
	if err != nil {
		log.Error("text extraction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to extract recipe from text: %w", err)
	}

	def := recipeResultToRecipeDef(result)
	if def.UnitSystem == "" {
		def.UnitSystem = user.Personalization.UnitSystem
	}
	resp, _, err := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportCopypasta, "", nil, result.Hashtags)
	return resp, err
}

// ImportManual creates a recipe from structured form input.
func (s *ImportService) ImportManual(ctx context.Context, recipeDef *models.RecipeDef, user *models.User, recipeType models.RecipeType, hashtags []string) (*RecipeResponse, error) {
	resp, _, err := s.createImportedRecipe(ctx, recipeDef, user, recipeType, "", nil, hashtags)
	return resp, err
}

// PreviewFromURL fetches a page and extracts recipe data without saving.
// When a CanonicalRepo is configured, it checks the cache first and saves
// extractions for future deduplication. Returns the recipe data and optional canonical ID.
func (s *ImportService) PreviewFromURL(ctx context.Context, rawURL string) (*models.RecipeDef, *uint, error) {
	log := logger.Get().With(zap.String("source_url", rawURL))

	if err := validateExternalURL(rawURL); err != nil {
		return nil, nil, fmt.Errorf("URL validation failed: %w", err)
	}

	// Check canonical cache if available
	if s.CanonicalRepo != nil {
		normalizedURL, normErr := NormalizeURL(rawURL)
		if normErr == nil {
			if canonical, err := s.CanonicalRepo.GetByNormalizedURL(normalizedURL); err == nil {
				if time.Since(canonical.FetchedAt) < canonicalTTL {
					log.Info("preview canonical cache hit")
					go s.CanonicalRepo.IncrementHitCount(canonical.ID)
					data := canonical.RecipeData
					if data.SourceURL == "" {
						data.SourceURL = rawURL
					}
					canonicalID := canonical.ID
					return &data, &canonicalID, nil
				}
			}
		}
	}

	recipeDef, _, method, err := s.extractFromURL(ctx, rawURL)
	if err != nil {
		log.Error("preview extraction failed", zap.Error(err))
		return nil, nil, err
	}

	// Save to canonical cache
	var canonicalID *uint
	if s.CanonicalRepo != nil {
		if normalizedURL, normErr := NormalizeURL(rawURL); normErr == nil {
			now := time.Now()
			entry := &models.CanonicalRecipe{
				NormalizedURL:    normalizedURL,
				OriginalURL:      rawURL,
				RecipeData:       *recipeDef,
				ExtractionMethod: method,
				FetchedAt:        now,
				LastAccessedAt:   now,
			}
			if upsertErr := s.CanonicalRepo.Upsert(entry); upsertErr == nil {
				canonicalID = &entry.ID
			} else {
				log.Warn("failed to upsert canonical for preview", zap.Error(upsertErr))
			}
		}
	}

	return recipeDef, canonicalID, nil
}

// createImportedRecipe creates a recipe in the DB from a RecipeDef.
// Returns the RecipeResponse and the raw DB recipe ID.
// canonicalID links the recipe to a canonical entry; nil for non-URL imports.
// hashtags are raw tag strings to associate with the recipe.
func (s *ImportService) createImportedRecipe(ctx context.Context, recipeDef *models.RecipeDef, user *models.User, recipeType models.RecipeType, sourcePrompt string, canonicalID *uint, hashtags []string) (*RecipeResponse, uint, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID), zap.String("type", string(recipeType)))

	if recipeDef.Title == "" {
		return nil, 0, fmt.Errorf("recipe title is required")
	}

	recipe := &models.Recipe{
		RecipeDef:          *recipeDef,
		CreatedBy:          user,
		PersonalizationUID: user.Personalization.UID,
		CanonicalID:        canonicalID,
		HasDiverged:        canonicalID == nil,
	}

	if err := s.RecipeRepo.CreateRecipe(recipe); err != nil {
		log.Error("failed to create imported recipe", zap.Error(err))
		return nil, 0, fmt.Errorf("failed to save imported recipe: %w", err)
	}

	// Associate tags if present
	if len(hashtags) > 0 {
		if err := s.RecipeService.AssociateTagsWithRecipe(recipe, hashtags); err != nil {
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

	// Generate and store embedding for similarity search (best-effort)
	s.RecipeService.generateAndStoreEmbedding(ctx, recipe.ID, recipeDef)

	log.Info("recipe imported successfully", zap.Uint("recipe_id", recipe.ID), zap.String("title", recipeDef.Title))

	recipeResponse := s.RecipeService.ToRecipeResponse(recipe)
	return recipeResponse, recipe.ID, nil
}

// StartCanonicalBackgroundTasks starts periodic refresh goroutines
// for the canonical recipe cache.
func (s *ImportService) StartCanonicalBackgroundTasks() {
	if s.CanonicalRepo == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.refreshStaleCanonicals()
		}
	}()
}

func (s *ImportService) refreshStaleCanonicals() {
	log := logger.Get()

	entries, err := s.CanonicalRepo.GetStaleEntries(canonicalTTL)
	if err != nil {
		log.Error("failed to get stale canonical entries", zap.Error(err))
		return
	}

	for _, entry := range entries {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		recipeDef, _, method, err := s.extractFromURL(ctx, entry.OriginalURL)
		cancel()
		if err != nil {
			log.Warn("failed to refresh canonical entry", zap.String("url", entry.OriginalURL), zap.Error(err))
			continue
		}

		now := time.Now()
		entry.RecipeData = *recipeDef
		entry.ExtractionMethod = method
		entry.FetchedAt = now
		entry.LastAccessedAt = now
		if err := s.CanonicalRepo.Upsert(&entry); err != nil {
			log.Warn("failed to upsert refreshed canonical", zap.String("url", entry.OriginalURL), zap.Error(err))
		}
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
// Returns the recipe definition and raw hashtag strings separately.
func extractJSONLD(html string) (*models.RecipeDef, []string, error) {
	re := regexp.MustCompile(`(?s)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	matches := re.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		jsonStr := strings.TrimSpace(match[1])

		// Try parsing as a single object
		recipeDef, hashtags, err := tryParseJSONLDObject(jsonStr)
		if err == nil && recipeDef != nil {
			return recipeDef, hashtags, nil
		}

		// Try parsing as an array
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &arr); err == nil {
			for _, item := range arr {
				recipeDef, hashtags, err := tryParseJSONLDObject(string(item))
				if err == nil && recipeDef != nil {
					return recipeDef, hashtags, nil
				}
			}
		}
	}

	return nil, nil, fmt.Errorf("no JSON-LD recipe found")
}

// tryParseJSONLDObject attempts to parse a JSON string as a JSON-LD Recipe.
func tryParseJSONLDObject(jsonStr string) (*models.RecipeDef, []string, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return nil, nil, err
	}

	// Check if this is a @graph container
	if graph, ok := obj["@graph"]; ok {
		if graphArr, ok := graph.([]interface{}); ok {
			for _, item := range graphArr {
				itemBytes, err := json.Marshal(item)
				if err != nil {
					continue
				}
				recipeDef, hashtags, err := tryParseJSONLDObject(string(itemBytes))
				if err == nil && recipeDef != nil {
					return recipeDef, hashtags, nil
				}
			}
		}
		return nil, nil, fmt.Errorf("no recipe found in @graph")
	}

	// Check @type
	if !isRecipeType(obj["@type"]) {
		return nil, nil, fmt.Errorf("not a Recipe type")
	}

	var recipe jsonLDRecipe
	if err := json.Unmarshal([]byte(jsonStr), &recipe); err != nil {
		return nil, nil, err
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
// Returns the recipe definition and raw hashtag strings separately.
func jsonLDToRecipeDef(recipe *jsonLDRecipe) (*models.RecipeDef, []string, error) {
	if recipe.Name == "" {
		return nil, nil, fmt.Errorf("recipe name is empty")
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

	unitSystem := detectUnitSystem(recipe.Ingredients)

	return &models.RecipeDef{
		Title:        recipe.Name,
		Ingredients:  ingredients,
		Instructions: instructions,
		CookTime:     cookTime,
		Portions:     portions,
		ImagePrompt:  fmt.Sprintf("A photo of %s", recipe.Name),
		UnitSystem:   unitSystem,
	}, hashtags, nil
}

// Compiled regexes for unit system detection.
var (
	// Matches metric units: "250g", "100 mL", "2 kg", "500ml", etc.
	metricRe = regexp.MustCompile(`(?i)\b\d+\s*(?:g|kg|ml|l)\b|\b(?:gram|kilogram|milliliter|millilitre|liter|litre)s?\b`)
	// Matches US units: "2 cups", "1 tbsp", "3 oz", etc.
	usRe = regexp.MustCompile(`(?i)\b(?:cups?|tbsp|tsp|tablespoons?|teaspoons?|fl\s*oz|ounces?|oz|pounds?|lbs?|pints?|quarts?|gallons?)\b`)
)

// detectUnitSystem scans ingredient strings for US or metric markers.
func detectUnitSystem(ingredients []string) string {
	var usCount, metricCount int
	for _, ing := range ingredients {
		if metricRe.MatchString(ing) {
			metricCount++
		}
		if usRe.MatchString(ing) {
			usCount++
		}
	}

	if metricCount > usCount {
		return "metric"
	}
	return "us_customary"
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

