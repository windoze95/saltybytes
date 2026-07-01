package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"go.uber.org/zap"
)

// Recipe-finder tuning constants.
const (
	// finderSearchCount is how many real candidates to search for and hand to
	// the ranker in a single find.
	finderSearchCount = 10
	// finderWarmTopN is how many top shortlisted results to proactively warm so
	// a later tap is an instant cache hit.
	finderWarmTopN = 4
)

// finderRefineChips are the bounded, tap-first refinement options offered after
// a shortlist. They are a constant so the interaction stays guided (a bounded
// trajectory), never an open-ended model loop.
var finderRefineChips = []string{"quicker", "cheaper", "more veg", "swap protein"}

// FinderEventType enumerates the SSE event types emitted during a recipe-finder
// run. The string values are the wire contract with the Flutter client.
type FinderEventType string

const (
	// FinderEventSearching announces the composed search query.
	FinderEventSearching FinderEventType = "searching"
	// FinderEventFound reports how many real candidates search returned.
	FinderEventFound FinderEventType = "found"
	// FinderEventFiltering signals the single ranking/filtering model call.
	FinderEventFiltering FinderEventType = "filtering"
	// FinderEventShortlist carries the ranked real results with rationales+safety.
	FinderEventShortlist FinderEventType = "shortlist"
	// FinderEventWarming lists the top URLs being proactively cache-warmed.
	FinderEventWarming FinderEventType = "warming"
	// FinderEventRefineReady offers tap-to-refine chips + broaden suggestions.
	FinderEventRefineReady FinderEventType = "refine_ready"
	// FinderEventDone terminates a successful run.
	FinderEventDone FinderEventType = "done"
	// FinderEventEmpty terminates a run that surfaced no real recipe (0 results
	// or all dropped) — it never invents one; it only suggests broader queries.
	FinderEventEmpty FinderEventType = "empty"
	// FinderEventError terminates a run that failed before any shortlist.
	FinderEventError FinderEventType = "error"
)

// FinderResultItem is one shortlisted recipe: the real search result plus the
// finder's one-line rationale and best-effort per-member dietary safety badges.
type FinderResultItem struct {
	Result ai.SearchResult   `json:"result"`
	Reason string            `json:"reason,omitempty"`
	Safety []ai.MemberSafety `json:"safety,omitempty"`
}

// FinderEvent is a single event streamed to the client during a find. Only the
// fields relevant to a given Type are populated (mirrors ai.StreamEvent's shape).
type FinderEvent struct {
	Type      FinderEventType    `json:"type"`
	Query     string             `json:"query,omitempty"`
	Count     int                `json:"count,omitempty"`
	FromCache bool               `json:"from_cache,omitempty"`
	Items     []FinderResultItem `json:"items,omitempty"`
	URLs      []string           `json:"urls,omitempty"`
	Chips     []string           `json:"chips,omitempty"`
	Broaden   []string           `json:"broaden,omitempty"`
	Error     string             `json:"error,omitempty"`
	// HasMore reports whether more pages of underlying search results are
	// available (derived from the search result, not the shortlist length —
	// the finder drops avoid/duplicate candidates). Carried on the shortlist event.
	HasMore bool `json:"has_more,omitempty"`
}

// FinderFacets are the tappable facet-chip selections that steer a find. They
// are the primary (tap-first) interaction; free text and voice are secondary.
type FinderFacets struct {
	Occasion     string   `json:"occasion,omitempty"`
	TimeBudget   string   `json:"time_budget,omitempty"`
	Protein      string   `json:"protein,omitempty"`
	Cuisine      string   `json:"cuisine,omitempty"`
	UseWhatIHave []string `json:"use_what_i_have,omitempty"`
	SurpriseMe   bool     `json:"surprise_me,omitempty"`
}

// FinderRefine carries a bounded, tap-first refinement of a prior find.
type FinderRefine struct {
	AddFacets  FinderFacets `json:"add_facets,omitempty"`
	Constraint string       `json:"constraint,omitempty"`
}

// FinderRequest is the decoded POST /v1/recipes/find body.
type FinderRequest struct {
	Facets   FinderFacets  `json:"facets"`
	FreeText string        `json:"free_text,omitempty"`
	Refine   *FinderRefine `json:"refine,omitempty"`
	// Offset pages through the underlying search results (0-based). Negative
	// values are clamped to 0.
	Offset int `json:"offset,omitempty"`
}

