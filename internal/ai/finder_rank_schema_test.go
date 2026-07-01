package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRankRecipesTool_RequiredFields locks in the harness fix: the rank_recipes
// tool schema must mark the top-level `ranked` array and the per-item `index`
// and `expand` fields required, so the cheap light tier always emits the
// collection flag instead of omitting it.
func TestRankRecipesTool_RequiredFields(t *testing.T) {
	raw, err := json.Marshal(rankRecipesTool())
	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}
	s := string(raw)

	// Normalize whitespace so the assertion doesn't depend on the SDK's spacing.
	compact := strings.Join(strings.Fields(s), "")
	if !strings.Contains(compact, `"required":["ranked"]`) {
		t.Errorf("tool schema missing top-level required [ranked]:\n%s", s)
	}
	if !strings.Contains(compact, `"required":["index","expand"]`) {
		t.Errorf("tool schema missing per-item required [index,expand]:\n%s", s)
	}
}

// TestBuildFinderRankPayload_IncludesURL locks in that the ranker is given each
// candidate's URL — the strongest signal for classifying collection pages.
func TestBuildFinderRankPayload_IncludesURL(t *testing.T) {
	payload := buildFinderRankPayload(FinderRankRequest{
		Candidates: []FinderCandidate{{
			Index:       0,
			Title:       "40 Easy Weeknight Dinner Recipes",
			URL:         "https://example.com/gallery/40-easy-weeknight-dinners",
			Description: "d",
		}},
	})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !strings.Contains(string(raw), `"url":"https://example.com/gallery/40-easy-weeknight-dinners"`) {
		t.Errorf("rank payload missing candidate url:\n%s", raw)
	}
}
