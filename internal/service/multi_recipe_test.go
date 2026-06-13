package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// --- extractAllJSONLDRecipes ---

func TestExtractAllJSONLDRecipes_MultipleScriptBlocks(t *testing.T) {
	html := `<html><head>
	<script type="application/ld+json">{"@type":"Recipe","name":"Pancakes","description":"Fluffy","image":"https://img.example.com/p.jpg"}</script>
	<script type="application/ld+json">{"@type":"Recipe","name":"Waffles"}</script>
	</head><body></body></html>`

	cards := extractAllJSONLDRecipes(html, "https://example.com/breakfast")
	if len(cards) != 2 {
		t.Fatalf("extractAllJSONLDRecipes returned %d cards, want 2", len(cards))
	}
	if cards[0].Title != "Pancakes" || cards[1].Title != "Waffles" {
		t.Errorf("card titles = %q, %q; want Pancakes, Waffles", cards[0].Title, cards[1].Title)
	}
	if cards[0].ImageURL != "https://img.example.com/p.jpg" {
		t.Errorf("card[0].ImageURL = %q, want the JSON-LD image", cards[0].ImageURL)
	}
	if cards[0].Description != "Fluffy" {
		t.Errorf("card[0].Description = %q, want 'Fluffy'", cards[0].Description)
	}
	for i, c := range cards {
		if c.SourceURL != "https://example.com/breakfast" {
			t.Errorf("card[%d].SourceURL = %q, want the page URL", i, c.SourceURL)
		}
		if c.ExtractionStatus != "pending" {
			t.Errorf("card[%d].ExtractionStatus = %q, want 'pending'", i, c.ExtractionStatus)
		}
	}
}

func TestExtractAllJSONLDRecipes_DeduplicatesByTitle(t *testing.T) {
	html := `<script type="application/ld+json">{"@type":"Recipe","name":"Chili"}</script>
	<script type="application/ld+json">{"@type":"Recipe","name":"Chili"}</script>`

	cards := extractAllJSONLDRecipes(html, "https://example.com")
	if len(cards) != 1 {
		t.Fatalf("extractAllJSONLDRecipes returned %d cards, want 1 after dedupe", len(cards))
	}
}

func TestExtractAllJSONLDRecipes_NoJSONLD(t *testing.T) {
	html := `<html><body><h1>Just an article</h1></body></html>`
	if cards := extractAllJSONLDRecipes(html, "https://example.com"); len(cards) != 0 {
		t.Errorf("extractAllJSONLDRecipes returned %d cards, want 0", len(cards))
	}
}

func TestExtractAllJSONLDRecipes_SkipsNonRecipeBlocks(t *testing.T) {
	html := `<script type="application/ld+json">{"@type":"Article","name":"Top 10 Dinners"}</script>
	<script type="application/ld+json">{"@type":"Recipe","name":"Lasagna"}</script>`

	cards := extractAllJSONLDRecipes(html, "https://example.com")
	if len(cards) != 1 || cards[0].Title != "Lasagna" {
		t.Fatalf("extractAllJSONLDRecipes cards = %+v, want only Lasagna", cards)
	}
}

// --- findAllRecipesInJSONLD ---

func TestFindAllRecipesInJSONLD_GraphContainer(t *testing.T) {
	jsonStr := `{"@context":"https://schema.org","@graph":[
		{"@type":"WebPage","name":"Page"},
		{"@type":"Recipe","name":"Tacos","description":"Crunchy"},
		{"@type":"Recipe","name":"Salsa"}
	]}`

	recipes := findAllRecipesInJSONLD(jsonStr)
	if len(recipes) != 2 {
		t.Fatalf("findAllRecipesInJSONLD returned %d recipes, want 2", len(recipes))
	}
	if recipes[0].Title != "Tacos" || recipes[1].Title != "Salsa" {
		t.Errorf("titles = %q, %q; want Tacos, Salsa", recipes[0].Title, recipes[1].Title)
	}
	if recipes[0].Description != "Crunchy" {
		t.Errorf("description = %q, want 'Crunchy'", recipes[0].Description)
	}
}