// RecipeFinderService drives the guided "recipe finder": one bounded trajectory
// that searches real recipes, ranks/filters them with a single cheap model call,
// warms the top results, and streams each step as a FinderEvent. It never
// invents a recipe — every shortlisted result is a real search hit referenced by
// index, so fabrication is structurally impossible.
type RecipeFinderService struct {
	Cfg        *config.Config
	Search     *SearchService
	FamilyRepo repository.FamilyRepo
	Warm       *WarmService
	// RankProvider is the light tier (Gemini Flash) that performs the single
	// query-expansion + ranking call.
	RankProvider ai.TextProvider
}

// NewRecipeFinderService wires the finder over the existing search, family and
// cache-warming machinery plus the light-tier ranking provider.
func NewRecipeFinderService(cfg *config.Config, search *SearchService, familyRepo repository.FamilyRepo, warm *WarmService, rankProvider ai.TextProvider) *RecipeFinderService {
	return &RecipeFinderService{
		Cfg:          cfg,
		Search:       search,
		FamilyRepo:   familyRepo,
		Warm:         warm,
		RankProvider: rankProvider,
	}
}

// FindRecipes runs the bounded finder trajectory, emitting FinderEvents as it
// goes and returning when the run reaches a terminal event (done/empty/error) or
// the context is cancelled. The caller owns the channel and closes it.
func (s *RecipeFinderService) FindRecipes(ctx context.Context, user *models.User, req FinderRequest, events chan<- FinderEvent) {
	// Auto-inject the family's dietary needs (server-side, never client-trusted):
	// allergies become hard query-excludes; restrictions/preferences steer the
	// model via the diet summary.
	dietSummary, allergenExcludes := s.dietContext(user)

	// 1. Compose the search query deterministically — no model call.
	query := composeFinderQuery(req, allergenExcludes)
	if !s.emit(ctx, events, FinderEvent{Type: FinderEventSearching, Query: query}) {
		return
	}

	// 2. Search real recipes (reuses the exact + semantic/embedding cache).
	// Page through results with the requested offset (negatives clamped to 0).
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	searchRes, err := s.Search.SearchRecipes(ctx, query, finderSearchCount, offset)
	if err != nil {
		logger.Get().Error("recipe finder search failed", zap.String("query", query), zap.Error(err))
		s.emit(ctx, events, FinderEvent{Type: FinderEventError, Error: "search failed"})
		return
	}
	results := searchRes.Results
	if !s.emit(ctx, events, FinderEvent{Type: FinderEventFound, Count: len(results), FromCache: searchRes.FromCache}) {
		return
	}

	// 3. Empty search → broaden suggestions only; never invent a recipe.
	if len(results) == 0 {
		s.emit(ctx, events, FinderEvent{Type: FinderEventEmpty, Broaden: broadenFallback(req)})
		return
	}

	// 4. The one and only model call: expand + rank + rationale + safety.
	if !s.emit(ctx, events, FinderEvent{Type: FinderEventFiltering}) {
		return
	}
	rank := s.rankCandidates(ctx, user, req, dietSummary, results)

	// 5. Map the model's rankings back to the REAL results, dropping any
	// out-of-range/duplicate index defensively and any candidate the model
	// flagged allergen-"avoid".
	items := buildShortlist(results, rank)

	// 6. Everything filtered out → empty; still never invent.
	if len(items) == 0 {
		s.emit(ctx, events, FinderEvent{Type: FinderEventEmpty, Broaden: broadenList(rank, req)})
		return
	}
	// HasMore is derived from the search page (not len(items)) so dropped
	// avoid/duplicate candidates never under-report that more pages exist.
	if !s.emit(ctx, events, FinderEvent{Type: FinderEventShortlist, Items: items, HasMore: searchRes.HasMore}) {
		return
	}

	// 7. Proactively warm the top results (best-effort) so a later tap is an
	// instant cache hit.
	if warmURLs := topURLs(items, finderWarmTopN); len(warmURLs) > 0 {
		if s.Warm != nil {
			s.Warm.WarmURLs(warmURLs)
		}
		if !s.emit(ctx, events, FinderEvent{Type: FinderEventWarming, URLs: warmURLs}) {
			return
		}
	}

	// 8. Offer bounded refinement, then finish.
	if !s.emit(ctx, events, FinderEvent{Type: FinderEventRefineReady, Chips: finderRefineChips, Broaden: broadenList(rank, req)}) {
		return
	}
	s.emit(ctx, events, FinderEvent{Type: FinderEventDone})
}

