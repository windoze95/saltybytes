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

// shownItems collects every result item the client would display — the shortlist
// plus everything appended via expanded.
func shownItems(events []FinderEvent) []FinderResultItem {
	var out []FinderResultItem
	for _, ev := range events {
		if ev.Type == FinderEventShortlist || ev.Type == FinderEventExpanded {
			out = append(out, ev.Items...)
		}
	}
	return out
}

func shownHasURL(events []FinderEvent, url string) bool {
	for _, it := range shownItems(events) {
		if it.Result.URL == url {
			return true
		}
	}
	return false
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

func TestFindRecipes_DigsFlaggedCollectionAndHidesIt(t *testing.T) {
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
	si, di, ei, done := indexOfType(events, FinderEventShortlist), indexOfType(events, FinderEventDigging), indexOfType(events, FinderEventExpanded), indexOfType(events, FinderEventDone)
	if si < 0 || di < 0 || ei < 0 || done < 0 {
		t.Fatalf("missing events (order: %v)", eventTypes(events))
	}
	if !(si < di && di < ei && ei < done) {
		t.Errorf("event order wrong: shortlist=%d digging=%d expanded=%d done=%d (%v)", si, di, ei, done, eventTypes(events))
	}

	// The collection itself is NEVER shown as a card.
	if shownHasURL(events, results[1].URL) {
		t.Errorf("collection %q was shown as a result — collections must be hidden", results[1].URL)
	}
	// The shortlist holds the two INDIVIDUAL candidates (0 and 2).
	shortlist, _ := firstEventOfType(events, FinderEventShortlist)
	if len(shortlist.Items) != 2 {
		t.Fatalf("shortlist has %d items, want 2 individual (collection excluded)", len(shortlist.Items))
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
		t.Fatalf("expanded has %d items, want 2 (pending card dropped)", len(exp.Items))
	}
	wantReason := fmt.Sprintf("from '%s'", results[1].Title)
	wantURLs := map[string]bool{"https://example.com/child-a?_recipe=child-a-0": true, "https://example.com/child-b": true}
	for _, it := range exp.Items {
		if it.Reason != wantReason {
			t.Errorf("folded reason = %q, want %q", it.Reason, wantReason)
		}
		if !wantURLs[it.Result.URL] {
			t.Errorf("folded URL = %q, want a card CachedURL", it.Result.URL)
		}
	}

	// Only the flagged collection was resolved, exactly once.
	if len(fake.calls) != 1 || fake.calls[0] != results[1].URL {
		t.Errorf("resolver calls = %v, want exactly [%s]", fake.calls, results[1].URL)
	}
}

func TestFindRecipes_DigsEvenWhenManyDirect(t *testing.T) {
	// 8 candidates: 3 flagged collections + 5 individual. Under the OLD gate
	// (dig only when direct < 8) this would NOT dig; now it must, because the 3
	// collections are hidden and mined for real recipes.
	results := digSearchResults(8)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, map[int]int{0: 3, 1: 2, 2: 1})

	entries := map[string]*MultiRecipeEntry{}
	for _, idx := range []int{0, 1, 2} {
		entries[results[idx].URL] = resolvedEntry(results[idx].URL, doneCard("Mined "+fmt.Sprint(idx), "https://example.com/mined"+fmt.Sprint(idx)))
	}
	fake := &fakeMultiResolver{entries: entries}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	if countEventsOfType(events, FinderEventDigging) == 0 || len(fake.calls) == 0 {
		t.Errorf("did not dig despite flagged collections + a full direct list (%v)", eventTypes(events))
	}
	for _, idx := range []int{0, 1, 2} {
		if shownHasURL(events, results[idx].URL) {
			t.Errorf("collection %q was shown — collections must be hidden", results[idx].URL)
		}
	}
}

func TestFindRecipes_AllCollectionsSeedShortlistFromMined(t *testing.T) {
	// Every candidate is a collection: the direct shortlist is empty, so the
	// first mined batch must SEED the shortlist (never an empty shortlist, never
	// a collection card, never an empty event when recipes were mined).
	results := digSearchResults(2)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, map[int]int{0: 9, 1: 5})

	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		results[0].URL: resolvedEntry(results[0].URL, doneCard("Mined A", "https://example.com/mined-a")),
		results[1].URL: resolvedEntry(results[1].URL, doneCard("Mined B", "https://example.com/mined-b")),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	if countEventsOfType(events, FinderEventEmpty) != 0 {
		t.Errorf("emitted empty despite mining real recipes from collections (%v)", eventTypes(events))
	}
	shortlist, ok := firstEventOfType(events, FinderEventShortlist)
	if !ok || len(shortlist.Items) == 0 {
		t.Fatalf("shortlist not seeded from mined recipes (order: %v)", eventTypes(events))
	}
	// Shown recipes are the mined individuals, not the collection pages.
	for _, idx := range []int{0, 1} {
		if shownHasURL(events, results[idx].URL) {
			t.Errorf("collection %q was shown", results[idx].URL)
		}
	}
	for _, it := range shownItems(events) {
		if it.Reason == "" {
			t.Errorf("mined pick %q has no reason", it.Result.Title)
		}
	}
}

func TestFindRecipes_CuratesToCap(t *testing.T) {
	// Far more individual candidates than the cap → the shown set is trimmed.
	results := digSearchResults(finderCuratedCap + 5)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, nil) // all individual, none flagged

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	if got := len(shownItems(events)); got != finderCuratedCap {
		t.Errorf("shown %d recipes, want the curated cap %d", got, finderCuratedCap)
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

	// A single collection with more DONE cards than we can show.
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

	// Total shown never exceeds the curated cap; folded never exceeds its cap.
	if got := len(shownItems(events)); got > finderCuratedCap {
		t.Errorf("shown %d recipes, want <= curated cap %d", got, finderCuratedCap)
	}
	folded := 0
	for _, ev := range events {
		if ev.Type == FinderEventExpanded {
			folded += len(ev.Items)
		}
	}
	if folded > finderDigMaxCards {
		t.Errorf("folded %d recipes, want <= finderDigMaxCards=%d", folded, finderDigMaxCards)
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
