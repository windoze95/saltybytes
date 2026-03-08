package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func newTestImportService(repo *testutil.MockRecipeRepo, textProvider ai.TextProvider, previewProvider ai.TextProvider) *ImportService {
	recipeService := &RecipeService{
		Cfg:  &config.Config{},
		Repo: repo,
	}
	return &ImportService{
		Cfg:             &config.Config{},
		RecipeRepo:      repo,
		RecipeService:   recipeService,
		TextProvider:    textProvider,
		VisionProvider:  nil,
		PreviewProvider: previewProvider,
	}
}

// jsonLDHTML wraps recipe JSON-LD in a minimal HTML page for test use.
func jsonLDHTML() string {
	return `<html><head><script type="application/ld+json">
	{"@context":"https://schema.org","@type":"Recipe","name":"Classic Pancakes",
	"recipeIngredient":["1 cup flour","2 eggs"],"recipeInstructions":[{"@type":"HowToStep","text":"Mix"}],
	"cookTime":"PT20M","recipeYield":"4 servings"}
	</script></head><body></body></html>`
}

// plainHTML returns HTML without JSON-LD for testing AI fallback.
func plainHTML() string {
	return `<html><head><title>My Recipe</title></head><body><h1>Pancakes</h1><p>Mix flour and eggs.</p></body></html>`
}

// cloudflareHTML returns HTML that mimics a Cloudflare challenge page.
func cloudflareHTML() string {
	return `<html><head><title>Just a moment...</title></head><body><div id="challenge-platform">please wait</div></body></html>`
}

func TestImportFromText_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	mockText := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newTestImportService(repo, mockText, nil)
	user := testutil.TestUser()

	resp, err := svc.ImportFromText(context.Background(), "Some recipe text", user)
	if err != nil {
		t.Fatalf("ImportFromText error: %v", err)
	}
	if resp == nil {
		t.Fatal("ImportFromText returned nil response")
	}
	if resp.Title != "Classic Pancakes" {
		t.Errorf("ImportFromText title = %q, want 'Classic Pancakes'", resp.Title)
	}
	if len(repo.Recipes) != 1 {
		t.Errorf("ImportFromText recipes in repo = %d, want 1", len(repo.Recipes))
	}
	if resp.UnitSystem != "us_customary" {
		t.Errorf("ImportFromText UnitSystem = %q, want 'us_customary'", resp.UnitSystem)
	}
}

func TestImportFromText_MetricUser(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	mockText := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newTestImportService(repo, mockText, nil)
	user := testutil.TestUser()
	user.Personalization.UnitSystem = "metric"

	resp, err := svc.ImportFromText(context.Background(), "Some recipe text", user)
	if err != nil {
		t.Fatalf("ImportFromText error: %v", err)
	}
	if resp.UnitSystem != "metric" {
		t.Errorf("ImportFromText UnitSystem = %q, want 'metric'", resp.UnitSystem)
	}
}

func TestImportManual_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title: "Manual Pancakes",
		Ingredients: models.Ingredients{
			{Name: "Flour", Unit: "cups", Amount: 2},
		},
		Instructions: []string{"Mix", "Cook"},
		ImagePrompt:  "A photo of pancakes",
	}

	resp, err := svc.ImportManual(context.Background(), recipeDef, user, models.RecipeTypeManualEntry, nil)
	if err != nil {
		t.Fatalf("ImportManual error: %v", err)
	}
	if resp == nil {
		t.Fatal("ImportManual returned nil response")
	}
	if resp.Title != "Manual Pancakes" {
		t.Errorf("ImportManual title = %q, want 'Manual Pancakes'", resp.Title)
	}
}

func TestImportManual_EmptyTitle(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:       "",
		Ingredients: models.Ingredients{{Name: "Flour"}},
	}

	_, err := svc.ImportManual(context.Background(), recipeDef, user, models.RecipeTypeManualEntry, nil)
	if err == nil {
		t.Fatal("ImportManual with empty title should return error")
	}
}

func TestImportManual_WithSourceURL(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:        "Linked Recipe",
		Ingredients:  models.Ingredients{{Name: "Water"}},
		Instructions: []string{"Boil"},
		ImagePrompt:  "A pot of water",
		SourceURL:    "https://example.com/recipe",
	}

	resp, err := svc.ImportManual(context.Background(), recipeDef, user, models.RecipeTypeImportLink, nil)
	if err != nil {
		t.Fatalf("ImportManual with source URL error: %v", err)
	}
	if resp.SourceURL != "https://example.com/recipe" {
		t.Errorf("SourceURL = %q, want 'https://example.com/recipe'", resp.SourceURL)
	}
}

func TestImportFromText_NoProvider(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	_, err := svc.ImportFromText(context.Background(), "Some text", user)
	if err == nil {
		t.Fatal("ImportFromText with nil TextProvider should return error")
	}
}

