package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// newTestDeps builds Deps over in-memory mocks with one user (ID 1, free tier).
func newTestDeps(t *testing.T) (*Deps, *testutil.MockUserRepo, *testutil.MockRecipeRepo) {
	t.Helper()
	cfg := &config.Config{EnvVars: config.EnvVars{
		PublicBaseURL: "https://api.example.com",
		S3Bucket:      "test-bucket",
		AWSRegion:     "us-east-2",
	}}

	userRepo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		UserID:         user.ID,
		Tier:           models.TierFree,
		MonthlyResetAt: time.Now().Add(24 * time.Hour),
	}
	userRepo.Users[user.ID] = user

	recipeRepo := testutil.NewMockRecipeRepo()
	userService := service.NewUserService(cfg, userRepo)
	subService := service.NewSubscriptionService(cfg, userRepo)
	recipeService := service.NewRecipeService(cfg, recipeRepo, nil, nil)

	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return []ai.SearchResult{
				{Title: "Golden Salmon", URL: "https://example.com/salmon", Source: "example.com", Rating: 4.6},
				{Title: "Weeknight Ramen", URL: "https://example.com/ramen", Source: "example.com"},
			}, nil
		},
	}
	searchService := service.NewSearchService(cfg, searchProvider, subService, nil)

	oauthService := service.NewOAuthService(cfg, testutil.NewMockOAuthRepo(), userService)

	return &Deps{
		OAuth:   oauthService,
		Users:   userService,
		Recipes: recipeService,
		Search:  searchService,
		Subs:    subService,
	}, userRepo, recipeRepo
}

// reqWithScopes fabricates an authenticated CallToolRequest the way the
// bearer middleware would produce it.
func reqWithScopes(scopes ...string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{
		Extra: &mcp.RequestExtra{
			TokenInfo: &auth.TokenInfo{
				UserID:     "1",
				Scopes:     scopes,
				Expiration: time.Now().Add(time.Hour),
			},
		},
	}
}

func TestUserForRequest_RejectsMissingToken(t *testing.T) {
	deps, _, _ := newTestDeps(t)
	if _, err := deps.userForRequest(&mcp.CallToolRequest{Extra: &mcp.RequestExtra{}}, "recipes:read"); err == nil {
		t.Fatal("expected error for missing token info")
	}
}

func TestUserForRequest_EnforcesScope(t *testing.T) {
	deps, _, _ := newTestDeps(t)
	if _, err := deps.userForRequest(reqWithScopes("recipes:read"), "recipes:write"); err == nil {
		t.Fatal("expected error for missing scope")
	}
	user, err := deps.userForRequest(reqWithScopes("recipes:read", "recipes:write"), "recipes:write")
	if err != nil {
		t.Fatalf("expected scope to pass: %v", err)
	}
	if user.ID != 1 {
		t.Fatalf("wrong user resolved: %d", user.ID)
	}
}

func TestGetRecipe(t *testing.T) {
	deps, _, recipeRepo := newTestDeps(t)
	recipe := testutil.TestRecipe()
	if err := recipeRepo.CreateRecipe(recipe); err != nil {
		t.Fatalf("seed recipe: %v", err)
	}

	_, out, err := deps.getRecipe(context.Background(), reqWithScopes("recipes:read"), getRecipeIn{RecipeID: "1"})
	if err != nil {
		t.Fatalf("getRecipe failed: %v", err)
	}
	if out.View != viewRecipeCard || out.Recipe == nil || out.Recipe.Title == "" {
		t.Fatalf("unexpected output: %+v", out)
	}

	if _, _, err := deps.getRecipe(context.Background(), reqWithScopes("recipes:read"), getRecipeIn{RecipeID: "999"}); err == nil {
		t.Fatal("expected error for unknown recipe")
	}
	if _, _, err := deps.getRecipe(context.Background(), reqWithScopes("recipes:read"), getRecipeIn{RecipeID: "abc"}); err == nil {
		t.Fatal("expected error for non-numeric id")
	}
}

