package service

import (
	"context"
	"errors"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
)

func TestDomainFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"simple", "https://example.com/recipe", "example.com"},
		{"www prefix stripped", "https://www.example.com/recipe", "example.com"},
		{"subdomain kept", "https://recipes.example.com/recipe", "recipes.example.com"},
		{"uppercase normalized", "https://WWW.EXAMPLE.COM/recipe", "example.com"},
		{"port ignored", "https://example.com:8080/recipe", "example.com"},
		{"empty string", "", ""},
		{"invalid url", "://invalid", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domainFromURL(tt.url)
			if got != tt.want {
				t.Errorf("domainFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestImportPolicy_RecordOutcome(t *testing.T) {
	p := NewImportPolicy()

	p.RecordOutcome("https://example.com/recipe1", models.ExtractionJSONLD, true)
	p.RecordOutcome("https://example.com/recipe2", models.ExtractionHaiku, false)
	p.RecordOutcome("https://other.com/recipe", models.ExtractionFirecrawlJSONLD, true)

	stats := p.GetDomainStats("https://example.com/any")
	if stats == nil {
		t.Fatal("expected stats for example.com")
	}
	if stats.TotalAttempts != 2 {
		t.Errorf("TotalAttempts = %d, want 2", stats.TotalAttempts)
	}
	if stats.JSONLDSuccesses != 1 {
		t.Errorf("JSONLDSuccesses = %d, want 1", stats.JSONLDSuccesses)
	}
	if stats.AIFailures != 1 {
		t.Errorf("AIFailures = %d, want 1", stats.AIFailures)
	}

	otherStats := p.GetDomainStats("https://other.com/any")
	if otherStats == nil {
		t.Fatal("expected stats for other.com")
	}
	if otherStats.FirecrawlSuccesses != 1 {
		t.Errorf("FirecrawlSuccesses = %d, want 1", otherStats.FirecrawlSuccesses)
	}
	if otherStats.JSONLDSuccesses != 1 {
		t.Errorf("JSONLDSuccesses = %d, want 1 (firecrawl_json_ld counts as JSON-LD)", otherStats.JSONLDSuccesses)
	}
}

func TestImportPolicy_RecordOutcome_EmptyURL(t *testing.T) {
	p := NewImportPolicy()
	// Should not panic
	p.RecordOutcome("", models.ExtractionJSONLD, true)
	all := p.GetAllStats()
	if len(all) != 0 {
		t.Errorf("expected no stats for empty URL, got %d", len(all))
	}
}

func TestImportPolicy_ShouldSkipDirectFetch(t *testing.T) {
	p := NewImportPolicy()
	url := "https://blocked-site.com/recipe"

	// Initially, should not skip
	if p.ShouldSkipDirectFetch(url) {
		t.Error("ShouldSkipDirectFetch should be false with no data")
	}

	// Record 2 blocked attempts - not enough
	p.RecordDirectFetchBlocked(url)
	p.RecordDirectFetchBlocked(url)
	if p.ShouldSkipDirectFetch(url) {
		t.Error("ShouldSkipDirectFetch should be false with only 2 blocks")
	}

	// 3 blocked but no firecrawl success yet
	p.RecordDirectFetchBlocked(url)
	if p.ShouldSkipDirectFetch(url) {
		t.Error("ShouldSkipDirectFetch should be false without firecrawl success")
	}

	// Now record a firecrawl success
	p.RecordOutcome(url, models.ExtractionFirecrawlJSONLD, true)
	if !p.ShouldSkipDirectFetch(url) {
		t.Error("ShouldSkipDirectFetch should be true with 3+ blocks and firecrawl success")
	}
}

func TestImportPolicy_ShouldSkipDirectFetch_EmptyURL(t *testing.T) {
	p := NewImportPolicy()
	if p.ShouldSkipDirectFetch("") {
		t.Error("ShouldSkipDirectFetch should be false for empty URL")
	}
}

func TestImportPolicy_ShouldSkipDirectFetch_UnknownDomain(t *testing.T) {
	p := NewImportPolicy()
	if p.ShouldSkipDirectFetch("https://unknown.com/recipe") {
		t.Error("ShouldSkipDirectFetch should be false for unknown domain")
	}
}

func TestImportPolicy_RecordDirectFetchBlocked_EmptyURL(t *testing.T) {
	p := NewImportPolicy()
	// Should not panic
	p.RecordDirectFetchBlocked("")
	all := p.GetAllStats()
	if len(all) != 0 {
		t.Errorf("expected no stats for empty URL, got %d", len(all))
	}
}

func TestImportPolicy_GetDomainStats_NilForUnknown(t *testing.T) {
	p := NewImportPolicy()
	stats := p.GetDomainStats("https://unknown.com/recipe")
	if stats != nil {
		t.Error("expected nil stats for unknown domain")
	}
}

func TestImportPolicy_GetDomainStats_ReturnsCopy(t *testing.T) {
	p := NewImportPolicy()
	p.RecordOutcome("https://example.com/r", models.ExtractionJSONLD, true)

	stats1 := p.GetDomainStats("https://example.com/r")
	stats1.TotalAttempts = 999

	stats2 := p.GetDomainStats("https://example.com/r")
	if stats2.TotalAttempts == 999 {
		t.Error("GetDomainStats should return a copy, not a reference")
	}
}

func TestImportPolicy_GetAllStats(t *testing.T) {
	p := NewImportPolicy()
	p.RecordOutcome("https://a.com/r", models.ExtractionJSONLD, true)
	p.RecordOutcome("https://b.com/r", models.ExtractionHaiku, true)
	p.RecordOutcome("https://c.com/r", models.ExtractionFirecrawlHaiku, false)

	all := p.GetAllStats()
	if len(all) != 3 {
		t.Errorf("GetAllStats returned %d entries, want 3", len(all))
	}
}

func TestImportPolicy_GetAllStats_Empty(t *testing.T) {
	p := NewImportPolicy()
	all := p.GetAllStats()
	if len(all) != 0 {
		t.Errorf("GetAllStats returned %d entries for empty policy, want 0", len(all))
	}
}

func TestImportPolicy_WwwStrippedConsistently(t *testing.T) {
	p := NewImportPolicy()
	p.RecordOutcome("https://www.example.com/r1", models.ExtractionJSONLD, true)
	p.RecordOutcome("https://example.com/r2", models.ExtractionHaiku, true)

	stats := p.GetDomainStats("https://www.example.com/any")
	if stats == nil {
		t.Fatal("expected stats for example.com")
	}
	if stats.TotalAttempts != 2 {
		t.Errorf("TotalAttempts = %d, want 2 (www and non-www should merge)", stats.TotalAttempts)
	}
}

func TestImportPolicy_FirecrawlHaikuRecordsBothCounters(t *testing.T) {
	p := NewImportPolicy()
	p.RecordOutcome("https://example.com/r", models.ExtractionFirecrawlHaiku, true)

	stats := p.GetDomainStats("https://example.com/r")
	if stats == nil {
		t.Fatal("expected stats")
	}
	if stats.AISuccesses != 1 {
		t.Errorf("AISuccesses = %d, want 1", stats.AISuccesses)
	}
	if stats.FirecrawlSuccesses != 1 {
		t.Errorf("FirecrawlSuccesses = %d, want 1", stats.FirecrawlSuccesses)
	}
}

// newPolicyTestService creates an ImportService with just enough wiring for extractFromURL tests.
func newPolicyTestService() *ImportService {
	return &ImportService{
		Cfg:    &config.Config{},
		Policy: NewImportPolicy(),
	}
}

func TestExtractFromURL_PolicyRecordsOutcome_JSONLD(t *testing.T) {
	svc := newPolicyTestService()
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte(jsonLDHTML()), 200, nil
	}

	_, _, method, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}
	if method != models.ExtractionJSONLD {
		t.Errorf("method = %q, want %q", method, models.ExtractionJSONLD)
	}

	stats := svc.Policy.GetDomainStats("https://example.com/recipe")
	if stats == nil {
		t.Fatal("expected policy stats after extraction")
	}
	if stats.JSONLDSuccesses != 1 {
		t.Errorf("JSONLDSuccesses = %d, want 1", stats.JSONLDSuccesses)
	}
}

