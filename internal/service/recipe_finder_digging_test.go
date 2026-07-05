package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
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

// TestFindRecipes_KnownMultiExcludedEvenWhenRankingFails locks the fail-closed
// invariant: when the ranking call fails entirely (fallback ranking, no expand
// flags), a candidate the canonical cache already knows is a multi-recipe
// collection page must STILL be excluded from the shown results and dug
// instead. This is the guard against the 2026-07-01 prod incident where every
// ranking failure painted "40 Easy ..." roundups as recipe cards.
func TestFindRecipes_KnownMultiExcludedEvenWhenRankingFails(t *testing.T) {
	collectionURL := "https://example.com/gallery/40-easy-weeknight-dinners"
	results := []ai.SearchResult{
		{Title: "Sheet Pan Chicken Fajitas", URL: "https://example.com/fajitas", Source: "example.com", Description: "Quick weeknight fajitas."},
		{Title: "40 Easy Weeknight Dinner Recipes", URL: collectionURL, Source: "example.com", Description: "Our favorite quick dinners."},
		{Title: "One-Pot Chicken and Rice", URL: "https://example.com/one-pot", Source: "example.com", Description: "A comforting one-pot classic."},
	}
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

	// The canonical cache knows the gallery URL is a multi-recipe page.
	canon := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(norm string) (*models.CanonicalRecipe, error) {
			if strings.Contains(norm, "40-easy-weeknight-dinners") {
				return &models.CanonicalRecipe{NormalizedURL: norm, IsMultiPage: true}, nil
			}
			return nil, fmt.Errorf("miss")
		},
	}
	imp := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	imp.CanonicalRepo = canon

	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		collectionURL: resolvedEntry(collectionURL,
			doneCard("Skillet Chicken Parm", "https://example.com/gallery/40-easy-weeknight-dinners?_recipe=chicken-parm-0"),
		),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.Warm = NewWarmService(imp, nil, 2, 0)
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	// The known collection must never be shown as a pick...
	for _, item := range shownItems(events) {
		if item.Result.URL == collectionURL {
			t.Fatalf("known multi-page collection shown as a result despite ranking failure: %+v", item)
		}
	}
	// ...the two real singles still are (ranking failure degrades gracefully)...
	shown := shownItems(events)
	titles := make([]string, 0, len(shown))
	for _, it := range shown {
		titles = append(titles, it.Result.Title)
	}
	if len(shown) < 3 { // 2 direct singles + at least 1 mined recipe
		t.Errorf("shown = %v, want the 2 singles plus mined recipes", titles)
	}
	// ...and it is dug instead: digging fired and the mined recipe was folded in.
	if len(fake.calls) != 1 || fake.calls[0] != collectionURL {
		t.Errorf("resolver calls = %v, want exactly the known collection", fake.calls)
	}
	if countEventsOfType(events, FinderEventDigging) == 0 {
		t.Errorf("no digging event for the known collection (%v)", eventTypes(events))
	}
	foundMined := false
	for _, it := range shown {
		if it.Result.Title == "Skillet Chicken Parm" {
			foundMined = true
		}
	}
	if !foundMined {
		t.Errorf("mined recipe not folded into results: %v", titles)
	}
}

