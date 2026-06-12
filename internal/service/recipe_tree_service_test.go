package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// seedTreeServiceFixture seeds a recipe (ID 7) plus a tree with an active root
// and an inactive child node, returning (root, child).
func seedTreeServiceFixture(t *testing.T, repo *testutil.MockRecipeRepo) (*models.RecipeNode, *models.RecipeNode) {
	t.Helper()
	def := testutil.TestRecipeDef()
	repo.Recipes[7] = &models.Recipe{
		Model:       gorm.Model{ID: 7},
		RecipeDef:   def,
		CreatedByID: 1,
	}
	repo.NextID = 8

	root := &models.RecipeNode{
		Prompt:      "make pancakes",
		Response:    &def,
		Type:        models.RecipeTypeChat,
		BranchName:  "original",
		CreatedByID: 1,
		IsActive:    true,
	}
	tree, err := repo.CreateRecipeTree(7, root)
	if err != nil {
		t.Fatalf("CreateRecipeTree() error = %v", err)
	}

	childDef := testutil.TestRecipeDef()
	childDef.Title = "Fluffier Pancakes"
	child := &models.RecipeNode{
		TreeID:      tree.ID,
		ParentID:    &root.ID,
		Prompt:      "fluffier",
		Response:    &childDef,
		Type:        models.RecipeTypeRegenChat,
		BranchName:  "original",
		CreatedByID: 1,
	}
	if err := repo.AddNodeToTree(child, false); err != nil {
		t.Fatalf("AddNodeToTree() error = %v", err)
	}
	return root, child
}

// --- GetTree ---

func TestRecipeTreeService_GetTree_FlatResponse(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	root, _ := seedTreeServiceFixture(t, repo)

	svc := NewRecipeTreeService(&config.Config{}, repo)
	resp, err := svc.GetTree(7)
	if err != nil {
		t.Fatalf("GetTree() error = %v", err)
	}

	if resp.RecipeID != 7 {
		t.Errorf("RecipeID = %d, want 7", resp.RecipeID)
	}
	if resp.RootNodeID == nil || *resp.RootNodeID != root.ID {
		t.Errorf("RootNodeID = %v, want %d", resp.RootNodeID, root.ID)
	}
	if resp.ActiveNodeID == nil || *resp.ActiveNodeID != root.ID {
		t.Errorf("ActiveNodeID = %v, want %d (root is active)", resp.ActiveNodeID, root.ID)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("Nodes count = %d, want 2 (flat list)", len(resp.Nodes))
	}
	// Clients rebuild structure from parent_id: the child must reference root.
	foundChild := false
	for _, n := range resp.Nodes {
		if n.ParentID != nil && *n.ParentID == root.ID {
			foundChild = true
		}
	}
	if !foundChild {
		t.Error("flat node list should include the child with its parent_id set")
	}
}

func TestRecipeTreeService_GetTree_EmptyNodesNonNil(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	repo.Trees[1] = &models.RecipeTree{ID: 1, RecipeID: 7}
	repo.NextTreeID = 2

	svc := NewRecipeTreeService(&config.Config{}, repo)
	resp, err := svc.GetTree(7)
	if err != nil {
		t.Fatalf("GetTree() error = %v", err)
	}
	if resp.Nodes == nil {
		t.Error("Nodes must be non-nil so JSON serializes as []")
	}
	if len(resp.Nodes) != 0 {
		t.Errorf("Nodes count = %d, want 0", len(resp.Nodes))
	}
	if resp.ActiveNodeID != nil {
		t.Errorf("ActiveNodeID = %v, want nil with no nodes", resp.ActiveNodeID)
	}
}

func TestRecipeTreeService_GetTree_NoTree(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()

	svc := NewRecipeTreeService(&config.Config{}, repo)
	if _, err := svc.GetTree(7); err == nil {
		t.Fatal("GetTree() error = nil, want error when the recipe has no tree")
	}
}

func TestRecipeTreeService_GetTree_NodesError(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	seedTreeServiceFixture(t, repo)
	repo.GetTreeWithNodesErr = errors.New("db down")

	svc := NewRecipeTreeService(&config.Config{}, repo)
	if _, err := svc.GetTree(7); err == nil {
		t.Fatal("GetTree() error = nil, want error when loading nodes fails")
	}
}

// --- CreateBranch ---

