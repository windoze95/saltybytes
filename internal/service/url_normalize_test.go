package service

import (
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "basic normalization",
			input: "HTTPS://Example.COM/recipe",
			want:  "https://example.com/recipe",
		},
		{
			name:  "trailing slash removed",
			input: "https://example.com/recipe/",
			want:  "https://example.com/recipe",
		},
		{
			name:  "root path keeps slash",
			input: "https://example.com/",
			want:  "https://example.com/",
		},
		{
			name:  "utm params stripped",
			input: "https://example.com/recipe?utm_source=twitter&utm_medium=social&id=42",
			want:  "https://example.com/recipe?id=42",
		},
		{
			name:  "fbclid stripped",
			input: "https://example.com/recipe?fbclid=abc123",
			want:  "https://example.com/recipe",
		},
		{
			name:  "gclid stripped",
			input: "https://example.com/recipe?gclid=abc123&page=1",
			want:  "https://example.com/recipe?page=1",
		},
		{
			name:  "ref stripped",
			input: "https://example.com/recipe?ref=homepage&sort=new",
			want:  "https://example.com/recipe?sort=new",
		},
		{
			name:  "query params sorted",
			input: "https://example.com/recipe?z=1&a=2&m=3",
			want:  "https://example.com/recipe?a=2&m=3&z=1",
		},
		{
			name:  "fragment removed",
			input: "https://example.com/recipe#step-3",
			want:  "https://example.com/recipe",
		},
		{
			name:  "complex combo",
			input: "HTTPS://WWW.Example.COM/recipes/pasta/?utm_source=ig&fbclid=xyz&sort=new&page=2#comments",
			want:  "https://www.example.com/recipes/pasta?page=2&sort=new",
		},
		{
			name:    "missing scheme",
			input:   "example.com/recipe",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NormalizeURL(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeURL(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
