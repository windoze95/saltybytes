package ai

import "testing"

func TestBraveSearchQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Plain keywords get the recipe steer.
		{"chicken parmesan", "chicken parmesan recipe"},
		// A query that already says "recipe" must not stutter.
		{"chicken parmesan recipe", "chicken parmesan recipe"},
		{"Chicken Parmesan Recipe", "Chicken Parmesan Recipe"},
		{"best pasta recipes", "best pasta recipes"},
		// The finder's composed query ends in "recipe" plus allergen excludes.
		{"beef comfort food recipe -shellfish", "beef comfort food recipe -shellfish"},
	}
	for _, c := range cases {
		if got := braveSearchQuery(c.in); got != c.want {
			t.Errorf("braveSearchQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
