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
	"github.com/windoze95/saltybytes-api/internal/units"
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
	SpeechProvider  ai.SpeechProvider
	PreviewProvider ai.TextProvider
	CanonicalRepo   repository.CanonicalRecipeRepo
	Policy          *ImportPolicy
	Normalize       *NormalizeService // optional; estimates portions when imports lack them
	// Events (nil-safe) persists terminal extraction outcomes for the
	// dashboard's completeness panel + failure drill-downs.
	Events repository.ExtractionEventRepo

	// Video-link import (premium). All three are nil until a ScrapeCreators key
	// is configured at startup, which keeps the feature dark by default.
	VideoRepo         repository.VideoImportRepo
	VideoFetcher      VideoFetcher
	VideoFrameSampler VideoFrameSampler
	// VideoProvider, when set (VIDEO_NATIVE_GEMINI enabled), extracts recipes by
	// ingesting the whole video natively (video+audio) instead of sampling frames
	// onto the vision provider. Falls back to VideoFrameSampler per-video on any
	// error or an oversized clip. Optional; nil keeps the frames path.
	VideoProvider ai.VideoProvider
	// SubService refunds the per-user video quota when an accepted import later
	// fails on our side. Optional; nil disables refunds (e.g. in tests).
	SubService *SubscriptionService
	// ThumbnailUploader stores a representative video frame and returns its URL.
	// Optional test seam; nil uses the default S3 uploader.
	ThumbnailUploader func(ctx context.Context, frameJPEG []byte, videoKey string) (string, error)

	// Test seams — nil in production, set in tests to bypass real HTTP/Firecrawl calls
	HTTPFetchOverride      func(ctx context.Context, url string) (body []byte, statusCode int, err error)
	FirecrawlFetchOverride func(ctx context.Context, url string) (html string, statusCode int, err error)
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
		Policy:          NewImportPolicy(),
	}
}

// ValidateExternalURL checks that a user-supplied URL is safe to fetch.
// It blocks private/internal IPs and non-HTTP(S) schemes to prevent SSRF.
// ValidateExternalURL checks that a user-supplied URL is safe to fetch.
// It blocks private/internal IPs and non-HTTP(S) schemes to prevent SSRF.
func ValidateExternalURL(rawURL string) error {
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
		return ValidateExternalURL(req.URL.String())
	},
}

// canonicalEmbedding generates an embedding literal for a canonical recipe
// from its title and ingredient names. Best-effort: returns nil when no
// embedding provider is configured or generation fails.
func (s *ImportService) canonicalEmbedding(ctx context.Context, recipeDef *models.RecipeDef) *string {
	if s.RecipeService == nil || s.RecipeService.EmbedProvider == nil {
		return nil
	}

	embedding, err := s.RecipeService.EmbedProvider.GenerateEmbedding(ctx, embeddingText(recipeDef))
	if err != nil {
		logger.Get().Warn("failed to generate canonical embedding", zap.String("title", recipeDef.Title), zap.Error(err))
		return nil
	}

	literal := repository.PgvectorLiteral(embedding)
	return &literal
}

// ImportFromURL fetches a page, tries JSON-LD extraction first, falls back to AI.
// When a CanonicalRepo is configured, it checks the canonical cache first and
// saves extractions for future deduplication.
func (s *ImportService) ImportFromURL(ctx context.Context, rawURL string, user *models.User) (*RecipeResponse, error) {
	ctx = WithExtractionOrigin(ctx, ExtractionOriginImport)
	log := logger.Get().With(zap.Uint("user_id", user.ID), zap.String("source_url", rawURL))

	if err := ValidateExternalURL(rawURL); err != nil {
		return nil, fmt.Errorf("URL validation failed: %w", err)
	}

	// Serve from the canonical cache when present. Entries never expire; a
	// cached URL is never automatically re-fetched.
	if s.CanonicalRepo != nil {
		normalizedURL, normErr := NormalizeURL(rawURL)
		if normErr == nil {
			if canonical, err := s.CanonicalRepo.GetByNormalizedURL(normalizedURL); err == nil && !canonical.IsMultiPage {
				log.Info("import from canonical cache hit")
				go s.CanonicalRepo.IncrementHitCount(canonical.ID)
				canonicalID := canonical.ID
				recipeResp, _, createErr := s.createImportedRecipe(ctx, &canonical.RecipeData, user, models.RecipeTypeImportLink, rawURL, "", &canonicalID, nil, canonical.PromptVersion)
				return recipeResp, createErr
			}
		}
	}

	recipeDef, hashtags, imageURL, method, promptVersion, err := s.extractFromURL(ctx, rawURL)
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
				PromptVersion:    promptVersion,
				Embedding:        s.canonicalEmbedding(ctx, recipeDef),
			}
			if upsertErr := s.CanonicalRepo.Upsert(entry); upsertErr == nil {
				canonicalID = &entry.ID
			} else {
				log.Warn("failed to upsert canonical", zap.Error(upsertErr))
			}
		}
	}

	recipeResp, _, createErr := s.createImportedRecipe(ctx, recipeDef, user, models.RecipeTypeImportLink, rawURL, imageURL, canonicalID, hashtags, promptVersion)
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
	resp, _, createErr := s.createImportedRecipe(ctx, &canonical.RecipeData, user, models.RecipeTypeImportLink, canonical.OriginalURL, "", &cID, nil, canonical.PromptVersion)
	return resp, createErr
}

// isBotBlockStatus returns true for HTTP status codes that indicate bot protection.
func isBotBlockStatus(code int) bool {
	return code == http.StatusPaymentRequired || // 402
		code == http.StatusForbidden || // 403
		code == http.StatusTooManyRequests || // 429
		code == http.StatusServiceUnavailable // 503
}

