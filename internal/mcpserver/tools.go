// Package mcpserver exposes SaltyBytes as a remote MCP server: OAuth-protected
// tools over Streamable HTTP at /mcp, with MCP Apps widgets rendered in-chat
// by hosts like Claude and ChatGPT.
package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"go.uber.org/zap"
)

// widgetURI is the single MCP Apps resource that renders every tool's result;
// it switches layout on the View discriminator in each tool's structured output.
const widgetURI = "ui://saltybytes/app.html"

// View discriminator values in tool structured output.
const (
	viewSearchResults = "search_results"
	viewRecipeCard    = "recipe_card"
	viewPreview       = "preview"
	viewRecipeList    = "recipe_list"
)

// Deps carries the service-layer dependencies for the MCP tools. The tools
// are thin adapters over the same services the REST handlers use.
type Deps struct {
	OAuth         *service.OAuthService
	Users         *service.UserService
	Recipes       *service.RecipeService
	Search        *service.SearchService
	Import        *service.ImportService
	MultiResolver *service.MultiRecipeResolver
	Subs          *service.SubscriptionService
}

// userForRequest resolves the authenticated SaltyBytes user from the verified
// bearer token attached by the auth middleware, and enforces the given scope.
func (d *Deps) userForRequest(req *mcp.CallToolRequest, scope string) (*models.User, error) {
	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil {
		return nil, fmt.Errorf("unauthorized: missing token")
	}
	scoped := false
	for _, sc := range extra.TokenInfo.Scopes {
		if sc == scope {
			scoped = true
			break
		}
	}
	if !scoped {
		return nil, fmt.Errorf("this connection was not granted the %q permission; reconnect SaltyBytes to grant it", scope)
	}
	userID, err := strconv.ParseUint(extra.TokenInfo.UserID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unauthorized: malformed token subject")
	}
	user, err := d.Users.GetUserByID(uint(userID))
	if err != nil {
		return nil, fmt.Errorf("unauthorized: account not found")
	}
	return user, nil
}

// textResult wraps a model-facing summary line; structured content is
// populated by the SDK from the typed output value.
func textResult(summary string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}
}

// widgetMeta is the MCP Apps declaration attached to every tool.
func widgetMeta() mcp.Meta {
	return mcp.Meta{"ui": map[string]any{"resourceUri": widgetURI}}
}

// --- search_recipes ---

type searchRecipesIn struct {
	Query  string `json:"query" jsonschema:"what to cook, e.g. 'weeknight salmon dinner' or 'gluten-free birthday cake'"`
	Count  int    `json:"count,omitempty" jsonschema:"number of results to return (default 10, max 20)"`
	Offset int    `json:"offset,omitempty" jsonschema:"pagination offset for fetching more results"`
}

type searchRecipesOut struct {
	View    string            `json:"view"`
	Query   string            `json:"query"`
	Results []ai.SearchResult `json:"results"`
	HasMore bool              `json:"has_more"`
}

func (d *Deps) searchRecipes(ctx context.Context, req *mcp.CallToolRequest, in searchRecipesIn) (*mcp.CallToolResult, searchRecipesOut, error) {
	out := searchRecipesOut{View: viewSearchResults, Query: in.Query}
	user, err := d.userForRequest(req, "search")
	if err != nil {
		return nil, out, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, out, fmt.Errorf("query is required")
	}
	if d.Search.SubService != nil {
		allowed, err := d.Search.SubService.CheckLimit(user.ID, "search")
		if err != nil {
			return nil, out, fmt.Errorf("could not check subscription limits")
		}
		if !allowed {
			return nil, out, fmt.Errorf("this account has used all of its recipe searches for the month; more become available when the monthly limit resets")
		}
	}
	count := in.Count
	if count <= 0 || count > 20 {
		count = 10
	}
	offset := in.Offset
	if offset < 0 || offset > 200 {
		offset = 0
	}
	result, err := d.Search.SearchRecipes(ctx, in.Query, count, offset)
	if err != nil {
		logger.Get().Error("mcp search failed", zap.Error(err))
		return nil, out, fmt.Errorf("recipe search failed; try again in a moment")
	}
	if !result.FromCache && d.Search.SubService != nil {
		if err := d.Search.SubService.IncrementUsage(user.ID, "search"); err != nil {
			logger.Get().Error("mcp: failed to increment search usage", zap.Uint("user_id", user.ID), zap.Error(err))
		}
	}
	out.Results = result.Results
	out.HasMore = result.HasMore

	titles := make([]string, 0, len(result.Results))
	for i, r := range result.Results {
		if i >= 5 {
			break
		}
		titles = append(titles, fmt.Sprintf("%q (%s)", r.Title, r.Source))
	}
	summary := fmt.Sprintf("Found %d real recipes for %q — shown to the user as interactive cards. Top results: %s. The user can tap a card to preview and save it; you can also call preview_recipe with a result's source_url.",
		len(result.Results), in.Query, strings.Join(titles, ", "))
	return textResult(summary), out, nil
}

