package service

import (
	"context"
	"sync"
	"time"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// Warm status values returned per URL.
const (
	WarmCached     = "cached"     // already in the canonical cache (instant)
	WarmExtracting = "extracting" // being warmed now (or just kicked off)
	WarmMulti      = "multi"      // a collection page (expands on tap)
	WarmUncached   = "uncached"   // not warmed (skipped: bad URL or safety cap)
	WarmFailed     = "failed"     // recently failed to extract (in cooldown)
)

// warmFailCooldown is how long a URL that failed to warm (e.g. a bot-blocked
// page) is reported terminal before another attempt, so we don't re-kick a
// failing extraction on every poll.
const warmFailCooldown = 15 * time.Minute

// WarmService proactively extracts and caches recipe URLs (cache warming) so a
// later preview/import is an instant cache hit. Extraction runs in parallel,
// bounded by a concurrency limit, deduped against in-flight work, and capped by
// a daily safety ceiling that guards against runaway cost (JSON-LD stays free
// either way).
type WarmService struct {
	Import   *ImportService
	Resolver *MultiRecipeResolver

	sem      chan struct{}
	inflight sync.Map // normalizedURL -> struct{}{}
	failed   sync.Map // normalizedURL -> int64 (unix-nano cooldown expiry)

	mu       sync.Mutex
	day      string
	count    int
	maxDaily int // <= 0 means unlimited (a high kill-switch, not a normal cap)
}

// NewWarmService builds a WarmService. concurrency defaults to 5; maxDaily <= 0
// disables the safety ceiling.
func NewWarmService(imp *ImportService, resolver *MultiRecipeResolver, concurrency, maxDaily int) *WarmService {
	if concurrency <= 0 {
		concurrency = 5
	}
	return &WarmService{
		Import:   imp,
		Resolver: resolver,
		sem:      make(chan struct{}, concurrency),
		maxDaily: maxDaily,
	}
}

// WarmURLs returns the current warm status of each URL, kicking off background
// extraction for any that are uncached and not already in flight. It is
// idempotent — calling it repeatedly with the same URLs is cheap (cached and
// in-flight URLs are never re-extracted), so it doubles as a status poll.
func (w *WarmService) WarmURLs(urls []string) map[string]string {
	out := make(map[string]string, len(urls))
	for _, rawURL := range urls {
		if rawURL == "" {
			continue
		}
		out[rawURL] = w.statusAndKick(rawURL)
	}
	return out
}

func (w *WarmService) statusAndKick(rawURL string) string {
	if w.Import == nil || w.Import.CanonicalRepo == nil {
		return WarmUncached
	}
	normalizedURL, err := NormalizeURL(rawURL)
	if err != nil {
		return WarmUncached
	}

	// Already cached?
	if canonical, err := w.Import.CanonicalRepo.GetByNormalizedURL(normalizedURL); err == nil {
		if canonical.IsMultiPage {
			return WarmMulti
		}
		return WarmCached
	}

	// Recently failed? Report it terminal during the cooldown instead of
	// re-kicking a failing/blocked page on every poll.
	if exp, ok := w.failed.Load(normalizedURL); ok {
		if time.Now().UnixNano() < exp.(int64) {
			return WarmFailed
		}
		w.failed.Delete(normalizedURL)
	}

	// Already being warmed by another request/goroutine?
	if _, loaded := w.inflight.LoadOrStore(normalizedURL, struct{}{}); loaded {
		return WarmExtracting
	}

	// Safety ceiling — back out of the in-flight claim if we're over budget so
	// the URL can still be warmed later (or extracted lazily on tap).
	if !w.allow() {
		w.inflight.Delete(normalizedURL)
		return WarmUncached
	}

	go w.warmOne(rawURL, normalizedURL)
	return WarmExtracting
}

func (w *WarmService) warmOne(rawURL, normalizedURL string) {
	defer w.inflight.Delete(normalizedURL)

	w.sem <- struct{}{}
	defer func() { <-w.sem }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := w.Import.WarmURL(ctx, w.Resolver, rawURL); err != nil {
		// Remember the failure so we stop hammering a blocked/failing page.
		w.failed.Store(normalizedURL, time.Now().Add(warmFailCooldown).UnixNano())
		logger.Get().Info("cache warming skipped", zap.String("url", rawURL), zap.Error(err))
	} else {
		w.failed.Delete(normalizedURL)
	}
}

// allow enforces the daily safety ceiling. It resets at the UTC day boundary.
func (w *WarmService) allow() bool {
	if w.maxDaily <= 0 {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	day := time.Now().UTC().Format("2006-01-02")
	if day != w.day {
		w.day = day
		w.count = 0
	}
	if w.count >= w.maxDaily {
		return false
	}
	w.count++
	return true
}
