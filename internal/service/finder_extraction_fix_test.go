package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// fullJSONLDRecipe builds a JSON-LD Recipe object string with ingredients.
func fullJSONLDRecipe(name string, ings ...string) string {
	quoted := make([]string, len(ings))
	for i, s := range ings {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf(`{"@context":"https://schema.org","@type":"Recipe","name":%q,"recipeIngredient":[%s],"recipeInstructions":[{"@type":"HowToStep","text":"Cook it."}]}`,
		name, strings.Join(quoted, ","))
}

// --- FIX B1: extractJSONLDRecipeByTitle (JSON-LD-by-title, no AI, no truncation) ---

func TestExtractJSONLDRecipeByTitle_MatchesByTitle(t *testing.T) {
	html := `<html><head>` +
		`<script type="application/ld+json">` + fullJSONLDRecipe("Sheet Pan Chicken Fajitas", "1 lb chicken", "2 bell peppers") + `</script>` +
		`<script type="application/ld+json">` + fullJSONLDRecipe("Black Bean Tacos", "1 can black beans", "6 tortillas") + `</script>` +
		`</head><body></body></html>`

	def, _, ok := extractJSONLDRecipeByTitle(html, "Black Bean Tacos")
	if !ok || def == nil {
		t.Fatalf("expected to extract a recipe by title, ok=%v", ok)
	}
	if len(def.Ingredients) != 2 {
		t.Fatalf("ingredients = %d, want 2", len(def.Ingredients))
	}
	// Confirm it picked the Tacos block, not the Fajitas block.
	joined := strings.ToLower(fmt.Sprint(def.Ingredients))
	if !strings.Contains(joined, "black bean") {
		t.Errorf("extracted the wrong recipe: %s", joined)
	}
}

func TestExtractJSONLDRecipeByTitle_GraphAndNoMatch(t *testing.T) {
	html := `<html><head><script type="application/ld+json">` +
		`{"@context":"https://schema.org","@graph":[{"@type":"WebPage","name":"index"},` +
		fullJSONLDRecipe("Lemon Garlic Salmon", "2 salmon fillets", "1 lemon") + `]}` +
		`</script></head></html>`

	def, _, ok := extractJSONLDRecipeByTitle(html, "Lemon Garlic Salmon")
	if !ok || def == nil || len(def.Ingredients) != 2 {
		t.Fatalf("expected salmon recipe from @graph, ok=%v", ok)
	}
	if _, _, ok := extractJSONLDRecipeByTitle(html, "Chocolate Lava Cake"); ok {
		t.Errorf("expected no match for an absent title")
	}
}

// --- FIX B3: ResolveFromURLWithReason distinguishes blocked vs no-recipes ---

func TestResolveFromURLWithReason_Reasons(t *testing.T) {
	singleDetect := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, q, rc string) (string, error) { return "SINGLE", nil },
	}

	t.Run("multi-recipe returns entry with empty reason", func(t *testing.T) {
		resolver := newResolverForTest(singleDetect, nil)
		resolver.ImportService.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
			return []byte(multiRecipeHTML("Recipe A", "Recipe B", "Recipe C")), http.StatusOK, nil
		}
		entry, reason := resolver.ResolveFromURLWithReason(context.Background(), "https://example.com/roundup")
		if entry == nil || reason != "" {
			t.Fatalf("multi: entry?=%v reason=%q, want an entry + empty reason", entry != nil, reason)
		}
	})

	t.Run("single-recipe page -> no_recipes", func(t *testing.T) {
		resolver := newResolverForTest(singleDetect, nil)
		resolver.ImportService.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
			return []byte(multiRecipeHTML("Only One Recipe")), http.StatusOK, nil
		}
		entry, reason := resolver.ResolveFromURLWithReason(context.Background(), "https://example.com/single")
		if entry != nil || reason != "no_recipes" {
			t.Errorf("single: entry?=%v reason=%q, want nil + no_recipes", entry != nil, reason)
		}
	})

	t.Run("bot-blocked (403) + firecrawl fails -> site_blocked", func(t *testing.T) {
		resolver := newResolverForTest(singleDetect, nil)
		resolver.ImportService.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
			return []byte("blocked"), http.StatusForbidden, nil
		}
		resolver.ImportService.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, int, error) {
			return "", 0, errors.New("firecrawl exhausted")
		}
		entry, reason := resolver.ResolveFromURLWithReason(context.Background(), "https://example.com/blocked")
		if entry != nil || reason != "site_blocked" {
			t.Errorf("blocked: entry?=%v reason=%q, want nil + site_blocked", entry != nil, reason)
		}
	})

	t.Run("rate-limited (429) + firecrawl fails -> site_blocked", func(t *testing.T) {
		resolver := newResolverForTest(singleDetect, nil)
		resolver.ImportService.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
			return []byte("rate limited"), http.StatusTooManyRequests, nil
		}
		resolver.ImportService.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, int, error) {
			return "", 0, errors.New("firecrawl exhausted")
		}
		entry, reason := resolver.ResolveFromURLWithReason(context.Background(), "https://example.com/rl")
		if entry != nil || reason != "site_blocked" {
			t.Errorf("429: entry?=%v reason=%q, want nil + site_blocked", entry != nil, reason)
		}
	})
}