// --- preview_recipe ---

type previewRecipeIn struct {
	URL string `json:"url" jsonschema:"the recipe page URL to preview (usually a source_url from search_recipes)"`
}

type previewRecipeOut struct {
	View        string                    `json:"view"`
	SourceURL   string                    `json:"source_url"`
	Recipe      *models.RecipeDef         `json:"recipe,omitempty"`
	CanonicalID *uint                     `json:"canonical_id,omitempty"`
	IsMulti     bool                      `json:"is_multi"`
	Recipes     []service.MultiRecipeCard `json:"recipes,omitempty"`
	FromCache   bool                      `json:"from_cache,omitempty"`
}

func (d *Deps) previewRecipe(ctx context.Context, req *mcp.CallToolRequest, in previewRecipeIn) (*mcp.CallToolResult, previewRecipeOut, error) {
	out := previewRecipeOut{View: viewPreview, SourceURL: in.URL}
	if _, err := d.userForRequest(req, "recipes:read"); err != nil {
		return nil, out, err
	}
	if strings.TrimSpace(in.URL) == "" {
		return nil, out, fmt.Errorf("url is required")
	}
	preview, err := d.Import.PreviewFromURLWithMultiCheck(ctx, in.URL, d.MultiResolver)
	if err != nil {
		logger.Get().Warn("mcp preview failed", zap.String("url", in.URL), zap.Error(err))
		return nil, out, fmt.Errorf("could not extract a recipe from that page — the site may be blocking access; try another result")
	}
	out.Recipe = preview.Recipe
	out.CanonicalID = preview.CanonicalID
	out.IsMulti = preview.IsMulti
	out.Recipes = preview.MultiCards
	out.FromCache = preview.FromCache

	if preview.IsMulti {
		return textResult(fmt.Sprintf("That page contains %d recipes — shown to the user as a picker. Ask which one they want, or call preview_recipe with an individual recipe's source_url.", len(preview.MultiCards))), out, nil
	}
	if preview.Recipe == nil {
		return nil, out, fmt.Errorf("no recipe found on that page")
	}
	return textResult(fmt.Sprintf("Previewing %q (%d ingredients, %d steps, ~%d min) — rendered as an interactive card with a save button. To save it for the user, call save_recipe with the same url.",
		preview.Recipe.Title, len(preview.Recipe.Ingredients), len(preview.Recipe.Instructions), preview.Recipe.CookTime)), out, nil
}

// --- save_recipe ---

type saveRecipeIn struct {
	URL string `json:"url" jsonschema:"the recipe page URL to import into the user's SaltyBytes collection"`
}

type saveRecipeOut struct {
	View   string                  `json:"view"`
	Saved  bool                    `json:"saved"`
	Recipe *service.RecipeResponse `json:"recipe,omitempty"`
}

func (d *Deps) saveRecipe(ctx context.Context, req *mcp.CallToolRequest, in saveRecipeIn) (*mcp.CallToolResult, saveRecipeOut, error) {
	out := saveRecipeOut{View: viewRecipeCard}
	user, err := d.userForRequest(req, "recipes:write")
	if err != nil {
		return nil, out, err
	}
	if strings.TrimSpace(in.URL) == "" {
		return nil, out, fmt.Errorf("url is required")
	}
	recipe, err := d.Import.ImportFromURL(ctx, in.URL, user)
	if err != nil {
		logger.Get().Warn("mcp import failed", zap.String("url", in.URL), zap.Error(err))
		return nil, out, fmt.Errorf("could not import that recipe — the site may be blocking access; try previewing it first")
	}
	out.Saved = true
	out.Recipe = recipe
	return textResult(fmt.Sprintf("Saved %q to the user's SaltyBytes collection (recipe id %s). It now appears in their app.", recipe.Title, recipe.ID)), out, nil
}

// --- list_my_recipes ---

type listMyRecipesIn struct {
	Query    string `json:"query,omitempty" jsonschema:"optional search over the user's own saved recipes (semantic + title match)"`
	Page     int    `json:"page,omitempty" jsonschema:"page number, starting at 1"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"results per page (default 12, max 50)"`
}

