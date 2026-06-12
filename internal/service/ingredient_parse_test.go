package service

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestParseIngredientLine(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAmount float64
		wantUnit   string
		wantName   string
		wantOK     bool
	}{
		// integers and decimals
		{"integer with cups", "2 cups flour", 2, "cup", "flour", true},
		{"decimal amount", "1.5 cups sugar", 1.5, "cup", "sugar", true},
		{"integer no unit", "3 eggs", 3, "", "eggs", true},
		// ASCII fractions
		{"ascii fraction", "1/2 cup butter", 0.5, "cup", "butter", true},
		{"ascii fraction tsp", "3/4 tsp salt", 0.75, "tsp", "salt", true},
		// unicode fractions
		{"unicode half", "½ cup milk", 0.5, "cup", "milk", true},
		{"unicode quarter", "¼ tsp nutmeg", 0.25, "tsp", "nutmeg", true},
		{"unicode third", "⅓ cup honey", 1.0 / 3, "cup", "honey", true},
		{"unicode eighth", "⅛ tsp cayenne", 0.125, "tsp", "cayenne", true},
		// mixed numbers
		{"mixed number spaced", "1 1/2 cups flour", 1.5, "cup", "flour", true},
		{"mixed number attached unicode", "1½ cups flour", 1.5, "cup", "flour", true},
		{"mixed number spaced unicode", "2 ¾ cups broth", 2.75, "cup", "broth", true},
		{"mixed number hyphenated", "1-1/2 cups flour", 1.5, "cup", "flour", true},
		// ranges — low value wins
		{"hyphen range", "1-2 tbsp olive oil", 1, "tbsp", "olive oil", true},
		{"spaced hyphen range", "2 - 3 cups stock", 2, "cup", "stock", true},
		{"to range", "1 to 2 tsp vanilla", 1, "tsp", "vanilla", true},
		{"fraction-to-fraction range", "1/2-3/4 cup butter", 0.5, "cup", "butter", true},
		{"unicode fraction range", "½-¾ cup sugar", 0.5, "cup", "sugar", true},
		{"decimal-led hyphen range", "1.5-2 cups stock", 1.5, "cup", "stock", true},
		// unit aliases
		{"tablespoon long form", "2 tablespoons butter", 2, "tbsp", "butter", true},
		{"teaspoon long form", "1 teaspoon vanilla", 1, "tsp", "vanilla", true},
		{"uppercase T is tbsp", "1 T sugar", 1, "tbsp", "sugar", true},
		{"lowercase t is tsp", "1 t sugar", 1, "tsp", "sugar", true},
		{"c with period", "2 c. flour", 2, "cup", "flour", true},
		{"ounces", "8 ounces cream cheese", 8, "oz", "cream cheese", true},
		{"pounds lbs", "2 lbs chicken thighs", 2, "lb", "chicken thighs", true},
		{"grams compact", "250 g flour", 250, "g", "flour", true},
		{"kilograms", "1 kg potatoes", 1, "kg", "potatoes", true},
		{"milliliters", "100 ml milk", 100, "mL", "milk", true},
		{"liters", "1 liter water", 1, "L", "water", true},
		{"fl oz two tokens", "4 fl oz heavy cream", 4, "fl oz", "heavy cream", true},
		{"fluid ounces", "8 fluid ounces water", 8, "fl oz", "water", true},
		{"pinch", "1 pinch saffron", 1, "pinch", "saffron", true},
		{"cloves map to pieces", "2 cloves garlic", 2, "pieces", "garlic", true},
		{"can maps to pieces", "1 can black beans", 1, "pieces", "black beans", true},
		{"slices map to pieces", "4 slices bacon", 4, "pieces", "bacon", true},
		// "of" stripping
		{"of after unit", "2 cups of flour", 2, "cup", "flour", true},
		{"pinch of", "1 pinch of salt", 1, "pinch", "salt", true},
		// no unit, descriptive words stay in name
		{"descriptive no unit", "2 large eggs", 2, "", "large eggs", true},
		// unparseable — words-only quantities stay unparsed
		{"words only quantity", "a pinch of salt", 0, "", "", false},
		{"no quantity at all", "salt to taste", 0, "", "", false},
		{"empty string", "", 0, "", "", false},
		{"only a number", "2", 0, "", "", false},
		{"unit but no name", "2 cups", 0, "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, unit, name, ok := ParseIngredientLine(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ParseIngredientLine(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if !almostEqual(amount, tt.wantAmount) {
				t.Errorf("ParseIngredientLine(%q) amount = %v, want %v", tt.input, amount, tt.wantAmount)
			}
			if unit != tt.wantUnit {
				t.Errorf("ParseIngredientLine(%q) unit = %q, want %q", tt.input, unit, tt.wantUnit)
			}
			if name != tt.wantName {
				t.Errorf("ParseIngredientLine(%q) name = %q, want %q", tt.input, name, tt.wantName)
			}
		})
	}
}
