package service

import (
	"strconv"
	"strings"
)

// unicodeFractions maps single-rune vulgar fractions to their decimal values.
var unicodeFractions = map[rune]float64{
	'½': 1.0 / 2,
	'⅓': 1.0 / 3,
	'⅔': 2.0 / 3,
	'¼': 1.0 / 4,
	'¾': 3.0 / 4,
	'⅕': 1.0 / 5,
	'⅖': 2.0 / 5,
	'⅗': 3.0 / 5,
	'⅘': 4.0 / 5,
	'⅙': 1.0 / 6,
	'⅚': 5.0 / 6,
	'⅐': 1.0 / 7,
	'⅛': 1.0 / 8,
	'⅜': 3.0 / 8,
	'⅝': 5.0 / 8,
	'⅞': 7.0 / 8,
	'⅑': 1.0 / 9,
	'⅒': 1.0 / 10,
}

// unitAliases maps lowercase unit spellings (plurals, abbreviations, long
// forms) to the canonical unit enum used by the create_recipe Claude tool
// schema: pieces, tsp, tbsp, fl oz, cup, pt, qt, gal, oz, lb, mL, L, mg, g,
// kg, pinch, dash, drop, bushel.
// "T"/"t" are handled case-sensitively before this table is consulted.
var unitAliases = map[string]string{
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

// ParseIngredientLine deterministically parses a free-form ingredient line
// (e.g. "1 1/2 cups all-purpose flour") into a structured amount, canonical
// unit, and name. It handles integer, decimal, ASCII-fraction, and
// unicode-fraction quantities, mixed numbers, and ranges (the low value is
// used). The unit is optional; when no known unit token follows the quantity,
// unit is returned empty. Lines without a leading numeric quantity (e.g.
// "a pinch of salt") return ok=false and should keep their original text.
func ParseIngredientLine(s string) (amount float64, unit string, name string, ok bool) {
	tokens := strings.Fields(strings.TrimSpace(s))
	if len(tokens) == 0 {
		return 0, "", "", false
	}

	amount, consumed, ok := parseQuantity(tokens)
	if !ok || amount <= 0 {
		return 0, "", "", false
	}
	rest := tokens[consumed:]

	// Optional unit token (possibly two tokens for "fl oz" variants).
	if len(rest) > 0 {
		if u, n := matchUnit(rest); n > 0 {
			unit = u
			rest = rest[n:]
		}
	}

	name = strings.Join(rest, " ")
	name = strings.TrimSpace(strings.TrimPrefix(name, "of "))
	if name == "" {
		return 0, "", "", false
	}
	return amount, unit, name, true
}

// parseQuantity parses a leading quantity from the token stream, returning the
// value and the number of tokens consumed. It handles plain numbers, ASCII and
// unicode fractions, mixed numbers ("1 1/2", "1½"), and ranges ("1-2",
// "1 to 2") where the low value is kept.
func parseQuantity(tokens []string) (float64, int, bool) {
	first, ok := parseNumberToken(tokens[0])
	if !ok {
		return 0, 0, false
	}
	consumed := 1

	// Mixed number across tokens: "1 1/2", "1 ½"
	if len(tokens) > consumed {
		if frac, fracOK := parseFractionToken(tokens[consumed]); fracOK {
			first += frac
			consumed++
		}
	}

	// Range across tokens: "1 - 2", "1 to 2" — keep the low value.
	if len(tokens) > consumed+1 && isRangeSeparator(tokens[consumed]) {
		if _, highOK := parseNumberToken(tokens[consumed+1]); highOK {
			consumed += 2
		}
	}

	return first, consumed, true
}

// isRangeSeparator reports whether a token separates the low and high values
// of a quantity range.
func isRangeSeparator(tok string) bool {
	switch tok {
	case "-", "–", "—", "to":
		return true
	}
	return false
}

// parseNumberToken parses a single token as a number: "2", "1.5", "1/2", "½",
// "1½", "1-2" (range — low value), "1-1/2" (mixed — 1.5).
func parseNumberToken(tok string) (float64, bool) {
	if tok == "" {
		return 0, false
	}

	// Hyphenated token: mixed number ("1-1/2") or range ("1-2").
	for _, sep := range []string{"-", "–", "—"} {
		if idx := strings.Index(tok, sep); idx > 0 && idx < len(tok)-len(sep) {
			low, lowOK := parseNumberToken(tok[:idx])
			if !lowOK {
				return 0, false
			}
			right := tok[idx+len(sep):]
			// A fractional right side is a mixed number only when the left
			// side is a whole integer ("1-1/2" = 1.5). With a fractional
			// left side it is a range ("1/2-3/4") and the low value is kept.
			if isWholeIntegerToken(tok[:idx]) {
				if frac, fracOK := parseFractionToken(right); fracOK {
					return low + frac, true // mixed number, e.g. "1-1/2"
				}
			}
			if _, highOK := parseNumberToken(right); highOK {
				return low, true // range, e.g. "1-2" or "1/2-3/4" — use the low value
			}
			return 0, false
		}
	}

	if frac, ok := parseFractionToken(tok); ok {
		return frac, true
	}

	// Integer prefix with attached unicode fraction: "1½"
	runes := []rune(tok)
	if len(runes) > 1 {
		if frac, ok := unicodeFractions[runes[len(runes)-1]]; ok {
			if whole, err := strconv.ParseFloat(string(runes[:len(runes)-1]), 64); err == nil {
				return whole + frac, true
			}
			return 0, false
		}
	}

	v, err := strconv.ParseFloat(tok, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// isWholeIntegerToken reports whether a token is a plain whole integer
// (digits only — no fraction slash, decimal point, or unicode fraction).
func isWholeIntegerToken(tok string) bool {
	if tok == "" {
		return false
	}
	for _, r := range tok {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseFractionToken parses a token that is purely a fraction: "1/2" or "½".
func parseFractionToken(tok string) (float64, bool) {
	runes := []rune(tok)
	if len(runes) == 1 {
		if v, ok := unicodeFractions[runes[0]]; ok {
			return v, true
		}
	}
	parts := strings.Split(tok, "/")
	if len(parts) != 2 {
		return 0, false
	}
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0, false
	}
	return num / den, true
}

// matchUnit matches the leading token(s) against the unit alias table and
// returns the canonical unit plus the number of tokens consumed (0 if no
// match). "T" and "t" are matched case-sensitively (tablespoon vs teaspoon);
// everything else is case-insensitive with trailing periods/commas stripped.
func matchUnit(tokens []string) (string, int) {
	tok := strings.TrimRight(tokens[0], ".,")
	if tok == "" {
		return "", 0
	}

	// Case-sensitive single-letter abbreviations.
	switch tok {
	case "T":
		return "tbsp", 1
	case "t":
		return "tsp", 1
	}

	lower := strings.ToLower(tok)

	// Two-token "fl oz" variants: "fl oz", "fl. oz.", "fluid ounce(s)".
	if (lower == "fl" || lower == "fluid") && len(tokens) > 1 {
		next := strings.ToLower(strings.TrimRight(tokens[1], ".,"))
		switch next {
		case "oz", "ozs", "ounce", "ounces":
			return "fl oz", 2
		}
		return "", 0
	}

	if canonical, ok := unitAliases[lower]; ok {
		return canonical, 1
	}
	return "", 0
}
