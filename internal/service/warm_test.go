package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// twoRecipeJSONLDHTML is a page exposing two distinct recipes in JSON-LD (a
// structured-data listicle).
func twoRecipeJSONLDHTML() string {
	return `<html><head>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Recipe","name":"Beef Stew","recipeIngredient":["beef"],"recipeInstructions":[{"@type":"HowToStep","text":"cook"}]}</script>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Recipe","name":"Chicken Soup","recipeIngredient":["chicken"],"recipeInstructions":[{"@type":"HowToStep","text":"simmer"}]}</script>
</head><body></body></html>`
}

func warmRepoCapturing(upserted **models.CanonicalRecipe) *testutil.MockCanonicalRecipeRepo {
	return &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(string) (*models.CanonicalRecipe, error) {
			return nil, errors.New("miss")
		},
		UpsertFunc: func(e *models.CanonicalRecipe) error { *upserted = e; return nil },
	}
}

func TestWarmURL_JSONLDSingle_CachesWithoutAI(t *testing.T) {
	var upserted *models.CanonicalRecipe
	// A provider that fails the test if any AI method runs — a JSON-LD page must
	// warm for free.
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(context.Context, string, string) (string, error) {
			t.Error("CookingQA should not run for a JSON-LD page")
			return "", nil
		},
		ExtractRecipeFromTextFunc: func(context.Context, string, string) (*ai.RecipeResult, error) {
			t.Error("AI extraction should not run for a JSON-LD page")
			return nil, nil
		},
	}
	imp := newTestImportService(testutil.NewMockRecipeRepo(), provider, nil)
	imp.CanonicalRepo = warmRepoCapturing(&upserted)
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return []byte(jsonLDHTML()), 200, nil
	}
	resolver := NewMultiRecipeResolver(NewMultiRecipeRegistry(), imp)

	if err := imp.WarmURL(context.Background(), resolver, "https://site.com/pancakes"); err != nil {
		t.Fatalf("WarmURL: %v", err)
	}
	if upserted == nil {
		t.Fatal("expected a canonical upsert")
	}
	if upserted.IsMultiPage {
		t.Error("a single recipe must not be marked multi")
	}
	if upserted.RecipeData.Title != "Classic Pancakes" {
		t.Errorf("cached title = %q, want 'Classic Pancakes'", upserted.RecipeData.Title)
	}
}

func TestWarmURL_JSONLDListicle_MarksMulti(t *testing.T) {
	var upserted *models.CanonicalRecipe
	imp := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	imp.CanonicalRepo = warmRepoCapturing(&upserted)
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return []byte(twoRecipeJSONLDHTML()), 200, nil
	}
	resolver := NewMultiRecipeResolver(NewMultiRecipeRegistry(), imp)

	if err := imp.WarmURL(context.Background(), resolver, "https://site.com/30-dinners"); err != nil {
		t.Fatalf("WarmURL: %v", err)
	}
	if upserted == nil || !upserted.IsMultiPage {
		t.Fatalf("expected an IsMultiPage marker, got %+v", upserted)
	}
	if len(upserted.RecipeData.Ingredients) != 0 {
		t.Error("a multi marker must carry empty recipe data (no sub-recipe extraction)")
	}
}

func TestWarmURL_NoJSONLD_UsesAI(t *testing.T) {
	var upserted *models.CanonicalRecipe
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(context.Context, string, string) (string, error) {
			return "SINGLE", nil // not a collection
		},
		ExtractRecipeFromTextFunc: func(context.Context, string, string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	imp := newTestImportService(testutil.NewMockRecipeRepo(), provider, nil)
	imp.CanonicalRepo = warmRepoCapturing(&upserted)
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return []byte(plainHTML()), 200, nil
	}
	resolver := NewMultiRecipeResolver(NewMultiRecipeRegistry(), imp)

	if err := imp.WarmURL(context.Background(), resolver, "https://site.com/plain"); err != nil {
		t.Fatalf("WarmURL: %v", err)
	}
	if upserted == nil || upserted.IsMultiPage {
		t.Fatalf("expected a single-recipe cache write, got %+v", upserted)
	}
	if upserted.ExtractionMethod != models.ExtractionHaiku {
		t.Errorf("method = %q, want the AI (haiku) fallback", upserted.ExtractionMethod)
	}
}

func TestWarmURLs_Statuses(t *testing.T) {
	multi := &models.CanonicalRecipe{NormalizedURL: "m", IsMultiPage: true, RecipeData: models.RecipeDef{}}
	single := testutil.TestCanonicalRecipe()
	canon := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(norm string) (*models.CanonicalRecipe, error) {
			switch {
			case strings.Contains(norm, "cached"):
				return single, nil
			case strings.Contains(norm, "collection"):
				return multi, nil
			default:
				return nil, errors.New("miss")
			}
		},
		UpsertFunc: func(*models.CanonicalRecipe) error { return nil },
	}
	imp := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	imp.CanonicalRepo = canon
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return []byte(jsonLDHTML()), 200, nil // the background warm of the uncached URL is harmless
	}
	resolver := NewMultiRecipeResolver(NewMultiRecipeRegistry(), imp)
	w := NewWarmService(imp, resolver, 2, 0)

	statuses := w.WarmURLs([]string{
		"https://site.com/cached-pie",
		"https://site.com/collection-30",
		"https://site.com/new-dish",
	})
	if got := statuses["https://site.com/cached-pie"]; got != WarmCached {
		t.Errorf("cached URL = %q, want %q", got, WarmCached)
	}
	if got := statuses["https://site.com/collection-30"]; got != WarmMulti {
		t.Errorf("collection URL = %q, want %q", got, WarmMulti)
	}
	if got := statuses["https://site.com/new-dish"]; got != WarmExtracting {
		t.Errorf("new URL = %q, want %q", got, WarmExtracting)
	}
}

func TestWarmURLs_SafetyCeiling(t *testing.T) {
	canon := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(string) (*models.CanonicalRecipe, error) {
			return nil, errors.New("miss")
		},
		UpsertFunc: func(*models.CanonicalRecipe) error { return nil },
	}
	imp := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	imp.CanonicalRepo = canon
	imp.HTTPFetchOverride = func(context.Context, string) ([]byte, int, error) {
		return []byte(jsonLDHTML()), 200, nil
	}
	resolver := NewMultiRecipeResolver(NewMultiRecipeRegistry(), imp)
	w := NewWarmService(imp, resolver, 2, 1) // ceiling: 1 warm/day

	first := w.WarmURLs([]string{"https://site.com/a"})
	if got := first["https://site.com/a"]; got != WarmExtracting {
		t.Fatalf("first URL = %q, want %q", got, WarmExtracting)
	}
	second := w.WarmURLs([]string{"https://site.com/b"})
	if got := second["https://site.com/b"]; got != WarmUncached {
		t.Errorf("second URL (over ceiling) = %q, want %q", got, WarmUncached)
	}
}