// TestFindRecipes_HeuristicCollectionsExcludedWhenRankingFails locks the second
// fail-closed layer: when ranking fails, FIRST-SEEN roundups (not yet in the
// canonical cache, so applyKnownCollections can't catch them) must still be
// pulled out of the results by URL/title pattern and dug instead of being
// painted as recipe cards.
func TestFindRecipes_HeuristicCollectionsExcludedWhenRankingFails(t *testing.T) {
	slugCollection := "https://example.com/40-easy-weeknight-dinners"
	pathCollection := "https://example.com/gallery/cozy-fall-soups"
	results := []ai.SearchResult{
		{Title: "Sheet Pan Chicken Fajitas", URL: "https://example.com/recipe/sheet-pan-chicken-fajitas", Source: "example.com", Description: "Quick weeknight fajitas."},
		{Title: "40 Easy Weeknight Dinner Recipes", URL: slugCollection, Source: "example.com", Description: "Our favorite quick dinners."},
		{Title: "Cozy Fall Soups", URL: pathCollection, Source: "example.com", Description: "A gallery of soups."},
		{Title: "5-Ingredient Brownies", URL: "https://example.com/recipe/5-ingredient-brownies", Source: "example.com", Description: "One bowl, five ingredients."},
	}
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
	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		slugCollection: resolvedEntry(slugCollection, doneCard("Skillet Chicken Parm", "https://example.com/recipe/skillet-chicken-parm")),
		pathCollection: resolvedEntry(pathCollection, doneCard("Butternut Squash Soup", "https://example.com/recipe/butternut-squash-soup")),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Occasion: "weeknight"}})

	// Neither pattern-flagged collection is ever shown as a card...
	for _, u := range []string{slugCollection, pathCollection} {
		if shownHasURL(events, u) {
			t.Errorf("heuristic collection %q shown as a result despite ranking failure", u)
		}
	}
	// ...but the real singles are, INCLUDING the count-qualified single recipe.
	for _, u := range []string{results[0].URL, results[3].URL} {
		if !shownHasURL(events, u) {
			t.Errorf("real single recipe %q missing from results", u)
		}
	}
	// Both heuristic collections were dug and their recipes folded in.
	if len(fake.calls) != 2 {
		t.Errorf("resolver calls = %v, want both heuristic collections dug", fake.calls)
	}
	for _, mined := range []string{"https://example.com/recipe/skillet-chicken-parm", "https://example.com/recipe/butternut-squash-soup"} {
		if !shownHasURL(events, mined) {
			t.Errorf("mined recipe %q not folded into results", mined)
		}
	}
}

// TestFindRecipes_DedupsMinedAcrossCollectionsAndDirect: the same recipe often
// appears in several roundups and as a direct pick; each distinct recipe is
// folded at most once (URL and normalized-title keys).
func TestFindRecipes_DedupsMinedAcrossCollectionsAndDirect(t *testing.T) {
	direct := ai.SearchResult{Title: "One-Pot Chicken and Rice", URL: "https://example.com/recipe/one-pot-chicken-rice", Source: "example.com", Description: "A comforting classic."}
	collA := ai.SearchResult{Title: "Roundup A", URL: "https://example.com/roundup-a", Source: "example.com"}
	collB := ai.SearchResult{Title: "Roundup B", URL: "https://example.com/roundup-b", Source: "example.com"}
	results := []ai.SearchResult{direct, collA, collB}

	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, map[int]int{1: 5, 2: 4})

	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		collA.URL: resolvedEntry(collA.URL,
			doneCard("One-Pot Chicken and Rice", direct.URL), // duplicate of the direct pick
			doneCard("Ground Beef Gyros", "https://example.com/recipe/ground-beef-gyros"),
			doneCard("Skillet Lasagna", "https://example.com/recipe/skillet-lasagna"),
		),
		collB.URL: resolvedEntry(collB.URL,
			doneCard("Ground Beef Gyros", "https://example.com/recipe/ground-beef-gyros"),  // dup by URL
			doneCard("Skillet  Lasagna", "https://example.com/recipe/skillet-lasagna-two"), // dup by normalized title
			doneCard("Chicken Tikka Masala", "https://example.com/recipe/chicken-tikka-masala"),
		),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	shown := shownItems(events)
	counts := map[string]int{}
	for _, it := range shown {
		counts[strings.ToLower(strings.Join(strings.Fields(it.Result.Title), " "))]++
	}
	for title, n := range counts {
		if n > 1 {
			t.Errorf("recipe %q shown %d times, want 1", title, n)
		}
	}
	// Exactly the 4 distinct recipes: direct + gyros + lasagna + tikka.
	if len(shown) != 4 {
		titles := make([]string, 0, len(shown))
		for _, it := range shown {
			titles = append(titles, it.Result.Title)
		}
		t.Errorf("shown %d items %v, want the 4 distinct recipes", len(shown), titles)
	}
}

