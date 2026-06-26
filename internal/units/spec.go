// Package units is the single source of truth for SaltyBytes' unit handling:
// canonical unit metadata (dimension, measurement system, factor to a base
// unit), measure-kind classification, base-quantity normalization, and
// deterministic display conversion.
//
// The conversion model is SAME-DIMENSION-FIRST: mass<->mass and volume<->volume
// are always exact, and crossing dimensions (volume<->mass) is the exception.
// We only ever cross dimensions in the US->metric direction, and only by
// trusting the AI-provided density-aware metric equivalent (metric_unit /
// metric_amount). There is deliberately NO hand-maintained ingredient-density
// table: a metric-source ingredient shown to a US viewer is rendered with an
// exact same-dimension unit (g -> oz/lb, mL -> tsp/cup), never a guessed cup
// count. This eliminates the entire class of density-guessing errors.
//
// This Go package is the canonical reference; the Flutter app mirrors these
// constants and rules in lib/core/utils/unit_converter.dart, pinned by the same
// golden vectors in both test suites.
package units

import (
	"math"
	"strings"
)

// Measure kinds. Count and Imprecise units are never converted.
const (
	KindMass      = "mass"
	KindVolume    = "volume"
	KindCount     = "count"
	KindImprecise = "imprecise"
)

// Measurement systems, matching models.RecipeDef.UnitSystem values.
const (
	SystemUS     = "us_customary"
	SystemMetric = "metric"
)

type unitMeta struct {
	dim    string  // KindMass | KindVolume
	system string  // SystemUS | SystemMetric
	factor float64 // amount * factor = base magnitude (g for mass, mL for volume)
}

// meta holds the canonical-unit metadata. Keys are the canonical unit enum used
// by the create_recipe Claude tool and the ingredient parser.
var meta = map[string]unitMeta{
	// US customary volume (base mL)
	"tsp":   {KindVolume, SystemUS, 4.92892},
	"tbsp":  {KindVolume, SystemUS, 14.7868},
	"fl oz": {KindVolume, SystemUS, 29.5735},
	"cup":   {KindVolume, SystemUS, 236.588},
	"pt":    {KindVolume, SystemUS, 473.176},
	"qt":    {KindVolume, SystemUS, 946.353},
	"gal":   {KindVolume, SystemUS, 3785.41},
	// US customary mass (base g)
	"oz": {KindMass, SystemUS, 28.3495},
	"lb": {KindMass, SystemUS, 453.592},
	// metric volume (base mL)
	"mL": {KindVolume, SystemMetric, 1},
	"L":  {KindVolume, SystemMetric, 1000},
	// metric mass (base g)
	"mg": {KindMass, SystemMetric, 0.001},
	"g":  {KindMass, SystemMetric, 1},
	"kg": {KindMass, SystemMetric, 1000},
}

// aliases maps lowercase unit spellings (plurals, abbreviations, long forms) to
// the canonical unit enum. This is the single source of truth; the ingredient
// line parser resolves tokens through Canonical.
var aliases = map[string]string{
	// volume — US customary
	"tsp": "tsp", "tsps": "tsp", "teaspoon": "tsp", "teaspoons": "tsp",
	"tbsp": "tbsp", "tbsps": "tbsp", "tbs": "tbsp", "tbl": "tbsp", "tblsp": "tbsp",
	"tablespoon": "tbsp", "tablespoons": "tbsp",
	"cup": "cup", "cups": "cup", "c": "cup",
	"pt": "pt", "pts": "pt", "pint": "pt", "pints": "pt",
	"qt": "qt", "qts": "qt", "quart": "qt", "quarts": "qt",
	"gal": "gal", "gals": "gal", "gallon": "gal", "gallons": "gal",
	"floz": "fl oz",
	// weight — US customary
	"oz": "oz", "ozs": "oz", "ounce": "oz", "ounces": "oz",
	"lb": "lb", "lbs": "lb", "pound": "lb", "pounds": "lb",
	// metric volume
	"ml": "mL", "mls": "mL", "milliliter": "mL", "milliliters": "mL",
	"millilitre": "mL", "millilitres": "mL", "cc": "mL",
	"cl": "cl", "centiliter": "cl", "centilitre": "cl",
	"dl": "dl", "deciliter": "dl", "decilitre": "dl",
	"l": "L", "liter": "L", "liters": "L", "litre": "L", "litres": "L",
	// metric weight
	"mg": "mg", "mgs": "mg", "milligram": "mg", "milligrams": "mg",
	"g": "g", "gram": "g", "grams": "g", "gr": "g",
	"kg": "kg", "kgs": "kg", "kilogram": "kg", "kilograms": "kg", "kilo": "kg", "kilos": "kg",
	// small measures
	"pinch": "pinch", "pinches": "pinch",
	"dash": "dash", "dashes": "dash",
	"drop": "drop", "drops": "drop",
	"bushel": "bushel", "bushels": "bushel",
	// countable descriptors — collapse to the canonical "pieces"
	"piece": "pieces", "pieces": "pieces", "pc": "pieces", "pcs": "pieces",
	"clove": "pieces", "cloves": "pieces",
	"can": "pieces", "cans": "pieces",
	"slice": "pieces", "slices": "pieces",
	"stick": "pieces", "sticks": "pieces",
	"stalk": "pieces", "stalks": "pieces",
	"sprig": "pieces", "sprigs": "pieces",
	"head": "pieces", "heads": "pieces",
	"bunch": "pieces", "bunches": "pieces",
	"package": "pieces", "packages": "pieces", "pkg": "pieces", "pkgs": "pieces",
	"ear": "pieces", "ears": "pieces",
	"fillet": "pieces", "fillets": "pieces",
}

