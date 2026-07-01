package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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
	Title            string            `json:"title"`
	ImageURL         string            `json:"image_url,omitempty"`
	Description      string            `json:"description,omitempty"`
	SourceURL        string            `json:"source_url"`
	ExtractionStatus string            `json:"extraction_status"` // "pending", "extracting", "done", "failed"
	RecipeDef        *models.RecipeDef `json:"recipe,omitempty"`  // populated when done
	Hashtags         []string          `json:"hashtags,omitempty"`
	// CachedURL is the canonical-cache key under which this card's extracted
	// recipe was stored (its own-page URL for listicles, or the distinct
	// ?_recipe=slug URL for inline recipes). Set once ExtractionStatus is "done";
	// a later preview/import of this URL is an instant cache hit.
	CachedURL string `json:"cached_url,omitempty"`
}

// MultiRecipeEntry tracks the resolution state of a multi-recipe URL.
type MultiRecipeEntry struct {
	mu         sync.RWMutex
	ID         string            `json:"multi_id"`
	SourceURL  string            `json:"source_url"`
	Status     string            `json:"status"` // "resolving", "resolved", "failed"
	Cards      []MultiRecipeCard `json:"recipes"`
	DetectedAt time.Time         `json:"detected_at"`
	ResolvedAt *time.Time        `json:"resolved_at,omitempty"`
	pageHTML   string            // stored for extraction, not serialized
}

func (e *MultiRecipeEntry) GetCards() []MultiRecipeCard {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cards := make([]MultiRecipeCard, len(e.Cards))
	for i, c := range e.Cards {
		cards[i] = c
		// Deep-copy the RecipeDef pointer to avoid races with extractSingleCard
		if c.RecipeDef != nil {
			defCopy := *c.RecipeDef
			cards[i].RecipeDef = &defCopy
		}
	}
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

const registryEvictionTTL = 30 * time.Minute

// NewMultiRecipeRegistry creates a new registry.
func NewMultiRecipeRegistry() *MultiRecipeRegistry {
	r := &MultiRecipeRegistry{
		entries: make(map[string]*MultiRecipeEntry),
	}
	go r.evictionLoop()
	return r
}

// shouldEvictEntry reports whether a registry entry in the given state is
// eligible for eviction at time now. Entries still resolving are never
// evicted; terminal entries (resolved/failed) are evicted once older than
// the TTL.
func shouldEvictEntry(status string, detectedAt time.Time, now time.Time) bool {
	if status != "resolved" && status != "failed" {
		return false
	}
	return now.Sub(detectedAt) > registryEvictionTTL
}

// evictionLoop periodically removes resolved/failed entries older than the TTL.
func (r *MultiRecipeRegistry) evictionLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		// Collect candidates under read lock, then delete under write lock.
		// This avoids holding registry lock while acquiring entry locks.
		r.mu.RLock()
		var toEvict []string
		for url, entry := range r.entries {
			entry.mu.RLock()
			status := entry.Status
			detected := entry.DetectedAt
			entry.mu.RUnlock()
			if shouldEvictEntry(status, detected, time.Now()) {
				toEvict = append(toEvict, url)
			}
		}
		r.mu.RUnlock()

		if len(toEvict) > 0 {
			r.mu.Lock()
			for _, url := range toEvict {
				delete(r.entries, url)
			}
			r.mu.Unlock()
		}
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

// slugStopwords are dropped from titles/slugs before matching so short
// connecting words don't create false link matches.
var slugStopwords = map[string]bool{
	"with": true, "and": true, "the": true, "a": true, "an": true, "in": true,
	"of": true, "for": true, "to": true, "on": true, "or": true, "your": true,
	"my": true, "recipe": true, "recipes": true,
}

// slugTokens lowercases a string and splits it into significant alphanumeric
// tokens, dropping stopwords and single-character tokens.
func slugTokens(s string) []string {
	var tokens []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if len(w) <= 1 || slugStopwords[w] {
			continue
		}
		tokens = append(tokens, w)
	}
	return tokens
}

// linkCandidate is a same-site link with the token set of its final path
// segment, used to match a card title to its individual recipe page.
type linkCandidate struct {
	url    string
	tokens map[string]bool
}

