package service

import "testing"

const collectionLinksHTML = `
<a href="https://site.com/recipes/easy-beef-stroganoff/">Easy Beef Stroganoff</a>
<a href="https://site.com/recipes/spicy-beef-pepper-stir-fry/">Spicy</a>
<a href="https://site.com/recipes/cajun-sirloin-mushroom-leek-sauce/">Cajun</a>
<a href="https://site.com/about-us/">About</a>
<a href="https://other-site.com/recipes/easy-beef-stroganoff/">Offsite</a>
`

func TestMatchRecipeURL(t *testing.T) {
	candidates := extractRecipeLinkCandidates(collectionLinksHTML, "https://site.com/collection/30-beef-dinners/")
	cases := []struct {
		title   string
		wantURL string
		wantOK  bool
	}{
		{"Easy Beef Stroganoff", "https://site.com/recipes/easy-beef-stroganoff", true},
		{"Spicy Beef & Pepper Stir-Fry", "https://site.com/recipes/spicy-beef-pepper-stir-fry", true},
		{"Cajun Sirloin with Mushroom Leek Sauce", "https://site.com/recipes/cajun-sirloin-mushroom-leek-sauce", true},
		{"Nonexistent Chicken Dish", "", false}, // no matching link
		{"Beef", "", false},                     // single significant token — too generic
	}
	for _, tc := range cases {
		got, ok := matchRecipeURL(tc.title, candidates)
		if ok != tc.wantOK || got != tc.wantURL {
			t.Errorf("matchRecipeURL(%q) = (%q, %v), want (%q, %v)", tc.title, got, ok, tc.wantURL, tc.wantOK)
		}
	}
}

func TestExtractRecipeLinkCandidates_SameHostOnly(t *testing.T) {
	c := extractRecipeLinkCandidates(collectionLinksHTML, "https://site.com/collection/x/")
	for _, cand := range c {
		if got := cand.url; len(got) < 8 || got[:8] != "https://" {
			t.Errorf("unexpected candidate url %q", got)
		}
		if matchHost(cand.url) != "site.com" {
			t.Errorf("candidate from wrong host: %q", cand.url)
		}
	}
}

func matchHost(u string) string {
	// tiny helper for the test — extract host from an https URL
	s := u[len("https://"):]
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i]
		}
	}
	return s
}

func TestAssignRecipeURLs(t *testing.T) {
	const page = "https://site.com/collection/30-beef-dinners/"
	cards := []MultiRecipeCard{
		{Title: "Easy Beef Stroganoff", SourceURL: page},
		{Title: "Cajun Sirloin with Mushroom Leek Sauce", SourceURL: page},
		{Title: "Totally Unmatched Dessert", SourceURL: page},
	}
	assignRecipeURLs(cards, collectionLinksHTML, page)

	if cards[0].SourceURL != "https://site.com/recipes/easy-beef-stroganoff" {
		t.Errorf("card 0 url = %q", cards[0].SourceURL)
	}
	if cards[1].SourceURL != "https://site.com/recipes/cajun-sirloin-mushroom-leek-sauce" {
		t.Errorf("card 1 url = %q", cards[1].SourceURL)
	}
	if cards[2].SourceURL != page { // unmatched card keeps the collection URL
		t.Errorf("card 2 url = %q, want unchanged (%q)", cards[2].SourceURL, page)
	}
}
