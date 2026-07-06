package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.EnvVars.SiteBaseURL = "https://saltybytes.ai"
	cfg.EnvVars.PublicBaseURL = "https://api.saltybytes.ai"
	return cfg
}

func testCanonical() *models.CanonicalRecipe {
	entry := &models.CanonicalRecipe{
		OriginalURL: "https://pinchofyum.com/bang-bang-salmon",
		RecipeData: models.RecipeDef{
			Title: "Bang Bang Salmon",
			Ingredients: models.Ingredients{
				{Name: "sweet chili sauce", Amount: 0.25, Unit: "cup"},
				{Name: "sriracha", Amount: 1, AmountHigh: 2, Unit: "tbsp"},
				{OriginalText: "1 avocado, diced"},
			},
			Instructions: []string{"Roast the salmon.", "Make the salsa."},
			CookTime:     25,
			Portions:     4,
			SourceURL:    "https://pinchofyum.com/bang-bang-salmon",
		},
	}
	entry.ID = 42
	return entry
}

func testRouter(repo repository.CanonicalRecipeRepo) *gin.Engine {
	r := gin.New()
	NewHandler(testConfig(), repo).Register(r)
	return r
}

func get(r *gin.Engine, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHome(t *testing.T) {
	repo := &testutil.MockCanonicalRecipeRepo{
		ListServableSummariesFunc: func(limit int) ([]repository.CanonicalSummary, error) {
			return []repository.CanonicalSummary{
				{ID: 7, Title: "Peasant Bread", OriginalURL: "https://alexandracooks.com/bread", UpdatedAt: time.Now()},
			}, nil
		},
	}
	w := get(testRouter(repo), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected html content type, got %q", ct)
	}
	for _, want := range []string{
		"SaltyBytes",
		"https://api.saltybytes.ai/mcp", // the connector URL
		"Peasant Bread",                 // recent strip
		"/r/7",
		`property="og:image" content="https://saltybytes.ai/static/og.png"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home missing %q", want)
		}
	}
}

func TestHome_NoRecent(t *testing.T) {
	repo := &testutil.MockCanonicalRecipeRepo{
		ListServableSummariesFunc: func(limit int) ([]repository.CanonicalSummary, error) {
			return nil, nil
		},
	}
	w := get(testRouter(repo), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "Fresh out of the extractor") {
		t.Error("empty recent list should hide the fresh section")
	}
}

func TestRecipePage(t *testing.T) {
	repo := &testutil.MockCanonicalRecipeRepo{
		GetByIDFunc: func(id uint) (*models.CanonicalRecipe, error) {
			if id != 42 {
				return nil, fmt.Errorf("not found")
			}
			return testCanonical(), nil
		},
	}
	w := get(testRouter(repo), "/r/42")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Bang Bang Salmon",
		"¼ cup",     // fraction formatting
		"1–2 tbsp",  // range formatting
		"1 avocado", // original_text passthrough
		"Roast the salmon.",
		"pinchofyum.com",
		`application/ld+json`,
		`"cookTime":"PT25M"`,
		`<link rel="canonical" href="https://saltybytes.ai/r/42">`,
		`data-src="https://pinchofyum.com/bang-bang-salmon"`, // feeds the open-in-app deep link
		`saltybytes://app/preview`,                           // the scheme the script attempts
	} {
		if !strings.Contains(body, want) {
			t.Errorf("recipe page missing %q", want)
		}
	}
}

func TestRecipePage_DecodesSourceEntities(t *testing.T) {
	entry := testCanonical()
	entry.RecipeData.Ingredients = models.Ingredients{
		{OriginalText: "1-2 tablespoons seasoning mix (the one I use &#8211; see notes)"},
	}
	entry.RecipeData.Title = "Salmon &amp; Salsa"
	repo := &testutil.MockCanonicalRecipeRepo{
		GetByIDFunc: func(id uint) (*models.CanonicalRecipe, error) { return entry, nil },
	}
	body := get(testRouter(repo), "/r/42").Body.String()
	if strings.Contains(body, "&amp;#8211;") || strings.Contains(body, "#8211") {
		t.Error("stored HTML entity leaked into the page text")
	}
	if !strings.Contains(body, "–") {
		t.Error("expected the en dash to render")
	}
	if !strings.Contains(body, "Salmon &amp; Salsa") {
		t.Error("decoded ampersand should re-escape exactly once on output")
	}
}

func TestRecipePage_EscapesUntrustedContent(t *testing.T) {
	entry := testCanonical()
	entry.RecipeData.Title = `<script>alert("pwn")</script> Salmon`
	repo := &testutil.MockCanonicalRecipeRepo{
		GetByIDFunc: func(id uint) (*models.CanonicalRecipe, error) { return entry, nil },
	}
	w := get(testRouter(repo), "/r/42")
	body := w.Body.String()
	if strings.Contains(body, `<script>alert`) {
		t.Fatal("recipe title was not escaped")
	}
	// The JSON-LD payload must also be safe inside its <script> element.
	if strings.Contains(body, `</script>alert`) {
		t.Fatal("JSON-LD allowed a script breakout")
	}
}

func TestRecipePage_NotFoundAndMarkers(t *testing.T) {
	multi := testCanonical()
	multi.IsMultiPage = true
	repo := &testutil.MockCanonicalRecipeRepo{
		GetByIDFunc: func(id uint) (*models.CanonicalRecipe, error) {
			if id == 9 {
				return multi, nil
			}
			return nil, fmt.Errorf("not found")
		},
	}
	r := testRouter(repo)
	for _, path := range []string{"/r/9", "/r/12345", "/r/notanumber"} {
		w := get(r, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "boiled") {
			t.Errorf("%s: expected friendly 404 page", path)
		}
	}
}

func TestRecipeByURL(t *testing.T) {
	repo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(normalizedURL string) (*models.CanonicalRecipe, error) {
			return testCanonical(), nil
		},
	}
	w := get(testRouter(repo), "/r?u=https://pinchofyum.com/bang-bang-salmon")
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/r/42" {
		t.Fatalf("expected redirect to /r/42, got %q", loc)
	}
}