// cl and dl are non-canonical metric volumes we accept on input and fold into
// mL so the rest of the pipeline only deals with the canonical enum.
var inputOnlyFactor = map[string]float64{
	"cl": 10,
	"dl": 100,
}

// liquidNames are ingredient-name substrings that mark an "oz" measurement as a
// fluid (volume) ounce rather than a weight ounce. Kept deliberately to
// unambiguous liquids — words like "cream" (cream cheese, sour cream) or
// "syrup"/"sauce" (often sold by weight) are excluded; the AI metric dimension
// is the primary signal and the name lexicon only a no-metric fallback.
var liquidNames = []string{
	"water", "milk", "stock", "broth", "wine", "juice", "oil",
	"vinegar", "beer", "coffee", "tea", "buttermilk",
	"liqueur", "rum", "vodka", "whiskey", "brandy", "soda",
}

// Canonical resolves a unit spelling (any case) to its canonical enum value.
func Canonical(token string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(token))
	if t == "" {
		return "", false
	}
	c, ok := aliases[t]
	return c, ok
}

func isLiquidName(name string) bool {
	n := strings.ToLower(name)
	for _, l := range liquidNames {
		if strings.Contains(n, l) {
			return true
		}
	}
	return false
}

// canonicalize resolves a possibly-aliased unit to its canonical form, leaving
// already-canonical units untouched.
func canonicalize(unit string) string {
	if _, ok := meta[unit]; ok {
		return unit
	}
	if c, ok := Canonical(unit); ok {
		return c
	}
	return unit
}

// DimensionOf returns KindMass or KindVolume for a measurable unit, or "" for
// count/imprecise/unknown units.
func DimensionOf(unit string) string {
	if m, ok := meta[canonicalize(unit)]; ok {
		return m.dim
	}
	return ""
}

// SystemOf returns SystemUS or SystemMetric for a measurable unit, or "" for
// count/imprecise/unknown units (which belong to no system).
func SystemOf(unit string) string {
	if m, ok := meta[canonicalize(unit)]; ok {
		return m.system
	}
	return ""
}

// MeasureKind classifies an ingredient. name and metricUnit disambiguate "oz"
// (weight vs fluid ounce): an oz is fluid when its metric equivalent is a
// volume or the ingredient name reads as a liquid.
func MeasureKind(unit, name, metricUnit string) string {
	u := canonicalize(unit)
	if u == "oz" {
		// Trust the AI metric dimension first; fall back to the name lexicon.
		switch DimensionOf(metricUnit) {
		case KindVolume:
			return KindVolume
		case KindMass:
			return KindMass
		}
		if isLiquidName(name) {
			return KindVolume
		}
		return KindMass
	}
	if m, ok := meta[u]; ok {
		return m.dim
	}
	switch u {
	case "pieces":
		return KindCount
	case "pinch", "dash", "drop", "bushel":
		return KindImprecise
	}
	return KindImprecise
}

// BaseAmount converts an amount to its base magnitude in the ingredient's own
// dimension: grams for mass, mL for volume, the amount itself for count.
// kind is the resolved MeasureKind (so a fluid "oz" uses the fl-oz factor).
// Returns 0 for imprecise units.
func BaseAmount(amount float64, unit, kind string) float64 {
	u := canonicalize(unit)
	switch kind {
	case KindCount:
		return amount
	case KindImprecise:
		return 0
	}
	if u == "oz" && kind == KindVolume {
		return amount * meta["fl oz"].factor
	}
	if f, ok := inputOnlyFactor[strings.ToLower(unit)]; ok {
		return amount * f
	}
	if m, ok := meta[u]; ok {
		return amount * m.factor
	}
	return 0
}

// Quantity is a value view of an ingredient measurement for conversion.
type Quantity struct {
	Amount       float64
	Unit         string
	Kind         string  // resolved MeasureKind
	BaseAmount   float64 // base magnitude in the source dimension (0 = derive)
	MetricUnit   string  // AI-provided density-aware metric equivalent
	MetricAmount float64
}