func TestExtractFromURL_PolicyRecordsBlocked(t *testing.T) {
	svc := newPolicyTestService()
	svc.Cfg.EnvVars.FirecrawlAPIKey = "test-key"
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Forbidden"), 403, nil
	}
	svc.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, int, error) {
		return jsonLDHTML(), 200, nil
	}

	_, _, _, err := svc.extractFromURL(context.Background(), "https://blocked.com/recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}

	stats := svc.Policy.GetDomainStats("https://blocked.com/recipe")
	if stats == nil {
		t.Fatal("expected policy stats")
	}
	if stats.DirectFetchBlocked != 1 {
		t.Errorf("DirectFetchBlocked = %d, want 1", stats.DirectFetchBlocked)
	}
	if stats.FirecrawlSuccesses != 1 {
		t.Errorf("FirecrawlSuccesses = %d, want 1 (firecrawl_json_ld counts)", stats.FirecrawlSuccesses)
	}
}

func TestExtractFromURL_PolicySkipsDirectFetch(t *testing.T) {
	svc := newPolicyTestService()
	svc.Cfg.EnvVars.FirecrawlAPIKey = "test-key"

	// Pre-populate policy: 3 blocks + firecrawl success
	for i := 0; i < 3; i++ {
		svc.Policy.RecordDirectFetchBlocked("https://blocked.com/recipe")
	}
	svc.Policy.RecordOutcome("https://blocked.com/recipe", models.ExtractionFirecrawlJSONLD, true)

	directFetchCalled := false
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		directFetchCalled = true
		return []byte("Forbidden"), 403, nil
	}
	svc.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, int, error) {
		return jsonLDHTML(), 200, nil
	}

	def, _, method, err := svc.extractFromURL(context.Background(), "https://blocked.com/new-recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}
	if directFetchCalled {
		t.Error("direct fetch should have been skipped for known-blocking domain")
	}
	if def.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", def.Title)
	}
	if method != models.ExtractionFirecrawlJSONLD {
		t.Errorf("method = %q, want %q", method, models.ExtractionFirecrawlJSONLD)
	}
}

