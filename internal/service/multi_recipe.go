package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
)

// MultiRecipeCard is a lightweight preview of one recipe found on a multi-recipe page.
type MultiRecipeCard struct {
	Title            string              `json:"title"`
	ImageURL         string              `json:"image_url,omitempty"`
	Description      string              `json:"description,omitempty"`
	SourceURL        string              `json:"source_url"`
	ExtractionStatus string              `json:"extraction_status"` // "pending", "extracting", "done", "failed"
	RecipeDef        *models.RecipeDef   `json:"recipe,omitempty"`  // populated when done
	Hashtags         []string            `json:"hashtags,omitempty"`
}

// MultiRecipeEntry tracks the resolution state of a multi-recipe URL.
type MultiRecipeEntry struct {
	mu          sync.RWMutex
	ID          string            `json:"multi_id"`
	SourceURL   string            `json:"source_url"`
	Status      string            `json:"status"` // "resolving", "resolved", "failed"
	Cards       []MultiRecipeCard `json:"recipes"`
	DetectedAt  time.Time         `json:"detected_at"`
	ResolvedAt  *time.Time        `json:"resolved_at,omitempty"`
}

func (e *MultiRecipeEntry) GetCards() []MultiRecipeCard {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cards := make([]MultiRecipeCard, len(e.Cards))
	copy(cards, e.Cards)
	return cards
}

func (e *MultiRecipeEntry) GetStatus() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Status
}

// MultiRecipeRegistry tracks multi-recipe URL resolution state in memory.
// Prevents duplicate extraction work when a result is clicked while already extracting.
type MultiRecipeRegistry struct {
	mu      sync.RWMutex
	entries map[string]*MultiRecipeEntry // keyed by source URL
	counter uint64
}

// NewMultiRecipeRegistry creates a new registry.
func NewMultiRecipeRegistry() *MultiRecipeRegistry {
	return &MultiRecipeRegistry{
		entries: make(map[string]*MultiRecipeEntry),
	}
}

// Get returns the entry for a URL, or nil if not tracked.
func (r *MultiRecipeRegistry) Get(sourceURL string) *MultiRecipeEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entries[sourceURL]
}

// GetByID returns the entry with the given multi_id, or nil.
func (r *MultiRecipeRegistry) GetByID(multiID string) *MultiRecipeEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if entry.ID == multiID {
			return entry
		}
	}
	return nil
}

// Register creates a new entry for a multi-recipe URL if one doesn't exist.
// Returns the entry (existing or new) and whether it was newly created.
func (r *MultiRecipeRegistry) Register(sourceURL string) (*MultiRecipeEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.entries[sourceURL]; ok {
		return existing, false
	}

	r.counter++
	entry := &MultiRecipeEntry{
		ID:         fmt.Sprintf("multi_%d_%d", time.Now().UnixMilli(), r.counter),
		SourceURL:  sourceURL,
		Status:     "resolving",
		DetectedAt: time.Now(),
	}
	r.entries[sourceURL] = entry
	return entry, true
}

// Compiled patterns for detecting multi-recipe titles.
var multiRecipePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\d+\s+(?:best|top|easy|quick|healthy|delicious|amazing|favorite|favourite|simple|great)\s+.+(?:recipes?|dishes|meals)`),
	regexp.MustCompile(`(?i)^(?:best|top)\s+\d+\s+.+(?:recipes?|dishes|meals)`),
	regexp.MustCompile(`(?i)\d+\s+.+(?:recipes?|dishes|meals)\s+(?:to|for|you|that|of)\b`),
	regexp.MustCompile(`(?i)^(?:the\s+)?(?:best|top|ultimate|definitive)\s+.+(?:recipes?|dishes|meals)\s+(?:of|for|in)\s+\d{4}`),
	regexp.MustCompile(`(?i)\d+\s+(?:ways?\s+to\s+(?:cook|make|prepare)|(?:recipes?|dishes|meals)\s+(?:everyone|you))`),
}

// IsMultiRecipeTitle returns true if the title looks like a multi-recipe listicle.
func IsMultiRecipeTitle(title string) bool {
	title = strings.TrimSpace(title)
	for _, re := range multiRecipePatterns {
		if re.MatchString(title) {
			return true
		}
	}
	return false
}

// extractAllJSONLDRecipes extracts ALL Recipe JSON-LD blocks from HTML,
// not just the first one. Returns lightweight cards for each.
func extractAllJSONLDRecipes(html string, sourceURL string) []MultiRecipeCard {
	re := regexp.MustCompile(`(?s)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	matches := re.FindAllStringSubmatch(html, -1)

	var cards []MultiRecipeCard
	seen := make(map[string]bool)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		jsonStr := strings.TrimSpace(match[1])
		recipes := findAllRecipesInJSONLD(jsonStr)
		for _, r := range recipes {
			if r.Title == "" || seen[r.Title] {
				continue
			}
			seen[r.Title] = true
			cards = append(cards, MultiRecipeCard{
				Title:            r.Title,
				ImageURL:         r.ImageURL,
				Description:      r.Description,
				SourceURL:        sourceURL,
				ExtractionStatus: "pending",
			})
		}
	}

	return cards
}

