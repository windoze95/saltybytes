package service

import (
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
)

// --- parseISO8601Duration ---

func TestParseISO8601Duration_30Minutes(t *testing.T) {
	got := parseISO8601Duration("PT30M")
	if got != 30 {
		t.Errorf("parseISO8601Duration(PT30M) = %d, want 30", got)
	}
}

func TestParseISO8601Duration_1Hour30Minutes(t *testing.T) {
	got := parseISO8601Duration("PT1H30M")
	if got != 90 {
		t.Errorf("parseISO8601Duration(PT1H30M) = %d, want 90", got)
	}
}

func TestParseISO8601Duration_45Seconds(t *testing.T) {
	got := parseISO8601Duration("PT45S")
	if got != 1 {
		t.Errorf("parseISO8601Duration(PT45S) = %d, want 1 (rounds up at 30s)", got)
	}
}

func TestParseISO8601Duration_20Seconds(t *testing.T) {
	got := parseISO8601Duration("PT20S")
	if got != 0 {
		t.Errorf("parseISO8601Duration(PT20S) = %d, want 0 (below 30s threshold)", got)
	}
}

func TestParseISO8601Duration_1Hour(t *testing.T) {
	got := parseISO8601Duration("PT1H")
	if got != 60 {
		t.Errorf("parseISO8601Duration(PT1H) = %d, want 60", got)
	}
}

func TestParseISO8601Duration_Empty(t *testing.T) {
	got := parseISO8601Duration("")
	if got != 0 {
		t.Errorf("parseISO8601Duration('') = %d, want 0", got)
	}
}

func TestParseISO8601Duration_Invalid(t *testing.T) {
	got := parseISO8601Duration("invalid")
	if got != 0 {
		t.Errorf("parseISO8601Duration(invalid) = %d, want 0", got)
	}
}

func TestParseISO8601Duration_Lowercase(t *testing.T) {
	got := parseISO8601Duration("pt15m")
	if got != 15 {
		t.Errorf("parseISO8601Duration(pt15m) = %d, want 15 (should be case-insensitive)", got)
	}
}

func TestParseISO8601Duration_HourMinuteSecond(t *testing.T) {
	got := parseISO8601Duration("PT2H15M30S")
	if got != 136 {
		t.Errorf("parseISO8601Duration(PT2H15M30S) = %d, want 136 (2*60+15+1)", got)
	}
}

// --- parseYield ---

func TestParseYield_String(t *testing.T) {
	got := parseYield("4 servings")
	if got != 4 {
		t.Errorf("parseYield('4 servings') = %d, want 4", got)
	}
}

func TestParseYield_Float64(t *testing.T) {
	got := parseYield(float64(6))
	if got != 6 {
		t.Errorf("parseYield(6.0) = %d, want 6", got)
	}
}

func TestParseYield_Array(t *testing.T) {
	got := parseYield([]interface{}{"4 servings"})
	if got != 4 {
		t.Errorf("parseYield(['4 servings']) = %d, want 4", got)
	}
}

func TestParseYield_Nil(t *testing.T) {
	got := parseYield(nil)
	if got != 0 {
		t.Errorf("parseYield(nil) = %d, want 0", got)
	}
}

func TestParseYield_EmptyArray(t *testing.T) {
	got := parseYield([]interface{}{})
	if got != 0 {
		t.Errorf("parseYield([]) = %d, want 0", got)
	}
}

func TestParseYield_StringNoNumber(t *testing.T) {
	got := parseYield("servings")
	if got != 0 {
		t.Errorf("parseYield('servings') = %d, want 0", got)
	}
}

// --- parseKeywords ---

func TestParseKeywords_CommaSeparatedString(t *testing.T) {
	got := parseKeywords("easy, breakfast, pancakes")
	if len(got) != 3 {
		t.Fatalf("parseKeywords comma-separated: got %d keywords, want 3", len(got))
	}
	if got[0] != "easy" || got[1] != "breakfast" || got[2] != "pancakes" {
		t.Errorf("parseKeywords comma-separated: got %v, want [easy breakfast pancakes]", got)
	}
}

func TestParseKeywords_Array(t *testing.T) {
	got := parseKeywords([]interface{}{"easy", "breakfast"})
	if len(got) != 2 {
		t.Fatalf("parseKeywords array: got %d keywords, want 2", len(got))
	}
	if got[0] != "easy" || got[1] != "breakfast" {
		t.Errorf("parseKeywords array: got %v, want [easy breakfast]", got)
	}
}

