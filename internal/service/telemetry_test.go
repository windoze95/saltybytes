package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// --- FinderRun telemetry -----------------------------------------------------

func TestFindRecipes_TelemetryHappyPath(t *testing.T) {
	results := digSearchResults(3)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, nil) // all ranked, nothing flagged

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	runs := testutil.NewMockFinderRunRepo()
	svc.Runs = runs

	runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	all := runs.Runs()
	if len(all) != 1 {
		t.Fatalf("recorded %d runs, want 1", len(all))
	}
	run := all[0]
	if run.Terminal != "done" {
		t.Errorf("Terminal = %q, want done", run.Terminal)
	}
	if !run.RankOK || run.RankError != "" {
		t.Errorf("RankOK = %v / RankError = %q, want ok", run.RankOK, run.RankError)
	}
	if run.ResultsFound != 3 {
		t.Errorf("ResultsFound = %d, want 3", run.ResultsFound)
	}
	if run.ShownDirect != 3 || run.ShownTotal != 3 {
		t.Errorf("Shown = %d/%d, want 3/3", run.ShownDirect, run.ShownTotal)
	}
	if run.CollectionsFlagged != 0 || run.CollectionsDug != 0 || run.CardsMined != 0 {
		t.Errorf("dig counters = %d/%d/%d, want zeros",
			run.CollectionsFlagged, run.CollectionsDug, run.CardsMined)
	}
	if run.Query == "" {
		t.Error("Query not recorded")
	}
	if run.UserID == 0 {
		t.Error("UserID not recorded")
	}
}

func TestFindRecipes_TelemetryRankFailure(t *testing.T) {
	results := digSearchResults(2)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			return nil, fmt.Errorf("model exploded")
		},
	}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	runs := testutil.NewMockFinderRunRepo()
	svc.Runs = runs

	runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	all := runs.Runs()
	if len(all) != 1 {
		t.Fatalf("recorded %d runs, want 1", len(all))
	}
	run := all[0]
	// The run still completes (graceful degradation) but the rank failure is
	// visible for the dashboard's failure-rate chart.
	if run.Terminal != "done" {
		t.Errorf("Terminal = %q, want done (degraded run still completes)", run.Terminal)
	}
	if run.RankOK {
		t.Error("RankOK = true, want false on a ranking failure")
	}
	if run.RankError == "" {
		t.Error("RankError empty, want the failure text")
	}
}

func TestFindRecipes_TelemetryDigCounters(t *testing.T) {
	results := digSearchResults(3)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	// Candidate 1 is a flagged collection; the fake resolver mines 2 recipes.
	ranker := rankAllFlagging(results, map[int]int{1: 5})
	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		results[1].URL: resolvedEntry(results[1].URL,
			doneCard("Mined One", results[1].URL+"?_recipe=mined-one-0"),
			doneCard("Mined Two", results[1].URL+"?_recipe=mined-two-1"),
		),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake
	runs := testutil.NewMockFinderRunRepo()
	svc.Runs = runs

	runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	all := runs.Runs()
	if len(all) != 1 {
		t.Fatalf("recorded %d runs, want 1", len(all))
	}
	run := all[0]
	if run.CollectionsFlagged != 1 {
		t.Errorf("CollectionsFlagged = %d, want 1", run.CollectionsFlagged)
	}
	if run.CollectionsDug != 1 {
		t.Errorf("CollectionsDug = %d, want 1", run.CollectionsDug)
	}
	if run.CardsMined != 2 {
		t.Errorf("CardsMined = %d, want 2", run.CardsMined)
	}
	if run.ShownTotal != run.ShownDirect+2 {
		t.Errorf("ShownTotal = %d, want direct(%d)+2", run.ShownTotal, run.ShownDirect)
	}
}

// --- ExtractionEvent telemetry ----------------------------------------------

func TestPreviewFromURL_RecordsFailureEvent(t *testing.T) {
	imp := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	events := testutil.NewMockExtractionEventRepo()
	imp.Events = events
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return nil, 404, nil
	}

	_, _, err := imp.PreviewFromURL(context.Background(), "https://example.com/recipes/lost-cake")
	if err == nil {
		t.Fatal("expected a not_found error")
	}

	evs := events.Events()
	if len(evs) != 1 {
		t.Fatalf("recorded %d events, want 1", len(evs))
	}
	ev := evs[0]
	if ev.Success {
		t.Error("Success = true, want false")
	}
	if ev.ErrorCode != "not_found" {
		t.Errorf("ErrorCode = %q, want not_found", ev.ErrorCode)
	}
	if ev.Origin != ExtractionOriginPreview {
		t.Errorf("Origin = %q, want preview", ev.Origin)
	}
	if ev.Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", ev.Domain)
	}
	if ev.Error == "" {
		t.Error("Error text empty, want the failure message")
	}
}

func TestPreviewFromURL_RecordsSuccessEvent(t *testing.T) {
	imp := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	events := testutil.NewMockExtractionEventRepo()
	imp.Events = events
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return []byte(jsonLDHTML()), 200, nil
	}

	if _, _, err := imp.PreviewFromURL(context.Background(), "https://example.com/recipes/great-cake"); err != nil {
		t.Fatalf("preview failed: %v", err)
	}

	evs := events.Events()
	if len(evs) != 1 {
		t.Fatalf("recorded %d events, want 1", len(evs))
	}
	ev := evs[0]
	if !ev.Success {
		t.Errorf("Success = false (err_code=%q err=%q), want true", ev.ErrorCode, ev.Error)
	}
	if ev.Method != string(models.ExtractionJSONLD) {
		t.Errorf("Method = %q, want %q", ev.Method, models.ExtractionJSONLD)
	}
	if ev.UsedFirecrawl {
		t.Error("UsedFirecrawl = true, want false for a direct fetch")
	}
}

func TestExtractionEventsNilRepoIsSafe(t *testing.T) {
	imp := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	imp.Events = nil // default: telemetry off
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return nil, 404, nil
	}
	// Must not panic.
	if _, _, err := imp.PreviewFromURL(context.Background(), "https://example.com/x"); err == nil {
		t.Fatal("expected error")
	}
}