// ToViewer renders a quantity in the viewer's measurement system, returning the
// converted amount/unit and ok=true when a useful alternate exists. It returns
// ok=false when no conversion is needed or possible (count/imprecise units, or
// the ingredient is already in the viewer's system).
//
// Cross-dimension density is only applied US->metric, and only via the
// AI-provided metric pair. Every other conversion is exact same-dimension.
func ToViewer(q Quantity, viewer string) (float64, string, bool) {
	if q.Kind == KindCount || q.Kind == KindImprecise || q.Kind == "" {
		return 0, "", false
	}
	src := SystemOf(q.Unit)
	if src == "" || src == viewer {
		return 0, "", false
	}

	base := q.BaseAmount
	if base == 0 {
		base = BaseAmount(q.Amount, q.Unit, q.Kind)
	}
	if base <= 0 {
		return 0, "", false
	}

	// US -> metric may cross dimensions via the density-aware AI metric pair.
	if viewer == SystemMetric && src == SystemUS {
		if q.MetricUnit != "" && q.MetricAmount > 0 {
			return q.MetricAmount, canonicalize(q.MetricUnit), true
		}
	}

	amount, unit := ExpressInSystem(base, q.Kind, viewer)
	if amount <= 0 {
		return 0, "", false
	}
	return amount, unit, true
}

// ExpressInSystem picks a cooking-friendly unit and amount for a base magnitude
// in the given system and dimension. Used for both same-dimension conversion
// and re-expressing a scaled primary in its own system.
func ExpressInSystem(base float64, kind, system string) (float64, string) {
	switch kind {
	case KindMass:
		if system == SystemMetric {
			return metricMass(base)
		}
		return usMass(base)
	case KindVolume:
		if system == SystemMetric {
			return metricVolume(base)
		}
		return usVolume(base)
	}
	return 0, ""
}

// Scale multiplies a quantity by factor and re-expresses it in its own
// (source) system with a cooking-friendly unit. Count/imprecise units scale
// their amount but keep their unit. Rounding happens once, here.
func Scale(q Quantity, factor float64) (float64, string) {
	if factor <= 0 {
		return q.Amount, q.Unit
	}
	if q.Kind == KindCount || q.Kind == KindImprecise || q.Kind == "" || SystemOf(q.Unit) == "" {
		return roundCount(q.Amount * factor), q.Unit
	}
	base := q.BaseAmount
	if base == 0 {
		base = BaseAmount(q.Amount, q.Unit, q.Kind)
	}
	return ExpressInSystem(base*factor, q.Kind, SystemOf(q.Unit))
}

// --- system-specific unit selection -----------------------------------------

func usVolume(ml float64) (float64, string) {
	switch {
	case ml < 14.7868: // under 1 tbsp
		return snapCookingFraction(ml / meta["tsp"].factor), "tsp"
	case ml < 0.25*meta["cup"].factor: // under 1/4 cup
		return snapCookingFraction(ml / meta["tbsp"].factor), "tbsp"
	case ml < 4.5*meta["cup"].factor: // up to ~4 cups
		return snapCookingFraction(ml / meta["cup"].factor), "cup"
	case ml < 4*meta["qt"].factor:
		return snapCookingFraction(ml / meta["qt"].factor), "qt"
	default:
		return round1(ml / meta["gal"].factor), "gal"
	}
}

func usMass(g float64) (float64, string) {
	if g >= meta["lb"].factor {
		return round1(g / meta["lb"].factor), "lb"
	}
	return round1(g / meta["oz"].factor), "oz"
}

func metricVolume(ml float64) (float64, string) {
	if ml >= 1000 {
		return round2(ml / 1000), "L"
	}
	return roundMetricSmall(ml), "mL"
}

func metricMass(g float64) (float64, string) {
	if g >= 1000 {
		return round2(g / 1000), "kg"
	}
	return roundMetricSmall(g), "g"
}

// --- rounding / snapping -----------------------------------------------------

// cookingFractions are the eighths and thirds/sixths cooks actually use.
var cookingFractions = []float64{
	0, 1.0 / 8, 1.0 / 6, 1.0 / 4, 1.0 / 3, 3.0 / 8, 1.0 / 2,
	5.0 / 8, 2.0 / 3, 3.0 / 4, 5.0 / 6, 7.0 / 8, 1,
}

// snapCookingFraction snaps a value to the nearest whole + common cooking
// fraction (1/8 granularity plus thirds/sixths).
func snapCookingFraction(x float64) float64 {
	if x <= 0 {
		return 0
	}
	whole := math.Floor(x)
	frac := x - whole
	best := 0.0
	bestDist := math.MaxFloat64
	for _, f := range cookingFractions {
		if d := math.Abs(frac - f); d < bestDist {
			bestDist = d
			best = f
		}
	}
	return whole + best
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }
func round2(x float64) float64 { return math.Round(x*100) / 100 }

// roundCount rounds discrete counts to a half when small, whole when larger,
// so scaling never yields "1.5 cans" style noise on big counts.
func roundCount(x float64) float64 {
	if x < 4 {
		return math.Round(x*2) / 2
	}
	return math.Round(x)
}

// roundMetricSmall rounds a sub-1000 metric magnitude to a tidy value: whole
// numbers, or the nearest 5 once we are into the hundreds.
func roundMetricSmall(x float64) float64 {
	if x >= 100 {
		return math.Round(x/5) * 5
	}
	if x >= 10 {
		return math.Round(x)
	}
	return round1(x)
}
