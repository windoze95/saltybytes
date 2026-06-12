package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// upsertRecorder is a thread-safe capture of canonical-recipe upserts; the
// resolver extracts cards on up to 3 concurrent goroutines.
type upsertRecorder struct {
	mu      sync.Mutex
	entries []models.CanonicalRecipe
}

func (u *upsertRecorder) record(entry *models.CanonicalRecipe) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.entries = append(u.entries, *entry)
	return nil
}

func (u *upsertRecorder) snapshot() []models.CanonicalRecipe {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]models.CanonicalRecipe, len(u.entries))
	copy(out, u.entries)
	return out
}

// multiRecipeHTML returns a page with the given JSON-LD recipe titles.
func multiRecipeHTML(titles ...string) string {
	var b strings.Builder
	b.WriteString("<html><head>")
	for _, title := range titles {
		fmt.Fprintf(&b, `<script type="application/ld+json">{"@type":"Recipe","name":%q}</script>`, title)
	}
	b.WriteString("</head><body><h1>Roundup</h1></body></html>")
	return b.String()
}

// newResolverForTest wires a resolver whose import service uses the given
// preview provider and canonical repo.
func newResolverForTest(preview ai.TextProvider, canonicalRepo *testutil.MockCanonicalRecipeRepo) *MultiRecipeResolver {
	svc := newTestImportService(testutil.NewMockRecipeRepo(), nil, preview)
	if canonicalRepo != nil {
		svc.CanonicalRepo = canonicalRepo
	}
	return NewMultiRecipeResolver(NewMultiRecipeRegistry(), svc)
}

// waitResolved polls the entry until background extraction finishes.
func waitResolved(t *testing.T, entry *MultiRecipeEntry) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if entry.GetStatus() == "resolved" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("entry never resolved; status = %q", entry.GetStatus())
}

func TestResolveFromHTML_JSONLDMultiRecipe(t *testing.T) {
	var detectionCalled atomic.Bool
	var extractInputs sync.Map
	preview := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			detectionCalled.Store(true)
			return "SINGLE", nil
		},
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			extractInputs.Store(text, true)
			if unitSystem != ai.UnitSystemPreserveSource {
				t.Errorf("ExtractRecipeFromText unitSystem = %q, want preserve-source sentinel", unitSystem)
			}
			return testutil.TestRecipeResult(), nil
		},
	}
	recorder := &upsertRecorder{}
	resolver := newResolverForTest(preview, &testutil.MockCanonicalRecipeRepo{UpsertFunc: recorder.record})

	html := multiRecipeHTML("Pancakes", "Waffles")
	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/breakfast", html)
	if entry == nil {
		t.Fatal("ResolveFromHTML returned nil for a multi-recipe page")
	}
	if detectionCalled.Load() {
		t.Error("AI detection ran even though JSON-LD already found multiple recipes")
	}

	waitResolved(t, entry)

	cards := entry.GetCards()
	if len(cards) != 2 {
		t.Fatalf("entry has %d cards, want 2", len(cards))
	}
	for i, card := range cards {
		if card.ExtractionStatus != "done" {
			t.Errorf("card[%d].ExtractionStatus = %q, want 'done'", i, card.ExtractionStatus)
		}
		if card.RecipeDef == nil {
			t.Errorf("card[%d].RecipeDef is nil after extraction", i)
			continue
		}
		if card.RecipeDef.SourceURL != "https://example.com/breakfast" {
			t.Errorf("card[%d].RecipeDef.SourceURL = %q, want the page URL", i, card.RecipeDef.SourceURL)
		}
		if len(card.Hashtags) == 0 {
			t.Errorf("card[%d].Hashtags empty, want hashtags from the extraction result", i)
		}
	}
	if entry.ResolvedAt == nil {
		t.Error("entry.ResolvedAt not set after resolution")
	}

	// Each card's extraction prompt must target its own title.
	for _, title := range []string{"Pancakes", "Waffles"} {
		found := false
		extractInputs.Range(func(key, _ interface{}) bool {
			if strings.Contains(key.(string), fmt.Sprintf("%q", title)) {
				found = true
				return false
			}
			return true
		})
		if !found {
			t.Errorf("no extraction input constrained to title %q", title)
		}
	}

	// Both extractions cached under distinct canonical keys.
	upserts := recorder.snapshot()
	if len(upserts) != 2 {
		t.Fatalf("canonical upserts = %d, want 2", len(upserts))
	}
	if upserts[0].NormalizedURL == upserts[1].NormalizedURL {
		t.Errorf("both cards cached under the same canonical key %q", upserts[0].NormalizedURL)
	}
	for i, up := range upserts {
		if !strings.Contains(up.OriginalURL, "_recipe=") {
			t.Errorf("upsert[%d].OriginalURL = %q, want a _recipe= slug param", i, up.OriginalURL)
		}
		if up.ExtractionMethod != models.ExtractionHaiku {
			t.Errorf("upsert[%d].ExtractionMethod = %q, want haiku", i, up.ExtractionMethod)
		}
	}
}