// looksJSRendered reports whether a 200 body is almost certainly a client-rendered
// shell that needs a headless render (Firecrawl): it carries no JSON-LD structured
// data AND has very little visible text. A page with JSON-LD, or with substantial
// prose, is left alone.
func looksJSRendered(body []byte) bool {
	s := string(body)
	if strings.Contains(s, "application/ld+json") {
		return false
	}
	return len(stripHTMLToText(s)) < 600
}

// firecrawlAvailable reports whether a Firecrawl escalation can actually run.
func (s *ImportService) firecrawlAvailable() bool {
	return s.FirecrawlFetchOverride != nil || (s.Cfg != nil && s.Cfg.EnvVars.FirecrawlAPIKey != "")
}

// maybeFirecrawlThinBody escalates a 200 body that looks like an un-rendered JS
// shell to Firecrawl, which renders the page. It only fires when Firecrawl is
// configured (so a missing key leaves behaviour unchanged), and on Firecrawl
// failure it keeps the original body rather than erroring — a thin 200 is still
// better than a hard failure.
func (s *ImportService) maybeFirecrawlThinBody(ctx context.Context, rawURL string, body []byte) string {
	if !s.firecrawlAvailable() || !looksJSRendered(body) {
		return string(body)
	}
	if s.Policy != nil {
		s.Policy.RecordDirectFetchBlocked(rawURL)
	}
	html, _, err := s.fetchViaFirecrawl(ctx, rawURL)
	if err != nil || html == "" {
		return string(body)
	}
	return html
}

// isCloudflareChallenge checks if HTML content is a Cloudflare challenge page.
func isCloudflareChallenge(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "<title>Just a moment...</title>") ||
		strings.Contains(s, "challenge-platform")
}

