package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// forkUser returns a user distinct from the source recipe's owner.
func forkUser() *models.User {
	user := testutil.TestUser()
	user.ID = 2
	user.Personalization.UserID = 2
	return user
}

// seedForkSource seeds a source recipe owned by user 1 with a tree whose root
// node is active, and returns the source recipe.
func seedForkSource(t *testing.T, repo *testutil.MockRecipeRepo) *models.Recipe {
	t.Helper()
	def := testutil.TestRecipeDef()
	source := &models.Recipe{
		Model:       gorm.Model{ID: 1},
		RecipeDef:   def,
		CreatedByID: 1,
	}
	repo.Recipes[source.ID] = source
	repo.NextID = 2

	rootNode := &models.RecipeNode{
		Prompt:      "make pancakes",
		Response:    &def,
		Summary:     "Initial pancake recipe",
		Type:        models.RecipeTypeChat,
		BranchName:  "original",
		CreatedByID: source.CreatedByID,
		IsActive:    true,
	}
	if _, err := repo.CreateRecipeTree(source.ID, rootNode); err != nil {
		t.Fatalf("CreateRecipeTree() error = %v", err)
	}
	return source
}

// seedForkPlaceholder creates the new (forked) recipe the way
// InitGenerateRecipeWithFork does.
func seedForkPlaceholder(t *testing.T, repo *testutil.MockRecipeRepo, user *models.User, source *models.Recipe) *models.Recipe {
	t.Helper()
	recipe := &models.Recipe{
		CreatedBy:          user,
		CreatedByID:        user.ID,
		ForkedFrom:         source,
		PersonalizationUID: user.Personalization.UID,
		Status:             "generating",
	}
	if err := repo.CreateRecipe(recipe); err != nil {
		t.Fatalf("CreateRecipe() error = %v", err)
	}
	return recipe
}

// --- InitGenerateRecipeWithFork ---