func TestLooksLikeCollectionPage(t *testing.T) {
	cases := []struct {
		url, title string
		want       bool
	}{
		{"https://example.com/40-easy-weeknight-dinners", "40 Easy Weeknight Dinner Recipes", true},
		{"https://example.com/dinners", "23 Best Chicken Dinners", true},
		{"https://example.com/dinners", "12+ Cozy Soups for Fall", true},
		{"https://example.com/gallery/best-soups", "Our Best Soups", true},
		{"https://example.com/recipes/category/desserts", "Desserts", true},
		{"https://example.com/slideshow/comfort-food", "Comfort Food", true},
		// Single recipes with counting qualifiers must NOT be flagged.
		{"https://example.com/recipe/5-ingredient-brownies", "5-Ingredient Brownies", false},
		{"https://example.com/recipe/30-minute-chili", "30-Minute Chili", false},
		{"https://example.com/recipe/15-bean-soup", "15 Bean Soup", false},
		{"https://example.com/recipe/3-cheese-lasagna", "3 Cheese Lasagna", false},
		{"https://example.com/recipe/lemon-garlic-chicken", "Creamy Lemon Garlic Chicken", false},
		{"https://example.com/recipe/one-pot-pasta", "1 Pot Pasta", false},
	}
	for _, c := range cases {
		if got := looksLikeCollectionPage(c.url, c.title); got != c.want {
			t.Errorf("looksLikeCollectionPage(%q, %q) = %v, want %v", c.url, c.title, got, c.want)
		}
	}
}

// TestFindRecipes_InstantResultsWithholdLikelyCollections: the pre-rank
// `results` event paints immediately but never paints an obvious roundup.
func TestFindRecipes_InstantResultsWithholdLikelyCollections(t *testing.T) {
	results := []ai.SearchResult{
		{Title: "Sheet Pan Chicken Fajitas", URL: "https://example.com/recipe/sheet-pan-chicken-fajitas", Source: "example.com", Description: "Quick fajitas."},
		{Title: "40 Easy Weeknight Dinner Recipes", URL: "https://example.com/40-easy-weeknight-dinners", Source: "example.com", Description: "Roundup."},
		{Title: "One-Pot Chicken and Rice", URL: "https://example.com/recipe/one-pot-chicken-rice", Source: "example.com", Description: "Classic."},
	}
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, map[int]int{1: 5})

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}})

	instant, ok := firstEventOfType(events, FinderEventResults)
	if !ok {
		t.Fatalf("no results event (%v)", eventTypes(events))
	}
	if len(instant.Items) != 2 {
		t.Fatalf("instant results has %d items, want 2 (roundup withheld)", len(instant.Items))
	}
	for _, it := range instant.Items {
		if it.Result.URL == results[1].URL {
			t.Errorf("obvious roundup %q painted in instant results", it.Result.URL)
		}
	}
	// The results event precedes the model call (filtering).
	ri, fi := indexOfType(events, FinderEventResults), indexOfType(events, FinderEventFiltering)
	if !(ri >= 0 && fi >= 0 && ri < fi) {
		t.Errorf("results (%d) must precede filtering (%d): %v", ri, fi, eventTypes(events))
	}
}

