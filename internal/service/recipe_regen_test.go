package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// seedOwnedRecipeWithTree seeds an owned recipe plus a tree with an active
// root node and returns (in-flight recipe, tree root node). The returned
// recipe is a detached copy of the stored row — like the real repository,
// which hydrates a fresh struct from the DB — so in-flight mutations by the
// service do not leak into the repo until an Update* call commits them.
func seedOwnedRecipeWithTree(t *testing.T, repo *testutil.MockRecipeRepo, ownerID uint) (*models.Recipe, *models.RecipeNode) {
	t.Helper()
	def := testutil.TestRecipeDef()
	recipe := &models.Recipe{
		Model:       gorm.Model{ID: 1},
		RecipeDef:   def,
		CreatedByID: ownerID,
	}
	repo.Recipes[recipe.ID] = recipe
	repo.NextID = 2

	rootNode := &models.RecipeNode{
		Prompt:      "make pancakes",
		Response:    &def,
		Summary:     "Initial pancake recipe",
		Type:        models.RecipeTypeChat,
		BranchName:  "original",
		CreatedByID: ownerID,
		IsActive:    true,
	}
	if _, err := repo.CreateRecipeTree(recipe.ID, rootNode); err != nil {
		t.Fatalf("CreateRecipeTree() error = %v", err)
	}
	inFlight := *recipe
	return &inFlight, rootNode
}

// --- InitRegenerateRecipe ---

func TestInitRegenerateRecipe_NilPersonalization(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	user.Personalization = &models.Personalization{}

	svc := newGenRecipeService(repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	if err := svc.InitRegenerateRecipe(user, 1, "fluffier", false); err == nil {
		t.Fatal("InitRegenerateRecipe() error = nil, want error for missing personalization")
	}
}

func TestInitRegenerateRecipe_RecipeNotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()

	svc := newGenRecipeService(repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	if err := svc.InitRegenerateRecipe(testutil.TestUser(), 999, "fluffier", false); err == nil {
		t.Fatal("InitRegenerateRecipe() error = nil, want error for missing recipe")
	}
}

func TestInitRegenerateRecipe_NotOwner(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser() // ID 1
	seedOwnedRecipeWithTree(t, repo, 99)

	svc := newGenRecipeService(repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	err := svc.InitRegenerateRecipe(user, 1, "fluffier", false)
	if err == nil {
		t.Fatal("InitRegenerateRecipe() error = nil, want unauthorized error")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error = %q, want unauthorized", err)
	}
}

func TestInitRegenerateRecipe_HappyPathStartsRegen(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	seedOwnedRecipeWithTree(t, repo, user.ID)

	gate := make(chan struct{})
	entered := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			close(entered)
			<-gate
			return nil, errors.New("aborted by test teardown")
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	if err := svc.InitRegenerateRecipe(user, 1, "fluffier", false); err != nil {
		t.Fatalf("InitRegenerateRecipe() error = %v", err)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("FinishRegenerateRecipe goroutine never called the text provider")
	}
}

// --- FinishRegenerateRecipe ---

func TestFinishRegenerateRecipe_HappyPathAppendsNode(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe, rootNode := seedOwnedRecipeWithTree(t, repo, user.ID)
	originalDefJSON, _ := json.Marshal(recipe.RecipeDef)

	var gotReq ai.RegenerateRequest
	result := testutil.TestRecipeResult()
	result.Title = "Fluffier Pancakes"
	result.UnitSystem = "metric"
	result.PromptVersion = "pv-regen-1"
	result.Summary = "Made them fluffier"
	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			gotReq = req
			return result, nil
		},
	}
	vector := &testutil.MockVectorRepo{}
	embed := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.3}, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, embed, vector)
	svc.FinishRegenerateRecipe(recipe, user, "make them fluffier", false)

	// Conversation context: the chain root rendered with the pre-regen def.
	wantHistory := []ai.Message{
		{Role: "user", Content: "make pancakes"},
		{Role: "assistant", Content: string(originalDefJSON)},
	}
	if !reflect.DeepEqual(gotReq.ExistingHistory, wantHistory) {
		t.Errorf("ExistingHistory = %+v, want %+v", gotReq.ExistingHistory, wantHistory)
	}
	if gotReq.UserPrompt != "make them fluffier" {
		t.Errorf("UserPrompt = %q", gotReq.UserPrompt)
	}
	// Def is re-rendered in the user's unit system preference.
	if gotReq.UnitSystem != "US Customary" {
		t.Errorf("req.UnitSystem = %q, want 'US Customary'", gotReq.UnitSystem)
	}

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil {
		t.Fatal("recipe should still exist")
	}
	if stored.Title != "Fluffier Pancakes" {
		t.Errorf("persisted Title = %q, want 'Fluffier Pancakes'", stored.Title)
	}
	if recipe.PromptVersion != "pv-regen-1" {
		t.Errorf("PromptVersion = %q, want the result's prompt version preserved", recipe.PromptVersion)
	}

	// New node appended as child of the previously active node.
	tree, _ := repo.GetTreeByRecipeID(recipe.ID)
	active, err := repo.GetActiveNode(tree.ID)
	if err != nil {
		t.Fatalf("GetActiveNode() error = %v", err)
	}
	if active.ID == rootNode.ID {
		t.Fatal("a new node should be active, not the old root")
	}
	if active.ParentID == nil || *active.ParentID != rootNode.ID {
		t.Errorf("new node ParentID = %v, want %d", active.ParentID, rootNode.ID)
	}
	if active.Type != models.RecipeTypeRegenChat {
		t.Errorf("new node Type = %q, want %q", active.Type, models.RecipeTypeRegenChat)
	}
	if active.BranchName != rootNode.BranchName {
		t.Errorf("new node BranchName = %q, want %q (inherited)", active.BranchName, rootNode.BranchName)
	}
	if active.Prompt != "make them fluffier" || active.Summary != "Made them fluffier" {
		t.Errorf("new node prompt/summary = %q/%q", active.Prompt, active.Summary)
	}
	if active.Response == nil || active.Response.Title != "Fluffier Pancakes" {
		t.Error("new node Response should carry the regenerated def")
	}
	if rootNode.IsActive {
		t.Error("old root should have been deactivated")
	}

	if len(vector.UpdateEmbeddingCalls) != 1 || vector.UpdateEmbeddingCalls[0] != recipe.ID {
		t.Errorf("UpdateEmbeddingCalls = %v, want [%d]", vector.UpdateEmbeddingCalls, recipe.ID)
	}
}