// fetchViaFirecrawl scrapes a URL using the Firecrawl API as a fallback
// when direct HTTP fetch is blocked by bot protection.
func (s *ImportService) fetchViaFirecrawl(ctx context.Context, rawURL string) (string, int, error) {
	if s.FirecrawlFetchOverride != nil {
		return s.FirecrawlFetchOverride(ctx, rawURL)
	}

	apiKey := s.Cfg.EnvVars.FirecrawlAPIKey
	if apiKey == "" {
		return "", 0, fmt.Errorf("firecrawl API key not configured")
	}

	payload, err := json.Marshal(map[string]interface{}{
		"url":     rawURL,
		"formats": []string{"html"},
	})
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal firecrawl request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.firecrawl.dev/v1/scrape", strings.NewReader(string(payload)))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create firecrawl request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("firecrawl request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", 0, fmt.Errorf("failed to read firecrawl response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("firecrawl returned status %d", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			HTML     string `json:"html"`
			Metadata struct {
				StatusCode int `json:"statusCode"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse firecrawl response: %w", err)
	}
	if !result.Success || result.Data.HTML == "" {
		return "", 0, fmt.Errorf("firecrawl returned no HTML")
	}

	return result.Data.HTML, result.Data.Metadata.StatusCode, nil
}

// extractFromURL fetches a URL and extracts recipe data via JSON-LD or AI fallback.
// Returns the recipe definition, raw hashtag strings, the page's recipe image
// URL (when available), the extraction method used, and the prompt version.
// Shared by PreviewFromURL, ImportFromURL, and background refresh. It wraps
// the pipeline with telemetry: exactly one ExtractionEvent per attempt,
// success or failure, with the terminal method, error class and duration.
func (s *ImportService) extractFromURL(ctx context.Context, rawURL string) (*models.RecipeDef, []string, string, models.ExtractionMethod, string, error) {
	start := time.Now()
	def, hashtags, imageURL, method, promptVersion, err := s.extractFromURLInner(ctx, rawURL)

	usedFirecrawl := method == models.ExtractionFirecrawlJSONLD || method == models.ExtractionFirecrawlHaiku
	errCode := extractionErrCode(err)
	// site_blocked means the direct fetch was blocked AND the Firecrawl
	// escalation failed too — firecrawl was in play even though no method
	// survived to say so.
	if errCode == "site_blocked" {
		usedFirecrawl = true
	}
	s.recordExtraction(ctx, models.ExtractionEvent{
		URL:           rawURL,
		Method:        string(method),
		Success:       err == nil,
		ErrorCode:     errCode,
		Error:         truncateErr(err),
		UsedFirecrawl: usedFirecrawl,
		DurationMS:    time.Since(start).Milliseconds(),
	})
	return def, hashtags, imageURL, method, promptVersion, err
}

func (s *ImportService) extractFromURLInner(ctx context.Context, rawURL string) (*models.RecipeDef, []string, string, models.ExtractionMethod, string, error) {
	log := logger.Get().With(zap.String("url", rawURL))

	// Check if this domain is known to block direct fetches
	skipDirectFetch := s.Policy != nil && s.Policy.ShouldSkipDirectFetch(rawURL)

	// Phase 1: Fetch HTML (with Firecrawl fallback for blocked sites)
	var html string
	var usedFirecrawl bool

	if skipDirectFetch {
		log.Info("skipping direct fetch for known-blocking domain, using firecrawl")
		fcHTML, fcStatus, fcErr := s.fetchViaFirecrawl(ctx, rawURL)
		if fcErr != nil {
			log.Warn("firecrawl fallback failed for known-blocking domain", zap.Error(fcErr))
			return nil, nil, "", "", "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
		}
		if fcStatus == http.StatusNotFound {
			return nil, nil, "", "", "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
		}
		html = fcHTML
		usedFirecrawl = true
	} else if s.HTTPFetchOverride != nil {
		body, statusCode, err := s.HTTPFetchOverride(ctx, rawURL)
		if err != nil {
			return nil, nil, "", "", "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to fetch URL: %v", err)}
		}
		if statusCode == http.StatusNotFound {
			return nil, nil, "", "", "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
		}
		if isBotBlockStatus(statusCode) || isCloudflareChallenge(body) {
			log.Info("direct fetch blocked, trying firecrawl", zap.Int("status", statusCode))
			if s.Policy != nil {
				s.Policy.RecordDirectFetchBlocked(rawURL)
			}
			fcHTML, _, fcErr := s.fetchViaFirecrawl(ctx, rawURL)
			if fcErr != nil {
				log.Warn("firecrawl fallback failed", zap.Error(fcErr))
				return nil, nil, "", "", "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
			}
			html = fcHTML
			usedFirecrawl = true
		} else if statusCode != http.StatusOK {
			return nil, nil, "", "", "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("URL returned status %d", statusCode)}
		} else {
			html = string(body)
		}
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, "", "", "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to create request: %v", err)}
		}
		req.Header.Set("User-Agent", defaultUserAgent)

		resp, err := safeHTTPClient.Do(req)
		if err != nil {
			return nil, nil, "", "", "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to fetch URL: %v", err)}
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if err != nil {
			return nil, nil, "", "", "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to read URL body: %v", err)}
		}

		if resp.StatusCode == http.StatusNotFound {
			return nil, nil, "", "", "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
		}

		if isBotBlockStatus(resp.StatusCode) || isCloudflareChallenge(body) {
			log.Info("direct fetch blocked, trying firecrawl", zap.Int("status", resp.StatusCode))
			if s.Policy != nil {
				s.Policy.RecordDirectFetchBlocked(rawURL)
			}
			fcHTML, _, fcErr := s.fetchViaFirecrawl(ctx, rawURL)
			if fcErr != nil {
				log.Warn("firecrawl fallback failed", zap.Error(fcErr))
				return nil, nil, "", "", "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
			}
			html = fcHTML
			usedFirecrawl = true
		} else if resp.StatusCode != http.StatusOK {
			return nil, nil, "", "", "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("URL returned status %d", resp.StatusCode)}
		} else {
			html = string(body)
		}
	}

	// Phase 2: Extract recipe from HTML
	recipeDef, hashtags, imageURL, jsonLDErr := extractJSONLD(html)
	if jsonLDErr == nil && recipeDef != nil {
		recipeDef.SourceURL = rawURL
		method := models.ExtractionJSONLD
		if usedFirecrawl {
			method = models.ExtractionFirecrawlJSONLD
		}
		if s.Policy != nil {
			s.Policy.RecordOutcome(rawURL, method, true)
		}
		return recipeDef, hashtags, imageURL, method, "", nil
	}

	// Fall back to AI extraction
	provider := s.PreviewProvider
	if provider == nil {
		provider = s.TextProvider
	}
	if provider == nil {
		return nil, nil, "", "", "", fmt.Errorf("no AI text provider configured for fallback extraction")
	}

	result, err := provider.ExtractRecipeFromText(ctx, html, ai.UnitSystemPreserveSource)
	if err != nil {
		method := models.ExtractionHaiku
		if usedFirecrawl {
			method = models.ExtractionFirecrawlHaiku
		}
		if s.Policy != nil {
			s.Policy.RecordOutcome(rawURL, method, false)
		}
		return nil, nil, "", "", "", fmt.Errorf("failed to extract recipe from URL: %w", err)
	}

	def := recipeResultToRecipeDef(result)
	def.SourceURL = rawURL
	ensureUnitSystem(&def)
	method := models.ExtractionHaiku
	if usedFirecrawl {
		method = models.ExtractionFirecrawlHaiku
	}
	if s.Policy != nil {
		s.Policy.RecordOutcome(rawURL, method, true)
	}
	return &def, result.Hashtags, "", method, result.PromptVersion, nil
}

// fetchAndExtractWithHTML fetches a URL once and returns both the extracted
// recipe and the raw HTML. Unlike calling extractFromURL + fetchHTML separately,
// this avoids a double fetch.
func (s *ImportService) fetchAndExtractWithHTML(ctx context.Context, rawURL string) (*models.RecipeDef, []string, string, error) {
	html, err := s.fetchHTML(ctx, rawURL)
	if err != nil {
		return nil, nil, "", err
	}

	recipeDef, hashtags, _, method := s.extractRecipeFromHTML(html, rawURL)
	if recipeDef != nil {
		if s.Policy != nil {
			s.Policy.RecordOutcome(rawURL, method, true)
		}
		return recipeDef, hashtags, html, nil
	}

	// JSON-LD failed — try AI extraction
	provider := s.PreviewProvider
	if provider == nil {
		provider = s.TextProvider
	}
	if provider == nil {
		// No AI provider, but we still have HTML for multi-recipe detection
		return nil, nil, html, nil
	}

	result, aiErr := provider.ExtractRecipeFromText(ctx, html, ai.UnitSystemPreserveSource)
	if aiErr != nil {
		// AI extraction failed but HTML is available for card detection
		return nil, nil, html, nil
	}

	def := recipeResultToRecipeDef(result)
	def.SourceURL = rawURL
	ensureUnitSystem(&def)
	return &def, result.Hashtags, html, nil
}

// extractRecipeFromHTML attempts JSON-LD extraction from already-fetched HTML.
// Returns nil recipe if no JSON-LD recipe found. The third return value is the
// recipe image URL from the JSON-LD payload, when present.
func (s *ImportService) extractRecipeFromHTML(html string, rawURL string) (*models.RecipeDef, []string, string, models.ExtractionMethod) {
	recipeDef, hashtags, imageURL, err := extractJSONLD(html)
	if err != nil || recipeDef == nil {
		return nil, nil, "", ""
	}
	recipeDef.SourceURL = rawURL
	return recipeDef, hashtags, imageURL, models.ExtractionJSONLD
}

// fetchHTML fetches the raw HTML of a URL, using Firecrawl fallback if needed.
func (s *ImportService) fetchHTML(ctx context.Context, rawURL string) (string, error) {
	skipDirectFetch := s.Policy != nil && s.Policy.ShouldSkipDirectFetch(rawURL)

	if skipDirectFetch {
		html, fcStatus, err := s.fetchViaFirecrawl(ctx, rawURL)
		if err != nil {
			return "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
		}
		if fcStatus == http.StatusNotFound {
			return "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
		}
		return html, nil
	}

	if s.HTTPFetchOverride != nil {
		body, statusCode, err := s.HTTPFetchOverride(ctx, rawURL)
		if err != nil {
			return "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to fetch URL: %v", err)}
		}
		if statusCode == http.StatusNotFound {
			return "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
		}
		if isBotBlockStatus(statusCode) || isCloudflareChallenge(body) {
			if s.Policy != nil {
				s.Policy.RecordDirectFetchBlocked(rawURL)
			}
			html, _, err := s.fetchViaFirecrawl(ctx, rawURL)
			if err != nil {
				return "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
			}
			return html, nil
		}
		if statusCode != http.StatusOK {
			return "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("URL returned status %d", statusCode)}
		}
		return s.maybeFirecrawlThinBody(ctx, rawURL, body), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := safeHTTPClient.Do(req)
	if err != nil {
		return "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to fetch URL: %v", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("failed to read URL body: %v", err)}
	}

	if resp.StatusCode == http.StatusNotFound {
		return "", &ExtractionError{Code: "not_found", Message: "recipe page not found"}
	}

	if isBotBlockStatus(resp.StatusCode) || isCloudflareChallenge(body) {
		if s.Policy != nil {
			s.Policy.RecordDirectFetchBlocked(rawURL)
		}
		html, _, fcErr := s.fetchViaFirecrawl(ctx, rawURL)
		if fcErr != nil {
			return "", &ExtractionError{Code: "site_blocked", Message: "this website blocks automated access"}
		}
		return html, nil
	}

	if resp.StatusCode != http.StatusOK {
		return "", &ExtractionError{Code: "fetch_failed", Message: fmt.Sprintf("URL returned status %d", resp.StatusCode)}
	}

	// 200, but a JS-shell body (no structured data + thin text) escalates to
	// Firecrawl, which renders the page. Real content is returned as-is.
	return s.maybeFirecrawlThinBody(ctx, rawURL, body), nil
}

// FileInput is a single uploaded file (image or PDF) for multi-source import.
type FileInput struct {
	Data []byte
}

// maxRecipesPerFileImport bounds how many recipes one multi-file import yields.
const maxRecipesPerFileImport = 20

// detectMediaKind classifies uploaded bytes as an image or PDF via magic bytes.
// ok is false for unsupported content.
func detectMediaKind(data []byte) (kind ai.MediaKind, ok bool) {
	switch {
	case len(data) >= 4 && data[0] == 0x25 && data[1] == 0x50 && data[2] == 0x44 && data[3] == 0x46: // %PDF
		return ai.MediaPDF, true
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF: // JPEG
		return ai.MediaImage, true
	case len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47: // PNG
		return ai.MediaImage, true
	case len(data) >= 3 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46: // GIF
		return ai.MediaImage, true
	case len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50: // WEBP
		return ai.MediaImage, true
	}
	return "", false
}

// ImportFromFiles extracts every recipe found across the provided files (images
// and/or PDFs) and saves each as a recipe for the user.
func (s *ImportService) ImportFromFiles(ctx context.Context, files []FileInput, user *models.User) ([]*RecipeResponse, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID), zap.Int("file_count", len(files)))

	if s.VisionProvider == nil {
		return nil, fmt.Errorf("no vision provider configured")
	}
	if len(files) == 0 {
		return nil, &ExtractionError{Code: "no_files", Message: "no files provided"}
	}

	media := make([]ai.MediaInput, 0, len(files))
	for _, f := range files {
		kind, ok := detectMediaKind(f.Data)
		if !ok {
			return nil, &ExtractionError{Code: "unsupported_file", Message: "unsupported file type; provide images (JPEG, PNG, GIF, WebP) or PDF documents"}
		}
		media = append(media, ai.MediaInput{Data: f.Data, Kind: kind})
	}

	unitSystem := user.Personalization.UnitSystemText()
	requirements := user.Personalization.Requirements

	results, err := s.VisionProvider.ExtractRecipesFromMedia(ctx, media, "", unitSystem, requirements)
	if err != nil {
		log.Error("multi-file extraction failed", zap.Error(err))
		return nil, &ExtractionError{Code: "extraction_failed", Message: "could not extract recipes from the provided files"}
	}

	if len(results) > maxRecipesPerFileImport {
		log.Warn("capping extracted recipes", zap.Int("found", len(results)), zap.Int("cap", maxRecipesPerFileImport))
		results = results[:maxRecipesPerFileImport]
	}

	responses := make([]*RecipeResponse, 0, len(results))
	for _, result := range results {
		def := recipeResultToRecipeDef(result)
		ensureUnitSystem(&def)
		resp, _, createErr := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportVision, "", "", nil, result.Hashtags, result.PromptVersion)
		if createErr != nil {
			log.Error("failed to create recipe from file import", zap.String("title", def.Title), zap.Error(createErr))
			continue
		}
		responses = append(responses, resp)
	}

	if len(responses) == 0 {
		return nil, &ExtractionError{Code: "extraction_failed", Message: "could not extract any recipes from the provided files"}
	}
	return responses, nil
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
	ensureUnitSystem(&def)

	// Create the recipe first to get an ID for S3 upload
	recipeResponse, recipeID, err := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportVision, "", "", nil, result.Hashtags, result.PromptVersion)
	if err != nil {
		return nil, err
	}

	// Upload original image to S3
	s3Key := fmt.Sprintf("recipes/%d/images/original_import.jpg", recipeID)
	imageURL, err := s3.UploadRecipeImageToS3(ctx, s.Cfg, imageData, s3Key, "image/jpeg")
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
	ensureUnitSystem(&def)
	resp, _, err := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportCopypasta, "", "", nil, result.Hashtags, result.PromptVersion)
	return resp, err
}

// ImportFromVoice transcribes spoken audio and extracts a recipe from the
// transcript, then saves it.
func (s *ImportService) ImportFromVoice(ctx context.Context, audioData []byte, format string, user *models.User) (*RecipeResponse, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID))

	if s.SpeechProvider == nil {
		return nil, fmt.Errorf("no speech provider configured")
	}
	if s.TextProvider == nil {
		return nil, fmt.Errorf("no AI text provider configured")
	}

	transcript, err := s.SpeechProvider.TranscribeAudio(ctx, audioData, format)
	if err != nil {
		log.Error("voice transcription failed", zap.Error(err))
		return nil, &ExtractionError{Code: "transcription_failed", Message: "could not transcribe the audio"}
	}
	if strings.TrimSpace(transcript) == "" {
		return nil, &ExtractionError{Code: "empty_transcript", Message: "no speech detected in the audio"}
	}

	unitSystem := user.Personalization.UnitSystemText()
	result, err := s.TextProvider.ExtractRecipeFromText(ctx, transcript, unitSystem)
	if err != nil {
		log.Error("voice text extraction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to extract recipe from transcript: %w", err)
	}

	def := recipeResultToRecipeDef(result)
	ensureUnitSystem(&def)
	resp, _, err := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportCopypasta, "", "", nil, result.Hashtags, result.PromptVersion)
	return resp, err
}

// ImportManual creates a recipe from structured form input.
// imageURL, when non-empty, is stored as the recipe's image.
func (s *ImportService) ImportManual(ctx context.Context, recipeDef *models.RecipeDef, user *models.User, recipeType models.RecipeType, hashtags []string, imageURL string) (*RecipeResponse, error) {
	resp, _, err := s.createImportedRecipe(ctx, recipeDef, user, recipeType, "", imageURL, nil, hashtags, "")
	return resp, err
}

// PreviewResult holds the result of a URL preview, which may be a single
// recipe or a multi-recipe page that needs resolution.
type PreviewResult struct {
	Recipe      *models.RecipeDef `json:"recipe,omitempty"`
	CanonicalID *uint             `json:"canonical_id,omitempty"`
	IsMulti     bool              `json:"is_multi"`
	MultiID     string            `json:"multi_id,omitempty"`
	MultiCards  []MultiRecipeCard `json:"recipes,omitempty"`
	// FromCache is true when the recipe was served from the canonical cache
	// (an instant load), so the client can show "loading saved recipe" rather
	// than an "extracting" state.
	FromCache bool `json:"from_cache,omitempty"`
}

// PreviewFromURL fetches a page and extracts recipe data without saving.
// When a CanonicalRepo is configured, it checks the cache first and saves
// extractions for future deduplication. Returns the recipe data and optional canonical ID.
func (s *ImportService) PreviewFromURL(ctx context.Context, rawURL string) (*models.RecipeDef, *uint, error) {
	ctx = WithExtractionOrigin(ctx, ExtractionOriginPreview)
	log := logger.Get().With(zap.String("source_url", rawURL))

	if err := ValidateExternalURL(rawURL); err != nil {
		return nil, nil, fmt.Errorf("URL validation failed: %w", err)
	}

	// Serve from the canonical cache when present. Entries never expire.
	if s.CanonicalRepo != nil {
		normalizedURL, normErr := NormalizeURL(rawURL)
		if normErr == nil {
			if canonical, err := s.CanonicalRepo.GetByNormalizedURL(normalizedURL); err == nil && !canonical.IsMultiPage {
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

	recipeDef, _, _, method, promptVersion, err := s.extractFromURL(ctx, rawURL)
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
				PromptVersion:    promptVersion,
				Embedding:        s.canonicalEmbedding(ctx, recipeDef),
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

// WarmURL extracts and caches a single recipe URL proactively (cache warming),
// so a later preview/import is an instant cache hit. It is safe against
// collection pages: if the URL is a multi-recipe page it is only marked
// IsMultiPage (so it still expands later) rather than extracting every
// sub-recipe. JSON-LD is used first (free); AI fills the gap when a page has no
// structured data. The caller is expected to have already skipped cached and
// in-flight URLs.
func (s *ImportService) WarmURL(ctx context.Context, resolver *MultiRecipeResolver, rawURL string) error {
	ctx = WithExtractionOrigin(ctx, ExtractionOriginWarm)
	if err := ValidateExternalURL(rawURL); err != nil {
		return err
	}
	normalizedURL, err := NormalizeURL(rawURL)
	if err != nil {
		return err
	}
	// Re-check the cache (a concurrent warm/import may have filled it).
	if _, err := s.CanonicalRepo.GetByNormalizedURL(normalizedURL); err == nil {
		return nil
	}

	start := time.Now()
	html, err := s.fetchHTML(ctx, rawURL)
	if err != nil {
		s.recordExtraction(ctx, models.ExtractionEvent{
			URL:        rawURL,
			Success:    false,
			ErrorCode:  extractionErrCode(err),
			Error:      truncateErr(err),
			DurationMS: time.Since(start).Milliseconds(),
		})
		return err
	}

	now := time.Now()
	markMulti := func() error {
		s.recordExtraction(ctx, models.ExtractionEvent{
			URL:        rawURL,
			Method:     "multi_marked",
			Success:    true,
			DurationMS: time.Since(start).Milliseconds(),
		})
		return s.CanonicalRepo.Upsert(&models.CanonicalRecipe{
			NormalizedURL:    normalizedURL,
			OriginalURL:      rawURL,
			IsMultiPage:      true,
			RecipeData:       models.RecipeDef{},
			ExtractionMethod: models.ExtractionJSONLD,
			FetchedAt:        now,
			LastAccessedAt:   now,
		})
	}

	// A JSON-LD listicle (multiple recipes in structured data)? Mark it and stop
	// — free, and never warm every sub-recipe.
	if len(extractAllJSONLDRecipes(html, rawURL)) > 1 {
		return markMulti()
	}

	// A single JSON-LD recipe is the common, free case — cache it without AI.
	recipeDef, _, _, method := s.extractRecipeFromHTML(html, rawURL)
	if recipeDef == nil {
		// No structured data. Only here do we spend AI: first confirm it isn't a
		// link-style collection (so a listicle isn't mis-cached as one recipe),
		// then extract.
		if resolver != nil && resolver.DetectMultiFromHTML(ctx, rawURL, html) {
			return markMulti()
		}
		provider := s.PreviewProvider
		if provider == nil {
			provider = s.TextProvider
		}
		if provider == nil {
			s.recordExtraction(ctx, models.ExtractionEvent{
				URL:        rawURL,
				Success:    false,
				ErrorCode:  "no_provider",
				Error:      "no text provider configured for warming",
				DurationMS: time.Since(start).Milliseconds(),
			})
			return fmt.Errorf("no text provider configured for warming")
		}
		result, aiErr := provider.ExtractRecipeFromText(ctx, html, ai.UnitSystemPreserveSource)
		if aiErr != nil {
			s.recordExtraction(ctx, models.ExtractionEvent{
				URL:        rawURL,
				Method:     string(models.ExtractionHaiku),
				Success:    false,
				ErrorCode:  extractionErrCode(aiErr),
				Error:      truncateErr(aiErr),
				DurationMS: time.Since(start).Milliseconds(),
				Context:    models.ExtractionContext{"html_len": len(html)},
			})
			return aiErr
		}
		def := recipeResultToRecipeDef(result)
		def.SourceURL = rawURL
		ensureUnitSystem(&def)
		recipeDef = &def
		method = models.ExtractionHaiku
	}

	s.recordExtraction(ctx, models.ExtractionEvent{
		URL:        rawURL,
		Method:     string(method),
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
	})
	return s.CanonicalRepo.Upsert(&models.CanonicalRecipe{
		NormalizedURL:    normalizedURL,
		OriginalURL:      rawURL,
		RecipeData:       *recipeDef,
		ExtractionMethod: method,
		FetchedAt:        now,
		LastAccessedAt:   now,
		Embedding:        s.canonicalEmbedding(ctx, recipeDef),
	})
}

// PreviewFromURLWithMultiCheck is like PreviewFromURL but also detects
// multi-recipe pages. When multiple JSON-LD recipes are found, it returns
// a PreviewResult with IsMulti=true and individual cards instead of
// extracting only the first recipe. The resolver handles background
// extraction of each individual recipe.
func (s *ImportService) PreviewFromURLWithMultiCheck(ctx context.Context, rawURL string, resolver *MultiRecipeResolver) (*PreviewResult, error) {
	ctx = WithExtractionOrigin(ctx, ExtractionOriginPreview)
	start := time.Now()
	log := logger.Get().With(zap.String("source_url", rawURL))

	if err := ValidateExternalURL(rawURL); err != nil {
		return nil, fmt.Errorf("URL validation failed: %w", err)
	}

	// Check if already tracked as multi-recipe
	if resolver != nil {
		if existing := resolver.Registry.Get(rawURL); existing != nil {
			return &PreviewResult{
				IsMulti:    true,
				MultiID:    existing.ID,
				MultiCards: existing.GetCards(),
			}, nil
		}
	}

	// Serve a previously-extracted single recipe straight from the cache (an
	// instant load — no re-extraction). The IsMultiPage marker keeps
	// collection/listicle URLs out of this fast path so they still expand into
	// their individual recipes.
	if s.CanonicalRepo != nil {
		if normalizedURL, normErr := NormalizeURL(rawURL); normErr == nil {
			if canonical, err := s.CanonicalRepo.GetByNormalizedURL(normalizedURL); err == nil && !canonical.IsMultiPage {
				log.Info("preview canonical cache hit")
				go s.CanonicalRepo.IncrementHitCount(canonical.ID)
				data := canonical.RecipeData
				if data.SourceURL == "" {
					data.SourceURL = rawURL
				}
				canonicalID := canonical.ID
				return &PreviewResult{Recipe: &data, CanonicalID: &canonicalID, FromCache: true}, nil
			}
		}
	}

	// Fetch HTML once and reuse for both multi-recipe detection and extraction
	html, err := s.fetchHTML(ctx, rawURL)
	if err != nil {
		log.Error("failed to fetch page", zap.Error(err))
		s.recordExtraction(ctx, models.ExtractionEvent{
			URL:        rawURL,
			Success:    false,
			ErrorCode:  extractionErrCode(err),
			Error:      truncateErr(err),
			DurationMS: time.Since(start).Milliseconds(),
		})
		return nil, err // pass through ExtractionError (not_found, site_blocked, etc.)
	}

	// Check for multiple recipes (JSON-LD first, then AI fallback)
	if resolver != nil {
		entry := resolver.ResolveFromHTML(ctx, rawURL, html)
		if entry != nil {
			// Durably mark this URL as a collection so future previews skip the
			// single-recipe cache and re-expand it (the in-memory registry is
			// lost on restart).
			if s.CanonicalRepo != nil {
				if normalizedURL, normErr := NormalizeURL(rawURL); normErr == nil {
					now := time.Now()
					if err := s.CanonicalRepo.Upsert(&models.CanonicalRecipe{
						NormalizedURL:    normalizedURL,
						OriginalURL:      rawURL,
						IsMultiPage:      true,
						RecipeData:       models.RecipeDef{},
						ExtractionMethod: models.ExtractionJSONLD,
						FetchedAt:        now,
						LastAccessedAt:   now,
					}); err != nil {
						log.Warn("failed to mark multi-recipe page", zap.Error(err))
					}
				}
			}
			s.recordExtraction(ctx, models.ExtractionEvent{
				URL:        rawURL,
				Method:     "multi_marked",
				Success:    true,
				DurationMS: time.Since(start).Milliseconds(),
				Context:    models.ExtractionContext{"cards": len(entry.GetCards())},
			})
			return &PreviewResult{
				IsMulti:    true,
				MultiID:    entry.ID,
				MultiCards: entry.GetCards(),
			}, nil
		}
	}

	// Single recipe — extract from the HTML we already fetched
	recipeDef, _, _, method := s.extractRecipeFromHTML(html, rawURL)
	if recipeDef == nil {
		// JSON-LD failed — try AI extraction from the same HTML
		provider := s.PreviewProvider
		if provider == nil {
			provider = s.TextProvider
		}
		if provider == nil {
			s.recordExtraction(ctx, models.ExtractionEvent{
				URL:        rawURL,
				Success:    false,
				ErrorCode:  "no_provider",
				Error:      "no AI text provider configured for fallback extraction",
				DurationMS: time.Since(start).Milliseconds(),
			})
			return nil, fmt.Errorf("no AI text provider configured for fallback extraction")
		}
		result, aiErr := provider.ExtractRecipeFromText(ctx, html, ai.UnitSystemPreserveSource)
		if aiErr != nil {
			s.recordExtraction(ctx, models.ExtractionEvent{
				URL:        rawURL,
				Method:     string(models.ExtractionHaiku),
				Success:    false,
				ErrorCode:  extractionErrCode(aiErr),
				Error:      truncateErr(aiErr),
				DurationMS: time.Since(start).Milliseconds(),
				Context:    models.ExtractionContext{"html_len": len(html)},
			})
			return nil, fmt.Errorf("failed to extract recipe from URL: %w", aiErr)
		}
		def := recipeResultToRecipeDef(result)
		def.SourceURL = rawURL
		ensureUnitSystem(&def)
		recipeDef = &def
		method = models.ExtractionHaiku
	}
	s.recordExtraction(ctx, models.ExtractionEvent{
		URL:        rawURL,
		Method:     string(method),
		Success:    true,
		DurationMS: time.Since(start).Milliseconds(),
	})

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
				Embedding:        s.canonicalEmbedding(ctx, recipeDef),
			}
			if upsertErr := s.CanonicalRepo.Upsert(entry); upsertErr == nil {
				canonicalID = &entry.ID
			} else {
				log.Warn("failed to upsert canonical for preview", zap.Error(upsertErr))
			}
		}
	}

	return &PreviewResult{Recipe: recipeDef, CanonicalID: canonicalID}, nil
}

// createImportedRecipe creates a recipe in the DB from a RecipeDef.
// Returns the RecipeResponse and the raw DB recipe ID.
// imageURL, when non-empty, is stored as the recipe's image.
// canonicalID links the recipe to a canonical entry; nil for non-URL imports.
// hashtags are raw tag strings to associate with the recipe.
func (s *ImportService) createImportedRecipe(ctx context.Context, recipeDef *models.RecipeDef, user *models.User, recipeType models.RecipeType, sourcePrompt string, imageURL string, canonicalID *uint, hashtags []string, promptVersion string) (*RecipeResponse, uint, error) {
	log := logger.Get().With(zap.Uint("user_id", user.ID), zap.String("type", string(recipeType)))

	if recipeDef.Title == "" {
		return nil, 0, fmt.Errorf("recipe title is required")
	}

	// Idempotent safety net: ensure every imported recipe carries normalized
	// measurement fields even if it reached here without going through a
	// builder (e.g. the manual path). Detach the ingredient slice first so a
	// cache-hit canonical's backing array is never mutated in place.
	recipeDef.Ingredients = append(models.Ingredients(nil), recipeDef.Ingredients...)
	normalizeIngredients(recipeDef)

	// Estimate portions when the import lacks them (best-effort).
	if recipeDef.Portions <= 0 && s.Normalize != nil {
		if estimate, err := s.Normalize.EstimatePortions(ctx, recipeDef); err != nil {
			log.Warn("failed to estimate portions for imported recipe", zap.String("title", recipeDef.Title), zap.Error(err))
		} else if estimate != nil && estimate.Portions > 0 {
			recipeDef.Portions = estimate.Portions
			if recipeDef.PortionSize == "" {
				recipeDef.PortionSize = estimate.PortionSize
			}
		}
	}

	recipe := &models.Recipe{
		RecipeDef:          *recipeDef,
		CreatedBy:          user,
		PersonalizationUID: user.Personalization.UID,
		CanonicalID:        canonicalID,
		HasDiverged:        canonicalID == nil,
		PromptVersion:      promptVersion,
		ImageURL:           imageURL,
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
// Returns the recipe definition, raw hashtag strings, and the recipe image URL.
func extractJSONLD(html string) (*models.RecipeDef, []string, string, error) {
	re := regexp.MustCompile(`(?s)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	matches := re.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		jsonStr := strings.TrimSpace(match[1])

		// Try parsing as a single object
		recipeDef, hashtags, imageURL, err := tryParseJSONLDObject(jsonStr)
		if err == nil && recipeDef != nil {
			return recipeDef, hashtags, imageURL, nil
		}

		// Try parsing as an array
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &arr); err == nil {
			for _, item := range arr {
				recipeDef, hashtags, imageURL, err := tryParseJSONLDObject(string(item))
				if err == nil && recipeDef != nil {
					return recipeDef, hashtags, imageURL, nil
				}
			}
		}
	}

	return nil, nil, "", fmt.Errorf("no JSON-LD recipe found")
}