func TestParseKeywords_Nil(t *testing.T) {
	got := parseKeywords(nil)
	if got != nil {
		t.Errorf("parseKeywords(nil) = %v, want nil", got)
	}
}

func TestParseKeywords_EmptyString(t *testing.T) {
	got := parseKeywords("")
	if len(got) != 0 {
		t.Errorf("parseKeywords('') = %v, want empty", got)
	}
}

func TestParseKeywords_StringWithEmptyParts(t *testing.T) {
	got := parseKeywords("easy,, ,breakfast")
	if len(got) != 2 {
		t.Fatalf("parseKeywords with blanks: got %d keywords, want 2", len(got))
	}
	if got[0] != "easy" || got[1] != "breakfast" {
		t.Errorf("parseKeywords with blanks: got %v", got)
	}
}

// --- isRecipeType ---

func TestIsRecipeType_Recipe(t *testing.T) {
	if !isRecipeType("Recipe") {
		t.Error("isRecipeType('Recipe') should be true")
	}
}

func TestIsRecipeType_SchemaOrg(t *testing.T) {
	if !isRecipeType("http://schema.org/Recipe") {
		t.Error("isRecipeType('http://schema.org/Recipe') should be true")
	}
}

func TestIsRecipeType_ArrayWithRecipe(t *testing.T) {
	if !isRecipeType([]interface{}{"Thing", "Recipe"}) {
		t.Error("isRecipeType(['Thing','Recipe']) should be true")
	}
}

func TestIsRecipeType_NotRecipe(t *testing.T) {
	if isRecipeType("NotRecipe") {
		t.Error("isRecipeType('NotRecipe') should be false")
	}
}

func TestIsRecipeType_ArrayWithoutRecipe(t *testing.T) {
	if isRecipeType([]interface{}{"Thing", "Article"}) {
		t.Error("isRecipeType(['Thing','Article']) should be false")
	}
}

func TestIsRecipeType_Nil(t *testing.T) {
	if isRecipeType(nil) {
		t.Error("isRecipeType(nil) should be false")
	}
}

// --- parseJSONLDInstructions ---

func TestParseJSONLDInstructions_String(t *testing.T) {
	got := parseJSONLDInstructions("Mix everything together.")
	if len(got) != 1 || got[0] != "Mix everything together." {
		t.Errorf("parseJSONLDInstructions(string) = %v", got)
	}
}

func TestParseJSONLDInstructions_ArrayOfStrings(t *testing.T) {
	got := parseJSONLDInstructions([]interface{}{"Step 1", "Step 2"})
	if len(got) != 2 || got[0] != "Step 1" || got[1] != "Step 2" {
		t.Errorf("parseJSONLDInstructions([]string) = %v", got)
	}
}

func TestParseJSONLDInstructions_HowToStep(t *testing.T) {
	steps := []interface{}{
		map[string]interface{}{"@type": "HowToStep", "text": "Preheat oven"},
		map[string]interface{}{"@type": "HowToStep", "text": "Mix ingredients"},
	}
	got := parseJSONLDInstructions(steps)
	if len(got) != 2 || got[0] != "Preheat oven" || got[1] != "Mix ingredients" {
		t.Errorf("parseJSONLDInstructions(HowToStep) = %v", got)
	}
}

func TestParseJSONLDInstructions_HowToSection(t *testing.T) {
	sections := []interface{}{
		map[string]interface{}{
			"@type": "HowToSection",
			"itemListElement": []interface{}{
				map[string]interface{}{"@type": "HowToStep", "text": "Sub-step 1"},
				map[string]interface{}{"@type": "HowToStep", "text": "Sub-step 2"},
			},
		},
	}
	got := parseJSONLDInstructions(sections)
	if len(got) != 2 || got[0] != "Sub-step 1" || got[1] != "Sub-step 2" {
		t.Errorf("parseJSONLDInstructions(HowToSection) = %v", got)
	}
}

func TestParseJSONLDInstructions_Nil(t *testing.T) {
	got := parseJSONLDInstructions(nil)
	if got != nil {
		t.Errorf("parseJSONLDInstructions(nil) = %v, want nil", got)
	}
}

// --- extractJSONLD ---