type listMyRecipesOut struct {
	View     string                   `json:"view"`
	Recipes  []service.RecipeListItem `json:"recipes"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

func (d *Deps) listMyRecipes(ctx context.Context, req *mcp.CallToolRequest, in listMyRecipesIn) (*mcp.CallToolResult, listMyRecipesOut, error) {
	out := listMyRecipesOut{View: viewRecipeList}
	user, err := d.userForRequest(req, "recipes:read")
	if err != nil {
		return nil, out, err
	}
	page := in.Page
	if page < 1 {
		page = 1
	}
	pageSize := in.PageSize
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 12
	}
	var items []service.RecipeListItem
	var total int64
	if strings.TrimSpace(in.Query) != "" {
		items, total, err = d.Recipes.SearchUserRecipes(ctx, user.ID, in.Query, page, pageSize)
	} else {
		items, total, err = d.Recipes.GetUserRecipes(user.ID, page, pageSize)
	}
	if err != nil {
		logger.Get().Error("mcp list recipes failed", zap.Uint("user_id", user.ID), zap.Error(err))
		return nil, out, fmt.Errorf("could not load the user's recipes")
	}
	out.Recipes = items
	out.Total = total
	out.Page = page
	out.PageSize = pageSize

	if total == 0 {
		return textResult("The user has no saved recipes yet. Suggest searching for something to cook with search_recipes."), out, nil
	}
	titles := make([]string, 0, len(items))
	for i, r := range items {
		if i >= 5 {
			break
		}
		titles = append(titles, fmt.Sprintf("%q (id %s)", r.Title, r.ID))
	}
	return textResult(fmt.Sprintf("The user has %d saved recipes (showing page %d as interactive cards): %s. Call get_recipe with an id for full details.",
		total, page, strings.Join(titles, ", "))), out, nil
}

// --- get_recipe ---

type getRecipeIn struct {
	RecipeID string `json:"recipe_id" jsonschema:"the SaltyBytes recipe id (from list_my_recipes or save_recipe)"`
}

type getRecipeOut struct {
	View   string                  `json:"view"`
	Saved  bool                    `json:"saved"`
	Recipe *service.RecipeResponse `json:"recipe,omitempty"`
}

func (d *Deps) getRecipe(ctx context.Context, req *mcp.CallToolRequest, in getRecipeIn) (*mcp.CallToolResult, getRecipeOut, error) {
	out := getRecipeOut{View: viewRecipeCard, Saved: true}
	if _, err := d.userForRequest(req, "recipes:read"); err != nil {
		return nil, out, err
	}
	id, err := strconv.ParseUint(strings.TrimSpace(in.RecipeID), 10, 64)
	if err != nil {
		return nil, out, fmt.Errorf("recipe_id must be a numeric id")
	}
	recipe, err := d.Recipes.GetRecipeByID(uint(id))
	if err != nil {
		return nil, out, fmt.Errorf("recipe %s not found", in.RecipeID)
	}
	out.Recipe = recipe
	return textResult(fmt.Sprintf("Showing %q (%d ingredients, %d steps, ~%d min) as an interactive recipe card.",
		recipe.Title, len(recipe.Ingredients), len(recipe.Instructions), recipe.CookTimeMinutes)), out, nil
}

// registerTools adds every SaltyBytes tool (with its MCP Apps widget
// declaration) to the server.
func registerTools(server *mcp.Server, deps *Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_recipes",
		Title:       "Search for recipes",
		Description: "Search the web for real recipes (not AI-generated) matching what the user wants to cook. Call this whenever the user asks for recipe ideas, dinner suggestions, or something specific to cook. Results render as interactive cards the user can preview and save.",
		Meta:        widgetMeta(),
	}, deps.searchRecipes)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "preview_recipe",
		Title:       "Preview a recipe",
		Description: "Extract and preview the full recipe (ingredients, steps, times) from a recipe page URL without saving it. Call this when the user picks a search result or pastes a recipe link. Renders an interactive card with a save button.",
		Meta:        widgetMeta(),
	}, deps.previewRecipe)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_recipe",
		Title:       "Save a recipe to SaltyBytes",
		Description: "Import a recipe from a URL into the user's SaltyBytes collection so it appears in their app. Call this when the user says to save, keep, or import a recipe they previewed or linked.",
		Meta:        widgetMeta(),
	}, deps.saveRecipe)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_my_recipes",
		Title:       "Browse saved recipes",
		Description: "List or search the user's own saved SaltyBytes recipes. Call this when the user asks what they've saved, wants to find one of their recipes, or asks 'what should I cook from my collection?'.",
		Meta:        widgetMeta(),
	}, deps.listMyRecipes)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_recipe",
		Title:       "Open a saved recipe",
		Description: "Fetch one saved SaltyBytes recipe by id with full ingredients and instructions, rendered as an interactive cooking card.",
		Meta:        widgetMeta(),
	}, deps.getRecipe)
}