// recipePreview holds lightweight data extracted from JSON-LD.
type recipePreview struct {
	Title       string
	ImageURL    string
	Description string
}

// findAllRecipesInJSONLD extracts all Recipe objects from a JSON-LD string.
func findAllRecipesInJSONLD(jsonStr string) []recipePreview {
	var results []recipePreview

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		// Try as array
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &arr); err != nil {
			return nil
		}
		for _, item := range arr {
			results = append(results, findAllRecipesInJSONLD(string(item))...)
		}
		return results
	}

	// Check @graph
	if graph, ok := obj["@graph"]; ok {
		if graphArr, ok := graph.([]interface{}); ok {
			for _, item := range graphArr {
				itemBytes, _ := json.Marshal(item)
				results = append(results, findAllRecipesInJSONLD(string(itemBytes))...)
			}
		}
		return results
	}

	// Check if this is a Recipe
	if isRecipeType(obj["@type"]) {
		preview := recipePreview{
			Title: stringField(obj, "name"),
		}
		// Extract image
		switch img := obj["image"].(type) {
		case string:
			preview.ImageURL = img
		case []interface{}:
			if len(img) > 0 {
				if s, ok := img[0].(string); ok {
					preview.ImageURL = s
				} else if m, ok := img[0].(map[string]interface{}); ok {
					preview.ImageURL = stringField(m, "url")
				}
			}
		case map[string]interface{}:
			preview.ImageURL = stringField(img, "url")
		}
		// Extract description
		preview.Description = stringField(obj, "description")
		if preview.Title != "" {
			results = append(results, preview)
		}
	}

	return results
}

func stringField(obj map[string]interface{}, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}

// MultiRecipeResolver handles detection and background extraction of multi-recipe pages.
type MultiRecipeResolver struct {
	Registry      *MultiRecipeRegistry
	ImportService *ImportService
}

// NewMultiRecipeResolver creates a new resolver.
func NewMultiRecipeResolver(registry *MultiRecipeRegistry, importService *ImportService) *MultiRecipeResolver {
	return &MultiRecipeResolver{
		Registry:      registry,
		ImportService: importService,
	}
}

// ResolveFromHTML detects and begins resolving a multi-recipe page from fetched HTML.
// Returns the entry if multi-recipe, or nil if single-recipe.
func (r *MultiRecipeResolver) ResolveFromHTML(sourceURL string, html string) *MultiRecipeEntry {
	cards := extractAllJSONLDRecipes(html, sourceURL)
	if len(cards) <= 1 {
		return nil
	}

	entry, isNew := r.Registry.Register(sourceURL)
	if !isNew {
		return entry // already being resolved
	}

	entry.mu.Lock()
	entry.Cards = cards
	entry.mu.Unlock()

	// Start background extraction for each card
	go r.extractAllRecipes(entry)

	return entry
}

// ResolveFromURL fetches a URL and resolves if it's multi-recipe.
// Used for late detection when a search result is clicked.
func (r *MultiRecipeResolver) ResolveFromURL(ctx context.Context, sourceURL string) *MultiRecipeEntry {
	// Check if already tracked
	if existing := r.Registry.Get(sourceURL); existing != nil {
		return existing
	}

	// Fetch the page
	recipeDef, _, html, err := r.ImportService.fetchAndExtractWithHTML(ctx, sourceURL)
	if err != nil || html == "" {
		return nil
	}

	// Check for multiple recipes
	entry := r.ResolveFromHTML(sourceURL, html)
	if entry != nil {
		return entry
	}

	// Single recipe — cache it in canonical if we got a result
	if recipeDef != nil {
		// Not multi-recipe, let normal import flow handle it
		return nil
	}

	return nil
}

// extractAllRecipes runs full extraction for each card in the entry.
func (r *MultiRecipeResolver) extractAllRecipes(entry *MultiRecipeEntry) {
	log := logger.Get().With(zap.String("source_url", entry.SourceURL), zap.String("multi_id", entry.ID))
	log.Info("starting multi-recipe extraction", zap.Int("recipe_count", len(entry.Cards)))

	var wg sync.WaitGroup

	for i := range entry.Cards {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r.extractSingleCard(entry, idx)
		}(i)
	}

	wg.Wait()

	now := time.Now()
	entry.mu.Lock()
	entry.Status = "resolved"
	entry.ResolvedAt = &now
	entry.mu.Unlock()

	log.Info("multi-recipe extraction complete")
}

