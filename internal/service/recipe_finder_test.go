package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// newFinderService builds a RecipeFinderService whose every dependency is
// offline: a real SearchService over a mock SearchProvider with no cache repo
// (so it always calls the provider, never the network), a WarmService with no
// Import (so WarmURLs just reports "uncached" without extracting), and a mock
// ranker.
func newFinderService(searchProvider ai.SearchProvider, ranker ai.TextProvider, familyRepo repository.FamilyRepo) *RecipeFinderService {
	cfg := &config.Config{}
	searchService := NewSearchService(cfg, searchProvider, nil, nil)
	warm := NewWarmService(nil, nil, 0, 0)
	return NewRecipeFinderService(cfg, searchService, familyRepo, warm, ranker)
}

// runFinder drains a full finder run and returns the events in order.
func runFinder(svc *RecipeFinderService, user *models.User, req FinderRequest) []FinderEvent {
	events := make(chan FinderEvent, 32)
	done := make(chan struct{})
	var collected []FinderEvent
	go func() {
		defer close(done)
		for ev := range events {
			collected = append(collected, ev)
		}
	}()
	svc.FindRecipes(context.Background(), user, req, events)
	close(events)
	<-done
	return collected
}

func eventTypes(events []FinderEvent) []FinderEventType {
	types := make([]FinderEventType, len(events))
	for i, ev := range events {
		types[i] = ev.Type
	}
	return types
}

func firstEventOfType(events []FinderEvent, t FinderEventType) (FinderEvent, bool) {
	for _, ev := range events {
		if ev.Type == t {
			return ev, true
		}
	}
	return FinderEvent{}, false
}

func countEventsOfType(events []FinderEvent, t FinderEventType) int {
	n := 0
	for _, ev := range events {
		if ev.Type == t {
			n++
		}
	}
	return n
}

func sameTypes(got []FinderEventType, want ...FinderEventType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// finderCandidates are three real search results reused across the tests.
func finderCandidates() []ai.SearchResult {
	return []ai.SearchResult{
		{Title: "Sheet Pan Chicken Fajitas", URL: "https://example.com/fajitas", Source: "example.com", Description: "Quick weeknight chicken fajitas on one pan."},
		{Title: "Peanut Chicken Stir-Fry", URL: "https://example.com/peanut", Source: "example.com", Description: "Chicken tossed in a rich peanut sauce."},
		{Title: "Black Bean Veggie Tacos", URL: "https://example.com/tacos", Source: "example.com", Description: "Vegetarian tacos with black beans and slaw."},
	}
}

func TestFindRecipes_HappyPath(t *testing.T) {
	candidates := finderCandidates()
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return candidates, nil
		},
	}

	var gotRankReq ai.FinderRankRequest
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			gotRankReq = req
			return &ai.FinderRankResult{
				Ranked: []ai.FinderRanking{
					{Index: 0, Reason: "Fast and family-friendly."},
					{Index: 2, Reason: "A solid vegetarian option."},
					{Index: 1, Reason: "Classic comfort flavor."},
				},
				BroadenQueries: []string{"easy chicken dinners"},
			}, nil
		},
	}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	events := runFinder(svc, testutil.TestUser(), FinderRequest{
		Facets:   FinderFacets{Protein: "chicken", TimeBudget: "30 minutes"},
		FreeText: "weeknight",
	})

	// Full happy-path event order.
	if got := eventTypes(events); !sameTypes(got,
		FinderEventSearching, FinderEventFound, FinderEventFiltering,
		FinderEventShortlist, FinderEventWarming, FinderEventRefineReady, FinderEventDone) {
		t.Fatalf("unexpected event order: %v", got)
	}

	// searching carries the deterministically-composed query.
	searching, _ := firstEventOfType(events, FinderEventSearching)
	if !strings.Contains(searching.Query, "chicken") || !strings.Contains(searching.Query, "recipe") {
		t.Errorf("searching query %q missing composed facets", searching.Query)
	}

	// found reports the real candidate count, not from cache.
	found, _ := firstEventOfType(events, FinderEventFound)
	if found.Count != len(candidates) {
		t.Errorf("found.Count = %d, want %d", found.Count, len(candidates))
	}
	if found.FromCache {
		t.Errorf("found.FromCache = true, want false (mock provider is not cached)")
	}

	// The ranker was handed the REAL candidates by index.
	if len(gotRankReq.Candidates) != len(candidates) {
		t.Fatalf("ranker got %d candidates, want %d", len(gotRankReq.Candidates), len(candidates))
	}
	if gotRankReq.Candidates[0].Index != 0 || gotRankReq.Candidates[0].Title != candidates[0].Title {
		t.Errorf("ranker candidate[0] = %+v, want index 0 title %q", gotRankReq.Candidates[0], candidates[0].Title)
	}

	// shortlist holds the real results in model rank order, each with a reason.
	shortlist, _ := firstEventOfType(events, FinderEventShortlist)
	if len(shortlist.Items) != 3 {
		t.Fatalf("shortlist has %d items, want 3", len(shortlist.Items))
	}
	if shortlist.Items[0].Result.Title != candidates[0].Title {
		t.Errorf("shortlist[0] title = %q, want %q", shortlist.Items[0].Result.Title, candidates[0].Title)
	}
	if shortlist.Items[1].Result.Title != candidates[2].Title {
		t.Errorf("shortlist[1] title = %q, want %q (model reordered)", shortlist.Items[1].Result.Title, candidates[2].Title)
	}
	for i, item := range shortlist.Items {
		if strings.TrimSpace(item.Reason) == "" {
			t.Errorf("shortlist item %d has no reason", i)
		}
	}

	// warming lists the top real URLs.
	warming, _ := firstEventOfType(events, FinderEventWarming)
	if len(warming.URLs) == 0 || warming.URLs[0] != candidates[0].URL {
		t.Errorf("warming URLs = %v, want first = %q", warming.URLs, candidates[0].URL)
	}

	// refine_ready offers the bounded chips + broaden suggestions.
	refine, _ := firstEventOfType(events, FinderEventRefineReady)
	if len(refine.Chips) == 0 {
		t.Errorf("refine_ready has no chips")
	}
	if len(refine.Broaden) == 0 || refine.Broaden[0] != "easy chicken dinners" {
		t.Errorf("refine_ready broaden = %v, want model's suggestion", refine.Broaden)
	}
}