// extractRecipeLinkCandidates returns deduped same-host links from the page (as
// absolute URLs), each with the token set of its final path segment. These are
// matched against card titles so collection/listicle pages (an index of links,
// not inline recipes) resolve each card to its own recipe page.
func extractRecipeLinkCandidates(html, baseURL string) []linkCandidate {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	re := regexp.MustCompile(`(?i)href=["']([^"'<> ]+)["']`)
	matches := re.FindAllStringSubmatch(html, -1)
	seen := make(map[string]bool)
	var out []linkCandidate
	for _, m := range matches {
		ref, err := url.Parse(strings.TrimSpace(m[1]))
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)
		if (abs.Scheme != "http" && abs.Scheme != "https") || abs.Host != base.Host {
			continue
		}
		abs.RawQuery = ""
		abs.Fragment = ""
		clean := strings.TrimRight(abs.String(), "/")
		if clean == "" || seen[clean] {
			continue
		}
		segs := strings.Split(strings.Trim(abs.Path, "/"), "/")
		toks := slugTokens(strings.ReplaceAll(segs[len(segs)-1], "-", " "))
		if len(toks) < 2 {
			continue // path segment too generic to match safely
		}
		seen[clean] = true
		tokenSet := make(map[string]bool, len(toks))
		for _, t := range toks {
			tokenSet[t] = true
		}
		out = append(out, linkCandidate{url: clean, tokens: tokenSet})
	}
	return out
}

// matchRecipeURL finds the individual recipe page whose slug best matches a
// card title. It is deliberately conservative: every significant title token
// must appear in the link slug, and the tightest such link wins (fewest extra
// tokens, capped). Returns ok=false when no confident match exists, so a card
// never resolves to the wrong recipe.
func matchRecipeURL(title string, candidates []linkCandidate) (string, bool) {
	titleToks := slugTokens(title)
	if len(titleToks) < 2 {
		return "", false
	}
	bestURL := ""
	bestExtra := 1 << 30
	for _, c := range candidates {
		allPresent := true
		for _, t := range titleToks {
			if !c.tokens[t] {
				allPresent = false
				break
			}
		}
		if !allPresent {
			continue
		}
		if extra := len(c.tokens) - len(titleToks); extra < bestExtra {
			bestExtra = extra
			bestURL = c.url
		}
	}
	// Reject loose matches where the link slug carries many extra tokens (likely
	// a different, longer recipe that merely contains these words).
	if bestURL == "" || bestExtra > 3 {
		return "", false
	}
	return bestURL, true
}