func TestCreateImportedRecipe_AssociatesTags(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:        "Tagged Recipe",
		Ingredients:  models.Ingredients{{Name: "Flour"}},
		Instructions: []string{"Bake"},
		ImagePrompt:  "A baked thing",
	}

	resp, _, err := svc.createImportedRecipe(context.Background(), recipeDef, user, models.RecipeTypeManualEntry, "", nil, []string{"baking", "easy"})
	if err != nil {
		t.Fatalf("createImportedRecipe error: %v", err)
	}
	if resp == nil {
		t.Fatal("createImportedRecipe returned nil")
	}

	// Tags should have been created
	if len(repo.Tags) < 2 {
		t.Errorf("Expected at least 2 tags, got %d", len(repo.Tags))
	}
}

func TestPreviewFromURL_CanonicalCacheHit(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	canonical := testutil.TestCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(normalizedURL string) (*models.CanonicalRecipe, error) {
			return canonical, nil
		},
	}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo

	recipeDef, canonicalID, err := svc.PreviewFromURL(context.Background(), "https://example.com/classic-pancakes")
	if err != nil {
		t.Fatalf("PreviewFromURL error: %v", err)
	}
	if recipeDef == nil {
		t.Fatal("PreviewFromURL returned nil recipeDef")
	}
	if recipeDef.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", recipeDef.Title)
	}
	if canonicalID == nil {
		t.Fatal("expected canonical_id to be set")
	}
	if *canonicalID != canonical.ID {
		t.Errorf("canonical_id = %d, want %d", *canonicalID, canonical.ID)
	}
}

func TestPreviewFromURL_StaleCanonicalSkipped(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	stale := testutil.TestStaleCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(normalizedURL string) (*models.CanonicalRecipe, error) {
			return stale, nil
		},
	}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo

	// With a stale canonical and no real server to fetch from, this should fail
	// during extraction — proving the stale cache was skipped.
	_, _, err := svc.PreviewFromURL(context.Background(), "https://example.com/classic-pancakes")
	if err == nil {
		t.Fatal("expected error when stale canonical is skipped and extraction fails")
	}
}

func TestImportFromURL_StaleCanonicalSkipped(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	stale := testutil.TestStaleCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByNormalizedURLFunc: func(normalizedURL string) (*models.CanonicalRecipe, error) {
			return stale, nil
		},
	}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo
	user := testutil.TestUser()

	// With a stale canonical and no real server to fetch from, this should fail
	// during extraction — proving the stale cache was skipped.
	_, err := svc.ImportFromURL(context.Background(), "https://example.com/classic-pancakes", user)
	if err == nil {
		t.Fatal("expected error when stale canonical is skipped and extraction fails")
	}
}

func TestImportFromCanonical_Success(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	canonical := testutil.TestCanonicalRecipe()

	canonicalRepo := &testutil.MockCanonicalRecipeRepo{
		GetByIDFunc: func(id uint) (*models.CanonicalRecipe, error) {
			if id == canonical.ID {
				return canonical, nil
			}
			return nil, fmt.Errorf("not found")
		},
	}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo
	user := testutil.TestUser()

	resp, err := svc.ImportFromCanonical(context.Background(), canonical.ID, user)
	if err != nil {
		t.Fatalf("ImportFromCanonical error: %v", err)
	}
	if resp == nil {
		t.Fatal("ImportFromCanonical returned nil response")
	}
	if resp.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", resp.Title)
	}
	if len(repo.Recipes) != 1 {
		t.Errorf("recipes in repo = %d, want 1", len(repo.Recipes))
	}
	// Verify the recipe has canonical link
	for _, r := range repo.Recipes {
		if r.CanonicalID == nil {
			t.Error("expected recipe to have CanonicalID set")
		}
		if r.HasDiverged {
			t.Error("expected recipe HasDiverged to be false")
		}
	}
}

func TestImportFromCanonical_NotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	canonicalRepo := &testutil.MockCanonicalRecipeRepo{}

	svc := newTestImportService(repo, nil, nil)
	svc.CanonicalRepo = canonicalRepo
	user := testutil.TestUser()

	_, err := svc.ImportFromCanonical(context.Background(), 999, user)
	if err == nil {
		t.Fatal("ImportFromCanonical with invalid ID should return error")
	}
}

func TestCreateImportedRecipe_CreatesTree(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	user := testutil.TestUser()

	recipeDef := &models.RecipeDef{
		Title:        "Tree Recipe",
		Ingredients:  models.Ingredients{{Name: "Salt"}},
		Instructions: []string{"Add salt"},
		ImagePrompt:  "Salty",
	}

	_, _, err := svc.createImportedRecipe(context.Background(), recipeDef, user, models.RecipeTypeImportCopypasta, "", nil, nil)
	if err != nil {
		t.Fatalf("createImportedRecipe error: %v", err)
	}

	// A tree should have been created
	if len(repo.Trees) != 1 {
		t.Errorf("Expected 1 tree, got %d", len(repo.Trees))
	}
	if len(repo.Nodes) != 1 {
		t.Errorf("Expected 1 node (root), got %d", len(repo.Nodes))
	}
}

// --- extractFromURL tests (using HTTPFetchOverride / FirecrawlFetchOverride) ---

