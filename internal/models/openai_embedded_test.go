package models

import (
	"encoding/json"
	"testing"
)

func TestRecipeDefScanValue_RoundTrip(t *testing.T) {
	original := RecipeDef{
		Title: "Test Recipe",
		Ingredients: Ingredients{
			{Name: "Flour", Unit: "cups", Amount: 2},
			{Name: "Sugar", Unit: "tbsp", Amount: 1},
		},
		Instructions: []string{"Mix", "Bake"},
		CookTime:     30,
		ImagePrompt:  "A baked item",
		Portions:     4,
		PortionSize:  "1 slice",
	}

	// Value -> bytes
	val, err := original.Value()
	if err != nil {
		t.Fatalf("RecipeDef.Value() error: %v", err)
	}
	bytes, ok := val.([]byte)
	if !ok {
		t.Fatal("RecipeDef.Value() did not return []byte")
	}

	// Scan -> RecipeDef
	var scanned RecipeDef
	if err := scanned.Scan(bytes); err != nil {
		t.Fatalf("RecipeDef.Scan() error: %v", err)
	}

	if scanned.Title != original.Title {
		t.Errorf("Title: got %q, want %q", scanned.Title, original.Title)
	}
	if len(scanned.Ingredients) != len(original.Ingredients) {
		t.Errorf("Ingredients count: got %d, want %d", len(scanned.Ingredients), len(original.Ingredients))
	}
	if scanned.CookTime != original.CookTime {
		t.Errorf("CookTime: got %d, want %d", scanned.CookTime, original.CookTime)
	}
	if scanned.Portions != original.Portions {
		t.Errorf("Portions: got %d, want %d", scanned.Portions, original.Portions)
	}
}

func TestRecipeDefScan_InvalidInput(t *testing.T) {
	var def RecipeDef
	err := def.Scan("not bytes")
	if err == nil {
		t.Error("RecipeDef.Scan(string) should return error")
	}
}

func TestRecipeDefScan_InvalidJSON(t *testing.T) {
	var def RecipeDef
	err := def.Scan([]byte("{invalid json"))
	if err == nil {
		t.Error("RecipeDef.Scan(invalid json) should return error")
	}
}

func TestIngredientsScanValue_RoundTrip(t *testing.T) {
	original := Ingredients{
		{Name: "Flour", Unit: "cups", Amount: 2, OriginalText: "2 cups flour"},
		{Name: "Eggs", Unit: "", Amount: 3, OriginalText: "3 eggs"},
	}

	// Value -> bytes
	val, err := original.Value()
	if err != nil {
		t.Fatalf("Ingredients.Value() error: %v", err)
	}
	bytes, ok := val.([]byte)
	if !ok {
		t.Fatal("Ingredients.Value() did not return []byte")
	}

	// Scan -> Ingredients
	var scanned Ingredients
	if err := scanned.Scan(bytes); err != nil {
		t.Fatalf("Ingredients.Scan() error: %v", err)
	}

	if len(scanned) != len(original) {
		t.Fatalf("Ingredients count: got %d, want %d", len(scanned), len(original))
	}
	if scanned[0].Name != "Flour" {
		t.Errorf("Ingredients[0].Name: got %q, want 'Flour'", scanned[0].Name)
	}
	if scanned[1].Amount != 3 {
		t.Errorf("Ingredients[1].Amount: got %f, want 3", scanned[1].Amount)
	}
}

func TestIngredientsScan_InvalidInput(t *testing.T) {
	var ings Ingredients
	err := ings.Scan(42)
	if err == nil {
		t.Error("Ingredients.Scan(int) should return error")
	}
}

func TestRecipeDefValue_ValidJSON(t *testing.T) {
	def := RecipeDef{Title: "Test"}
	val, err := def.Value()
	if err != nil {
		t.Fatalf("RecipeDef.Value() error: %v", err)
	}
	bytes := val.([]byte)

	var parsed map[string]interface{}
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("RecipeDef.Value() produced invalid JSON: %v", err)
	}
	if parsed["title"] != "Test" {
		t.Errorf("RecipeDef.Value() title = %v, want 'Test'", parsed["title"])
	}
}