// assignRecipeURLs points each card at its own recipe page when the source is a
// collection of links rather than inline recipes. Cards without a confident
// match keep the original (page) URL and fall back to inline extraction.
func assignRecipeURLs(cards []MultiRecipeCard, html, sourceURL string) {
	candidates := extractRecipeLinkCandidates(html, sourceURL)
	if len(candidates) == 0 {
		return
	}
	for i := range cards {
		if u, ok := matchRecipeURL(cards[i].Title, candidates); ok {
			cards[i].SourceURL = u
		}
	}
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

// stripHTMLToText extracts visible text from HTML, removing scripts, styles,
// nav elements, and tags. Produces a much smaller input for AI detection
// than raw HTML (~10-20x reduction).
func stripHTMLToText(html string) string {
	// Remove script, style, nav, header, footer blocks entirely
	for _, tag := range []string{"script", "style", "nav", "header", "footer", "noscript", "svg"} {
		re := regexp.MustCompile(`(?is)<` + tag + `[^>]*>.*?</` + tag + `>`)
		html = re.ReplaceAllString(html, " ")
	}

	// Remove HTML comments
	commentRe := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = commentRe.ReplaceAllString(html, " ")

	// Replace block-level tags with newlines to preserve structure
	blockRe := regexp.MustCompile(`(?i)<(?:h[1-6]|p|div|li|tr|br|article|section)[^>]*>`)
	html = blockRe.ReplaceAllString(html, "\n")

	// Remove all remaining tags
	tagRe := regexp.MustCompile(`<[^>]+>`)
	html = tagRe.ReplaceAllString(html, " ")

	// Decode common HTML entities
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")
	html = strings.ReplaceAll(html, "&nbsp;", " ")

	// Collapse whitespace
	spaceRe := regexp.MustCompile(`[ \t]+`)
	html = spaceRe.ReplaceAllString(html, " ")

	// Collapse multiple newlines
	nlRe := regexp.MustCompile(`\n{3,}`)
	html = nlRe.ReplaceAllString(html, "\n\n")

	return strings.TrimSpace(html)
}

// detectMultipleRecipesFromHTML uses AI to detect recipe titles in HTML when
// JSON-LD detection finds zero or one Recipe blocks. Returns individual cards
// if multiple recipes found, nil otherwise.
func detectMultipleRecipesFromHTML(ctx context.Context, provider ai.TextProvider, html string, sourceURL string) []MultiRecipeCard {
	if provider == nil {
		return nil
	}

	// Strip HTML to plain text — reduces input ~10-20x, avoids rate limits
	text := stripHTMLToText(html)

	// Truncate to a reasonable size for detection (not full extraction)
	const maxDetectBytes = 15_000
	if len(text) > maxDetectBytes {
		text = text[:maxDetectBytes]
	}

	prompt := `This page may contain multiple recipes. List ALL distinct recipe titles found on the page.

Rules:
- Only list actual recipes with ingredients/instructions, not article titles or navigation links
- If there is only 0 or 1 recipe, respond with just: SINGLE
- If there are multiple recipes, respond with one title per line, nothing else
- Do not add numbering, bullets, or formatting — just the recipe name per line

Page content:
` + text

	result, err := provider.CookingQA(ctx, prompt, "")
	if err != nil {
		return nil
	}

	// Parse the response
	response := strings.TrimSpace(result)
	if response == "SINGLE" || response == "" {
		return nil
	}

	const maxCards = 20 // cap to prevent runaway extraction from malformed responses
	lines := strings.Split(response, "\n")
	var cards []MultiRecipeCard
	seen := make(map[string]bool)
	for _, line := range lines {
		title := strings.TrimSpace(line)
		// Skip empty lines, "SINGLE", and noisy/too-long lines
		if title == "" || title == "SINGLE" || len(title) < 3 || len(title) > 200 || seen[title] {
			continue
		}
		seen[title] = true
		cards = append(cards, MultiRecipeCard{
			Title:            title,
			SourceURL:        sourceURL,
			ExtractionStatus: "pending",
		})
		if len(cards) >= maxCards {
			break
		}
	}

	if len(cards) <= 1 {
		return nil
	}

	logger.Get().Info("AI detected multiple recipes on page",
		zap.String("url", sourceURL),
		zap.Int("count", len(cards)),
	)
	return cards
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

// detectCards returns the recipe cards found on a page: JSON-LD first (free),
// then an AI fallback when JSON-LD finds 0-1. Detection only — no registration
// or extraction.
func (r *MultiRecipeResolver) detectCards(ctx context.Context, sourceURL string, html string) []MultiRecipeCard {
	cards := extractAllJSONLDRecipes(html, sourceURL)
	if len(cards) <= 1 {
		provider := r.ImportService.PreviewProvider
		if provider == nil {
			provider = r.ImportService.TextProvider
		}
		if aiCards := detectMultipleRecipesFromHTML(ctx, provider, html, sourceURL); aiCards != nil {
			cards = aiCards
		}
	}
	return cards
}

// DetectMultiFromHTML reports whether the page holds multiple recipes, without
// registering or extracting anything. Cache-warming uses this to mark a
// collection page (so it still expands later) without paying to extract every
// sub-recipe.
func (r *MultiRecipeResolver) DetectMultiFromHTML(ctx context.Context, sourceURL string, html string) bool {
	return len(r.detectCards(ctx, sourceURL, html)) > 1
}

// MultiResolver is the finder's seam over the multi-recipe resolver. Digging
// depends only on this interface so the finder's tests can inject a fake and
// stay fully offline.
type MultiResolver interface {
	ResolveFromURLN(ctx context.Context, sourceURL string, maxCards int) *MultiRecipeEntry
}

var _ MultiResolver = (*MultiRecipeResolver)(nil)

// ResolveFromHTML detects and begins resolving a multi-recipe page from fetched HTML.
// Uses JSON-LD detection first, then falls back to AI-based detection.
// Returns the entry if multi-recipe, or nil if single-recipe.
func (r *MultiRecipeResolver) ResolveFromHTML(ctx context.Context, sourceURL string, html string) *MultiRecipeEntry {
	return r.resolveFromHTMLN(ctx, sourceURL, html, 0)
}

// resolveFromHTMLN is ResolveFromHTML with an optional cap on the number of
// cards resolved+extracted. maxCards <= 0 means no cap. Cards are truncated
// before URL assignment, registration and extraction, so a capped resolve never
// kicks off extraction for the dropped cards.
func (r *MultiRecipeResolver) resolveFromHTMLN(ctx context.Context, sourceURL string, html string, maxCards int) *MultiRecipeEntry {
	cards := r.detectCards(ctx, sourceURL, html)
	if len(cards) <= 1 {
		return nil
	}

	if maxCards > 0 && len(cards) > maxCards {
		cards = cards[:maxCards]
	}

	// Point cards at their own recipe pages when this is a collection/listicle
	// of links (so each card extracts from its real recipe page, not the index).
	assignRecipeURLs(cards, html, sourceURL)

	entry, isNew := r.Registry.Register(sourceURL)
	if !isNew {
		return entry // already being resolved
	}

	entry.mu.Lock()
	entry.Cards = cards
	entry.pageHTML = html
	entry.mu.Unlock()

	// Start background extraction for each card
	go r.extractAllRecipes(entry)

	return entry
}

// ResolveFromURL fetches a URL and resolves if it's multi-recipe.
// Used for late detection when a search result is clicked.
func (r *MultiRecipeResolver) ResolveFromURL(ctx context.Context, sourceURL string) *MultiRecipeEntry {
	return r.ResolveFromURLN(ctx, sourceURL, 0)
}

// ResolveFromURLN is ResolveFromURL with a cap on how many cards are resolved
// and extracted (maxCards <= 0 means no cap). The finder's bounded digging uses
// it so a huge listicle never triggers unbounded background extraction.
func (r *MultiRecipeResolver) ResolveFromURLN(ctx context.Context, sourceURL string, maxCards int) *MultiRecipeEntry {
	// Check if already tracked
	if existing := r.Registry.Get(sourceURL); existing != nil {
		return existing
	}

	// Fetch only the HTML — skip AI extraction since we only need
	// the page content for JSON-LD multi-recipe card detection.
	html, err := r.ImportService.fetchHTML(ctx, sourceURL)
	if err != nil || html == "" {
		return nil
	}

	// Check for multiple recipes
	return r.resolveFromHTMLN(ctx, sourceURL, html, maxCards)
}

// extractAllRecipes runs full extraction for each card in the entry.
func (r *MultiRecipeResolver) extractAllRecipes(entry *MultiRecipeEntry) {
	log := logger.Get().With(zap.String("source_url", entry.SourceURL), zap.String("multi_id", entry.ID))
	log.Info("starting multi-recipe extraction", zap.Int("recipe_count", len(entry.Cards)))

	// Limit concurrent LLM requests to avoid rate limits and cost spikes
	const maxConcurrent = 3
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i := range entry.Cards {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release
			r.extractSingleCard(entry, idx)
		}(i)
	}

	wg.Wait()

	now := time.Now()
	entry.mu.Lock()
	entry.Status = "resolved"
	entry.ResolvedAt = &now
	entry.pageHTML = "" // free memory — no longer needed after extraction
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
	pageHTML := entry.pageHTML
	entry.mu.Unlock()

	log := logger.Get().With(zap.String("title", title), zap.String("source_url", sourceURL))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Preferred path: when this card resolved to its own recipe page (a
	// collection/listicle of links, not inline recipes), fetch and extract that
	// page directly — it carries the full recipe, usually via free JSON-LD.
	if sourceURL != "" && sourceURL != entry.SourceURL {
		if def, hashtags, _, ferr := r.ImportService.fetchAndExtractWithHTML(ctx, sourceURL); ferr == nil && def != nil && len(def.Ingredients) > 0 {
			def.SourceURL = sourceURL
			ensureUnitSystem(def)
			entry.mu.Lock()
			entry.Cards[idx].ExtractionStatus = "done"
			entry.Cards[idx].RecipeDef = def
			entry.Cards[idx].Hashtags = hashtags
			// The recipe lives at its own page, so that URL is its cache key.
			entry.Cards[idx].CachedURL = sourceURL
			entry.mu.Unlock()
			if r.ImportService.CanonicalRepo != nil {
				if normalizedURL, nerr := NormalizeURL(sourceURL); nerr == nil {
					now := time.Now()
					if uerr := r.ImportService.CanonicalRepo.Upsert(&models.CanonicalRecipe{
						NormalizedURL:    normalizedURL,
						OriginalURL:      sourceURL,
						RecipeData:       *def,
						ExtractionMethod: models.ExtractionJSONLD,
						FetchedAt:        now,
						LastAccessedAt:   now,
					}); uerr != nil {
						log.Warn("failed to cache extracted recipe", zap.Error(uerr))
					}
				}
			}
			log.Info("extracted recipe from its own page", zap.Int("ingredients", len(def.Ingredients)))
			return
		}
		log.Info("individual recipe page unavailable; falling back to collection text")
	}

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

	// Strip HTML to text and truncate — raw HTML wastes tokens on tags/CSS/JS
	pageText := stripHTMLToText(pageHTML)
	const maxExtractBytes = 30_000
	if len(pageText) > maxExtractBytes {
		pageText = pageText[:maxExtractBytes]
	}

	// Pass the stripped text with a constraint to extract only this recipe by title
	extractionInput := fmt.Sprintf("Extract ONLY the recipe titled %q from the following page. Ignore all other recipes.\n\n%s", title, pageText)
	result, err := provider.ExtractRecipeFromText(ctx, extractionInput, ai.UnitSystemPreserveSource)
	if err != nil {
		log.Error("failed to extract individual recipe", zap.Error(err))
		entry.mu.Lock()
		entry.Cards[idx].ExtractionStatus = "failed"
		entry.mu.Unlock()
		return
	}

	def := recipeResultToRecipeDef(result)
	def.SourceURL = sourceURL
	ensureUnitSystem(&def)

	entry.mu.Lock()
	entry.Cards[idx].ExtractionStatus = "done"
	entry.Cards[idx].RecipeDef = &def
	entry.Cards[idx].Hashtags = result.Hashtags
	if entry.Cards[idx].ImageURL == "" && result.ImagePrompt != "" {
		entry.Cards[idx].Description = result.Summary
	}
	entry.mu.Unlock()

	// Cache in canonical repo with a distinct key per card.
	// NormalizeURL strips fragments, so we append a slug query param instead.
	if r.ImportService.CanonicalRepo != nil {
		slug := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			if r >= 'A' && r <= 'Z' {
				return r + 32 // lowercase
			}
			if r == ' ' {
				return '-'
			}
			return -1 // drop
		}, title)
		// Append card index for collision-proofing — different titles can
		// collapse to the same slug (punctuation/spacing variants).
		// Also handles non-ASCII titles that produce empty slugs.
		slug = fmt.Sprintf("%s-%d", slug, idx)
		separator := "?"
		if strings.Contains(sourceURL, "?") {
			separator = "&"
		}
		distinctURL := sourceURL + separator + "_recipe=" + slug
		// This inline recipe only exists in our cache under the distinct key.
		entry.mu.Lock()
		entry.Cards[idx].CachedURL = distinctURL
		entry.mu.Unlock()
		if normalizedURL, err := NormalizeURL(distinctURL); err == nil {
			now := time.Now()
			canonical := &models.CanonicalRecipe{
				NormalizedURL:    normalizedURL,
				OriginalURL:      distinctURL, // use distinct URL so refresh extracts this specific recipe
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