func TestExtractJSONLD_ValidRecipe(t *testing.T) {
	html := `<html><head>
	<script type="application/ld+json">
	{
		"@context": "https://schema.org",
		"@type": "Recipe",
		"name": "Test Recipe",
		"recipeIngredient": ["1 cup flour", "2 eggs"],
		"recipeInstructions": [{"@type":"HowToStep","text":"Mix"}],
		"cookTime": "PT30M"
	}
	</script>
	</head><body></body></html>`

	def, err := extractJSONLD(html)
	if err != nil {
		t.Fatalf("extractJSONLD valid recipe: unexpected error: %v", err)
	}
	if def.Title != "Test Recipe" {
		t.Errorf("extractJSONLD title = %q, want 'Test Recipe'", def.Title)
	}
	if len(def.Ingredients) != 2 {
		t.Errorf("extractJSONLD ingredients count = %d, want 2", len(def.Ingredients))
	}
	if def.CookTime != 30 {
		t.Errorf("extractJSONLD cookTime = %d, want 30", def.CookTime)
	}
}

func TestExtractJSONLD_GraphContainer(t *testing.T) {
	html := `<html><head>
	<script type="application/ld+json">
	{
		"@context": "https://schema.org",
		"@graph": [
			{"@type": "WebPage", "name": "Page"},
			{
				"@type": "Recipe",
				"name": "Graph Recipe",
				"recipeIngredient": ["salt"],
				"recipeInstructions": "Cook it"
			}
		]
	}
	</script>
	</head><body></body></html>`

	def, err := extractJSONLD(html)
	if err != nil {
		t.Fatalf("extractJSONLD @graph: unexpected error: %v", err)
	}
	if def.Title != "Graph Recipe" {
		t.Errorf("extractJSONLD @graph title = %q, want 'Graph Recipe'", def.Title)
	}
}

func TestExtractJSONLD_NoJSONLD(t *testing.T) {
	html := `<html><head><title>No JSON-LD</title></head><body></body></html>`
	_, err := extractJSONLD(html)
	if err == nil {
		t.Error("extractJSONLD with no JSON-LD should return error")
	}
}

func TestExtractJSONLD_NonRecipeType(t *testing.T) {
	html := `<html><head>
	<script type="application/ld+json">
	{"@context": "https://schema.org", "@type": "Article", "name": "Not a recipe"}
	</script>
	</head><body></body></html>`

	_, err := extractJSONLD(html)
	if err == nil {
		t.Error("extractJSONLD with non-Recipe type should return error")
	}
}

func TestExtractJSONLD_UsesTotalTimeAsFallback(t *testing.T) {
	html := `<html><head>
	<script type="application/ld+json">
	{
		"@context": "https://schema.org",
		"@type": "Recipe",
		"name": "Fallback Time",
		"recipeIngredient": ["water"],
		"totalTime": "PT45M"
	}
	</script>
	</head><body></body></html>`

	def, err := extractJSONLD(html)
	if err != nil {
		t.Fatalf("extractJSONLD totalTime fallback: unexpected error: %v", err)
	}
	if def.CookTime != 45 {
		t.Errorf("extractJSONLD totalTime fallback = %d, want 45", def.CookTime)
	}
}

// --- aiResultToRecipeDef ---

func TestAiResultToRecipeDef_AllFieldsMapped(t *testing.T) {
	result := &ai.RecipeResult{
		Title: "Test Recipe",
		Ingredients: []ai.IngredientResult{
			{Name: "Flour", Unit: "cups", Amount: 2, OriginalText: "2 cups flour", NormalizedAmount: 240, NormalizedUnit: "g", IsEstimated: false},
			{Name: "Sugar", Unit: "tbsp", Amount: 1, OriginalText: "1 tbsp sugar"},
		},
		Instructions:      []string{"Mix", "Bake"},
		CookTime:          30,
		ImagePrompt:       "A baked item",
		Hashtags:          []string{"baking"},
		LinkedSuggestions: []string{"Cookie Recipe"},
		Portions:          8,
		PortionSize:       "1 slice",
		SourceURL:         "https://example.com",
	}

	def := aiResultToRecipeDef(result)
	if def.Title != "Test Recipe" {
		t.Errorf("aiResultToRecipeDef Title = %q, want 'Test Recipe'", def.Title)
	}
	if len(def.Ingredients) != 2 {
		t.Errorf("aiResultToRecipeDef Ingredients count = %d, want 2", len(def.Ingredients))
	}
	if def.Ingredients[0].Name != "Flour" {
		t.Errorf("aiResultToRecipeDef Ingredients[0].Name = %q, want 'Flour'", def.Ingredients[0].Name)
	}
	if def.Ingredients[0].NormalizedAmount != 240 {
		t.Errorf("aiResultToRecipeDef Ingredients[0].NormalizedAmount = %f, want 240", def.Ingredients[0].NormalizedAmount)
	}
	if def.Ingredients[0].NormalizedUnit != "g" {
		t.Errorf("aiResultToRecipeDef Ingredients[0].NormalizedUnit = %q, want 'g'", def.Ingredients[0].NormalizedUnit)
	}
	if len(def.Instructions) != 2 {
		t.Errorf("aiResultToRecipeDef Instructions count = %d, want 2", len(def.Instructions))
	}
	if def.CookTime != 30 {
		t.Errorf("aiResultToRecipeDef CookTime = %d, want 30", def.CookTime)
	}
	if def.ImagePrompt != "A baked item" {
		t.Errorf("aiResultToRecipeDef ImagePrompt = %q", def.ImagePrompt)
	}
	if len(def.Hashtags) != 1 || def.Hashtags[0] != "baking" {
		t.Errorf("aiResultToRecipeDef Hashtags = %v", def.Hashtags)
	}
	if def.Portions != 8 {
		t.Errorf("aiResultToRecipeDef Portions = %d, want 8", def.Portions)
	}
	if def.PortionSize != "1 slice" {
		t.Errorf("aiResultToRecipeDef PortionSize = %q", def.PortionSize)
	}
	if def.SourceURL != "https://example.com" {
		t.Errorf("aiResultToRecipeDef SourceURL = %q", def.SourceURL)
	}
}