func TestFindAllRecipesInJSONLD_TopLevelArray(t *testing.T) {
	jsonStr := `[
		{"@type":"Recipe","name":"Soup"},
		{"@type":"BreadcrumbList"},
		{"@type":"Recipe","name":"Salad"}
	]`

	recipes := findAllRecipesInJSONLD(jsonStr)
	if len(recipes) != 2 {
		t.Fatalf("findAllRecipesInJSONLD returned %d recipes, want 2", len(recipes))
	}
}

func TestFindAllRecipesInJSONLD_NestedGraphInArray(t *testing.T) {
	jsonStr := `[{"@graph":[{"@type":"Recipe","name":"Stew"}]},{"@type":"Recipe","name":"Pie"}]`

	recipes := findAllRecipesInJSONLD(jsonStr)
	if len(recipes) != 2 {
		t.Fatalf("findAllRecipesInJSONLD returned %d recipes, want 2 (nested @graph + sibling)", len(recipes))
	}
}

func TestFindAllRecipesInJSONLD_TypeArray(t *testing.T) {
	jsonStr := `{"@type":["Recipe","NewsArticle"],"name":"Fusion Bowl"}`

	recipes := findAllRecipesInJSONLD(jsonStr)
	if len(recipes) != 1 || recipes[0].Title != "Fusion Bowl" {
		t.Fatalf("findAllRecipesInJSONLD = %+v, want one recipe 'Fusion Bowl'", recipes)
	}
}

func TestFindAllRecipesInJSONLD_ImageVariants(t *testing.T) {
	cases := []struct {
		name    string
		jsonStr string
		want    string
	}{
		{"string image", `{"@type":"Recipe","name":"A","image":"https://x/img.jpg"}`, "https://x/img.jpg"},
		{"string array image", `{"@type":"Recipe","name":"A","image":["https://x/1.jpg","https://x/2.jpg"]}`, "https://x/1.jpg"},
		{"object array image", `{"@type":"Recipe","name":"A","image":[{"@type":"ImageObject","url":"https://x/obj.jpg"}]}`, "https://x/obj.jpg"},
		{"object image", `{"@type":"Recipe","name":"A","image":{"@type":"ImageObject","url":"https://x/single.jpg"}}`, "https://x/single.jpg"},
		{"missing image", `{"@type":"Recipe","name":"A"}`, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recipes := findAllRecipesInJSONLD(tc.jsonStr)
			if len(recipes) != 1 {
				t.Fatalf("got %d recipes, want 1", len(recipes))
			}
			if recipes[0].ImageURL != tc.want {
				t.Errorf("ImageURL = %q, want %q", recipes[0].ImageURL, tc.want)
			}
		})
	}
}

func TestFindAllRecipesInJSONLD_InvalidAndEmptyInputs(t *testing.T) {
	cases := []struct {
		name    string
		jsonStr string
	}{
		{"invalid json", `{not json`},
		{"recipe without name", `{"@type":"Recipe"}`},
		{"non-recipe object", `{"@type":"Person","name":"Chef"}`},
		{"empty string", ``},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if recipes := findAllRecipesInJSONLD(tc.jsonStr); len(recipes) != 0 {
				t.Errorf("findAllRecipesInJSONLD(%q) = %+v, want none", tc.jsonStr, recipes)
			}
		})
	}
}

// --- stripHTMLToText ---

