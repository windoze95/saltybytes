package ai

import (
	"context"
	"os"
	"testing"
)

// TestExpandAndRankRecipes_Live_FlagsCollection runs the real light tier (Gemini
// Flash) against the new prompt + schema and asserts it flags a collection page
// (expand=true) but not a single recipe. This is the harness check the finder
// depends on.
//
// Run with:
//
//	FINDER_LIVE_TEST=1 \
//	LIGHT_API_KEY=<gemini key> LIGHT_BASE_URL=<gemini openai-compat base> LIGHT_MODEL=gemini-2.5-flash \
//	go test ./internal/ai/ -run TestExpandAndRankRecipes_Live_FlagsCollection -v
func TestExpandAndRankRecipes_Live_FlagsCollection(t *testing.T) {
	if os.Getenv("FINDER_LIVE_TEST") == "" {
		t.Skip("set FINDER_LIVE_TEST=1 + a light-tier key (LIGHT_API_KEY/GEMINI_API_KEY), optional LIGHT_BASE_URL/LIGHT_MODEL, to run the live ranker collection-flagging test")
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

	res, err := p.ExpandAndRankRecipes(context.Background(), FinderRankRequest{
		Facets:   "protein: chicken; occasion: weeknight dinner",
		FreeText: "quick",
		Candidates: []FinderCandidate{
			{Index: 0, Title: "Sheet-Pan Lemon Garlic Chicken", URL: "https://cookingsite.example/recipe/sheet-pan-lemon-garlic-chicken", Source: "cookingsite.example", Description: "A one-pan weeknight chicken dinner with lemon and garlic."},
			{Index: 1, Title: "40 Easy Weeknight Dinner Recipes", URL: "https://cookingsite.example/gallery/40-easy-weeknight-dinner-recipes", Source: "cookingsite.example", Description: "Our favorite quick dinners for busy nights."},
		},
	})
	if err != nil {
		t.Fatalf("live ExpandAndRankRecipes failed: %v", err)
	}

	byIndex := make(map[int]FinderRanking, len(res.Ranked))
	for _, r := range res.Ranked {
		byIndex[r.Index] = r
	}
	if r, ok := byIndex[1]; !ok || !r.Expand {
		t.Errorf("collection (index 1, '40 Easy Weeknight Dinner Recipes') not flagged expand=true; ranked=%+v", res.Ranked)
	}
	if r, ok := byIndex[0]; ok && r.Expand {
		t.Errorf("single recipe (index 0) wrongly flagged expand=true; ranked=%+v", res.Ranked)
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