// --- Outcome-based escalation + windowing + per-card retry helpers ---

func TestLooksJSRendered(t *testing.T) {
	jsShell := []byte(`<html><head><title>App</title></head><body><div id="root"></div><script src="/app.js"></script></body></html>`)
	if !looksJSRendered(jsShell) {
		t.Errorf("thin JS shell should look JS-rendered")
	}
	withJSONLD := []byte(`<html><head><script type="application/ld+json">{"@type":"Recipe"}</script></head><body><div id="root"></div></body></html>`)
	if looksJSRendered(withJSONLD) {
		t.Errorf("page with JSON-LD should not be treated as JS-rendered")
	}
	thickProse := []byte("<html><body>" + strings.Repeat("This recipe is delicious and easy to make. ", 40) + "</body></html>")
	if looksJSRendered(thickProse) {
		t.Errorf("page with substantial prose should not be treated as JS-rendered")
	}
}

func TestWindowAroundTitle(t *testing.T) {
	// Target sits well past a naive 30 KB head truncation.
	head := strings.Repeat("filler alpha beta gamma ", 3000) // ~72 KB
	text := head + " TARGET RECIPE TITLE with 2 cups flour and 3 eggs " + strings.Repeat(" tail", 200)

	win := windowAroundTitle(text, "Target Recipe Title", 30_000)
	if len(win) > 30_000 {
		t.Errorf("window len = %d, want <= 30000", len(win))
	}
	if !strings.Contains(win, "TARGET RECIPE TITLE") {
		t.Errorf("window did not include the target title (naive head-truncation would have missed it)")
	}

	short := "just a little text"
	if windowAroundTitle(short, "anything", 30_000) != short {
		t.Errorf("short text should be returned unchanged")
	}
}