func TestStripHTMLToText_RemovesScriptsAndStyles(t *testing.T) {
	html := `<html><head>
	<script>var hidden = "secret-js";</script>
	<style>.hidden { color: red; }</style>
	<noscript>enable-js-note</noscript>
	</head><body>
	<nav>Home | About</nav>
	<header>Site Header</header>
	<svg><path d="M0 0"/></svg>
	<p>Visible recipe text</p>
	<footer>Copyright Notice</footer>
	</body></html>`

	text := stripHTMLToText(html)

	for _, hidden := range []string{"secret-js", "color: red", "enable-js-note", "Home | About", "Site Header", "Copyright Notice", "M0 0"} {
		if strings.Contains(text, hidden) {
			t.Errorf("stripHTMLToText output contains %q, should be stripped", hidden)
		}
	}
	if !strings.Contains(text, "Visible recipe text") {
		t.Errorf("stripHTMLToText output = %q, want it to contain visible text", text)
	}
}

func TestStripHTMLToText_RemovesComments(t *testing.T) {
	text := stripHTMLToText(`<p>before</p><!-- hidden comment --><p>after</p>`)
	if strings.Contains(text, "hidden comment") {
		t.Errorf("stripHTMLToText kept comment content: %q", text)
	}
	if !strings.Contains(text, "before") || !strings.Contains(text, "after") {
		t.Errorf("stripHTMLToText lost visible text: %q", text)
	}
}

func TestStripHTMLToText_DecodesEntities(t *testing.T) {
	text := stripHTMLToText(`<p>Mac &amp; Cheese &lt;3 &quot;best&quot; mom&#39;s&nbsp;recipe &gt;</p>`)
	want := `Mac & Cheese <3 "best" mom's recipe >`
	if !strings.Contains(text, want) {
		t.Errorf("stripHTMLToText = %q, want it to contain %q", text, want)
	}
}

func TestStripHTMLToText_BlockTagsBecomeNewlines(t *testing.T) {
	text := stripHTMLToText(`<h1>Title</h1><p>One</p><li>Two</li>`)
	lines := strings.Split(text, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(l))
		}
	}
	if len(nonEmpty) != 3 {
		t.Fatalf("stripHTMLToText produced %d lines %v, want 3 (block tags preserved as newlines)", len(nonEmpty), nonEmpty)
	}
}

func TestStripHTMLToText_CollapsesWhitespace(t *testing.T) {
	text := stripHTMLToText("<p>a    \t   b</p>\n\n\n\n\n<p>c</p>")
	if strings.Contains(text, "  ") {
		t.Errorf("stripHTMLToText left runs of spaces: %q", text)
	}
	if strings.Contains(text, "\n\n\n") {
		t.Errorf("stripHTMLToText left 3+ consecutive newlines: %q", text)
	}
	if got := stripHTMLToText("   <p>  trimmed  </p>   "); strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Errorf("stripHTMLToText output not trimmed: %q", got)
	}
}

// --- detectMultipleRecipesFromHTML ---

func TestDetectMultipleRecipes_NilProvider(t *testing.T) {
	cards := detectMultipleRecipesFromHTML(context.Background(), nil, "<p>page</p>", "https://example.com")
	if cards != nil {
		t.Errorf("detectMultipleRecipesFromHTML with nil provider = %+v, want nil", cards)
	}
}

func TestDetectMultipleRecipes_SingleResponse(t *testing.T) {
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return "SINGLE", nil
		},
	}
	cards := detectMultipleRecipesFromHTML(context.Background(), provider, "<p>one recipe</p>", "https://example.com")
	if cards != nil {
		t.Errorf("detectMultipleRecipesFromHTML = %+v, want nil for SINGLE response", cards)
	}
}

func TestDetectMultipleRecipes_ProviderError(t *testing.T) {
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return "", errors.New("ai unavailable")
		},
	}
	cards := detectMultipleRecipesFromHTML(context.Background(), provider, "<p>page</p>", "https://example.com")
	if cards != nil {
		t.Errorf("detectMultipleRecipesFromHTML = %+v, want nil on provider error", cards)
	}
}

