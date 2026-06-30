package ai

import (
	"encoding/json"
	"testing"
)

func TestIngredientToolResList_TolerantUnmarshal(t *testing.T) {
	cases := []struct {
		name          string
		json          string
		wantCount     int
		wantFirstName string
	}{
		{"array of objects", `[{"name":"flour","amount":2},{"name":"sugar"}]`, 2, "flour"},
		{"array mixing strings + objects", `["1 cup flour", {"name":"sugar"}]`, 2, "1 cup flour"},
		{"bare string, newline-separated", "\"1 cup flour\\n2 eggs\\n\"", 2, "1 cup flour"},
		{"bare string, comma-separated", `"flour, sugar, eggs"`, 3, "flour"},
		{"null", `null`, 0, ""},
		{"empty array", `[]`, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var l ingredientToolResList
			if err := json.Unmarshal([]byte(tc.json), &l); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(l) != tc.wantCount {
				t.Fatalf("count = %d, want %d (%+v)", len(l), tc.wantCount, l)
			}
			if tc.wantCount > 0 && l[0].Name != tc.wantFirstName {
				t.Errorf("first name = %q, want %q", l[0].Name, tc.wantFirstName)
			}
		})
	}
}

func TestRecipeToolResult_IngredientsAsString(t *testing.T) {
	// The whole ingredients field arriving as a string (the model's shape drift
	// that previously failed extraction) must parse, not error.
	const blob = `{"title":"Pancakes","ingredients":"1 cup flour\n2 eggs","instructions":["mix","cook"]}`
	var tr recipeToolResult
	if err := json.Unmarshal([]byte(blob), &tr); err != nil {
		t.Fatalf("tolerant unmarshal failed: %v", err)
	}
	if tr.Title != "Pancakes" {
		t.Errorf("title = %q", tr.Title)
	}
	if len(tr.Ingredients) != 2 {
		t.Errorf("ingredients = %d, want 2 (%+v)", len(tr.Ingredients), tr.Ingredients)
	}
}
