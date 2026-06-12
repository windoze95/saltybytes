package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// RecipeTreeService is the business logic layer for recipe tree operations.
type RecipeTreeService struct {
	Cfg  *config.Config
	Repo repository.RecipeRepo
	// Optional: set these to keep recipe embeddings in sync when switching
	// the active node rewrites the recipe definition.
	EmbedProvider ai.EmbeddingProvider
	VectorRepo    repository.VectorRepo
}

// NewRecipeTreeService creates a new RecipeTreeService.
func NewRecipeTreeService(cfg *config.Config, repo repository.RecipeRepo) *RecipeTreeService {
	return &RecipeTreeService{
		Cfg:  cfg,
		Repo: repo,
	}
}

// GetTree retrieves the full tree for a recipe as a flat node list. Clients
// rebuild the tree structure from each node's parent_id.
func (s *RecipeTreeService) GetTree(recipeID uint) (*TreeResponse, error) {
	treeRef, err := s.Repo.GetTreeByRecipeID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe tree: %w", err)
	}

	tree, err := s.Repo.GetTreeWithNodes(treeRef.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree nodes: %w", err)
	}

	if tree.Nodes == nil {
		tree.Nodes = []models.RecipeNode{}
	}

	return &TreeResponse{
		TreeID:       tree.ID,
		RecipeID:     tree.RecipeID,
		RootNodeID:   tree.RootNodeID,
		ActiveNodeID: activeNodeID(tree.Nodes),
		Nodes:        tree.Nodes,
	}, nil
}

// CreateBranch creates a new branch node as a child of the specified parent node.
func (s *RecipeTreeService) CreateBranch(recipeID uint, parentNodeID uint, branchName string, userID uint) (*models.RecipeNode, error) {
	tree, err := s.Repo.GetTreeByRecipeID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe tree: %w", err)
	}

	// Verify the parent node exists and belongs to this tree
	parentNode, err := s.Repo.GetNodeByID(parentNodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent node: %w", err)
	}
	if parentNode.TreeID != tree.ID {
		return nil, errors.New("parent node does not belong to this recipe's tree")
	}

	node := &models.RecipeNode{
		TreeID:      tree.ID,
		ParentID:    &parentNodeID,
		BranchName:  branchName,
		CreatedByID: userID,
		IsActive:    false,
	}

	if err := s.Repo.AddNodeToTree(node, false); err != nil {
		return nil, fmt.Errorf("failed to create branch node: %w", err)
	}

	return node, nil
}

// SetActiveNode sets the specified node as the active node for the recipe's tree.
func (s *RecipeTreeService) SetActiveNode(recipeID uint, nodeID uint) error {
	tree, err := s.Repo.GetTreeByRecipeID(recipeID)
	if err != nil {
		return fmt.Errorf("failed to get recipe tree: %w", err)
	}

	if err := s.Repo.SetActiveNode(tree.ID, nodeID); err != nil {
		return fmt.Errorf("failed to set active node: %w", err)
	}

	// Update the recipe's fields from the node's response
	node, err := s.Repo.GetNodeByID(nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	if node.Response != nil {
		if err := s.Repo.UpdateRecipeFromNode(recipeID, node); err != nil {
			return fmt.Errorf("failed to update recipe from node: %w", err)
		}

		// The recipe definition changed, so the stored embedding is stale.
		// Regenerate it best-effort (warn-and-continue on failure).
		generateAndStoreRecipeEmbedding(context.Background(), s.EmbedProvider, s.VectorRepo, recipeID, node.Response)
	}

	return nil
}