func TestAiResultToRecipeDef_EmptyIngredients(t *testing.T) {
	result := &ai.RecipeResult{
		Title:        "Empty Ingredients",
		Ingredients:  nil,
		Instructions: []string{"Do nothing"},
		CookTime:     0,
	}
	def := aiResultToRecipeDef(result)
	if len(def.Ingredients) != 0 {
		t.Errorf("aiResultToRecipeDef with nil ingredients = %d, want 0", len(def.Ingredients))
	}
}

// --- jsonLDToRecipeDef ---

func TestJsonLDToRecipeDef_Valid(t *testing.T) {
	recipe := &jsonLDRecipe{
		Name:         "Pasta",
		Ingredients:  []string{"2 cups pasta", "1 cup sauce"},
		Instructions: []interface{}{"Boil pasta", "Add sauce"},
		CookTime:     "PT20M",
		Yield:        "4 servings",
		Keywords:     "italian, pasta, easy",
	}

	def, err := jsonLDToRecipeDef(recipe)
	if err != nil {
		t.Fatalf("jsonLDToRecipeDef valid: %v", err)
	}
	if def.Title != "Pasta" {
		t.Errorf("title = %q, want 'Pasta'", def.Title)
	}
	if len(def.Ingredients) != 2 {
		t.Errorf("ingredients count = %d, want 2", len(def.Ingredients))
	}
	// Check that original text is set
	if def.Ingredients[0].OriginalText != "2 cups pasta" {
		t.Errorf("ingredient original text = %q, want '2 cups pasta'", def.Ingredients[0].OriginalText)
	}
	if def.CookTime != 20 {
		t.Errorf("cookTime = %d, want 20", def.CookTime)
	}
	if def.Portions != 4 {
		t.Errorf("portions = %d, want 4", def.Portions)
	}
	if len(def.Hashtags) != 3 {
		t.Errorf("hashtags count = %d, want 3", len(def.Hashtags))
	}
	if def.ImagePrompt == "" {
		t.Error("imagePrompt should be auto-generated from recipe name")
	}
}

func TestJsonLDToRecipeDef_EmptyName(t *testing.T) {
	recipe := &jsonLDRecipe{
		Name:        "",
		Ingredients: []string{"flour"},
	}
	_, err := jsonLDToRecipeDef(recipe)
	if err == nil {
		t.Error("jsonLDToRecipeDef with empty name should return error")
	}
}

func TestJsonLDToRecipeDef_TotalTimeFallback(t *testing.T) {
	recipe := &jsonLDRecipe{
		Name:        "Test",
		TotalTime:   "PT1H",
		Ingredients: []string{"water"},
	}
	def, err := jsonLDToRecipeDef(recipe)
	if err != nil {
		t.Fatalf("jsonLDToRecipeDef totalTime fallback: %v", err)
	}
	if def.CookTime != 60 {
		t.Errorf("cookTime totalTime fallback = %d, want 60", def.CookTime)
	}
}

// --- cleanHashtag (unexported helper in recipe.go) ---

