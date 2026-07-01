package ai

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// prodShapedRankRequest mirrors what a real prod finder run sends the ranker:
// a full 10-candidate search page (with several roundup/collection hits), a
// multi-member family diet summary, and personalization context. The original
// live test used 2 candidates and no family — it passed while prod failed,
// because Gemini 2.5 Flash spends completion budget on thinking and a
// prod-sized payload thinks well past a small MaxTokens before emitting the
// forced tool call.
func prodShapedRankRequest() FinderRankRequest {
	return FinderRankRequest{
		Facets:      "occasion: quick weeknight; time: under 30 minutes; protein: chicken",
		FreeText:    "something cozy the kids will actually eat",
		DietSummary: "Junior: allergic to peanuts, intolerant to lactose; Sam: vegetarian on weekdays, no cilantro; Alex: allergic to shellfish, low sodium",
		UnitSystem:  "US customary",
		CookingContext: "Cooks for a family of five on weeknights; prefers one-pan meals " +
			"and minimal cleanup; has an instant pot and an air fryer.",
		Requirements: "Avoid deep frying. Prefer recipes with a make-ahead component.",
		Candidates: []FinderCandidate{
			{Index: 0, Title: "Sheet-Pan Lemon Garlic Chicken Thighs", URL: "https://cooking.example/recipes/sheet-pan-lemon-garlic-chicken", Source: "cooking.example", Description: "Crispy chicken thighs roasted with baby potatoes and green beans in one pan — a 35-minute weeknight staple."},
			{Index: 1, Title: "40 Easy Weeknight Dinner Recipes", URL: "https://mealsite.example/gallery/40-easy-weeknight-dinners", Source: "mealsite.example", Description: "Our 40 favorite quick dinners for busy nights, from pastas to stir-fries."},
			{Index: 2, Title: "Creamy Tuscan Chicken Pasta", URL: "https://pastalove.example/creamy-tuscan-chicken-pasta", Source: "pastalove.example", Description: "Sun-dried tomatoes, spinach and parmesan in a silky cream sauce over penne, ready in 30 minutes."},
			{Index: 3, Title: "25 Best Chicken Dinner Ideas for Busy Families", URL: "https://familyeats.example/roundups/25-best-chicken-dinners", Source: "familyeats.example", Description: "From casseroles to skillet meals, twenty-five reader-favorite chicken dinners."},
			{Index: 4, Title: "One-Pot Chicken and Rice", URL: "https://simplesuppers.example/one-pot-chicken-and-rice", Source: "simplesuppers.example", Description: "A comforting one-pot classic with turmeric rice and juicy chicken, minimal cleanup."},
			{Index: 5, Title: "Air Fryer Chicken Tenders (Kid Favorite!)", URL: "https://airfrieddaily.example/chicken-tenders", Source: "airfrieddaily.example", Description: "Crunchy panko tenders in 15 minutes with a honey mustard dip kids love."},
			{Index: 6, Title: "Slow Cooker Salsa Chicken Tacos", URL: "https://tacotuesday.example/slow-cooker-salsa-chicken", Source: "tacotuesday.example", Description: "Three-ingredient shredded chicken tacos that cook while you work."},
			{Index: 7, Title: "31 Quick Dinners to Make in July", URL: "https://seasonalplates.example/collections/31-quick-july-dinners", Source: "seasonalplates.example", Description: "A month of fast summer dinner inspiration, one for every night."},
			{Index: 8, Title: "Skillet Chicken Parmesan", URL: "https://weeknightitalian.example/skillet-chicken-parm", Source: "weeknightitalian.example", Description: "Lightly breaded cutlets under bubbling mozzarella and marinara, no oven needed."},
			{Index: 9, Title: "Instant Pot Chicken Noodle Soup", URL: "https://pressureluck.example/instant-pot-chicken-noodle", Source: "pressureluck.example", Description: "Classic comfort in 25 minutes, with shredded rotisserie-style chicken and egg noodles."},
		},
	}
}

// TestExpandAndRankRecipes_Live_ProdShapedStress runs the REAL light tier with
// a prod-shaped payload several times in a row and requires every attempt to
// succeed AND to flag the three collection candidates. This is the regression
// bar for the "no rank_recipes tool call" / thinking-budget failure seen in
// prod on 2026-07-01 (a run that returns no tool call or misses the obvious
// roundups means the harness regressed).
//
// Run with:
//
//	FINDER_LIVE_TEST=1 \
//	LIGHT_API_KEY=<gemini key> LIGHT_BASE_URL=<gemini openai-compat base> LIGHT_MODEL=gemini-2.5-flash \
//	go test ./internal/ai/ -run TestExpandAndRankRecipes_Live_ProdShapedStress -v
func TestExpandAndRankRecipes_Live_ProdShapedStress(t *testing.T) {
	if os.Getenv("FINDER_LIVE_TEST") == "" {
		t.Skip("set FINDER_LIVE_TEST=1 + a light-tier key (LIGHT_API_KEY/GEMINI_API_KEY), optional LIGHT_BASE_URL/LIGHT_MODEL, to run the prod-shaped live stress test")
	}
	apiKey := firstNonEmptyEnv("LIGHT_API_KEY", "GEMINI_API_KEY", "OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("no light-tier API key (LIGHT_API_KEY / GEMINI_API_KEY) set")
	}
	model := os.Getenv("LIGHT_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}

	p := NewOpenAICompatProvider(apiKey, os.Getenv("LIGHT_BASE_URL"), model, "gemini", testPrompts())

	const attempts = 5
	collectionIdx := map[int]bool{1: true, 3: true, 7: true}
	var failures []string
	for i := 0; i < attempts; i++ {
		res, err := p.ExpandAndRankRecipes(context.Background(), prodShapedRankRequest())
		if err != nil {
			failures = append(failures, fmt.Sprintf("attempt %d: call failed: %v", i+1, err))
			continue
		}
		// The product invariant: a collection candidate must NEVER be returned
		// with expand=false (that is what paints a roundup as a recipe card).
		// Being dropped from the ranking entirely is fine — dropped candidates
		// are never shown. And no single recipe may be flagged as a collection.
		anyCollectionHandled := false
		for _, r := range res.Ranked {
			if collectionIdx[r.Index] {
				if !r.Expand {
					failures = append(failures, fmt.Sprintf("attempt %d: collection candidate %d returned with expand=false", i+1, r.Index))
				} else {
					anyCollectionHandled = true
				}
			} else if r.Expand {
				failures = append(failures, fmt.Sprintf("attempt %d: single recipe %d wrongly flagged expand=true", i+1, r.Index))
			}
		}
		// The on-topic roundups (1: weeknight dinners, 3: chicken dinners) are
		// strong matches — at least one collection should be flagged for digging
		// rather than all of them dropped.
		if !anyCollectionHandled {
			failures = append(failures, fmt.Sprintf("attempt %d: no collection flagged expand=true at all (ranked=%v)", i+1, res.Ranked))
		}
	}

	if len(failures) > 0 {
		t.Fatalf("prod-shaped rank stress: %d problem(s) across %d attempts:\n%s",
			len(failures), attempts, joinLines(failures))
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += "  - " + l + "\n"
	}
	return out
}