func TestExtractSingleCard_RetriesTransientAIError(t *testing.T) {
	var attempts sync.Map // title -> *int32
	preview := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, q, rc string) (string, error) { return "SINGLE", nil },
		ExtractRecipeFromTextFunc: func(ctx context.Context, text, unitSystem string) (*ai.RecipeResult, error) {
			var key string
			for _, tt := range []string{"Recipe A", "Recipe B"} {
				if strings.Contains(text, tt) {
					key = tt
				}
			}
			n, _ := attempts.LoadOrStore(key, new(int32))
			// Fail the first attempt for each card; succeed on the retry.
			if atomic.AddInt32(n.(*int32), 1) == 1 {
				return nil, errors.New("transient blip")
			}
			return testutil.TestRecipeResult(), nil
		},
	}
	resolver := newResolverForTest(preview, nil)

	// multiRecipeHTML JSON-LD has no ingredients, so extraction falls to the AI
	// path — exactly where the per-card retry lives.
	entry := resolver.ResolveFromHTML(context.Background(), "https://example.com/roundup", multiRecipeHTML("Recipe A", "Recipe B"))
	if entry == nil {
		t.Fatal("expected a multi-recipe entry")
	}
	waitResolved(t, entry)

	for _, c := range entry.GetCards() {
		if c.ExtractionStatus != "done" {
			t.Errorf("card %q status = %q, want done (retry should recover a transient error)", c.Title, c.ExtractionStatus)
		}
	}
}

// --- LIVE (gated): real roundup extraction ---

// TestResolveFromURL_Live_ExtractsRoundup fetches and extracts a real roundup URL
// end to end, asserting at least one card reaches "done" with a RecipeDef.
//
// Run with:
//
//	FINDER_LIVE_TEST=1 \
//	FINDER_LIVE_ROUNDUP_URL="https://<a real roundup/listicle>" \
//	LIGHT_API_KEY=<gemini key> LIGHT_BASE_URL=<gemini openai-compat base> LIGHT_MODEL=gemini-2.5-flash \
//	FIRECRAWL_API_KEY=<optional, for bot-blocked sites> \
//	go test ./internal/service/ -run TestResolveFromURL_Live_ExtractsRoundup -v
func TestResolveFromURL_Live_ExtractsRoundup(t *testing.T) {
	if os.Getenv("FINDER_LIVE_TEST") == "" {
		t.Skip("set FINDER_LIVE_TEST=1 + FINDER_LIVE_ROUNDUP_URL + a light-tier key to run the live roundup extraction test")
	}
	roundupURL := os.Getenv("FINDER_LIVE_ROUNDUP_URL")
	if roundupURL == "" {
		t.Skip("FINDER_LIVE_ROUNDUP_URL not set")
	}
	apiKey := firstNonEmpty(os.Getenv("LIGHT_API_KEY"), os.Getenv("GEMINI_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("no light-tier API key (LIGHT_API_KEY / GEMINI_API_KEY) set")
	}
	promptsPath := os.Getenv("PROMPTS_PATH")
	if promptsPath == "" {
		promptsPath = "../../configs/prompts.yaml"
	}
	prompts, err := config.LoadPrompts(promptsPath)
	if err != nil {
		t.Skipf("could not load prompts from %s: %v", promptsPath, err)
	}
	model := os.Getenv("LIGHT_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}

	preview := ai.NewOpenAICompatProvider(apiKey, os.Getenv("LIGHT_BASE_URL"), model, "gemini", prompts)
	svc := newTestImportService(testutil.NewMockRecipeRepo(), nil, preview)
	svc.Cfg.EnvVars.FirecrawlAPIKey = os.Getenv("FIRECRAWL_API_KEY")
	resolver := NewMultiRecipeResolver(NewMultiRecipeRegistry(), svc)

	entry := resolver.ResolveFromURL(context.Background(), roundupURL)
	if entry == nil {
		t.Fatalf("roundup %q did not resolve as multi-recipe", roundupURL)
	}

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) && entry.GetStatus() == "resolving" {
		time.Sleep(500 * time.Millisecond)
	}

	var doneWithRecipe int
	cards := entry.GetCards()
	for _, c := range cards {
		if c.ExtractionStatus == "done" && c.RecipeDef != nil && len(c.RecipeDef.Ingredients) > 0 {
			doneWithRecipe++
		}
	}
	t.Logf("roundup resolved: %d/%d cards done with a RecipeDef", doneWithRecipe, len(cards))
	if doneWithRecipe == 0 {
		t.Errorf("no card reached done with a RecipeDef for %q", roundupURL)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