// TestFindRecipes_PicksCurateAcrossDirectAndMined: after digging, one more
// cheap ranking call orders direct + mined TOGETHER; mined picks get a real
// model rationale (provenance kept in via) and warming targets the picks.
func TestFindRecipes_PicksCurateAcrossDirectAndMined(t *testing.T) {
	direct := ai.SearchResult{Title: "Sheet Pan Chicken Fajitas", URL: "https://example.com/recipe/sheet-pan-chicken-fajitas", Source: "example.com", Description: "Quick fajitas."}
	coll := ai.SearchResult{Title: "Best Weeknight Dinners", URL: "https://example.com/roundup-weeknight", Source: "example.com", Description: "Roundup."}
	results := []ai.SearchResult{direct, coll}
	minedURL := "https://example.com/recipe/ground-beef-gyros"

	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	rankCalls := 0
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			rankCalls++
			if rankCalls == 1 {
				return &ai.FinderRankResult{Ranked: []ai.FinderRanking{
					{Index: 0, Reason: "Solid fajitas."},
					{Index: 1, Expand: true, ExpandPriority: 5},
				}}, nil
			}
			// Picks call: pool = [direct, mined]; put the mined recipe first
			// with a fresh rationale.
			if len(req.Candidates) != 2 {
				t.Errorf("picks call got %d candidates, want 2", len(req.Candidates))
			}
			return &ai.FinderRankResult{Ranked: []ai.FinderRanking{
				{Index: 1, Reason: "Weeknight hero: 20 minutes, one skillet."},
				{Index: 0, Reason: "Fajitas fit the brief."},
			}}, nil
		},
	}
	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		coll.URL: resolvedEntry(coll.URL, doneCard("Ground Beef Gyros", minedURL)),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Occasion: "weeknight"}})

	if rankCalls != 2 {
		t.Fatalf("rank calls = %d, want 2 (initial + picks)", rankCalls)
	}
	picks, ok := firstEventOfType(events, FinderEventPicks)
	if !ok {
		t.Fatalf("no picks event (%v)", eventTypes(events))
	}
	if len(picks.Items) != 2 {
		t.Fatalf("picks has %d items, want 2", len(picks.Items))
	}
	top := picks.Items[0]
	if top.Result.URL != minedURL {
		t.Errorf("picks[0] = %q, want the mined recipe ranked first", top.Result.URL)
	}
	if top.Reason != "Weeknight hero: 20 minutes, one skillet." {
		t.Errorf("picks[0] reason = %q, want the fresh model rationale", top.Reason)
	}
	if top.Via != coll.Title {
		t.Errorf("picks[0] via = %q, want provenance %q kept", top.Via, coll.Title)
	}
	// Warming targets the picks order (mined recipe first).
	warming, _ := firstEventOfType(events, FinderEventWarming)
	if len(warming.URLs) == 0 || warming.URLs[0] != minedURL {
		t.Errorf("warming URLs = %v, want picks-first (%q)", warming.URLs, minedURL)
	}
	// The picks event lands after digging/expanded and before warming.
	pi, ei, wi := indexOfType(events, FinderEventPicks), indexOfType(events, FinderEventExpanded), indexOfType(events, FinderEventWarming)
	if !(ei < pi && pi < wi) {
		t.Errorf("event order wrong: expanded=%d picks=%d warming=%d (%v)", ei, pi, wi, eventTypes(events))
	}
}

// TestFindRecipes_PicksFallbackOnPickRankFailure: a failed picks call degrades
// to pre-pick order (direct then mined, provenance reasons intact) — curation
// can improve the result, never erase it.
func TestFindRecipes_PicksFallbackOnPickRankFailure(t *testing.T) {
	direct := ai.SearchResult{Title: "Sheet Pan Chicken Fajitas", URL: "https://example.com/recipe/sheet-pan-chicken-fajitas", Source: "example.com", Description: "Quick fajitas."}
	coll := ai.SearchResult{Title: "Best Weeknight Dinners", URL: "https://example.com/roundup-weeknight", Source: "example.com", Description: "Roundup."}
	results := []ai.SearchResult{direct, coll}

	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	rankCalls := 0
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			rankCalls++
			if rankCalls == 1 {
				return &ai.FinderRankResult{Ranked: []ai.FinderRanking{
					{Index: 0, Reason: "Solid fajitas."},
					{Index: 1, Expand: true, ExpandPriority: 5},
				}}, nil
			}
			return nil, fmt.Errorf("picks model exploded")
		},
	}
	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		coll.URL: resolvedEntry(coll.URL, doneCard("Ground Beef Gyros", "https://example.com/recipe/ground-beef-gyros")),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Occasion: "weeknight"}})

	picks, ok := firstEventOfType(events, FinderEventPicks)
	if !ok {
		t.Fatalf("no picks event after picks-rank failure (%v)", eventTypes(events))
	}
	if len(picks.Items) != 2 {
		t.Fatalf("picks has %d items, want 2 (pre-pick order fallback)", len(picks.Items))
	}
	if picks.Items[0].Result.URL != direct.URL {
		t.Errorf("picks[0] = %q, want direct pick first on fallback", picks.Items[0].Result.URL)
	}
	if picks.Items[1].Reason == "" || picks.Items[1].Via != coll.Title {
		t.Errorf("mined fallback pick lost provenance: reason=%q via=%q", picks.Items[1].Reason, picks.Items[1].Via)
	}
	// done still terminates the run.
	if indexOfType(events, FinderEventDone) < 0 {
		t.Errorf("run did not finish after picks fallback (%v)", eventTypes(events))
	}
}