func TestResolveFromHTML_SingleRecipeNotMulti(t *testing.T) {
	preview := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return "SINGLE", nil
		},
	}
	resolver := newResolverForTest(preview, nil)

	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/one", multiRecipeHTML("Lone Recipe"))
	if entry != nil {
		t.Errorf("ResolveFromHTML = %+v, want nil for a single-recipe page", entry)
	}
	if tracked := resolver.Registry.Get("https://example.com/one"); tracked != nil {
		t.Error("single-recipe URL should not be registered")
	}
}

func TestResolveFromHTML_AIFallbackDetection(t *testing.T) {
	preview := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			// Includes noise lines the detector must filter out.
			return "Slow Cooker Chili\n\nok\nWhite Chicken Chili\nVegetarian Chili", nil
		},
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	resolver := newResolverForTest(preview, nil)

	html := `<html><body><h1>15 Chili Recipes</h1><p>No JSON-LD here.</p></body></html>`
	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/chili-roundup", html)
	if entry == nil {
		t.Fatal("ResolveFromHTML returned nil; AI fallback should have detected multiple recipes")
	}

	waitResolved(t, entry)

	cards := entry.GetCards()
	if len(cards) != 3 {
		t.Fatalf("entry has %d cards, want 3 (noise filtered)", len(cards))
	}
	wantTitles := []string{"Slow Cooker Chili", "White Chicken Chili", "Vegetarian Chili"}
	for i, want := range wantTitles {
		if cards[i].Title != want {
			t.Errorf("card[%d].Title = %q, want %q", i, cards[i].Title, want)
		}
		if cards[i].ExtractionStatus != "done" {
			t.Errorf("card[%d].ExtractionStatus = %q, want 'done'", i, cards[i].ExtractionStatus)
		}
	}
}

func TestResolveFromHTML_AlreadyRegisteredSkipsReextraction(t *testing.T) {
	var extractCalls atomic.Int32
	preview := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			extractCalls.Add(1)
			return testutil.TestRecipeResult(), nil
		},
	}
	resolver := newResolverForTest(preview, nil)

	existing, _ := resolver.Registry.Register("https://example.com/breakfast")

	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/breakfast", multiRecipeHTML("Pancakes", "Waffles"))
	if entry != existing {
		t.Error("ResolveFromHTML should return the already-registered entry")
	}
	// No new extraction work was started for the duplicate resolve.
	time.Sleep(50 * time.Millisecond)
	if n := extractCalls.Load(); n != 0 {
		t.Errorf("extraction ran %d times for an already-registered URL, want 0", n)
	}
}

func TestResolveFromHTML_ExtractionFailureMarksCardFailed(t *testing.T) {
	preview := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			if strings.Contains(text, `"Waffles"`) {
				return nil, errors.New("model exploded")
			}
			return testutil.TestRecipeResult(), nil
		},
	}
	resolver := newResolverForTest(preview, nil)

	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/breakfast", multiRecipeHTML("Pancakes", "Waffles"))
	if entry == nil {
		t.Fatal("ResolveFromHTML returned nil")
	}
	waitResolved(t, entry)

	byTitle := map[string]MultiRecipeCard{}
	for _, c := range entry.GetCards() {
		byTitle[c.Title] = c
	}
	if got := byTitle["Pancakes"].ExtractionStatus; got != "done" {
		t.Errorf("Pancakes status = %q, want 'done'", got)
	}
	if got := byTitle["Waffles"].ExtractionStatus; got != "failed" {
		t.Errorf("Waffles status = %q, want 'failed'", got)
	}
	if byTitle["Waffles"].RecipeDef != nil {
		t.Error("failed card should not carry a RecipeDef")
	}
}

