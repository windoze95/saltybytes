package repository

import (
	"strconv"
	"strings"
	"testing"
)

func TestPgvectorLiteral_Formatting(t *testing.T) {
	tests := []struct {
		name string
		in   []float32
		want string
	}{
		{"empty slice", []float32{}, "[]"},
		{"nil slice", nil, "[]"},
		{"single value", []float32{0.5}, "[0.5]"},
		{"multiple values", []float32{0.1, 0.2, 0.3}, "[0.1,0.2,0.3]"},
		{"negative and zero", []float32{-1.5, 0, 2}, "[-1.5,0,2]"},
		{"large value uses exponent", []float32{1e10}, "[1e+10]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PgvectorLiteral(tc.in)
			if got != tc.want {
				t.Errorf("PgvectorLiteral(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPgvectorLiteral_RoundTrip(t *testing.T) {
	in := []float32{0.1, -2.5, 3.1415927, 1e-7, 12345.678, -0.000123}

	literal := PgvectorLiteral(in)
	if !strings.HasPrefix(literal, "[") || !strings.HasSuffix(literal, "]") {
		t.Fatalf("literal %q is not bracketed", literal)
	}

	parts := strings.Split(strings.Trim(literal, "[]"), ",")
	if len(parts) != len(in) {
		t.Fatalf("literal has %d components, want %d: %q", len(parts), len(in), literal)
	}

	for i, part := range parts {
		f, err := strconv.ParseFloat(part, 32)
		if err != nil {
			t.Fatalf("component %d (%q) does not parse as float: %v", i, part, err)
		}
		if float32(f) != in[i] {
			t.Errorf("component %d round-tripped to %g, want %g", i, float32(f), in[i])
		}
	}
}
