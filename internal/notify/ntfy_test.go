package notify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type capturedRequest struct {
	path     string
	title    string
	click    string
	auth     string
	body     string
	priority string
}

func startCaptureServer(t *testing.T) (*httptest.Server, chan capturedRequest) {
	t.Helper()
	got := make(chan capturedRequest, 10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- capturedRequest{
			path:     r.URL.Path,
			title:    r.Header.Get("Title"),
			click:    r.Header.Get("Click"),
			auth:     r.Header.Get("Authorization"),
			body:     string(body),
			priority: r.Header.Get("Priority"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts, got
}

func TestAlert_SendsToTopicWithHeaders(t *testing.T) {
	ts, got := startCaptureServer(t)
	Init(ts.URL, "salty-alerts", "tok-123")
	t.Cleanup(ResetForTest)

	Alert("k1", "Budget reached", "AI spend hit $25", "https://dash.example", time.Minute)

	select {
	case req := <-got:
		if req.path != "/salty-alerts" {
			t.Errorf("path = %q", req.path)
		}
		if req.title != "Budget reached" {
			t.Errorf("title = %q", req.title)
		}
		if req.click != "https://dash.example" {
			t.Errorf("click = %q", req.click)
		}
		if req.auth != "Bearer tok-123" {
			t.Errorf("auth = %q", req.auth)
		}
		if req.body != "AI spend hit $25" {
			t.Errorf("body = %q", req.body)
		}
		if req.priority != "high" {
			t.Errorf("priority = %q", req.priority)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("alert never reached the server")
	}
}

func TestAlert_DedupesWithinCooldown(t *testing.T) {
	ts, got := startCaptureServer(t)
	Init(ts.URL, "salty-alerts", "")
	t.Cleanup(ResetForTest)

	Alert("same-key", "first", "msg", "", time.Hour)
	Alert("same-key", "second", "msg", "", time.Hour)
	Alert("other-key", "third", "msg", "", time.Hour)

	titles := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case req := <-got:
			titles[req.title] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("expected 2 alerts, got %d", len(titles))
		}
	}
	if titles["second"] {
		t.Error("duplicate key inside cooldown must be dropped")
	}
	if !titles["first"] || !titles["third"] {
		t.Errorf("expected 'first' and 'third', got %v", titles)
	}

	select {
	case req := <-got:
		t.Errorf("unexpected extra alert: %q", req.title)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAlert_DisabledIsNoOp(t *testing.T) {
	ResetForTest()
	if Enabled() {
		t.Fatal("notifier should be disabled after reset")
	}
	// Must not panic or block.
	Alert("k", "title", "msg", "", time.Minute)

	Init("", "topic-only", "")
	if Enabled() {
		t.Error("empty URL must leave alerting disabled")
	}
	Init("https://ntfy.example", "", "")
	if Enabled() {
		t.Error("empty topic must leave alerting disabled")
	}
}