func TestDetectMultipleRecipes_FiltersNoisyLinesAndDuplicates(t *testing.T) {
	longLine := strings.Repeat("x", 201)
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return strings.Join([]string{
				"Pumpkin Soup",
				"",             // empty — skipped
				"  ",           // whitespace — skipped
				"ab",           // too short — skipped
				longLine,       // too long — skipped
				"SINGLE",       // literal SINGLE mid-list — skipped
				"Pumpkin Soup", // duplicate — skipped
				"Apple Crumble",
			}, "\n"), nil
		},
	}

	cards := detectMultipleRecipesFromHTML(context.Background(), provider, "<p>page</p>", "https://example.com/desserts")
	if len(cards) != 2 {
		t.Fatalf("detectMultipleRecipesFromHTML returned %d cards, want 2 after filtering", len(cards))
	}
	if cards[0].Title != "Pumpkin Soup" || cards[1].Title != "Apple Crumble" {
		t.Errorf("card titles = %q, %q", cards[0].Title, cards[1].Title)
	}
	for i, c := range cards {
		if c.SourceURL != "https://example.com/desserts" || c.ExtractionStatus != "pending" {
			t.Errorf("card[%d] = %+v, want pending card pointing at source URL", i, c)
		}
	}
}

func TestDetectMultipleRecipes_CapsAtTwentyCards(t *testing.T) {
	var titles []string
	for i := 0; i < 30; i++ {
		titles = append(titles, fmt.Sprintf("Recipe Number %d", i))
	}
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return strings.Join(titles, "\n"), nil
		},
	}

	cards := detectMultipleRecipesFromHTML(context.Background(), provider, "<p>page</p>", "https://example.com")
	if len(cards) != 20 {
		t.Errorf("detectMultipleRecipesFromHTML returned %d cards, want cap of 20", len(cards))
	}
}

func TestDetectMultipleRecipes_OneTitleIsNotMulti(t *testing.T) {
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			return "Just One Recipe", nil
		},
	}
	cards := detectMultipleRecipesFromHTML(context.Background(), provider, "<p>page</p>", "https://example.com")
	if cards != nil {
		t.Errorf("detectMultipleRecipesFromHTML = %+v, want nil when only one title detected", cards)
	}
}

func TestDetectMultipleRecipes_StripsAndTruncatesPromptInput(t *testing.T) {
	// Build HTML well above the 15k detection budget; the prompt must carry
	// stripped text, not raw HTML, and stay bounded.
	html := "<script>var noise='should-not-appear';</script>" +
		"<p>" + strings.Repeat("filler words for the page body ", 2000) + "</p>"

	var gotQuestion string
	provider := &testutil.MockTextProvider{
		CookingQAFunc: func(ctx context.Context, question string, recipeContext string) (string, error) {
			gotQuestion = question
			return "SINGLE", nil
		},
	}

	detectMultipleRecipesFromHTML(context.Background(), provider, html, "https://example.com")

	if strings.Contains(gotQuestion, "should-not-appear") {
		t.Error("detection prompt contains script content; HTML was not stripped")
	}
	if strings.Contains(gotQuestion, "<p>") {
		t.Error("detection prompt contains raw HTML tags")
	}
	// Prompt = fixed instructions + at most 15k of page text.
	if len(gotQuestion) > 16_000 {
		t.Errorf("detection prompt length = %d, want page text truncated to ~15k", len(gotQuestion))
	}
}

// --- MultiRecipeRegistry ---

func TestRegistryRegister_NewEntry(t *testing.T) {
	r := NewMultiRecipeRegistry()

	entry, isNew := r.Register("https://example.com/roundup")
	if !isNew {
		t.Fatal("Register of an unseen URL should report isNew=true")
	}
	if entry.Status != "resolving" {
		t.Errorf("new entry Status = %q, want 'resolving'", entry.Status)
	}
	if entry.SourceURL != "https://example.com/roundup" {
		t.Errorf("new entry SourceURL = %q", entry.SourceURL)
	}
	if !strings.HasPrefix(entry.ID, "multi_") {
		t.Errorf("new entry ID = %q, want 'multi_' prefix", entry.ID)
	}
	if entry.DetectedAt.IsZero() {
		t.Error("new entry DetectedAt is zero")
	}
}

