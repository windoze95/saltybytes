package units

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.05 }

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"grams": "g", "Grams": "g", "ml": "mL", "ML": "mL",
		"tablespoons": "tbsp", "CUPS": "cup", "pounds": "lb",
		"cloves": "pieces", "sticks": "pieces", "dl": "dl",
	}
	for in, want := range cases {
		if got, ok := Canonical(in); !ok || got != want {
			t.Errorf("Canonical(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}
	if _, ok := Canonical("frobnicate"); ok {
		t.Error("Canonical(frobnicate) should not resolve")
	}
}

func TestMeasureKind(t *testing.T) {
	cases := []struct {
		unit, name, metric, want string
	}{
		{"cup", "flour", "g", KindVolume},
		{"g", "flour", "", KindMass},
		{"kg", "beef", "", KindMass},
		{"mL", "milk", "", KindVolume},
		{"pieces", "egg", "", KindCount},
		{"clove", "garlic", "", KindCount},
		{"pinch", "salt", "", KindImprecise},
		{"", "salt to taste", "", KindImprecise},
		// oz disambiguation
		{"oz", "cream cheese", "g", KindMass},   // metric=g -> weight
		{"oz", "milk", "mL", KindVolume},        // metric=mL -> fluid
		{"oz", "water", "", KindVolume},         // liquid name, no metric
		{"oz", "chocolate chips", "", KindMass}, // default weight
		{"oz", "sour cream", "", KindMass},      // "cream" must NOT read as fluid
	}
	for _, c := range cases {
		if got := MeasureKind(c.unit, c.name, c.metric); got != c.want {
			t.Errorf("MeasureKind(%q,%q,%q) = %q; want %q", c.unit, c.name, c.metric, got, c.want)
		}
	}
}

func TestBaseAmount(t *testing.T) {
	cases := []struct {
		amount     float64
		unit, kind string
		want       float64
	}{
		{2, "cup", KindVolume, 473.176},
		{200, "g", KindMass, 200},
		{8, "oz", KindMass, 226.796},
		{8, "oz", KindVolume, 236.588}, // fluid oz factor
		{1, "kg", KindMass, 1000},
		{1, "L", KindVolume, 1000},
		{2, "pieces", KindCount, 2},
		{1, "pinch", KindImprecise, 0},
		{5, "dl", KindVolume, 500}, // input-only metric unit
	}
	for _, c := range cases {
		if got := BaseAmount(c.amount, c.unit, c.kind); !approx(got, c.want) {
			t.Errorf("BaseAmount(%v,%q,%q) = %v; want %v", c.amount, c.unit, c.kind, got, c.want)
		}
	}
}

func TestToViewer(t *testing.T) {
	cases := []struct {
		name       string
		q          Quantity
		viewer     string
		wantOK     bool
		wantAmount float64
		wantUnit   string
	}{
		{
			name:   "US cup to metric uses AI density pair",
			q:      Quantity{Amount: 2, Unit: "cup", Kind: KindVolume, BaseAmount: 473.176, MetricUnit: "g", MetricAmount: 240},
			viewer: SystemMetric, wantOK: true, wantAmount: 240, wantUnit: "g",
		},
		{
			name:   "US cup to metric without pair falls to same-dimension mL",
			q:      Quantity{Amount: 2, Unit: "cup", Kind: KindVolume, BaseAmount: 473.176},
			viewer: SystemMetric, wantOK: true, wantAmount: 475, wantUnit: "mL",
		},
		{
			name:   "metric grams to US is exact ounces, never cups",
			q:      Quantity{Amount: 200, Unit: "g", Kind: KindMass, BaseAmount: 200},
			viewer: SystemUS, wantOK: true, wantAmount: 7.1, wantUnit: "oz",
		},
		{
			name:   "metric grams chicken to US is pounds, never cups",
			q:      Quantity{Amount: 500, Unit: "g", Kind: KindMass, BaseAmount: 500},
			viewer: SystemUS, wantOK: true, wantAmount: 1.1, wantUnit: "lb",
		},
		{
			name:   "metric mL to US cup",
			q:      Quantity{Amount: 240, Unit: "mL", Kind: KindVolume, BaseAmount: 240},
			viewer: SystemUS, wantOK: true, wantAmount: 1, wantUnit: "cup",
		},
		{
			name:   "small metric mL to US tsp",
			q:      Quantity{Amount: 10, Unit: "mL", Kind: KindVolume, BaseAmount: 10},
			viewer: SystemUS, wantOK: true, wantAmount: 2, wantUnit: "tsp",
		},
		{
			name:   "same system needs no conversion",
			q:      Quantity{Amount: 2, Unit: "cup", Kind: KindVolume, BaseAmount: 473.176},
			viewer: SystemUS, wantOK: false,
		},
		{
			name:   "count never converts",
			q:      Quantity{Amount: 3, Unit: "pieces", Kind: KindCount},
			viewer: SystemMetric, wantOK: false,
		},
		{
			name:   "imprecise never converts",
			q:      Quantity{Amount: 1, Unit: "pinch", Kind: KindImprecise},
			viewer: SystemMetric, wantOK: false,
		},
	}
	for _, c := range cases {
		amt, unit, ok := ToViewer(c.q, c.viewer)
		if ok != c.wantOK {
			t.Errorf("%s: ok = %v; want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if !approx(amt, c.wantAmount) || unit != c.wantUnit {
			t.Errorf("%s: = %v %q; want %v %q", c.name, amt, unit, c.wantAmount, c.wantUnit)
		}
	}
}

func TestScale(t *testing.T) {
	cases := []struct {
		name       string
		q          Quantity
		factor     float64
		wantAmount float64
		wantUnit   string
	}{
		{"8 tbsp doubled is 1 cup", Quantity{Amount: 8, Unit: "tbsp", Kind: KindVolume, BaseAmount: 118.294}, 2, 1, "cup"},
		{"1/3 cup tripled is 1 cup", Quantity{Amount: 1.0 / 3, Unit: "cup", Kind: KindVolume, BaseAmount: 236.588 / 3}, 3, 1, "cup"},
		{"3 eggs halved is 1.5 pieces", Quantity{Amount: 3, Unit: "pieces", Kind: KindCount}, 0.5, 1.5, "pieces"},
		{"200 g doubled is 400 g", Quantity{Amount: 200, Unit: "g", Kind: KindMass, BaseAmount: 200}, 2, 400, "g"},
	}
	for _, c := range cases {
		amt, unit := Scale(c.q, c.factor)
		if !approx(amt, c.wantAmount) || unit != c.wantUnit {
			t.Errorf("%s: = %v %q; want %v %q", c.name, amt, unit, c.wantAmount, c.wantUnit)
		}
	}
}

func TestExpressInSystem(t *testing.T) {
	cases := []struct {
		base       float64
		kind, sys  string
		wantAmount float64
		wantUnit   string
	}{
		{473.176, KindVolume, SystemUS, 2, "cup"},
		{1000, KindVolume, SystemMetric, 1, "L"},
		{1500, KindMass, SystemMetric, 1.5, "kg"},
		{28.3495, KindMass, SystemUS, 1, "oz"},
	}
	for _, c := range cases {
		amt, unit := ExpressInSystem(c.base, c.kind, c.sys)
		if !approx(amt, c.wantAmount) || unit != c.wantUnit {
			t.Errorf("ExpressInSystem(%v,%q,%q) = %v %q; want %v %q", c.base, c.kind, c.sys, amt, unit, c.wantAmount, c.wantUnit)
		}
	}
}