func TestRecipeTreeService_CreateBranch_HappyPath(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	root, _ := seedTreeServiceFixture(t, repo)

	svc := NewRecipeTreeService(&config.Config{}, repo)
	node, err := svc.CreateBranch(7, root.ID, "experiment", 1)
	if err != nil {
		t.Fatalf("CreateBranch() error = %v", err)
	}

	if node.ID == 0 {
		t.Error("branch node should get an ID from the repo")
	}
	if node.ParentID == nil || *node.ParentID != root.ID {
		t.Errorf("ParentID = %v, want %d", node.ParentID, root.ID)
	}
	if node.BranchName != "experiment" {
		t.Errorf("BranchName = %q, want 'experiment'", node.BranchName)
	}
	if node.TreeID != root.TreeID {
		t.Errorf("TreeID = %d, want %d", node.TreeID, root.TreeID)
	}
	if node.CreatedByID != 1 {
		t.Errorf("CreatedByID = %d, want 1", node.CreatedByID)
	}
	// The branch node is a placeholder: no prompt/response yet and not active.
	if node.IsActive {
		t.Error("a new branch node must not become active")
	}
	if node.Response != nil {
		t.Error("a new branch node must not carry a response")
	}
	if root.IsActive == false {
		t.Error("creating a branch must not deactivate the current active node")
	}
	if _, err := repo.GetNodeByID(node.ID); err != nil {
		t.Errorf("branch node should be stored in the repo: %v", err)
	}
}

func TestRecipeTreeService_CreateBranch_ParentInDifferentTree(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	seedTreeServiceFixture(t, repo)

	// A second recipe with its own tree; its root is a foreign parent.
	otherDef := testutil.TestRecipeDef()
	repo.Recipes[8] = &models.Recipe{Model: gorm.Model{ID: 8}, RecipeDef: otherDef, CreatedByID: 1}
	otherRoot := &models.RecipeNode{
		Prompt:      "other recipe",
		Response:    &otherDef,
		Type:        models.RecipeTypeChat,
		CreatedByID: 1,
		IsActive:    true,
	}
	if _, err := repo.CreateRecipeTree(8, otherRoot); err != nil {
		t.Fatalf("CreateRecipeTree() error = %v", err)
	}

	svc := NewRecipeTreeService(&config.Config{}, repo)
	_, err := svc.CreateBranch(7, otherRoot.ID, "experiment", 1)
	if err == nil {
		t.Fatal("CreateBranch() error = nil, want cross-tree parent rejection")
	}
	if !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("error = %q, want parent-tree mismatch message", err)
	}
}

func TestRecipeTreeService_CreateBranch_TreeNotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()

	svc := NewRecipeTreeService(&config.Config{}, repo)
	if _, err := svc.CreateBranch(7, 1, "experiment", 1); err == nil {
		t.Fatal("CreateBranch() error = nil, want error when the recipe has no tree")
	}
}

func TestRecipeTreeService_CreateBranch_ParentNotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	seedTreeServiceFixture(t, repo)

	svc := NewRecipeTreeService(&config.Config{}, repo)
	if _, err := svc.CreateBranch(7, 999, "experiment", 1); err == nil {
		t.Fatal("CreateBranch() error = nil, want error for missing parent node")
	}
}

func TestRecipeTreeService_CreateBranch_AddNodeError(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	root, _ := seedTreeServiceFixture(t, repo)
	repo.AddNodeToTreeErr = errors.New("db down")

	svc := NewRecipeTreeService(&config.Config{}, repo)
	if _, err := svc.CreateBranch(7, root.ID, "experiment", 1); err == nil {
		t.Fatal("CreateBranch() error = nil, want error when the repo insert fails")
	}
}

// --- SetActiveNode ---

func TestRecipeTreeService_SetActiveNode_RewritesDefAndRegeneratesEmbedding(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	root, child := seedTreeServiceFixture(t, repo)

	var embeddedText string
	embed := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			embeddedText = text
			return []float32{0.9}, nil
		},
	}
	vector := &testutil.MockVectorRepo{}

	svc := NewRecipeTreeService(&config.Config{}, repo)
	svc.EmbedProvider = embed
	svc.VectorRepo = vector

	if err := svc.SetActiveNode(7, child.ID); err != nil {
		t.Fatalf("SetActiveNode() error = %v", err)
	}

	if !child.IsActive {
		t.Error("target node should be active")
	}
	if root.IsActive {
		t.Error("previous active node should be deactivated")
	}

	stored := repo.RecipeSnapshot(7)
	if stored.Title != "Fluffier Pancakes" {
		t.Errorf("recipe Title = %q, want def rewritten from the node's response", stored.Title)
	}

	if len(vector.UpdateEmbeddingCalls) != 1 || vector.UpdateEmbeddingCalls[0] != 7 {
		t.Errorf("UpdateEmbeddingCalls = %v, want [7]", vector.UpdateEmbeddingCalls)
	}
	if !strings.Contains(embeddedText, "Fluffier Pancakes") {
		t.Errorf("embedding text = %q, want it built from the new def", embeddedText)
	}
}