func TestRegistryRegister_ExistingEntryReturned(t *testing.T) {
	r := NewMultiRecipeRegistry()

	first, _ := r.Register("https://example.com/roundup")
	second, isNew := r.Register("https://example.com/roundup")
	if isNew {
		t.Error("Register of a tracked URL should report isNew=false")
	}
	if first != second {
		t.Error("Register should return the existing entry for a tracked URL")
	}
}

func TestRegistryRegister_UniqueIDs(t *testing.T) {
	r := NewMultiRecipeRegistry()

	a, _ := r.Register("https://example.com/a")
	b, _ := r.Register("https://example.com/b")
	if a.ID == b.ID {
		t.Errorf("two registered entries share ID %q; counter should make them unique", a.ID)
	}
}

func TestRegistryGet_UntrackedURL(t *testing.T) {
	r := NewMultiRecipeRegistry()
	if entry := r.Get("https://example.com/unknown"); entry != nil {
		t.Errorf("Get(untracked) = %+v, want nil", entry)
	}
}

func TestRegistryGetByID(t *testing.T) {
	r := NewMultiRecipeRegistry()
	entry, _ := r.Register("https://example.com/roundup")

	if got := r.GetByID(entry.ID); got != entry {
		t.Errorf("GetByID(%q) = %+v, want the registered entry", entry.ID, got)
	}
	if got := r.GetByID("multi_nope_42"); got != nil {
		t.Errorf("GetByID(unknown) = %+v, want nil", got)
	}
}

// --- eviction decision (pure; the loop itself is time-driven and not spawned here) ---

func TestShouldEvictEntry(t *testing.T) {
	now := time.Now()
	old := now.Add(-registryEvictionTTL - time.Minute)
	fresh := now.Add(-time.Minute)

	cases := []struct {
		name       string
		status     string
		detectedAt time.Time
		want       bool
	}{
		{"resolved and stale", "resolved", old, true},
		{"failed and stale", "failed", old, true},
		{"resolving never evicted even when stale", "resolving", old, false},
		{"resolved but fresh", "resolved", fresh, false},
		{"failed but fresh", "failed", fresh, false},
		{"exactly at TTL boundary not evicted", "resolved", now.Add(-registryEvictionTTL), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEvictEntry(tc.status, tc.detectedAt, now); got != tc.want {
				t.Errorf("shouldEvictEntry(%q, %v ago) = %v, want %v", tc.status, now.Sub(tc.detectedAt), got, tc.want)
			}
		})
	}
}

// --- MultiRecipeEntry accessors ---

func TestEntryGetCards_DeepCopiesRecipeDef(t *testing.T) {
	def := testutil.TestRecipeDef()
	entry := &MultiRecipeEntry{
		Status: "resolved",
		Cards: []MultiRecipeCard{
			{Title: "Pancakes", RecipeDef: &def, ExtractionStatus: "done"},
		},
	}

	cards := entry.GetCards()
	if len(cards) != 1 {
		t.Fatalf("GetCards returned %d cards, want 1", len(cards))
	}
	if cards[0].RecipeDef == entry.Cards[0].RecipeDef {
		t.Error("GetCards returned the same RecipeDef pointer; want a deep copy")
	}
	if cards[0].RecipeDef.Title != def.Title {
		t.Errorf("copied RecipeDef.Title = %q, want %q", cards[0].RecipeDef.Title, def.Title)
	}

	// Mutating the copy must not leak back into the entry.
	cards[0].RecipeDef.Title = "Mutated"
	if entry.Cards[0].RecipeDef.Title == "Mutated" {
		t.Error("mutating a GetCards copy changed the entry's card")
	}
}

func TestEntryGetStatus(t *testing.T) {
	entry := &MultiRecipeEntry{Status: "resolving"}
	if got := entry.GetStatus(); got != "resolving" {
		t.Errorf("GetStatus = %q, want 'resolving'", got)
	}
}
