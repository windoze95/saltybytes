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

// TestParseFinderRankToolArgs_SalvagesTruncated locks in the truncation-tolerant
// parse: a rank_recipes tool-args JSON cut off mid-array (output-token overflow)
// still yields the complete ranked items instead of erroring into fallback.
func TestParseFinderRankToolArgs_SalvagesTruncated(t *testing.T) {
	truncated := `{"ranked":[` +
		`{"index":0,"reason":"a","expand":false,"safety":[]},` +
		`{"index":1,"reason":"b","expand":true,"expand_priority":7,"safety":[{"member_name":"Kid","status":"avoid","note":"peanuts"}]},` +
		`{"index":2,"reason":"c partially wr`

	tr, err := parseFinderRankToolArgs(truncated)
	if err != nil {
		t.Fatalf("expected salvage, got err: %v", err)
	}
	if len(tr.Ranked) != 2 {
		t.Fatalf("salvaged %d complete items, want 2", len(tr.Ranked))
	}
	if tr.Ranked[1].Index != 1 || !tr.Ranked[1].Expand || tr.Ranked[1].ExpandPriority != 7 {
		t.Errorf("second item = %+v, want index=1 expand=true prio=7", tr.Ranked[1])
	}
	if len(tr.Ranked[1].Safety) != 1 || tr.Ranked[1].Safety[0].Status != "avoid" {
		t.Errorf("second item safety not preserved: %+v", tr.Ranked[1].Safety)
	}
}

func TestParseFinderRankToolArgs_StrictWhenValid(t *testing.T) {
	valid := `{"ranked":[{"index":0,"reason":"x","expand":true,"expand_priority":3,"safety":[]}],"broaden_queries":["more chicken"]}`
	tr, err := parseFinderRankToolArgs(valid)
	if err != nil || len(tr.Ranked) != 1 || len(tr.BroadenQueries) != 1 {
		t.Fatalf("strict parse failed: tr=%+v err=%v", tr, err)
	}
}