func TestRecipeTreeService_SetActiveNode_NilVectorDepsNoPanic(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	_, child := seedTreeServiceFixture(t, repo)

	// Constructor leaves EmbedProvider/VectorRepo nil; must be nil-safe.
	svc := NewRecipeTreeService(&config.Config{}, repo)
	if err := svc.SetActiveNode(7, child.ID); err != nil {
		t.Fatalf("SetActiveNode() error = %v, want success with nil vector deps", err)
	}

	stored := repo.RecipeSnapshot(7)
	if stored.Title != "Fluffier Pancakes" {
		t.Errorf("recipe Title = %q, want def rewritten even without vector deps", stored.Title)
	}
}

func TestRecipeTreeService_SetActiveNode_EmbeddingFailureNonFatal(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	_, child := seedTreeServiceFixture(t, repo)

	vector := &testutil.MockVectorRepo{}
	svc := NewRecipeTreeService(&config.Config{}, repo)
	svc.EmbedProvider = &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return nil, errors.New("embedding api down")
		},
	}
	svc.VectorRepo = vector

	if err := svc.SetActiveNode(7, child.ID); err != nil {
		t.Fatalf("SetActiveNode() error = %v, want embedding failure to be best-effort", err)
	}
	if len(vector.UpdateEmbeddingCalls) != 0 {
		t.Errorf("UpdateEmbeddingCalls = %v, want none after embed failure", vector.UpdateEmbeddingCalls)
	}
}

func TestRecipeTreeService_SetActiveNode_NodeWithoutResponse(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	root, _ := seedTreeServiceFixture(t, repo)

	// An ephemeral branch placeholder: no response yet.
	placeholder := &models.RecipeNode{
		TreeID:      root.TreeID,
		ParentID:    &root.ID,
		BranchName:  "experiment",
		CreatedByID: 1,
	}
	if err := repo.AddNodeToTree(placeholder, false); err != nil {
		t.Fatalf("AddNodeToTree() error = %v", err)
	}

	vector := &testutil.MockVectorRepo{}
	svc := NewRecipeTreeService(&config.Config{}, repo)
	svc.EmbedProvider = &testutil.MockEmbeddingProvider{}
	svc.VectorRepo = vector

	if err := svc.SetActiveNode(7, placeholder.ID); err != nil {
		t.Fatalf("SetActiveNode() error = %v", err)
	}

	stored := repo.RecipeSnapshot(7)
	if stored.Title != "Classic Pancakes" {
		t.Errorf("recipe Title = %q, want unchanged when the node has no response", stored.Title)
	}
	if len(vector.UpdateEmbeddingCalls) != 0 {
		t.Errorf("UpdateEmbeddingCalls = %v, want none for a response-less node", vector.UpdateEmbeddingCalls)
	}
	if !placeholder.IsActive {
		t.Error("placeholder should still become the active node")
	}
}

func TestRecipeTreeService_SetActiveNode_TreeNotFound(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()

	svc := NewRecipeTreeService(&config.Config{}, repo)
	if err := svc.SetActiveNode(7, 1); err == nil {
		t.Fatal("SetActiveNode() error = nil, want error when the recipe has no tree")
	}
}

func TestRecipeTreeService_SetActiveNode_RepoErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(repo *testutil.MockRecipeRepo)
	}{
		{"set active fails", func(repo *testutil.MockRecipeRepo) {
			repo.SetActiveNodeErr = errors.New("db down")
		}},
		{"node fetch fails", func(repo *testutil.MockRecipeRepo) {
			repo.GetNodeByIDErr = errors.New("db down")
		}},
		{"recipe rewrite fails", func(repo *testutil.MockRecipeRepo) {
			repo.UpdateRecipeFromNodeErr = errors.New("db down")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testutil.NewMockRecipeRepo()
			_, child := seedTreeServiceFixture(t, repo)
			tt.setup(repo)

			svc := NewRecipeTreeService(&config.Config{}, repo)
			if err := svc.SetActiveNode(7, child.ID); err == nil {
				t.Fatal("SetActiveNode() error = nil, want repo error to propagate")
			}
		})
	}
}