func TestExtractFromURL_PolicySkipsDirectFetch_404(t *testing.T) {
	svc := newPolicyTestService()
	svc.Cfg.EnvVars.FirecrawlAPIKey = "test-key"

	// Pre-populate policy: 3 blocks + firecrawl success
	for i := 0; i < 3; i++ {
		svc.Policy.RecordDirectFetchBlocked("https://blocked.com/recipe")
	}
	svc.Policy.RecordOutcome("https://blocked.com/recipe", models.ExtractionFirecrawlJSONLD, true)

	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		t.Error("direct fetch should not be called for known-blocking domain")
		return nil, 0, nil
	}
	svc.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, int, error) {
		return "<html>Not Found</html>", 404, nil
	}

	_, _, _, err := svc.extractFromURL(context.Background(), "https://blocked.com/deleted-recipe")
	if err == nil {
		t.Fatal("expected error for 404 on skip-direct domain")
	}
	var extractErr *ExtractionError
	if !errors.As(err, &extractErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extractErr.Code != "not_found" {
		t.Errorf("code = %q, want 'not_found'", extractErr.Code)
	}
}

func TestExtractFromURL_NilPolicyStillWorks(t *testing.T) {
	svc := &ImportService{
		Cfg:    &config.Config{},
		Policy: nil, // explicitly nil
	}
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte(jsonLDHTML()), 200, nil
	}

	def, _, method, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}
	if def.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", def.Title)
	}
	if method != models.ExtractionJSONLD {
		t.Errorf("method = %q, want %q", method, models.ExtractionJSONLD)
	}
}
