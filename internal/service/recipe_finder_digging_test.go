package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// fakeMultiResolver is an offline MultiResolver: it records which URLs were
// resolved and returns pre-built entries, so digging can be tested without any
// network or background extraction.
type fakeMultiResolver struct {
	entries map[string]*MultiRecipeEntry
	calls   []string
}

func (f *fakeMultiResolver) ResolveFromURLN(ctx context.Context, sourceURL string, maxCards int) *MultiRecipeEntry {
	f.calls = append(f.calls, sourceURL)
	return f.entries[sourceURL]
}

// resolvedEntry builds a *MultiRecipeEntry already in the "resolved" state so
// waitForCollection returns its cards immediately.
func resolvedEntry(sourceURL string, cards ...MultiRecipeCard) *MultiRecipeEntry {
	return &MultiRecipeEntry{
		ID:        "multi_test_" + sourceURL,
		SourceURL: sourceURL,
		Status:    "resolved",
		Cards:     cards,
	}
}

func doneCard(title, cachedURL string) MultiRecipeCard {
	return MultiRecipeCard{Title: title, ExtractionStatus: "done", CachedURL: cachedURL, Description: title + " desc"}
}

func indexOfType(events []FinderEvent, t FinderEventType) int {
	for i, ev := range events {
		if ev.Type == t {
			return i
		}
	}
	return -1
}

// digSearchResults builds n distinct real search candidates.
func digSearchResults(n int) []ai.SearchResult {
	out := make([]ai.SearchResult, n)
	for i := range out {
		out[i] = ai.SearchResult{
			Title:       fmt.Sprintf("Candidate %d", i),
			URL:         fmt.Sprintf("https://example.com/c%d", i),
			Source:      "example.com",
			Description: "desc",
		}
	}
	return out
}

// rankAllFlagging ranks every candidate in order, flagging the given indices as
// expandable collections with the supplied priorities.
func rankAllFlagging(results []ai.SearchResult, flags map[int]int) *testutil.MockTextProvider {
	return &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			ranked := make([]ai.FinderRanking, len(results))
			for i := range results {
				r := ai.FinderRanking{Index: i, Reason: "fits"}
				if prio, ok := flags[i]; ok {
					r.Expand = true
					r.ExpandPriority = prio
				}
				ranked[i] = r
			}
			return &ai.FinderRankResult{Ranked: ranked}, nil
		},
	}
}

func TestFindRecipes_DigsFlaggedCollection(t *testing.T) {
	results := digSearchResults(3)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	// Flag candidate 1 as a collection.
	ranker := rankAllFlagging(results, map[int]int{1: 5})

	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		results[1].URL: resolvedEntry(results[1].URL,
			doneCard("Child A", "https://example.com/child-a?_recipe=child-a-0"),
			doneCard("Child B", "https://example.com/child-b"),
			MultiRecipeCard{Title: "Still Pending", ExtractionStatus: "pending"}, // must be skipped
		),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	// digging then expanded appear, after the shortlist and before done.
	di := indexOfType(events, FinderEventDigging)
	ei := indexOfType(events, FinderEventExpanded)
	si := indexOfType(events, FinderEventShortlist)
	done := indexOfType(events, FinderEventDone)
	if si < 0 || di < 0 || ei < 0 || done < 0 {
		t.Fatalf("missing events (order: %v)", eventTypes(events))
	}
	if !(si < di && di < ei && ei < done) {
		t.Errorf("event order wrong: shortlist=%d digging=%d expanded=%d done=%d (%v)", si, di, ei, done, eventTypes(events))
	}

	// digging names the collection.
	dig, _ := firstEventOfType(events, FinderEventDigging)
	if dig.CollectionTitle != results[1].Title {
		t.Errorf("digging collection_title = %q, want %q", dig.CollectionTitle, results[1].Title)
	}

	// expanded folds the two DONE cards (pending dropped), by CachedURL, with the
	// "from '<collection>'" reason.
	exp, _ := firstEventOfType(events, FinderEventExpanded)
	if len(exp.Items) != 2 {
		t.Fatalf("expanded has %d items, want 2 (pending card must be dropped)", len(exp.Items))
	}
	wantReason := fmt.Sprintf("from '%s'", results[1].Title)
	wantURLs := map[string]bool{"https://example.com/child-a?_recipe=child-a-0": true, "https://example.com/child-b": true}
	for _, it := range exp.Items {
		if it.Reason != wantReason {
			t.Errorf("folded reason = %q, want %q", it.Reason, wantReason)
		}
		if !wantURLs[it.Result.URL] {
			t.Errorf("folded URL = %q, want one of the cards' CachedURL", it.Result.URL)
		}
	}

	// Only the flagged collection was resolved, exactly once.
	if len(fake.calls) != 1 || fake.calls[0] != results[1].URL {
		t.Errorf("resolver calls = %v, want exactly [%s]", fake.calls, results[1].URL)
	}
}

