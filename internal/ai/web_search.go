package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// exhaustionCooldown is how long to wait before retrying a provider after a
// quota-exhaustion response (429/403). Providers reset their quotas on
// different schedules (Google = daily, Brave = monthly), but a 1-hour
// cooldown is a reasonable middle ground that avoids hammering a depleted
// API while still recovering within the same process lifetime.
const exhaustionCooldown = 1 * time.Hour

// WebSearchProvider implements SearchProvider using Brave Search as the
// primary engine. Google Custom Search support is retained but disabled
// (see SearchRecipes) because Google CSE no longer supports searching
// the entire web — it requires a curated list of sites. DO NOT DELETE
// the Google code; it can be re-enabled if a suitable CSE config is created.
type WebSearchProvider struct {
	googleAPIKey      string
	googleCX          string
	braveAPIKey       string
	httpClient        *http.Client
	googleExhaustedAt atomic.Int64 // Unix timestamp; 0 = not exhausted
	braveExhaustedAt  atomic.Int64 // Unix timestamp; 0 = not exhausted
}

// NewWebSearchProvider creates a search provider with Google primary + Brave fallback.
func NewWebSearchProvider(googleAPIKey, googleCX, braveAPIKey string) *WebSearchProvider {
	return &WebSearchProvider{
		googleAPIKey: googleAPIKey,
		googleCX:     googleCX,
		braveAPIKey:  braveAPIKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// isExhausted returns true if the provider's cooldown period hasn't elapsed yet.
func isExhausted(exhaustedAt *atomic.Int64) bool {
	ts := exhaustedAt.Load()
	if ts == 0 {
		return false
	}
	return time.Since(time.Unix(ts, 0)) < exhaustionCooldown
}

// markExhausted records the current time as the exhaustion timestamp.
func markExhausted(exhaustedAt *atomic.Int64) {
	exhaustedAt.Store(time.Now().Unix())
}

// SearchRecipes tries Brave first. Google CSE is currently disabled because
// Google no longer allows Custom Search Engines to search the entire web
// (a curated site list is required). DO NOT DELETE the Google code — it can
// be re-enabled once a suitable CSE configuration is set up.
func (p *WebSearchProvider) SearchRecipes(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = 10
	}

	// Try Brave (unless we recently hit a quota limit)
	if !isExhausted(&p.braveExhaustedAt) && p.braveAPIKey != "" {
		results, err := p.searchBrave(ctx, query, count)
		if err == nil {
			return results, nil
		}
		logger.Get().Warn("brave search failed", zap.Error(err))
	}

	// NOTE: Google CSE fallback is disabled. See struct comment for details.
	// To re-enable, uncomment the block below:
	//
	// if !isExhausted(&p.googleExhaustedAt) && p.googleAPIKey != "" {
	// 	return p.searchGoogle(ctx, query, count)
	// }

	return nil, fmt.Errorf("no search providers available")
}

// --- Google Custom Search ---

const googleSearchEndpoint = "https://www.googleapis.com/customsearch/v1"

type googleSearchResponse struct {
	Items []googleSearchItem `json:"items"`
	Error *googleErrorBlock  `json:"error"`
}

type googleSearchItem struct {
	Title   string             `json:"title"`
	Link    string             `json:"link"`
	Snippet string             `json:"snippet"`
	Pagemap *googlePagemap     `json:"pagemap"`
}

type googlePagemap struct {
	CSEThumbnail []googleThumbnail  `json:"cse_thumbnail"`
	AggregateRating []googleRating  `json:"aggregaterating"`
}

type googleThumbnail struct {
	Src string `json:"src"`
}

type googleRating struct {
	RatingValue string `json:"ratingvalue"`
}

type googleErrorBlock struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (p *WebSearchProvider) searchGoogle(ctx context.Context, query string, count int) ([]SearchResult, error) {
	// Google CSE max is 10 per request
	if count > 10 {
		count = 10
	}

	// Site filtering is handled by the Custom Search Engine config,
	// so we just pass the raw query here.
	params := url.Values{}
	params.Set("key", p.googleAPIKey)
	params.Set("cx", p.googleCX)
	params.Set("q", query)
	params.Set("num", fmt.Sprintf("%d", count))

	reqURL := fmt.Sprintf("%s?%s", googleSearchEndpoint, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create google request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read google response: %w", err)
	}

	// 429 = quota exhausted for today
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 403 {
		markExhausted(&p.googleExhaustedAt)
		return nil, fmt.Errorf("google quota exhausted (status %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google API returned status %d: %s", resp.StatusCode, string(body))
	}

	var gResp googleSearchResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		return nil, fmt.Errorf("failed to parse google response: %w", err)
	}

	if gResp.Error != nil {
		if gResp.Error.Code == 429 || gResp.Error.Code == 403 {
			markExhausted(&p.googleExhaustedAt)
		}
		return nil, fmt.Errorf("google API error %d: %s", gResp.Error.Code, gResp.Error.Message)
	}

	results := make([]SearchResult, 0, len(gResp.Items))
	for _, item := range gResp.Items {
		r := SearchResult{
			Title:       item.Title,
			URL:         item.Link,
			Source:      extractDomain(item.Link),
			Description: item.Snippet,
		}
		if item.Pagemap != nil && len(item.Pagemap.CSEThumbnail) > 0 {
			r.ImageURL = item.Pagemap.CSEThumbnail[0].Src
		}
		results = append(results, r)
	}
	return results, nil
}

// --- Brave Search ---

const braveSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"

type braveSearchResponse struct {
	Web *braveWebResults `json:"web"`
}

type braveWebResults struct {
	Results []braveResult `json:"results"`
}

type braveResult struct {
	Title       string          `json:"title"`
	URL         string          `json:"url"`
	Description string          `json:"description"`
	Thumbnail   *braveThumbnail `json:"thumbnail"`
}

type braveThumbnail struct {
	Src string `json:"src"`
}

func (p *WebSearchProvider) searchBrave(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if count > 20 {
		count = 20
	}

	params := url.Values{}
	params.Set("q", query+" recipe")
	params.Set("count", fmt.Sprintf("%d", count))

	reqURL := fmt.Sprintf("%s?%s", braveSearchEndpoint, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create brave request: %w", err)
	}
	req.Header.Set("X-Subscription-Token", p.braveAPIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read brave response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 403 {
		markExhausted(&p.braveExhaustedAt)
		return nil, fmt.Errorf("brave quota exhausted (status %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave API returned status %d: %s", resp.StatusCode, string(body))
	}

	var bResp braveSearchResponse
	if err := json.Unmarshal(body, &bResp); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	if bResp.Web == nil {
		return nil, nil
	}

	results := make([]SearchResult, 0, len(bResp.Web.Results))
	for _, r := range bResp.Web.Results {
		sr := SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      extractDomain(r.URL),
			Description: r.Description,
		}
		if r.Thumbnail != nil {
			sr.ImageURL = r.Thumbnail.Src
		}
		results = append(results, sr)
	}
	return results, nil
}

// extractDomain pulls the hostname from a URL string.
func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Hostname()
}