func TestInitGenerateRecipeWithFork_NilPersonalization(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := forkUser()
	user.Personalization = &models.Personalization{}

	svc := newGenRecipeService(repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	if _, err := svc.InitGenerateRecipeWithFork(user, 1, "make it vegan", false); err == nil {
		t.Fatal("InitGenerateRecipeWithFork() error = nil, want error for missing personalization")
	}
}

func TestInitGenerateRecipeWithFork_SourceNotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()

	svc := newGenRecipeService(repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	if _, err := svc.InitGenerateRecipeWithFork(forkUser(), 999, "make it vegan", false); err == nil {
		t.Fatal("InitGenerateRecipeWithFork() error = nil, want error for missing source recipe")
	}
}

func TestInitGenerateRecipeWithFork_CreatesGeneratingPlaceholder(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	source := seedForkSource(t, repo)
	user := forkUser()

	gate := make(chan struct{})
	entered := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	provider := &testutil.MockTextProvider{
		ForkRecipeFunc: func(ctx context.Context, req ai.ForkRequest) (*ai.RecipeResult, error) {
			close(entered)
			<-gate
			return nil, errors.New("aborted by test teardown")
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	resp, err := svc.InitGenerateRecipeWithFork(user, source.ID, "make it vegan", false)
	if err != nil {
		t.Fatalf("InitGenerateRecipeWithFork() error = %v", err)
	}
	if resp.Status != "generating" {
		t.Errorf("response Status = %q, want 'generating'", resp.Status)
	}

	stored := repo.RecipeSnapshot(2)
	if stored == nil {
		t.Fatal("forked placeholder recipe should exist with ID 2")
	}
	if stored.Status != "generating" {
		t.Errorf("stored Status = %q, want 'generating'", stored.Status)
	}
	if stored.ForkedFrom == nil || stored.ForkedFrom.ID != source.ID {
		t.Error("placeholder should reference the forked source recipe")
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("FinishGenerateRecipeWithFork goroutine never called the text provider")
	}
}

// --- FinishGenerateRecipeWithFork ---

func TestFinishGenerateRecipeWithFork_HappyPath(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	source := seedForkSource(t, repo)
	sourceDefJSON, _ := json.Marshal(source.RecipeDef)
	user := forkUser()
	recipe := seedForkPlaceholder(t, repo, user, source)
	sourceNodesBefore := len(repo.Nodes)

	var gotReq ai.ForkRequest
	result := testutil.TestRecipeResult()
	result.Title = "Vegan Pancakes"
	result.PromptVersion = "pv-fork-1"
	result.Summary = "Forked vegan"
	provider := &testutil.MockTextProvider{
		ForkRecipeFunc: func(ctx context.Context, req ai.ForkRequest) (*ai.RecipeResult, error) {
			gotReq = req
			return result, nil
		},
	}
	vector := &testutil.MockVectorRepo{}
	embed := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.7}, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, embed, vector)
	svc.FinishGenerateRecipeWithFork(recipe, source, user, "make it vegan", false)

	// Conversation context comes from the source recipe's tree.
	wantHistory := []ai.Message{
		{Role: "user", Content: "make pancakes"},
		{Role: "assistant", Content: string(sourceDefJSON)},
	}
	if len(gotReq.ExistingHistory) != 2 ||
		gotReq.ExistingHistory[0] != wantHistory[0] ||
		gotReq.ExistingHistory[1] != wantHistory[1] {
		t.Errorf("ExistingHistory = %+v, want %+v", gotReq.ExistingHistory, wantHistory)
	}
	if gotReq.UserPrompt != "make it vegan" {
		t.Errorf("UserPrompt = %q", gotReq.UserPrompt)
	}

	// New recipe is owned by the forker and persisted as ready.
	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil {
		t.Fatal("forked recipe should exist")
	}
	if stored.CreatedByID != user.ID {
		t.Errorf("forked recipe CreatedByID = %d, want forker %d", stored.CreatedByID, user.ID)
	}
	if stored.Title != "Vegan Pancakes" || stored.Status != "ready" {
		t.Errorf("forked recipe = title %q status %q, want persisted+ready", stored.Title, stored.Status)
	}
	if recipe.PromptVersion != "pv-fork-1" {
		t.Errorf("PromptVersion = %q, want 'pv-fork-1'", recipe.PromptVersion)
	}

	// New tree initialized for the fork, rooted at a fork node owned by the forker.
	tree, err := repo.GetTreeByRecipeID(recipe.ID)
	if err != nil {
		t.Fatalf("forked recipe should have its own tree: %v", err)
	}
	root, err := repo.GetActiveNode(tree.ID)
	if err != nil {
		t.Fatalf("GetActiveNode() error = %v", err)
	}
	if root.Type != models.RecipeTypeFork {
		t.Errorf("root node Type = %q, want %q", root.Type, models.RecipeTypeFork)
	}
	if root.BranchName != "original" {
		t.Errorf("root node BranchName = %q, want 'original'", root.BranchName)
	}
	if root.CreatedByID != user.ID {
		t.Errorf("root node CreatedByID = %d, want forker %d", root.CreatedByID, user.ID)
	}

	// Embedding generated for the new recipe.
	if len(vector.UpdateEmbeddingCalls) != 1 || vector.UpdateEmbeddingCalls[0] != recipe.ID {
		t.Errorf("UpdateEmbeddingCalls = %v, want [%d]", vector.UpdateEmbeddingCalls, recipe.ID)
	}

	// Source recipe untouched: same def, no extra nodes in its tree.
	storedSource := repo.RecipeSnapshot(source.ID)
	if storedSource.Title != "Classic Pancakes" {
		t.Errorf("source Title = %q, fork must not mutate the source", storedSource.Title)
	}
	sourceTree, _ := repo.GetTreeByRecipeID(source.ID)
	sourceNodes := 0
	for _, n := range repo.Nodes {
		if n.TreeID == sourceTree.ID {
			sourceNodes++
		}
	}
	if sourceNodes != sourceNodesBefore {
		t.Errorf("source tree nodes = %d, want %d (unchanged)", sourceNodes, sourceNodesBefore)
	}
}

func TestFinishGenerateRecipeWithFork_CanonicalSourceReadOnly(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := forkUser()

	// Source is a thin canonical reference (HasDiverged=false) with a tree.
	source := testutil.TestCanonicalLinkedRecipe()
	source.CreatedByID = 1
	repo.Recipes[source.ID] = source
	repo.NextID = source.ID + 1
	def := source.Canonical.RecipeData
	rootNode := &models.RecipeNode{
		Prompt:      "imported recipe",
		Response:    &def,
		Type:        models.RecipeTypeImportLink,
		BranchName:  "original",
		CreatedByID: source.CreatedByID,
		IsActive:    true,
	}
	if _, err := repo.CreateRecipeTree(source.ID, rootNode); err != nil {
		t.Fatalf("CreateRecipeTree() error = %v", err)
	}
	recipe := seedForkPlaceholder(t, repo, user, source)

	var gotReq ai.ForkRequest
	provider := &testutil.MockTextProvider{
		ForkRecipeFunc: func(ctx context.Context, req ai.ForkRequest) (*ai.RecipeResult, error) {
			gotReq = req
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipeWithFork(recipe, source, user, "make it vegan", false)

	// The fork context resolved the canonical's def without materializing it.
	canonicalJSON, _ := json.Marshal(effectiveRecipeDef(source))
	last := gotReq.ExistingHistory[len(gotReq.ExistingHistory)-1]
	if last.Role != "assistant" || last.Content != string(canonicalJSON) {
		t.Errorf("fork context last message = %+v, want the canonical def", last)
	}

	storedSource := repo.RecipeSnapshot(source.ID)
	if storedSource.HasDiverged {
		t.Error("fork must read the canonical read-only: source must NOT be materialized")
	}
}

func TestFinishGenerateRecipeWithFork_AIFailureDeletesForkOnly(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	source := seedForkSource(t, repo)
	user := forkUser()
	recipe := seedForkPlaceholder(t, repo, user, source)

	provider := &testutil.MockTextProvider{
		ForkRecipeFunc: func(ctx context.Context, req ai.ForkRequest) (*ai.RecipeResult, error) {
			return nil, errors.New("claude unavailable")
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipeWithFork(recipe, source, user, "make it vegan", false)

	if repo.RecipeSnapshot(recipe.ID) != nil {
		t.Error("forked placeholder should be deleted after AI failure")
	}
	if repo.RecipeSnapshot(source.ID) == nil {
		t.Error("source recipe must survive a fork failure")
	}
	statuses := repo.StatusUpdates()
	if len(statuses) != 1 || statuses[0].RecipeID != recipe.ID || statuses[0].Status != "failed" {
		t.Errorf("StatusUpdates = %v, want the fork marked failed", statuses)
	}
}

func TestFinishGenerateRecipeWithFork_SourceWithoutTreeNoHistory(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	source := &models.Recipe{
		Model:       gorm.Model{ID: 1},
		RecipeDef:   testutil.TestRecipeDef(),
		CreatedByID: 1,
	}
	repo.Recipes[source.ID] = source
	repo.NextID = 2
	user := forkUser()
	recipe := seedForkPlaceholder(t, repo, user, source)

	var gotHistory []ai.Message
	provider := &testutil.MockTextProvider{
		ForkRecipeFunc: func(ctx context.Context, req ai.ForkRequest) (*ai.RecipeResult, error) {
			gotHistory = req.ExistingHistory
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipeWithFork(recipe, source, user, "make it vegan", false)

	if gotHistory != nil {
		t.Errorf("ExistingHistory = %+v, want nil when the source has no tree", gotHistory)
	}
	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil || stored.Status != "ready" {
		t.Error("fork should still complete without source history")
	}
}
