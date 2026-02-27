package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// RecipeTreeService is the business logic layer for recipe tree operations.
type RecipeTreeService struct {
	Cfg  *config.Config
	Repo *repository.RecipeRepository
}

// NewRecipeTreeService creates a new RecipeTreeService.
func NewRecipeTreeService(cfg *config.Config, repo *repository.RecipeRepository) *RecipeTreeService {
	return &RecipeTreeService{
		Cfg:  cfg,
		Repo: repo,
	}
}

// NodeResponse is the response object for a single tree node, suitable for Flutter tree visualization.
type NodeResponse struct {
	ID          uint            `json:"id"`
	ParentID    *uint           `json:"parent_id"`
	BranchName  string          `json:"branch_name"`
	Summary     string          `json:"summary"`
	Type        models.RecipeType `json:"type"`
	IsActive    bool            `json:"is_active"`
	IsEphemeral bool            `json:"is_ephemeral"`
	CreatedByID uint            `json:"created_by_id"`
	CreatedAt   time.Time       `json:"created_at"`
	Children    []NodeResponse  `json:"children"`
}

// TreeResponse is the response object for a full recipe tree.
type FullTreeResponse struct {
	TreeID     uint          `json:"tree_id"`
	RecipeID   uint          `json:"recipe_id"`
	RootNodeID *uint         `json:"root_node_id"`
	RootNode   *NodeResponse `json:"root_node"`
}

// GetTree retrieves the full tree for a recipe and returns it as a nested structure.
func (s *RecipeTreeService) GetTree(recipeID uint) (*FullTreeResponse, error) {
	treeRef, err := s.Repo.GetTreeByRecipeID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe tree: %w", err)
	}

	tree, err := s.Repo.GetTreeWithNodes(treeRef.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree nodes: %w", err)
	}

	// Build a map of nodeID -> node for quick lookup
	nodeMap := make(map[uint]*NodeResponse)
	for _, n := range tree.Nodes {
		nodeMap[n.ID] = &NodeResponse{
			ID:          n.ID,
			ParentID:    n.ParentID,
			BranchName:  n.BranchName,
			Summary:     n.Summary,
			Type:        n.Type,
			IsActive:    n.IsActive,
			IsEphemeral: n.IsEphemeral,
			CreatedByID: n.CreatedByID,
			CreatedAt:   n.CreatedAt,
			Children:    []NodeResponse{},
		}
	}

	// Build the tree by linking children to parents
	var rootNode *NodeResponse
	for _, n := range tree.Nodes {
		nr := nodeMap[n.ID]
		if n.ParentID != nil {
			if parent, ok := nodeMap[*n.ParentID]; ok {
				parent.Children = append(parent.Children, *nr)
			}
		}
		if tree.RootNodeID != nil && n.ID == *tree.RootNodeID {
			rootNode = nr
		}
	}

	// Re-assign children after all are linked (since we copied by value above)
	if rootNode != nil {
		rootNode = rebuildNode(rootNode, nodeMap, tree.Nodes)
	}

	return &FullTreeResponse{
		TreeID:     tree.ID,
		RecipeID:   tree.RecipeID,
		RootNodeID: tree.RootNodeID,
		RootNode:   rootNode,
	}, nil
}

// rebuildNode recursively builds the nested node tree from the flat node list.
func rebuildNode(node *NodeResponse, nodeMap map[uint]*NodeResponse, allNodes []models.RecipeNode) *NodeResponse {
	node.Children = []NodeResponse{}
	for _, n := range allNodes {
		if n.ParentID != nil && *n.ParentID == node.ID {
			child := nodeMap[n.ID]
			child = rebuildNode(child, nodeMap, allNodes)
			node.Children = append(node.Children, *child)
		}
	}
	return node
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
	}

	return nil
}