func TestFindRecipes_DropsAllergenAvoid(t *testing.T) {
	candidates := finderCandidates()
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return candidates, nil
		},
	}

	var gotRankReq ai.FinderRankRequest
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			gotRankReq = req
			return &ai.FinderRankResult{
				Ranked: []ai.FinderRanking{
					{Index: 0, Reason: "Fast and family-friendly."},
					{Index: 1, Reason: "Peanut sauce.", Safety: []ai.MemberSafety{
						{MemberName: "Kiddo", Status: "avoid", Note: "contains peanuts"},
					}},
					{Index: 2, Reason: "Vegetarian, no allergens."},
				},
			}, nil
		},
	}

	// A family with a peanut allergy exercises the diet-context path.
	familyRepo := &testutil.MockFamilyRepo{
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{
				ID:      1,
				OwnerID: ownerID,
				Members: []models.FamilyMember{
					{
						Name: "Kiddo",
						DietaryProfile: &models.DietaryProfile{
							Allergies:    models.AllergyList{{Name: "peanuts", Severity: "severe"}},
							Restrictions: models.StringList{"vegetarian"},
						},
					},
				},
			}, nil
		},
	}

	svc := newFinderService(searchProvider, ranker, familyRepo)
	events := runFinder(svc, testutil.TestUser(), FinderRequest{
		Facets: FinderFacets{Protein: "chicken"},
	})

	// Same overall trajectory (a shortlist still forms from the survivors).
	if got := eventTypes(events); !sameTypes(got,
		FinderEventSearching, FinderEventFound, FinderEventFiltering,
		FinderEventShortlist, FinderEventWarming, FinderEventRefineReady, FinderEventDone) {
		t.Fatalf("unexpected event order: %v", got)
	}

	// The allergen-"avoid" candidate is dropped; the other two survive in order.
	shortlist, _ := firstEventOfType(events, FinderEventShortlist)
	if len(shortlist.Items) != 2 {
		t.Fatalf("shortlist has %d items, want 2 (the avoid candidate dropped)", len(shortlist.Items))
	}
	for _, item := range shortlist.Items {
		if item.Result.Title == candidates[1].Title {
			t.Errorf("allergen-avoid candidate %q was not dropped", candidates[1].Title)
		}
	}
	if shortlist.Items[0].Result.Title != candidates[0].Title || shortlist.Items[1].Result.Title != candidates[2].Title {
		t.Errorf("survivors = [%q, %q], want [%q, %q]",
			shortlist.Items[0].Result.Title, shortlist.Items[1].Result.Title, candidates[0].Title, candidates[2].Title)
	}

	// Diet context reached the model + the query, sourced server-side.
	if !strings.Contains(strings.ToLower(gotRankReq.DietSummary), "peanuts") {
		t.Errorf("diet summary %q missing the peanut allergy", gotRankReq.DietSummary)
	}
	searching, _ := firstEventOfType(events, FinderEventSearching)
	if !strings.Contains(searching.Query, "-peanuts") {
		t.Errorf("query %q missing the hard allergen exclude", searching.Query)
	}
}

