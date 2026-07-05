package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func TestPreferOwnPageCards(t *testing.T) {
	page := "https://example.com/roundup"
	cards := []MultiRecipeCard{
		{Title: "Inline One", SourceURL: page},
		{Title: "Own A", SourceURL: "https://example.com/recipe/own-a"},
		{Title: "Inline Two", SourceURL: page},
		{Title: "Own B", SourceURL: "https://example.com/recipe/own-b"},
	}
	got := preferOwnPageCards(cards, page)
	wantOrder := []string{"Own A", "Own B", "Inline One", "Inline Two"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d cards, want %d", len(got), len(wantOrder))
	}
	for i, title := range wantOrder {
		if got[i].Title != title {
			t.Errorf("card[%d] = %q, want %q (own-page cards first, stable)", i, got[i].Title, title)
		}
	}
}

// TestExtractSingleCard_DigSkipsInlineAI locks the dig-context cost guard: a
// card with no own recipe page and no matching inline JSON-LD is dropped
// WITHOUT calling the AI text extractor (in prod that path failed 10/10,
// burning seconds and light-tier quota per card). Non-dig flows keep the AI
// fallback.
func TestExtractSingleCard_DigSkipsInlineAI(t *testing.T) {
	page := "https://example.com/roundup"
	// JSON-LD blocks carry names only (no ingredients), so inline JSON-LD
	// extraction misses and the AI fallback is the next step.
	html := multiRecipeHTML("Pancakes", "Waffles")

	t.Run("finder_dig skips AI and fails the card", func(t *testing.T) {
		var aiCalls atomic.Int32
		preview := &testutil.MockTextProvider{
			ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
				aiCalls.Add(1)
				return testutil.TestRecipeResult(), nil
			},
		}
		resolver := newResolverForTest(preview, &testutil.MockCanonicalRecipeRepo{})

		ctx := WithExtractionOrigin(context.Background(), ExtractionOriginFinderDig)
		entry := resolver.ResolveFromHTML(ctx, page, html)
		if entry == nil {
			t.Fatal("ResolveFromHTML returned nil for a multi-recipe page")
		}
		waitResolved(t, entry)

		if n := aiCalls.Load(); n != 0 {
			t.Errorf("AI extractor called %d times during digging, want 0", n)
		}
		for _, card := range entry.GetCards() {
			if card.ExtractionStatus != "failed" {
				t.Errorf("dig card %q status = %q, want failed (skipped, not extracted)", card.Title, card.ExtractionStatus)
			}
		}
	})

	t.Run("preview flow keeps the AI fallback", func(t *testing.T) {
		var aiCalls atomic.Int32
		preview := &testutil.MockTextProvider{
			ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
				aiCalls.Add(1)
				return testutil.TestRecipeResult(), nil
			},
		}
		resolver := newResolverForTest(preview, &testutil.MockCanonicalRecipeRepo{})

		entry := resolver.ResolveFromHTML(context.Background(), page, html)
		if entry == nil {
			t.Fatal("ResolveFromHTML returned nil for a multi-recipe page")
		}
		waitResolved(t, entry)

		if n := aiCalls.Load(); n == 0 {
			t.Error("AI extractor never called in a non-dig flow, want fallback to run")
		}
		for _, card := range entry.GetCards() {
			if card.ExtractionStatus != "done" {
				t.Errorf("preview card %q status = %q, want done", card.Title, card.ExtractionStatus)
			}
		}
	})
}