// tryParseJSONLDObject attempts to parse a JSON string as a JSON-LD Recipe.
func tryParseJSONLDObject(jsonStr string) (*models.RecipeDef, []string, string, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return nil, nil, "", err
	}

	// Check if this is a @graph container
	if graph, ok := obj["@graph"]; ok {
		if graphArr, ok := graph.([]interface{}); ok {
			for _, item := range graphArr {
				itemBytes, err := json.Marshal(item)
				if err != nil {
					continue
				}
				recipeDef, hashtags, imageURL, err := tryParseJSONLDObject(string(itemBytes))
				if err == nil && recipeDef != nil {
					return recipeDef, hashtags, imageURL, nil
				}
			}
		}
		return nil, nil, "", fmt.Errorf("no recipe found in @graph")
	}

	// Check @type
	if !isRecipeType(obj["@type"]) {
		return nil, nil, "", fmt.Errorf("not a Recipe type")
	}

	var recipe jsonLDRecipe
	if err := json.Unmarshal([]byte(jsonStr), &recipe); err != nil {
		return nil, nil, "", err
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
// Returns the recipe definition, raw hashtag strings, and the recipe image URL.
func jsonLDToRecipeDef(recipe *jsonLDRecipe) (*models.RecipeDef, []string, string, error) {
	if recipe.Name == "" {
		return nil, nil, "", fmt.Errorf("recipe name is empty")
	}

	// Parse ingredients into structured amount/unit/name where possible,
	// always preserving the original text for display.
	ingredients := make(models.Ingredients, len(recipe.Ingredients))
	for i, ingStr := range recipe.Ingredients {
		if amount, high, unit, name, ok := ParseIngredientLine(ingStr); ok {
			ingredients[i] = models.Ingredient{
				Name:         name,
				Unit:         unit,
				Amount:       amount,
				AmountHigh:   high,
				OriginalText: ingStr,
			}
		} else {
			ingredients[i] = models.Ingredient{
				Name:         ingStr,
				OriginalText: ingStr,
			}
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

	imageURL := parseJSONLDImage(recipe.Image)

	def := &models.RecipeDef{
		Title:        recipe.Name,
		Ingredients:  ingredients,
		Instructions: instructions,
		CookTime:     cookTime,
		Portions:     portions,
		ImagePrompt:  fmt.Sprintf("A photo of %s", recipe.Name),
		UnitSystem:   unitSystem,
	}
	normalizeIngredients(def)
	return def, hashtags, imageURL, nil
}

// parseJSONLDImage extracts the first usable https image URL from a JSON-LD
// image field, which can be a string, an array, or an ImageObject {url: ...}.
func parseJSONLDImage(image interface{}) string {
	switch v := image.(type) {
	case string:
		if strings.HasPrefix(v, "https://") {
			return v
		}
	case []interface{}:
		for _, item := range v {
			if url := parseJSONLDImage(item); url != "" {
				return url
			}
		}
	case map[string]interface{}:
		if url, ok := v["url"].(string); ok && strings.HasPrefix(url, "https://") {
			return url
		}
	}
	return ""
}

// ensureUnitSystem fills RecipeDef.UnitSystem heuristically when AI extraction
// with the preserve-source sentinel left it empty, so ToRecipeResponse does
// not silently mislabel metric recipes as us_customary.
func ensureUnitSystem(def *models.RecipeDef) {
	if def == nil || def.UnitSystem != "" {
		return
	}
	lines := make([]string, 0, len(def.Ingredients))
	for _, ing := range def.Ingredients {
		if ing.OriginalText != "" {
			lines = append(lines, ing.OriginalText)
			continue
		}
		lines = append(lines, strings.TrimSpace(fmt.Sprintf("%v %s %s", ing.Amount, ing.Unit, ing.Name)))
	}
	def.UnitSystem = detectUnitSystem(lines)
}

// normalizeIngredients populates the deterministic, user-agnostic measurement
// fields on every ingredient (MeasureKind + BaseAmount) and runs the density
// sanity guard on the AI metric equivalent. It is idempotent and takes no user
// input, so it is safe to call at every RecipeDef-construction point and keeps
// the canonical recipe identical regardless of who imported it.
//
// The density guard: when a volume ingredient carries a mass metric equivalent,
// the implied density must be physically plausible (roughly 0.1–3.0 g/mL spans
// everything from puffed cereal to honey). An order-of-magnitude hallucination
// ("2 cups flour = 5000 g") is rejected in favor of an exact same-dimension
// metric volume, so a metric viewer still gets a sane number with no curated
// density table.
func normalizeIngredients(def *models.RecipeDef) {
	if def == nil {
		return
	}
	for i := range def.Ingredients {
		ing := &def.Ingredients[i]
		kind := units.MeasureKind(ing.Unit, ing.Name, ing.MetricUnit)
		ing.MeasureKind = kind
		ing.BaseAmount = units.BaseAmount(ing.Amount, ing.Unit, kind)

		if kind == units.KindVolume && ing.BaseAmount > 0 && ing.MetricAmount > 0 &&
			units.DimensionOf(ing.MetricUnit) == units.KindMass {
			grams := units.BaseAmount(ing.MetricAmount, ing.MetricUnit, units.KindMass)
			if density := grams / ing.BaseAmount; density < 0.1 || density > 3.0 {
				amt, unit := units.ExpressInSystem(ing.BaseAmount, units.KindVolume, units.SystemMetric)
				ing.MetricAmount = amt
				ing.MetricUnit = unit
			}
		}
	}
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

// DetectUnitSystemFromIngredients detects the measurement system from manual
// ingredient entries, returning ok=false when no line carries any unit marker
// at all (so the caller can fall back to a user preference instead of
// defaulting to us_customary). This keeps a metric user's "2 cups" recipe
// labeled us_customary while a unit-less "2 eggs" recipe defers to preference.
func DetectUnitSystemFromIngredients(ings models.Ingredients) (string, bool) {
	lines := make([]string, 0, len(ings))
	hasMarker := false
	for _, ing := range ings {
		line := ing.OriginalText
		if line == "" {
			line = strings.TrimSpace(fmt.Sprintf("%v %s %s", ing.Amount, ing.Unit, ing.Name))
		}
		lines = append(lines, line)
		if metricRe.MatchString(line) || usRe.MatchString(line) {
			hasMarker = true
		}
	}
	return detectUnitSystem(lines), hasMarker
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