func TestCleanHashtag_WithHash(t *testing.T) {
	got := cleanHashtag("#Breakfast")
	if got != "breakfast" {
		t.Errorf("cleanHashtag('#Breakfast') = %q, want 'breakfast'", got)
	}
}

func TestCleanHashtag_WithSpaces(t *testing.T) {
	got := cleanHashtag("Easy Meal")
	if got != "easymeal" {
		t.Errorf("cleanHashtag('Easy Meal') = %q, want 'easymeal'", got)
	}
}

func TestCleanHashtag_AlreadyClean(t *testing.T) {
	got := cleanHashtag("pasta")
	if got != "pasta" {
		t.Errorf("cleanHashtag('pasta') = %q, want 'pasta'", got)
	}
}

// --- recipeResultToRecipeDef (in recipe.go) ---

func TestRecipeResultToRecipeDef(t *testing.T) {
	result := &ai.RecipeResult{
		Title: "From RecipeService",
		Ingredients: []ai.IngredientResult{
			{Name: "Salt", Unit: "tsp", Amount: 0.5},
		},
		Instructions: []string{"Add salt"},
		CookTime:     5,
		ImagePrompt:  "Salty dish",
		Hashtags:     []string{"salty"},
		Portions:     2,
		PortionSize:  "1 serving",
	}

	def := recipeResultToRecipeDef(result)
	if def.Title != "From RecipeService" {
		t.Errorf("recipeResultToRecipeDef Title = %q", def.Title)
	}
	if len(def.Ingredients) != 1 || def.Ingredients[0].Name != "Salt" {
		t.Errorf("recipeResultToRecipeDef Ingredients = %v", def.Ingredients)
	}
}

// --- validateRecipeCoreFields ---

func TestValidateRecipeCoreFields_Valid(t *testing.T) {
	recipe := &models.Recipe{
		RecipeDef: models.RecipeDef{
			Title:        "Valid",
			Ingredients:  models.Ingredients{{Name: "flour"}},
			Instructions: []string{"mix"},
			ImagePrompt:  "a dish",
		},
	}
	err := validateRecipeCoreFields(recipe)
	if err != nil {
		t.Errorf("validateRecipeCoreFields valid recipe: %v", err)
	}
}

func TestValidateRecipeCoreFields_MissingTitle(t *testing.T) {
	recipe := &models.Recipe{
		RecipeDef: models.RecipeDef{
			Ingredients:  models.Ingredients{{Name: "flour"}},
			Instructions: []string{"mix"},
			ImagePrompt:  "a dish",
		},
	}
	err := validateRecipeCoreFields(recipe)
	if err == nil {
		t.Error("validateRecipeCoreFields missing title should return error")
	}
}

func TestValidateRecipeCoreFields_MissingImagePrompt(t *testing.T) {
	recipe := &models.Recipe{
		RecipeDef: models.RecipeDef{
			Title:        "Test",
			Ingredients:  models.Ingredients{{Name: "flour"}},
			Instructions: []string{"mix"},
		},
	}
	err := validateRecipeCoreFields(recipe)
	if err == nil {
		t.Error("validateRecipeCoreFields missing image prompt should return error")
	}
}

// --- NodeHistoryToEntries ---

func TestNodeHistoryToEntries(t *testing.T) {
	def := models.RecipeDef{Title: "Test"}
	nodes := []models.RecipeNode{
		{Prompt: "Make a cake", Response: &def, Summary: "Cake", Type: models.RecipeTypeChat},
		{Prompt: "Add chocolate", Response: &def, Summary: "Chocolate cake", Type: models.RecipeTypeRegenChat},
	}
	entries := NodeHistoryToEntries(nodes)
	if len(entries) != 2 {
		t.Fatalf("NodeHistoryToEntries: got %d entries, want 2", len(entries))
	}
	if entries[0].Order != 0 || entries[1].Order != 1 {
		t.Errorf("NodeHistoryToEntries order: [%d, %d], want [0, 1]", entries[0].Order, entries[1].Order)
	}
	if entries[0].Prompt != "Make a cake" {
		t.Errorf("NodeHistoryToEntries[0].Prompt = %q", entries[0].Prompt)
	}
	if entries[1].Type != models.RecipeTypeRegenChat {
		t.Errorf("NodeHistoryToEntries[1].Type = %q", entries[1].Type)
	}
}

func TestNodeHistoryToEntries_Empty(t *testing.T) {
	entries := NodeHistoryToEntries(nil)
	if len(entries) != 0 {
		t.Errorf("NodeHistoryToEntries(nil): got %d entries, want 0", len(entries))
	}
}