func TestFindRecipes_EmptyNeverInvents(t *testing.T) {
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return nil, nil // no results
		},
	}

	rankerCalled := false
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			rankerCalled = true
			return &ai.FinderRankResult{}, nil
		},
	}

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	events := runFinder(svc, testutil.TestUser(), FinderRequest{
		Facets: FinderFacets{Cuisine: "thai", Protein: "tofu"},
	})

	// Exactly one empty event, and never a shortlist.
	if n := countEventsOfType(events, FinderEventEmpty); n != 1 {
		t.Fatalf("got %d empty events, want exactly 1 (order: %v)", n, eventTypes(events))
	}
	if n := countEventsOfType(events, FinderEventShortlist); n != 0 {
		t.Errorf("got %d shortlist events on the empty path, want 0", n)
	}

	// The model is never called (nothing to rank → nothing to invent).
	if rankerCalled {
		t.Errorf("ranker was called on the empty path; the finder must never invent")
	}

	// The empty event carries broaden suggestions (deterministic fallback).
	empty, _ := firstEventOfType(events, FinderEventEmpty)
	if len(empty.Broaden) == 0 {
		t.Errorf("empty event has no broaden suggestions")
	}

	// No event fabricated a result.
	for _, ev := range events {
		if len(ev.Items) != 0 {
			t.Errorf("event %s carried %d fabricated items on the empty path", ev.Type, len(ev.Items))
		}
	}
}

func TestFindRecipes_Offset(t *testing.T) {
	makeCandidates := func(n int) []ai.SearchResult {
		out := make([]ai.SearchResult, n)
		for i := range out {
			out[i] = ai.SearchResult{
				Title:       fmt.Sprintf("Recipe %d", i),
				URL:         fmt.Sprintf("https://example.com/r%d", i),
				Source:      "example.com",
				Description: "desc",
			}
		}
		return out
	}

	// Rank only the first two candidates, so the shortlist is deliberately
	// shorter than the search page — HasMore must still reflect the search page,
	// never len(items).
	ranker := &testutil.MockTextProvider{
		ExpandAndRankRecipesFunc: func(ctx context.Context, req ai.FinderRankRequest) (*ai.FinderRankResult, error) {
			return &ai.FinderRankResult{
				Ranked: []ai.FinderRanking{{Index: 0, Reason: "a"}, {Index: 1, Reason: "b"}},
			}, nil
		},
	}

	cases := []struct {
		name        string
		reqOffset   int
		pageSize    int // results the provider returns
		wantOffset  int
		wantHasMore bool
	}{
		{name: "full page has more", reqOffset: 10, pageSize: finderSearchCount, wantOffset: 10, wantHasMore: true},
		{name: "short page no more", reqOffset: 20, pageSize: 3, wantOffset: 20, wantHasMore: false},
		{name: "negative offset clamped to zero", reqOffset: -5, pageSize: 3, wantOffset: 0, wantHasMore: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotOffset int
			searchProvider := &testutil.MockSearchProvider{
				SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
					gotOffset = offset
					return makeCandidates(tc.pageSize), nil
				},
			}
			svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
			events := runFinder(svc, testutil.TestUser(), FinderRequest{
				Facets: FinderFacets{Protein: "chicken"},
				Offset: tc.reqOffset,
			})

			// The requested offset (clamped) reaches the search provider.
			if gotOffset != tc.wantOffset {
				t.Errorf("search offset = %d, want %d", gotOffset, tc.wantOffset)
			}

			// HasMore rides the shortlist event and reflects the search page.
			shortlist, ok := firstEventOfType(events, FinderEventShortlist)
			if !ok {
				t.Fatalf("no shortlist event (order: %v)", eventTypes(events))
			}
			if shortlist.HasMore != tc.wantHasMore {
				t.Errorf("shortlist.HasMore = %v, want %v", shortlist.HasMore, tc.wantHasMore)
			}
			// The shortlist stays shorter than the page (proves HasMore is not len(items)).
			if len(shortlist.Items) != 2 {
				t.Errorf("shortlist has %d items, want 2 (ranker returned 2)", len(shortlist.Items))
			}
		})
	}
}