func TestListMyRecipes(t *testing.T) {
	deps, _, recipeRepo := newTestDeps(t)
	recipe := testutil.TestRecipe()
	if err := recipeRepo.CreateRecipe(recipe); err != nil {
		t.Fatalf("seed recipe: %v", err)
	}

	_, out, err := deps.listMyRecipes(context.Background(), reqWithScopes("recipes:read"), listMyRecipesIn{})
	if err != nil {
		t.Fatalf("listMyRecipes failed: %v", err)
	}
	if out.View != viewRecipeList || out.Page != 1 {
		t.Fatalf("unexpected output metadata: %+v", out)
	}
	if len(out.Recipes) != 1 {
		t.Fatalf("expected 1 recipe, got %d", len(out.Recipes))
	}
}

func TestSearchRecipes_HappyAndMetered(t *testing.T) {
	deps, userRepo, _ := newTestDeps(t)

	_, out, err := deps.searchRecipes(context.Background(), reqWithScopes("search"), searchRecipesIn{Query: "salmon"})
	if err != nil {
		t.Fatalf("searchRecipes failed: %v", err)
	}
	if out.View != viewSearchResults || len(out.Results) != 2 {
		t.Fatalf("unexpected output: %+v", out)
	}
	if used := userRepo.Users[1].Subscription.WebSearchesUsed; used != 1 {
		t.Fatalf("expected usage increment to 1, got %d", used)
	}
}

func TestSearchRecipes_GatesAtLimit(t *testing.T) {
	deps, userRepo, _ := newTestDeps(t)
	userRepo.Users[1].Subscription.WebSearchesUsed = 20 // free-tier cap

	_, _, err := deps.searchRecipes(context.Background(), reqWithScopes("search"), searchRecipesIn{Query: "salmon"})
	if err == nil || !strings.Contains(err.Error(), "searches") {
		t.Fatalf("expected limit error, got %v", err)
	}
}

func TestSearchRecipes_RequiresQuery(t *testing.T) {
	deps, _, _ := newTestDeps(t)
	if _, _, err := deps.searchRecipes(context.Background(), reqWithScopes("search"), searchRecipesIn{Query: "  "}); err == nil {
		t.Fatal("expected error for empty query")
	}
}

// TestServerLifecycle_ToolsAndWidget exercises the assembled MCP server over
// an in-memory transport, exactly as a host would: list tools (checking the
// MCP Apps declarations), read the widget resource, and verify an
// unauthenticated tool call surfaces a tool-level error.
func TestServerLifecycle_ToolsAndWidget(t *testing.T) {
	deps, _, _ := newTestDeps(t)
	cfg := &config.Config{EnvVars: config.EnvVars{S3Bucket: "test-bucket", AWSRegion: "us-east-2"}}
	server := BuildServer(cfg, deps)

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-host", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	wantTools := map[string]bool{
		"search_recipes": false, "preview_recipe": false, "save_recipe": false,
		"list_my_recipes": false, "get_recipe": false,
	}
	for _, tool := range tools.Tools {
		if _, known := wantTools[tool.Name]; known {
			wantTools[tool.Name] = true
		}
		ui, ok := tool.Meta["ui"].(map[string]any)
		if !ok || ui["resourceUri"] != widgetURI {
			t.Fatalf("tool %s missing MCP Apps declaration: %v", tool.Name, tool.Meta)
		}
	}
	for name, seen := range wantTools {
		if !seen {
			t.Fatalf("tool %s not registered", name)
		}
	}

	res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: widgetURI})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 || res.Contents[0].MIMEType != mcpAppMIMEType {
		t.Fatalf("unexpected widget resource: %+v", res.Contents)
	}
	for _, marker := range []string{"ui/initialize", "ui/notifications/tool-result", "tools/call"} {
		if !strings.Contains(res.Contents[0].Text, marker) {
			t.Fatalf("widget HTML missing protocol marker %q", marker)
		}
	}

	// No bearer token on the in-memory transport → tool must return a
	// tool-level error (IsError), not a protocol failure.
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_recipe",
		Arguments: map[string]any{"recipe_id": "1"},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for unauthenticated tool call")
	}
}
