// Package notify pushes operational alerts to an ntfy topic so the operator
// hears about problems (budget trips, provider failures) from their phone,
// with a tap-through link to the place where the problem gets fixed.
//
// Mirrors the logger package's global-singleton style: Init once at boot,
// call Alert anywhere. Uninitialized or unconfigured, every call is a no-op,
// so callers never guard.
package notify

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// Notifier posts alerts to a single ntfy topic.
type Notifier struct {
	url    string
	topic  string
	token  string
	client *http.Client

	mu       sync.Mutex
	lastSent map[string]time.Time
}

var (
	defaultMu       sync.RWMutex
	defaultNotifier *Notifier
)

// Init configures the process-wide notifier. Empty url or topic leaves
// alerting disabled.
func Init(url, topic, token string) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if url == "" || topic == "" {
		defaultNotifier = nil
		return
	}
	defaultNotifier = &Notifier{
		url:      strings.TrimRight(url, "/"),
		topic:    topic,
		token:    token,
		client:   &http.Client{Timeout: 5 * time.Second},
		lastSent: make(map[string]time.Time),
	}
}

// Enabled reports whether alerts will actually be sent.
func Enabled() bool {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultNotifier != nil
}

// Alert sends one high-priority notification. key dedupes repeats: further
// alerts with the same key inside cooldown are dropped, so an exhausted
// budget pages once, not once per request. clickURL, when non-empty, becomes
// the notification's tap-through target. Sending is async and best-effort —
// an unreachable ntfy server never blocks or fails a request.
func Alert(key, title, message, clickURL string, cooldown time.Duration) {
	defaultMu.RLock()
	n := defaultNotifier
	defaultMu.RUnlock()
	if n == nil {
		return
	}

	n.mu.Lock()
	if last, ok := n.lastSent[key]; ok && time.Since(last) < cooldown {
		n.mu.Unlock()
		return
	}
	n.lastSent[key] = time.Now()
	n.mu.Unlock()

	go n.send(title, message, clickURL)
}

func (n *Notifier) send(title, message, clickURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.url+"/"+n.topic, strings.NewReader(message))
	if err != nil {
		logger.Get().Warn("ntfy alert request build failed", zap.Error(err))
		return
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", "high")
	req.Header.Set("Tags", "warning")
	if clickURL != "" {
		req.Header.Set("Click", clickURL)
	}
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		logger.Get().Warn("ntfy alert send failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		logger.Get().Warn("ntfy alert rejected", zap.Int("status", resp.StatusCode))
	}
}

// ResetForTest clears the global notifier (tests only).
func ResetForTest() {
	defaultMu.Lock()
	defaultNotifier = nil
	defaultMu.Unlock()
}