func TestExtractFromURL_DirectSuccess(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte(jsonLDHTML()), 200, nil
	}

	def, _, method, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}
	if def.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", def.Title)
	}
	if method != models.ExtractionJSONLD {
		t.Errorf("method = %q, want %q", method, models.ExtractionJSONLD)
	}
}

func TestExtractFromURL_DirectSuccess_AIFallback(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	mockPreview := &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	svc := newTestImportService(repo, nil, mockPreview)
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte(plainHTML()), 200, nil
	}

	def, _, method, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}
	if def.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", def.Title)
	}
	if method != models.ExtractionHaiku {
		t.Errorf("method = %q, want %q", method, models.ExtractionHaiku)
	}
}

func TestExtractFromURL_403_FirecrawlSuccess(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	svc.Cfg.EnvVars.FirecrawlAPIKey = "test-key"
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Forbidden"), 403, nil
	}
	svc.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, error) {
		return jsonLDHTML(), nil
	}

	def, _, method, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}
	if def.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", def.Title)
	}
	if method != models.ExtractionFirecrawlJSONLD {
		t.Errorf("method = %q, want %q", method, models.ExtractionFirecrawlJSONLD)
	}
}

func TestExtractFromURL_403_FirecrawlFail(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	svc.Cfg.EnvVars.FirecrawlAPIKey = "test-key"
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Forbidden"), 403, nil
	}
	svc.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, error) {
		return "", fmt.Errorf("firecrawl error")
	}

	_, _, _, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err == nil {
		t.Fatal("expected error when firecrawl fails")
	}
	var extractErr *ExtractionError
	if !errors.As(err, &extractErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extractErr.Code != "site_blocked" {
		t.Errorf("code = %q, want 'site_blocked'", extractErr.Code)
	}
}

func TestExtractFromURL_403_NoFirecrawlKey(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	// No FirecrawlAPIKey set
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Forbidden"), 403, nil
	}

	_, _, _, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err == nil {
		t.Fatal("expected error when no firecrawl key")
	}
	var extractErr *ExtractionError
	if !errors.As(err, &extractErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extractErr.Code != "site_blocked" {
		t.Errorf("code = %q, want 'site_blocked'", extractErr.Code)
	}
}

func TestExtractFromURL_404(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Not Found"), 404, nil
	}

	_, _, _, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err == nil {
		t.Fatal("expected error on 404")
	}
	var extractErr *ExtractionError
	if !errors.As(err, &extractErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extractErr.Code != "not_found" {
		t.Errorf("code = %q, want 'not_found'", extractErr.Code)
	}
}

func TestExtractFromURL_CloudflareChallenge(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	svc.Cfg.EnvVars.FirecrawlAPIKey = "test-key"
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte(cloudflareHTML()), 200, nil
	}
	svc.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, error) {
		return jsonLDHTML(), nil
	}

	def, _, method, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err != nil {
		t.Fatalf("extractFromURL error: %v", err)
	}
	if def.Title != "Classic Pancakes" {
		t.Errorf("title = %q, want 'Classic Pancakes'", def.Title)
	}
	if method != models.ExtractionFirecrawlJSONLD {
		t.Errorf("method = %q, want %q", method, models.ExtractionFirecrawlJSONLD)
	}
}

func TestExtractFromURL_500_NoFirecrawl(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	svc := newTestImportService(repo, nil, nil)
	svc.Cfg.EnvVars.FirecrawlAPIKey = "test-key"
	firecrawlCalled := false
	svc.HTTPFetchOverride = func(ctx context.Context, url string) ([]byte, int, error) {
		return []byte("Internal Server Error"), 500, nil
	}
	svc.FirecrawlFetchOverride = func(ctx context.Context, url string) (string, error) {
		firecrawlCalled = true
		return "", fmt.Errorf("should not be called")
	}

	_, _, _, err := svc.extractFromURL(context.Background(), "https://example.com/recipe")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	var extractErr *ExtractionError
	if !errors.As(err, &extractErr) {
		t.Fatalf("expected ExtractionError, got %T: %v", err, err)
	}
	if extractErr.Code != "fetch_failed" {
		t.Errorf("code = %q, want 'fetch_failed'", extractErr.Code)
	}
	if firecrawlCalled {
		t.Error("firecrawl should not be called for a 500 response")
	}
}

func TestIsBotBlockStatus(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{200, false},
		{301, false},
		{400, false},
		{402, true},
		{403, true},
		{404, false},
		{429, false},
		{500, false},
		{502, false},
		{503, true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.code), func(t *testing.T) {
			got := isBotBlockStatus(tt.code)
			if got != tt.want {
				t.Errorf("isBotBlockStatus(%d) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestIsCloudflareChallenge(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"cloudflare title", "<title>Just a moment...</title>", true},
		{"challenge-platform div", "<div id=\"challenge-platform\"></div>", true},
		{"normal page", "<html><title>My Recipe</title></html>", false},
		{"empty body", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCloudflareChallenge([]byte(tt.body))
			if got != tt.want {
				t.Errorf("isCloudflareChallenge(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