// TestFindRecipes_NoPicksOnPagedRuns: load-more pages are browsing material —
// no picks event, no second model call.
func TestFindRecipes_NoPicksOnPagedRuns(t *testing.T) {
	results := digSearchResults(3)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	rankCalls := 0
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			rankCalls++
			ranked := make([]ai.FinderRanking, len(req.Candidates))
			for i := range req.Candidates {
				ranked[i] = ai.FinderRanking{Index: i, Reason: "fits"}
			}
			return &ai.FinderRankResult{Ranked: ranked}, nil
		},
	}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}, Offset: 10})

	if n := countEventsOfType(events, FinderEventPicks); n != 0 {
		t.Errorf("paged run emitted %d picks events, want 0", n)
	}
	if rankCalls != 1 {
		t.Errorf("paged run made %d rank calls, want 1", rankCalls)
	}
	// Instant results still paint on paged runs.
	if countEventsOfType(events, FinderEventResults) != 1 {
		t.Errorf("paged run missing instant results event (%v)", eventTypes(events))
	}
}

// TestFindRecipes_LateHarvestNoDoubleFold: digging and the late-harvest sweep
// both read the same entries; every foldable card must be folded exactly once.
// (The slow-card recovery itself is exercised directly in
// TestLateHarvest_FoldsCardsMissedByDigging.)
func TestFindRecipes_LateHarvestNoDoubleFold(t *testing.T) {
	coll := ai.SearchResult{Title: "Best Weeknight Dinners", URL: "https://example.com/roundup-weeknight", Source: "example.com", Description: "Roundup."}
	results := []ai.SearchResult{coll}
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, map[int]int{0: 5})
	fake := &fakeMultiResolver{entries: map[string]*MultiRecipeEntry{
		coll.URL: resolvedEntry(coll.URL,
			doneCard("Ground Beef Gyros", "https://example.com/recipe/ground-beef-gyros"),
			doneCard("Skillet Lasagna", "https://example.com/recipe/skillet-lasagna"),
		),
	}}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.MultiResolver = fake

	events := runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Occasion: "weeknight"}})

	shown := shownItems(events)
	counts := map[string]int{}
	for _, it := range shown {
		counts[it.Result.URL]++
	}
	for u, n := range counts {
		if n > 1 {
			t.Errorf("recipe %q folded %d times across dig + late harvest, want 1", u, n)
		}
	}
	if len(shown) != 2 {
		t.Errorf("shown %d items, want 2", len(shown))
	}
}

// TestLateHarvest_FoldsCardsMissedByDigging exercises the sweep directly: a
// card that was NOT folded during the dig window (its key is absent from seen)
// is folded by the sweep, deduped against what digging already took, and
// emitted as expanded with its collection's provenance.
func TestLateHarvest_FoldsCardsMissedByDigging(t *testing.T) {
	svc := newFinderService(&testutil.MockSearchProvider{}, &testutil.MockTextProvider{}, &testutil.MockFamilyRepo{})

	entry := resolvedEntry("https://example.com/roundup-weeknight",
		doneCard("Ground Beef Gyros", "https://example.com/recipe/ground-beef-gyros"), // folded during digging
		doneCard("Skillet Lasagna", "https://example.com/recipe/skillet-lasagna"),     // finished late
		MultiRecipeCard{Title: "Still Extracting", ExtractionStatus: "extracting"},    // never folds
	)
	seen := map[string]bool{}
	markResultSeen(seen, "https://example.com/recipe/ground-beef-gyros", "Ground Beef Gyros")

	events := make(chan FinderEvent, 8)
	shortlistEmitted := true
	folded := svc.lateHarvest(context.Background(), events,
		[]dugCollection{{entry: entry, title: "Best Weeknight Dinners"}},
		seen, 4, 1, &shortlistEmitted, false)
	close(events)

	if len(folded) != 1 || folded[0].Result.URL != "https://example.com/recipe/skillet-lasagna" {
		t.Fatalf("late harvest folded %+v, want exactly the late lasagna card", folded)
	}
	if folded[0].Via != "Best Weeknight Dinners" {
		t.Errorf("late-folded via = %q, want the collection title", folded[0].Via)
	}
	var got []FinderEvent
	for ev := range events {
		got = append(got, ev)
	}
	if len(got) != 1 || got[0].Type != FinderEventExpanded || len(got[0].Items) != 1 {
		t.Errorf("late harvest emitted %v, want one expanded event with one item", eventTypes(got))
	}
}