// extractSingleCard extracts a full recipe for one card in the entry.
func (r *MultiRecipeResolver) extractSingleCard(entry *MultiRecipeEntry, idx int) {
	entry.mu.Lock()
	card := &entry.Cards[idx]
	card.ExtractionStatus = "extracting"
	title := card.Title
	sourceURL := card.SourceURL
	entry.mu.Unlock()

	log := logger.Get().With(zap.String("title", title), zap.String("source_url", sourceURL))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	provider := r.ImportService.PreviewProvider
	if provider == nil {
		provider = r.ImportService.TextProvider
	}
	if provider == nil {
		log.Error("no AI provider available for multi-recipe extraction")
		entry.mu.Lock()
		entry.Cards[idx].ExtractionStatus = "failed"
		entry.mu.Unlock()
		return
	}

	// Ask Claude to extract just this specific recipe by name
	prompt := fmt.Sprintf("Extract ONLY the recipe titled %q from the following page. Ignore all other recipes on the page.", title)
	result, err := provider.ExtractRecipeFromText(ctx, prompt, "preserve source")
	if err != nil {
		log.Error("failed to extract individual recipe", zap.Error(err))
		entry.mu.Lock()
		entry.Cards[idx].ExtractionStatus = "failed"
		entry.mu.Unlock()
		return
	}

	def := recipeResultToRecipeDef(result)
	def.SourceURL = sourceURL

	entry.mu.Lock()
	entry.Cards[idx].ExtractionStatus = "done"
	entry.Cards[idx].RecipeDef = &def
	entry.Cards[idx].Hashtags = result.Hashtags
	if entry.Cards[idx].ImageURL == "" && result.ImagePrompt != "" {
		entry.Cards[idx].Description = result.Summary
	}
	entry.mu.Unlock()

	// Cache in canonical repo
	if r.ImportService.CanonicalRepo != nil {
		if normalizedURL, err := NormalizeURL(sourceURL + "#" + title); err == nil {
			now := time.Now()
			canonical := &models.CanonicalRecipe{
				NormalizedURL:    normalizedURL,
				OriginalURL:      sourceURL,
				RecipeData:       def,
				ExtractionMethod: models.ExtractionHaiku,
				FetchedAt:        now,
				LastAccessedAt:   now,
			}
			if err := r.ImportService.CanonicalRepo.Upsert(canonical); err != nil {
				log.Warn("failed to cache extracted recipe", zap.Error(err))
			}
		}
	}

	log.Info("individual recipe extracted successfully")
}

// PostProcessSearchResults checks search results for multi-recipe titles
// and begins background resolution for any detected.
func (r *MultiRecipeResolver) PostProcessSearchResults(results []ai.SearchResult) []ai.SearchResult {
	for i := range results {
		if IsMultiRecipeTitle(results[i].Title) {
			// Check if already tracked
			if existing := r.Registry.Get(results[i].URL); existing != nil {
				// Already resolving/resolved — mark it
				results[i].IsMulti = true
				results[i].MultiID = existing.ID
				continue
			}

			// Mark as multi — actual resolution happens when we can fetch the page
			results[i].IsMulti = true

			// Register and start background resolution
			entry, isNew := r.Registry.Register(results[i].URL)
			results[i].MultiID = entry.ID
			if isNew {
				go r.resolveInBackground(results[i].URL, entry)
			}
		}
	}
	return results
}

// resolveInBackground fetches a URL and resolves its recipes.
func (r *MultiRecipeResolver) resolveInBackground(sourceURL string, entry *MultiRecipeEntry) {
	log := logger.Get().With(zap.String("url", sourceURL), zap.String("multi_id", entry.ID))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, _, html, err := r.ImportService.fetchAndExtractWithHTML(ctx, sourceURL)
	if err != nil || html == "" {
		log.Warn("failed to fetch multi-recipe page for resolution", zap.Error(err))
		entry.mu.Lock()
		entry.Status = "failed"
		entry.mu.Unlock()
		return
	}

	cards := extractAllJSONLDRecipes(html, sourceURL)
	if len(cards) <= 1 {
		// Not actually multi-recipe — mark as resolved with single/no results
		entry.mu.Lock()
		entry.Status = "resolved"
		now := time.Now()
		entry.ResolvedAt = &now
		entry.Cards = cards
		entry.mu.Unlock()
		return
	}

	entry.mu.Lock()
	entry.Cards = cards
	entry.mu.Unlock()

	r.extractAllRecipes(entry)
}