// emit sends one event, returning false if the context is cancelled (e.g. the
// client disconnected) so the producer never blocks on an unconsumed channel.
func (s *RecipeFinderService) emit(ctx context.Context, events chan<- FinderEvent, ev FinderEvent) bool {
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// rankCandidates runs the single ranking model call. On any error it degrades
// gracefully to the real results in search order (no reasons/safety) rather than
// dead-ending — search already returned real recipes, so the worst case is an
// unranked-but-real shortlist, never a fabricated one.
func (s *RecipeFinderService) rankCandidates(ctx context.Context, user *models.User, req FinderRequest, dietSummary string, results []ai.SearchResult) *ai.FinderRankResult {
	candidates := make([]ai.FinderCandidate, len(results))
	for i, r := range results {
		candidates[i] = ai.FinderCandidate{
			Index:       i,
			Title:       r.Title,
			Source:      r.Source,
			Description: r.Description,
		}
	}

	rankReq := ai.FinderRankRequest{
		Facets:      facetsSummary(req),
		FreeText:    strings.TrimSpace(req.FreeText),
		DietSummary: dietSummary,
		Candidates:  candidates,
	}
	if user != nil && user.Personalization != nil {
		rankReq.UnitSystem = user.Personalization.UnitSystemText()
		rankReq.CookingContext = user.Personalization.CookingContextPrompt()
		rankReq.Requirements = user.Personalization.Requirements
	}

	res, err := s.RankProvider.ExpandAndRankRecipes(ctx, rankReq)
	if err != nil {
		logger.Get().Warn("recipe finder ranking failed; showing unranked real results", zap.Error(err))
		return fallbackRanking(len(results))
	}
	return res
}

// dietContext compacts the owner's family dietary needs into a model-facing
// summary and a list of hard allergen excludes. It is best-effort: a missing
// family, a repo error or absent profiles simply yield no dietary steering.
func (s *RecipeFinderService) dietContext(user *models.User) (summary string, allergenExcludes []string) {
	if s.FamilyRepo == nil || user == nil {
		return "", nil
	}
	family, err := s.FamilyRepo.GetFamilyByOwnerID(user.ID)
	if err != nil || family == nil {
		return "", nil
	}

	var parts []string
	seen := make(map[string]bool)
	for _, member := range family.Members {
		if member.DietaryProfile == nil {
			continue
		}
		dp := member.DietaryProfile
		var needs []string
		for _, a := range dp.Allergies {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				continue
			}
			needs = append(needs, "allergic to "+name)
			if key := strings.ToLower(name); !seen[key] {
				seen[key] = true
				allergenExcludes = append(allergenExcludes, name)
			}
		}
		for _, in := range dp.Intolerances {
			if in = strings.TrimSpace(in); in != "" {
				needs = append(needs, "intolerant to "+in)
			}
		}
		for _, r := range dp.Restrictions {
			if r = strings.TrimSpace(r); r != "" {
				needs = append(needs, r)
			}
		}
		for _, p := range dp.Preferences {
			if p = strings.TrimSpace(p); p != "" {
				needs = append(needs, p)
			}
		}
		if len(needs) > 0 {
			name := strings.TrimSpace(member.Name)
			if name == "" {
				name = "member"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", name, strings.Join(needs, ", ")))
		}
	}
	return strings.Join(parts, "; "), allergenExcludes
}

// composeFinderQuery builds the search query string deterministically from the
// facets, free text and constraint, appending allergen excludes as query
// negations. No model is involved.
func composeFinderQuery(req FinderRequest, allergenExcludes []string) string {
	f := effectiveFacets(req)

	var parts []string
	parts = appendNonEmpty(parts, f.Cuisine, f.Protein, f.Occasion, f.TimeBudget)
	for _, ing := range f.UseWhatIHave {
		if ing = strings.TrimSpace(ing); ing != "" {
			parts = append(parts, ing)
		}
	}
	if ft := strings.TrimSpace(req.FreeText); ft != "" {
		parts = append(parts, ft)
	}
	if req.Refine != nil {
		if c := strings.TrimSpace(req.Refine.Constraint); c != "" {
			parts = append(parts, c)
		}
	}

	core := strings.TrimSpace(strings.Join(parts, " "))
	if core == "" {
		// Tap-first "surprise me" / no selection: a broad but real query.
		core = "popular dinner"
	}
	query := core + " recipe"

	for _, ex := range allergenExcludes {
		if ex = strings.TrimSpace(ex); ex != "" {
			query += " -" + ex
		}
	}
	return query
}

// facetsSummary renders the effective facets as a compact, human-readable line
// for the model.
func facetsSummary(req FinderRequest) string {
	f := effectiveFacets(req)
	var parts []string
	if f.Occasion != "" {
		parts = append(parts, "occasion: "+f.Occasion)
	}
	if f.TimeBudget != "" {
		parts = append(parts, "time: "+f.TimeBudget)
	}
	if f.Protein != "" {
		parts = append(parts, "protein: "+f.Protein)
	}
	if f.Cuisine != "" {
		parts = append(parts, "cuisine: "+f.Cuisine)
	}
	if len(f.UseWhatIHave) > 0 {
		parts = append(parts, "use what I have: "+strings.Join(f.UseWhatIHave, ", "))
	}
	if f.SurpriseMe {
		parts = append(parts, "surprise me")
	}
	if req.Refine != nil {
		if c := strings.TrimSpace(req.Refine.Constraint); c != "" {
			parts = append(parts, "refine: "+c)
		}
	}
	return strings.Join(parts, "; ")
}

// effectiveFacets merges the base facets with any refinement facets.
func effectiveFacets(req FinderRequest) FinderFacets {
	f := req.Facets
	if req.Refine != nil {
		f = mergeFacets(f, req.Refine.AddFacets)
	}
	return f
}

// mergeFacets overlays add onto base: non-empty scalar fields override,
// ingredient lists concatenate, and SurpriseMe is sticky.
func mergeFacets(base, add FinderFacets) FinderFacets {
	out := base
	if add.Occasion != "" {
		out.Occasion = add.Occasion
	}
	if add.TimeBudget != "" {
		out.TimeBudget = add.TimeBudget
	}
	if add.Protein != "" {
		out.Protein = add.Protein
	}
	if add.Cuisine != "" {
		out.Cuisine = add.Cuisine
	}
	if len(add.UseWhatIHave) > 0 {
		merged := make([]string, 0, len(out.UseWhatIHave)+len(add.UseWhatIHave))
		merged = append(merged, out.UseWhatIHave...)
		merged = append(merged, add.UseWhatIHave...)
		out.UseWhatIHave = merged
	}
	if add.SurpriseMe {
		out.SurpriseMe = true
	}
	return out
}

// buildShortlist maps the model's rankings back to the real results in rank
// order, dropping out-of-range or duplicate indices defensively and dropping any
// candidate flagged allergen-"avoid" for any family member.
func buildShortlist(results []ai.SearchResult, rank *ai.FinderRankResult) []FinderResultItem {
	if rank == nil {
		return nil
	}
	items := make([]FinderResultItem, 0, len(rank.Ranked))
	used := make(map[int]bool, len(rank.Ranked))
	for _, r := range rank.Ranked {
		if r.Index < 0 || r.Index >= len(results) || used[r.Index] {
			continue
		}
		used[r.Index] = true
		if hasAvoid(r.Safety) {
			continue
		}
		items = append(items, FinderResultItem{
			Result: results[r.Index],
			Reason: r.Reason,
			Safety: r.Safety,
		})
	}
	return items
}

// hasAvoid reports whether any member's safety status is "avoid".
func hasAvoid(safety []ai.MemberSafety) bool {
	for _, s := range safety {
		if strings.EqualFold(strings.TrimSpace(s.Status), "avoid") {
			return true
		}
	}
	return false
}

// topURLs returns up to n non-empty result URLs, preserving shortlist order.
func topURLs(items []FinderResultItem, n int) []string {
	urls := make([]string, 0, n)
	for _, it := range items {
		if len(urls) >= n {
			break
		}
		if u := strings.TrimSpace(it.Result.URL); u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}

// broadenList prefers the model's broadened queries, falling back to
// deterministic suggestions derived from the facets.
func broadenList(rank *ai.FinderRankResult, req FinderRequest) []string {
	if rank != nil && len(rank.BroadenQueries) > 0 {
		return rank.BroadenQueries
	}
	return broadenFallback(req)
}

// broadenFallback derives a few broadened real query strings from the facets,
// used on the empty path (no model call happened) or when the model returned no
// broaden suggestions.
func broadenFallback(req FinderRequest) []string {
	f := effectiveFacets(req)
	seeds := appendNonEmpty(nil, f.Protein, f.Cuisine, f.Occasion, strings.TrimSpace(req.FreeText))

	var out []string
	for _, seed := range seeds {
		out = append(out, seed+" recipes")
		if len(out) >= 3 {
			break
		}
	}
	if len(out) == 0 {
		return []string{"easy dinner recipes", "quick weeknight recipes", "popular recipes"}
	}
	return out
}

// fallbackRanking ranks all candidates in their original order with no reasons
// or safety, used when the ranking call fails.
func fallbackRanking(n int) *ai.FinderRankResult {
	ranked := make([]ai.FinderRanking, n)
	for i := range ranked {
		ranked[i] = ai.FinderRanking{Index: i}
	}
	return &ai.FinderRankResult{Ranked: ranked}
}

// appendNonEmpty appends each trimmed non-empty value to dst.
func appendNonEmpty(dst []string, values ...string) []string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			dst = append(dst, v)
		}
	}
	return dst
}