func TestFinishRegenerateRecipe_CanonicalCopyOnWrite(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	seeded := testutil.TestCanonicalLinkedRecipe() // HasDiverged=false, Canonical set
	seeded.CreatedByID = user.ID
	repo.Recipes[seeded.ID] = seeded
	repo.NextID = seeded.ID + 1
	inFlight := *seeded
	recipe := &inFlight

	result := testutil.TestRecipeResult()
	result.Title = "Diverged Pancakes"
	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishRegenerateRecipe(recipe, user, "make it mine", false)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil {
		t.Fatal("recipe should still exist")
	}
	if !stored.HasDiverged {
		t.Error("recipe should be materialized from canonical (HasDiverged=true) before regen mutates it")
	}
	if stored.Title != "Diverged Pancakes" {
		t.Errorf("persisted Title = %q, want the regenerated def to win", stored.Title)
	}
}

func TestFinishRegenerateRecipe_AIFailurePreservesRecipe(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe, _ := seedOwnedRecipeWithTree(t, repo, user.ID)
	nodesBefore := len(repo.Nodes)

	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			return nil, errors.New("claude unavailable")
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishRegenerateRecipe(recipe, user, "fluffier", false)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil {
		t.Fatal("regen failure must NOT delete the existing recipe")
	}
	if stored.Title != "Classic Pancakes" {
		t.Errorf("persisted Title = %q, want the previous version preserved", stored.Title)
	}
	if len(repo.Nodes) != nodesBefore {
		t.Errorf("nodes = %d, want %d (no node appended on failure)", len(repo.Nodes), nodesBefore)
	}
}

func TestFinishRegenerateRecipe_ValidationFailurePreservesRecipe(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe, _ := seedOwnedRecipeWithTree(t, repo, user.ID)

	result := testutil.TestRecipeResult()
	result.Title = ""
	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishRegenerateRecipe(recipe, user, "fluffier", false)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil || stored.Title != "Classic Pancakes" {
		t.Error("validation failure must leave the previous recipe intact")
	}
}

func TestFinishRegenerateRecipe_UnitSystemFallbackToPersonalization(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe, _ := seedOwnedRecipeWithTree(t, repo, user.ID)

	result := testutil.TestRecipeResult()
	result.UnitSystem = ""
	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishRegenerateRecipe(recipe, user, "fluffier", false)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil || stored.UnitSystem != "us_customary" {
		t.Errorf("persisted UnitSystem should fall back to personalization, got %+v", stored)
	}
}

