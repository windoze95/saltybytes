package repository

import (
	"errors"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// --- RecipeTree/RecipeNode repository methods on RecipeRepository ---

// CreateRecipeTree creates a new recipe tree with a root node.
func (r *RecipeRepository) CreateRecipeTree(recipeID uint, rootNode *models.RecipeNode) (*models.RecipeTree, error) {
	var tree *models.RecipeTree

	err := r.DB.Transaction(func(tx *gorm.DB) error {
		tree = &models.RecipeTree{
			RecipeID: recipeID,
		}
		if err := tx.Create(tree).Error; err != nil {
			return err
		}

		rootNode.TreeID = tree.ID
		rootNode.IsActive = true
		if err := tx.Create(rootNode).Error; err != nil {
			return err
		}

		// Set the root node reference and link tree to recipe
		if err := tx.Model(tree).Update("root_node_id", rootNode.ID).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Recipe{}).Where("id = ?", recipeID).
			Update("tree_id", tree.ID).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Get().Error("failed to create recipe tree", zap.Uint("recipe_id", recipeID), zap.Error(err))
		return nil, err
	}

	return tree, nil
}

// GetTreeByRecipeID retrieves a recipe tree by the recipe ID.
func (r *RecipeRepository) GetTreeByRecipeID(recipeID uint) (*models.RecipeTree, error) {
	var tree models.RecipeTree
	err := r.DB.Where("recipe_id = ?", recipeID).First(&tree).Error
	if err != nil {
		return nil, err
	}
	return &tree, nil
}

// GetTreeWithNodes retrieves a recipe tree with all its nodes loaded.
func (r *RecipeRepository) GetTreeWithNodes(treeID uint) (*models.RecipeTree, error) {
	var tree models.RecipeTree
	err := r.DB.Preload("RootNode").First(&tree, treeID).Error
	if err != nil {
		return nil, err
	}

	// Manually load Nodes (gorm:"-" prevents Preload)
	if err := r.DB.Where("tree_id = ?", tree.ID).Order("created_at ASC").Find(&tree.Nodes).Error; err != nil {
		return nil, err
	}

	return &tree, nil
}

// GetActiveNode retrieves the currently active node for a tree.
func (r *RecipeRepository) GetActiveNode(treeID uint) (*models.RecipeNode, error) {
	var node models.RecipeNode
	err := r.DB.Where("tree_id = ? AND is_active = ?", treeID, true).First(&node).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFoundError{message: "no active node found"}
		}
		return nil, err
	}
	return &node, nil
}

// GetNodeByID retrieves a single recipe node by ID.
func (r *RecipeRepository) GetNodeByID(nodeID uint) (*models.RecipeNode, error) {
	var node models.RecipeNode
	err := r.DB.First(&node, nodeID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFoundError{message: "recipe node not found"}
		}
		return nil, err
	}
	return &node, nil
}

// GetNodeChildren retrieves the direct children of a node.
func (r *RecipeRepository) GetNodeChildren(nodeID uint) ([]models.RecipeNode, error) {
	var children []models.RecipeNode
	err := r.DB.Where("parent_id = ?", nodeID).Order("created_at ASC").Find(&children).Error
	if err != nil {
		return nil, err
	}
	return children, nil
}

// GetNodeAncestors walks from a node up to the root, returning the chain in root-first order.
func (r *RecipeRepository) GetNodeAncestors(nodeID uint) ([]models.RecipeNode, error) {
	var ancestors []models.RecipeNode
	currentID := nodeID

	for {
		var node models.RecipeNode
		if err := r.DB.First(&node, currentID).Error; err != nil {
			return nil, err
		}
		ancestors = append([]models.RecipeNode{node}, ancestors...)
		if node.ParentID == nil {
			break
		}
		currentID = *node.ParentID
	}

	return ancestors, nil
}

// AddNodeToTree adds a new node as a child of the specified parent and optionally sets it as active.
func (r *RecipeRepository) AddNodeToTree(node *models.RecipeNode, setActive bool) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(node).Error; err != nil {
			return err
		}

		if setActive {
			// Deactivate all other nodes in the tree
			if err := tx.Model(&models.RecipeNode{}).
				Where("tree_id = ? AND id != ?", node.TreeID, node.ID).
				Update("is_active", false).Error; err != nil {
				return err
			}
			if err := tx.Model(node).Update("is_active", true).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// SetActiveNode sets a specific node as the active node for its tree, deactivating all others.
func (r *RecipeRepository) SetActiveNode(treeID uint, nodeID uint) error {
	return r.DB.Transaction(func(tx *gorm.DB) error {
		// Verify the node belongs to this tree
		var node models.RecipeNode
		if err := tx.Where("id = ? AND tree_id = ?", nodeID, treeID).First(&node).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("node does not belong to the specified tree")
			}
			return err
		}

		// Deactivate all nodes in the tree
		if err := tx.Model(&models.RecipeNode{}).
			Where("tree_id = ?", treeID).
			Update("is_active", false).Error; err != nil {
			return err
		}

		// Activate the target node
		return tx.Model(&models.RecipeNode{}).
			Where("id = ?", nodeID).
			Update("is_active", true).Error
	})
}

// UpdateRecipeFromNode updates the recipe's core fields from a node's response and sets it as active.
func (r *RecipeRepository) UpdateRecipeFromNode(recipeID uint, node *models.RecipeNode) error {
	if node.Response == nil {
		return errors.New("node response is nil")
	}

	return r.DB.Transaction(func(tx *gorm.DB) error {
		// Update recipe core fields from the node's response
		if err := tx.Model(&models.Recipe{}).
			Where("id = ?", recipeID).
			Updates(map[string]interface{}{
				"Title":             node.Response.Title,
				"Ingredients":       node.Response.Ingredients,
				"Instructions":      node.Response.Instructions,
				"CookTime":          node.Response.CookTime,
				"LinkedSuggestions": node.Response.LinkedSuggestions,
				"ImagePrompt":       node.Response.ImagePrompt,
			}).Error; err != nil {
			return err
		}

		// Set this node as active
		return r.SetActiveNode(node.TreeID, node.ID)
	})
}