func TestResolveFromHTML_NoProviderMarksAllCardsFailed(t *testing.T) {
	// JSON-LD detection needs no AI, but extraction does; with no provider
	// every card must fail without panicking and the entry still resolves.
	resolver := newResolverForTest(nil, nil)

	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/breakfast", multiRecipeHTML("Pancakes", "Waffles"))
	if entry == nil {
		t.Fatal("ResolveFromHTML returned nil")
	}
	waitResolved(t, entry)

	for i, card := range entry.GetCards() {
		if card.ExtractionStatus != "failed" {
			t.Errorf("card[%d].ExtractionStatus = %q, want 'failed' with no provider", i, card.ExtractionStatus)
		}
	}
}

func TestExtractAllRecipes_CanonicalKeysSurviveSlugCollisions(t *testing.T) {
	preview := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	recorder := &upsertRecorder{}
	resolver := newResolverForTest(preview, &testutil.MockCanonicalRecipeRepo{UpsertFunc: recorder.record})

	// "Best Brownies!" and "Best Brownies?" slugify identically; the
	// non-ASCII titles slugify to empty strings. All four must still get
	// distinct canonical cache keys.
	html := multiRecipeHTML("Best Brownies!", "Best Brownies?", "クッキー", "ケーキ")
	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/sweets?page=2", html)
	if entry == nil {
		t.Fatal("ResolveFromHTML returned nil")
	}
	waitResolved(t, entry)

	upserts := recorder.snapshot()
	if len(upserts) != 4 {
		t.Fatalf("canonical upserts = %d, want 4", len(upserts))
	}
	seen := make(map[string]string)
	for _, up := range upserts {
		if prev, dup := seen[up.NormalizedURL]; dup {
			t.Errorf("canonical key collision: %q used by %q and %q", up.NormalizedURL, prev, up.OriginalURL)
		}
		seen[up.NormalizedURL] = up.OriginalURL
		// Source URL already has a query string, so the slug must be
		// appended with '&', not a second '?'.
		if strings.Count(up.OriginalURL, "?") != 1 {
			t.Errorf("OriginalURL %q malformed; want exactly one '?'", up.OriginalURL)
		}
	}
}

func TestResolveFromURL_FetchesAndResolves(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, multiRecipeHTML("Pancakes", "Waffles", "Crepes"))
	}))
	defer server.Close()

	preview := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	resolver := newResolverForTest(preview, nil)

	pageURL := server.URL + "/breakfast-roundup"
	entry := resolver.ResolveFromURL(context.Background(), pageURL)
	if entry == nil {
		t.Fatal("ResolveFromURL returned nil for a multi-recipe page")
	}
	if entry.SourceURL != pageURL {
		t.Errorf("entry.SourceURL = %q, want %q", entry.SourceURL, pageURL)
	}
	waitResolved(t, entry)
	if got := len(entry.GetCards()); got != 3 {
		t.Errorf("entry has %d cards, want 3", got)
	}
}

func TestResolveFromURL_AlreadyTrackedSkipsFetch(t *testing.T) {
	var fetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		fmt.Fprint(w, multiRecipeHTML("Pancakes", "Waffles"))
	}))
	defer server.Close()

	resolver := newResolverForTest(nil, nil)
	pageURL := server.URL + "/tracked"
	existing, _ := resolver.Registry.Register(pageURL)

	entry := resolver.ResolveFromURL(context.Background(), pageURL)
	if entry != existing {
		t.Error("ResolveFromURL should return the tracked entry")
	}
	if n := fetches.Load(); n != 0 {
		t.Errorf("ResolveFromURL fetched %d times for a tracked URL, want 0", n)
	}
}

func TestResolveFromURL_FetchFailureReturnsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	resolver := newResolverForTest(nil, nil)
	if entry := resolver.ResolveFromURL(context.Background(), server.URL+"/missing"); entry != nil {
		t.Errorf("ResolveFromURL = %+v, want nil on fetch failure", entry)
	}
}

func TestResolveFromURL_SingleRecipePageReturnsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, multiRecipeHTML("Lone Recipe"))
	}))
	defer server.Close()

	preview := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return "SINGLE", nil
		},
	}
	resolver := newResolverForTest(preview, nil)

	if entry := resolver.ResolveFromURL(context.Background(), server.URL+"/single"); entry != nil {
		t.Errorf("ResolveFromURL = %+v, want nil for a single-recipe page", entry)
	}
}