func TestRecipeByURL_MissEmptyAndInvalid(t *testing.T) {
	repo := &testutil.MockCanonicalRecipeRepo{}
	r := testRouter(repo)
	if w := get(r, "/r?u=https://example.com/nope"); w.Code != http.StatusNotFound {
		t.Errorf("cache miss: expected 404, got %d", w.Code)
	}
	if w := get(r, "/r"); w.Code != http.StatusFound {
		t.Errorf("empty u: expected redirect home, got %d", w.Code)
	}
}

func TestSitemapAndRobots(t *testing.T) {
	repo := &testutil.MockCanonicalRecipeRepo{
		ListServableSummariesFunc: func(limit int) ([]repository.CanonicalSummary, error) {
			return []repository.CanonicalSummary{{ID: 42, Title: "X", UpdatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}}, nil
		},
	}
	r := testRouter(repo)

	w := get(r, "/sitemap.xml")
	if w.Code != http.StatusOK {
		t.Fatalf("sitemap: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"<loc>https://saltybytes.ai/</loc>", "<loc>https://saltybytes.ai/r/42</loc>", "<lastmod>2026-07-01</lastmod>"} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap missing %q", want)
		}
	}

	w = get(r, "/robots.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("robots: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Sitemap: https://saltybytes.ai/sitemap.xml") {
		t.Error("robots.txt missing sitemap line")
	}
}

func TestNoRouteContentNegotiation(t *testing.T) {
	r := testRouter(&testutil.MockCanonicalRecipeRepo{})

	w := get(r, "/v1/does-not-exist", "Accept", "text/html")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("API-path 404 should be JSON even for browsers, got %d %s", w.Code, w.Header().Get("Content-Type"))
	}

	w = get(r, "/some-random-page", "Accept", "text/html,application/xhtml+xml")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("browser 404 should be HTML, got %d %s", w.Code, w.Header().Get("Content-Type"))
	}

	w = get(r, "/some-random-page")
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("non-browser 404 should be JSON, got %s", w.Header().Get("Content-Type"))
	}
}

func TestStaticAssets(t *testing.T) {
	r := testRouter(&testutil.MockCanonicalRecipeRepo{})
	w := get(r, "/static/site.css")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for site.css, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=86400") {
		t.Errorf("static assets should be cacheable, got %q", cc)
	}
	if w := get(r, "/static/nope.css"); w.Code != http.StatusNotFound {
		t.Errorf("missing asset: expected 404, got %d", w.Code)
	}
}

func TestWellKnownAssociationFiles(t *testing.T) {
	r := testRouter(&testutil.MockCanonicalRecipeRepo{})

	aasa := get(r, "/.well-known/apple-app-site-association")
	if aasa.Code != http.StatusOK || !strings.Contains(aasa.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("AASA: expected 200 json, got %d %s", aasa.Code, aasa.Header().Get("Content-Type"))
	}
	for _, want := range []string{"2M54LKDR89.codes.julian.saltybytes", `"/r/*"`, "webcredentials"} {
		if !strings.Contains(aasa.Body.String(), want) {
			t.Errorf("AASA missing %q", want)
		}
	}

	links := get(r, "/.well-known/assetlinks.json")
	if links.Code != http.StatusOK || !strings.Contains(links.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("assetlinks: expected 200 json, got %d %s", links.Code, links.Header().Get("Content-Type"))
	}
	for _, want := range []string{"codes.julian.saltybytes", "delegate_permission/common.handle_all_urls", "6E:E1:3B:60"} {
		if !strings.Contains(links.Body.String(), want) {
			t.Errorf("assetlinks missing %q", want)
		}
	}
}

func TestPrivacyPage(t *testing.T) {
	w := get(testRouter(&testutil.MockCanonicalRecipeRepo{}), "/privacy")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Privacy Policy") {
		t.Error("privacy page missing content")
	}
}

func TestFormatAmount(t *testing.T) {
	cases := map[float64]string{
		0:     "",
		0.25:  "¼",
		0.5:   "½",
		1:     "1",
		1.5:   "1½",
		2.25:  "2¼",
		0.33:  "⅓",
		0.67:  "⅔",
		3:     "3",
		1.37:  "1.37",
		0.125: "⅛",
	}
	for in, want := range cases {
		if got := formatAmount(in); got != want {
			t.Errorf("formatAmount(%v) = %q, want %q", in, got, want)
		}
	}
}
