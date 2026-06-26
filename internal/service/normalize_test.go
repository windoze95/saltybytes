package service

import (
	"math"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/units"
)

func TestNormalizeIngredients_SetsMeasureFields(t *testing.T) {
	def := &models.RecipeDef{Ingredients: models.Ingredients{
		{Name: "flour", Unit: "cup", Amount: 2, MetricUnit: "g", MetricAmount: 240},
		{Name: "beef", Unit: "g", Amount: 500},
		{Name: "egg", Unit: "pieces", Amount: 3},
		{Name: "salt", Unit: "pinch", Amount: 1},
	}}
	normalizeIngredients(def)

	if k := def.Ingredients[0].MeasureKind; k != units.KindVolume {
		t.Errorf("flour kind = %q; want volume", k)
	}
	if b := def.Ingredients[0].BaseAmount; math.Abs(b-473.176) > 0.1 {
		t.Errorf("flour base = %v; want ~473.18 mL", b)
	}
	if k := def.Ingredients[1].MeasureKind; k != units.KindMass {
		t.Errorf("beef kind = %q; want mass", k)
	}
	if def.Ingredients[2].MeasureKind != units.KindCount || def.Ingredients[2].BaseAmount != 3 {
		t.Errorf("egg = %q/%v; want count/3", def.Ingredients[2].MeasureKind, def.Ingredients[2].BaseAmount)
	}
	if def.Ingredients[3].MeasureKind != units.KindImprecise || def.Ingredients[3].BaseAmount != 0 {
		t.Errorf("salt = %q/%v; want imprecise/0", def.Ingredients[3].MeasureKind, def.Ingredients[3].BaseAmount)
	}
}

func TestNormalizeIngredients_DensityGuard(t *testing.T) {
	// Legit density (flour ~0.5 g/mL) is preserved.
	legit := &models.RecipeDef{Ingredients: models.Ingredients{
		{Name: "flour", Unit: "cup", Amount: 2, MetricUnit: "g", MetricAmount: 240},
	}}
	normalizeIngredients(legit)
	if legit.Ingredients[0].MetricUnit != "g" || legit.Ingredients[0].MetricAmount != 240 {
		t.Errorf("legit metric pair was altered: %v %v", legit.Ingredients[0].MetricAmount, legit.Ingredients[0].MetricUnit)
	}

	// Order-of-magnitude hallucination (2 cups -> 5000 g, ~10.6 g/mL) is
	// rejected in favor of an exact same-dimension metric volume.
	bogus := &models.RecipeDef{Ingredients: models.Ingredients{
		{Name: "flour", Unit: "cup", Amount: 2, MetricUnit: "g", MetricAmount: 5000},
	}}
	normalizeIngredients(bogus)
	if bogus.Ingredients[0].MetricUnit != "mL" {
		t.Errorf("bogus metric pair not rejected: got %v %v; want mL volume",
			bogus.Ingredients[0].MetricAmount, bogus.Ingredients[0].MetricUnit)
	}
}

// The canonical recipe must be byte-identical regardless of who imported it:
// normalization takes no user input.
func TestNormalizeIngredients_UserAgnostic(t *testing.T) {
	mk := func() *models.RecipeDef {
		return &models.RecipeDef{Ingredients: models.Ingredients{
			{Name: "flour", Unit: "cup", Amount: 2, MetricUnit: "g", MetricAmount: 240},
			{Name: "milk", Unit: "mL", Amount: 250},
		}}
	}
	a, b := mk(), mk()
	normalizeIngredients(a)
	normalizeIngredients(b)
	for i := range a.Ingredients {
		if a.Ingredients[i] != b.Ingredients[i] {
			t.Errorf("ingredient %d differs across runs: %+v vs %+v", i, a.Ingredients[i], b.Ingredients[i])
		}
	}
}