func TestFinishRegenerateRecipe_NoTreeStillUpdatesDef(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	seeded := &models.Recipe{
		Model:       gorm.Model{ID: 1},
		RecipeDef:   testutil.TestRecipeDef(),
		CreatedByID: user.ID,
	}
	repo.Recipes[seeded.ID] = seeded
	repo.NextID = 2
	inFlight := *seeded
	recipe := &inFlight

	var gotHistory []ai.Message
	result := testutil.TestRecipeResult()
	result.Title = "Treeless Pancakes"
	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			gotHistory = req.ExistingHistory
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishRegenerateRecipe(recipe, user, "fluffier", false)

	if gotHistory != nil {
		t.Errorf("ExistingHistory = %+v, want nil when no tree exists", gotHistory)
	}
	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil || stored.Title != "Treeless Pancakes" {
		t.Error("def should still be updated when the recipe has no tree")
	}
	if len(repo.Nodes) != 0 {
		t.Errorf("no node should be created without a tree, got %d", len(repo.Nodes))
	}
}

func TestFinishRegenerateRecipe_PersistFailureSkipsNodeAppend(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	repo.UpdateRecipeDefErr = errors.New("db down")
	user := testutil.TestUser()
	recipe, _ := seedOwnedRecipeWithTree(t, repo, user.ID)
	nodesBefore := len(repo.Nodes)

	provider := &testutil.MockTextProvider{
		RegenerateRecipeFunc: func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishRegenerateRecipe(recipe, user, "fluffier", false)

	if repo.RecipeSnapshot(recipe.ID) == nil {
		t.Fatal("regen persist failure must not delete the recipe")
	}
	if len(repo.Nodes) != nodesBefore {
		t.Errorf("nodes = %d, want %d (no node appended when persist fails)", len(repo.Nodes), nodesBefore)
	}
}

// --- nodeChainToMessages ---

func chainNode(prompt, summary string, nodeType models.RecipeType) models.RecipeNode {
	return models.RecipeNode{Prompt: prompt, Summary: summary, Type: nodeType}
}

func TestNodeChainToMessages_TableDriven(t *testing.T) {
	def := testutil.TestRecipeDef()
	defJSON, _ := json.Marshal(&def)

	tests := []struct {
		name  string
		nodes []models.RecipeNode
		want  []ai.Message
	}{
		{
			name:  "empty chain",
			nodes: nil,
			want:  nil,
		},
		{
			name:  "single chat node uses current def",
			nodes: []models.RecipeNode{chainNode("make pancakes", "initial", models.RecipeTypeChat)},
			want: []ai.Message{
				{Role: "user", Content: "make pancakes"},
				{Role: "assistant", Content: string(defJSON)},
			},
		},
		{
			name: "earlier nodes use summaries, last uses def",
			nodes: []models.RecipeNode{
				chainNode("make pancakes", "initial recipe", models.RecipeTypeChat),
				chainNode("fluffier", "made fluffier", models.RecipeTypeRegenChat),
				chainNode("less sugar", "reduced sugar", models.RecipeTypeRegenChat),
			},
			want: []ai.Message{
				{Role: "user", Content: "make pancakes"},
				{Role: "assistant", Content: "initial recipe"},
				{Role: "user", Content: "fluffier"},
				{Role: "assistant", Content: "made fluffier"},
				{Role: "user", Content: "less sugar"},
				{Role: "assistant", Content: string(defJSON)},
			},
		},
		{
			name: "manual entry mid-chain is skipped",
			nodes: []models.RecipeNode{
				chainNode("", "", models.RecipeTypeManualEntry),
				chainNode("fluffier", "made fluffier", models.RecipeTypeRegenChat),
			},
			want: []ai.Message{
				{Role: "user", Content: "fluffier"},
				{Role: "assistant", Content: string(defJSON)},
			},
		},
		{
			name: "manual entry as last node uses boilerplate",
			nodes: []models.RecipeNode{
				chainNode("make pancakes", "initial recipe", models.RecipeTypeChat),
				chainNode("", "", models.RecipeTypeManualEntry),
			},
			want: []ai.Message{
				{Role: "user", Content: "make pancakes"},
				{Role: "assistant", Content: "initial recipe"},
				{Role: "user", Content: "The following response from you is the current revision of the recipe."},
				{Role: "assistant", Content: string(defJSON)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodeChainToMessages(tt.nodes, &def)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("nodeChainToMessages() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// --- compactNodeChain ---

// regenChain builds a chain of n regen nodes with predictable prompts/summaries.
func regenChain(n int) []models.RecipeNode {
	nodes := make([]models.RecipeNode, n)
	for i := range nodes {
		nodes[i] = chainNode(
			fmt.Sprintf("prompt-%d", i+1),
			fmt.Sprintf("summary-%d", i+1),
			models.RecipeTypeRegenChat,
		)
	}
	return nodes
}

func TestCompactNodeChain_ThresholdMatrix(t *testing.T) {
	def := testutil.TestRecipeDef()
	const keepRecent = 2

	tests := []struct {
		name          string
		numNodes      int
		wantCompacted bool
	}{
		{"below threshold passes through", keepRecent, false},
		// Exactly one older node: compaction boilerplate would cost more
		// tokens than it saves (commit 70a9239), so it is skipped.
		{"single-ancestor overflow skips compaction", keepRecent + 1, false},
		{"two older nodes trigger compaction", keepRecent + 2, true},
		{"deep chain triggers compaction", keepRecent + 5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := regenChain(tt.numNodes)
			got := compactNodeChain(nodes, &def, keepRecent)

			if !tt.wantCompacted {
				want := nodeChainToMessages(nodes, &def)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("uncompacted chain should equal nodeChainToMessages, got\n%+v\nwant\n%+v", got, want)
				}
				return
			}

			// Compacted: summary message + ack + 2*keepRecent verbatim messages.
			wantLen := 2 + 2*keepRecent
			if len(got) != wantLen {
				t.Fatalf("message count = %d, want %d", len(got), wantLen)
			}
			numOlder := tt.numNodes - keepRecent
			if got[0].Role != "user" || !strings.Contains(got[0].Content, fmt.Sprintf("%d prior revisions", numOlder)) {
				t.Errorf("summary message = %+v, want user message mentioning %d prior revisions", got[0], numOlder)
			}
			for i := 1; i <= numOlder; i++ {
				bullet := fmt.Sprintf("- prompt-%d: summary-%d", i, i)
				if !strings.Contains(got[0].Content, bullet) {
					t.Errorf("summary message missing bullet %q", bullet)
				}
			}
			// No recent node may leak into the summary.
			if strings.Contains(got[0].Content, fmt.Sprintf("prompt-%d", numOlder+1)) {
				t.Error("summary message should not include recent nodes")
			}
			if got[1].Role != "assistant" {
				t.Errorf("second message role = %q, want assistant ack", got[1].Role)
			}
			// Recent nodes verbatim, last one carrying the current def.
			wantRecent := nodeChainToMessages(nodes[numOlder:], &def)
			if !reflect.DeepEqual(got[2:], wantRecent) {
				t.Errorf("recent messages =\n%+v\nwant\n%+v", got[2:], wantRecent)
			}
		})
	}
}

func TestCompactNodeChain_OlderNodeWithoutSummaryUsesPromptBullet(t *testing.T) {
	def := testutil.TestRecipeDef()
	nodes := []models.RecipeNode{
		chainNode("prompt-only", "", models.RecipeTypeRegenChat),
		chainNode("prompt-2", "summary-2", models.RecipeTypeRegenChat),
		chainNode("recent-1", "summary-r1", models.RecipeTypeRegenChat),
	}

	got := compactNodeChain(nodes, &def, 1)
	if len(got) != 4 {
		t.Fatalf("message count = %d, want 4", len(got))
	}
	if !strings.Contains(got[0].Content, "- prompt-only\n") {
		t.Errorf("summary should contain a prompt-only bullet, got %q", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "- prompt-2: summary-2") {
		t.Errorf("summary should contain the prompt+summary bullet, got %q", got[0].Content)
	}
}

func TestCompactNodeChain_AllOlderNodesEmptySkipsSummaryMessage(t *testing.T) {
	def := testutil.TestRecipeDef()
	nodes := []models.RecipeNode{
		chainNode("", "", models.RecipeTypeRegenChat),
		chainNode("", "", models.RecipeTypeRegenChat),
		chainNode("recent-1", "summary-r1", models.RecipeTypeRegenChat),
	}

	got := compactNodeChain(nodes, &def, 1)
	want := nodeChainToMessages(nodes[2:], &def)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty older nodes should produce only recent messages, got\n%+v\nwant\n%+v", got, want)
	}
}

func TestCompactNodeChain_ProductionThreshold(t *testing.T) {
	// The regen/fork flows compact only when more than maxUncompactedNodes+1
	// nodes exist; pin the production constant so a change is deliberate.
	if maxUncompactedNodes != 6 {
		t.Errorf("maxUncompactedNodes = %d, want 6", maxUncompactedNodes)
	}
}
