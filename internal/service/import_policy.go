package service

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
)

// DomainStats tracks extraction success/failure rates for a domain.
type DomainStats struct {
	Domain              string
	TotalAttempts       int
	JSONLDSuccesses     int
	JSONLDFailures      int
	DirectFetchBlocked  int
	FirecrawlSuccesses  int
	FirecrawlFailures   int
	AISuccesses         int
	AIFailures          int
	LastAttempt         time.Time
	LastSuccess         time.Time
}

// ImportPolicy tracks per-domain extraction outcomes and recommends strategies.
type ImportPolicy struct {
	mu    sync.RWMutex
	stats map[string]*DomainStats // keyed by domain
}

// NewImportPolicy creates a new ImportPolicy.
func NewImportPolicy() *ImportPolicy {
	return &ImportPolicy{
		stats: make(map[string]*DomainStats),
	}
}

// domainFromURL extracts the domain from a URL, stripping "www." prefix.
func domainFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	return host
}

// RecordOutcome records the result of an extraction attempt.
func (p *ImportPolicy) RecordOutcome(rawURL string, method models.ExtractionMethod, success bool) {
	domain := domainFromURL(rawURL)
	if domain == "" {
		return
	}

	now := time.Now()

	p.mu.Lock()
	defer p.mu.Unlock()

	stats, ok := p.stats[domain]
	if !ok {
		stats = &DomainStats{Domain: domain}
		p.stats[domain] = stats
	}

	stats.TotalAttempts++
	stats.LastAttempt = now
	if success {
		stats.LastSuccess = now
	}

	switch method {
	case models.ExtractionJSONLD, models.ExtractionFirecrawlJSONLD:
		if success {
			stats.JSONLDSuccesses++
		} else {
			stats.JSONLDFailures++
		}
	case models.ExtractionHaiku, models.ExtractionFirecrawlHaiku:
		if success {
			stats.AISuccesses++
		} else {
			stats.AIFailures++
		}
	}

	if method == models.ExtractionFirecrawlJSONLD || method == models.ExtractionFirecrawlHaiku {
		if success {
			stats.FirecrawlSuccesses++
		} else {
			stats.FirecrawlFailures++
		}
	}

	logger.Get().Debug("import policy: recorded outcome",
		zap.String("domain", domain),
		zap.String("method", string(method)),
		zap.Bool("success", success),
		zap.Int("total_attempts", stats.TotalAttempts),
	)
}

// ShouldSkipDirectFetch returns true if a domain consistently blocks direct fetches.
// A domain is considered "blocking" if it has 3+ blocked attempts with no recent successes.
func (p *ImportPolicy) ShouldSkipDirectFetch(rawURL string) bool {
	domain := domainFromURL(rawURL)
	if domain == "" {
		return false
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	stats, ok := p.stats[domain]
	if !ok {
		return false
	}

	// If the domain has blocked 3+ times and Firecrawl has worked, skip direct fetch
	return stats.DirectFetchBlocked >= 3 && stats.FirecrawlSuccesses > 0
}

// RecordDirectFetchBlocked records that a direct fetch was blocked for a domain.
func (p *ImportPolicy) RecordDirectFetchBlocked(rawURL string) {
	domain := domainFromURL(rawURL)
	if domain == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	stats, ok := p.stats[domain]
	if !ok {
		stats = &DomainStats{Domain: domain}
		p.stats[domain] = stats
	}
	stats.DirectFetchBlocked++
}

// GetDomainStats returns stats for a domain. Returns nil if no data exists.
func (p *ImportPolicy) GetDomainStats(rawURL string) *DomainStats {
	domain := domainFromURL(rawURL)
	if domain == "" {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	stats, ok := p.stats[domain]
	if !ok {
		return nil
	}

	// Return a copy
	copy := *stats
	return &copy
}

// GetAllStats returns stats for all tracked domains.
func (p *ImportPolicy) GetAllStats() []DomainStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]DomainStats, 0, len(p.stats))
	for _, stats := range p.stats {
		result = append(result, *stats)
	}
	return result
}