func TestFindRecipes_DigCapsAndPrioritizes(t *testing.T) {
	results := digSearchResults(4)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	// Flag three collections with distinct priorities: 2 (low), 1 (high), 3 (mid).
	ranker := rankAllFlagging(results, map[int]int{2: 1, 1: 9, 3: 5})

	entries := map[string]*MultiRecipeEntry{}
	for _, idx := range []int{1, 2, 3} {
		entries[results[idx].URL] = resolvedEntry(results[idx].URL, doneCard("Child", "https://example.com/child"+fmt.Sprint(idx)))
	}
	fake := &fakeMultiResolver{entries: entries}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	_ = runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	// Only finderDigK collections are dug, highest ExpandPriority first.
	if len(fake.calls) != finderDigK {
		t.Fatalf("resolver called %d times, want finderDigK=%d (%v)", len(fake.calls), finderDigK, fake.calls)
	}
	if fake.calls[0] != results[1].URL || fake.calls[1] != results[3].URL {
		t.Errorf("dug %v, want highest-priority [%s %s]", fake.calls, results[1].URL, results[3].URL)
	}
}

func TestFindRecipes_DigCapsTotalFolded(t *testing.T) {
	results := digSearchResults(2)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, map[int]int{1: 5})

	// A single collection with more DONE cards than finderDigMaxCards.
	var cards []MultiRecipeCard
	for i := 0; i < finderDigMaxCards+4; i++ {
		cards = append(cards, doneCard(fmt.Sprintf("Child %d", i), fmt.Sprintf("https://example.com/child-%d", i)))
	}
	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		results[1].URL: resolvedEntry(results[1].URL, cards...),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	totalFolded := 0
	for _, ev := range events {
		if ev.Type == FinderEventExpanded {
			totalFolded += len(ev.Items)
		}
	}
	if totalFolded > finderDigMaxCards {
		t.Errorf("folded %d recipes, want <= finderDigMaxCards=%d", totalFolded, finderDigMaxCards)
	}
}

func TestFindRecipes_NoDigWhenNothingFlagged(t *testing.T) {
	results := digSearchResults(3)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, nil) // nothing flagged

	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{}}
	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	if n := countEventsOfType(events, FinderEventDigging) + countEventsOfType(events, FinderEventExpanded); n != 0 {
		t.Errorf("emitted %d dig events with nothing flagged, want 0 (%v)", n, eventTypes(events))
	}
	if len(fake.calls) != 0 {
		t.Errorf("resolver called %v with nothing flagged, want none", fake.calls)
	}
}

func TestFindRecipes_NoDigWhenShortlistFull(t *testing.T) {
	// A full direct shortlist (>= finderDigMinDirect) means no need to dig, even
	// though a collection is flagged.
	results := digSearchResults(finderDigMinDirect)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, map[int]int{0: 5})

	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		results[0].URL: resolvedEntry(results[0].URL, doneCard("Child", "https://example.com/child")),
	}}
	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	if n := countEventsOfType(events, FinderEventDigging); n != 0 {
		t.Errorf("dug despite a full shortlist (%d direct), want no digging", len(results))
	}
	if len(fake.calls) != 0 {
		t.Errorf("resolver called %v despite a full shortlist, want none", fake.calls)
	}
}
