package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// RecipeTreeHandler is the handler for recipe tree-related requests.
type RecipeTreeHandler struct {
	Service *service.RecipeTreeService
}

// NewRecipeTreeHandler creates a new RecipeTreeHandler.
func NewRecipeTreeHandler(treeService *service.RecipeTreeService) *RecipeTreeHandler {
	return &RecipeTreeHandler{Service: treeService}
}

// GetTree returns the full tree structure for a recipe.
// GET /v1/recipes/:recipe_id/tree
func (h *RecipeTreeHandler) GetTree(c *gin.Context) {
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	treeResponse, err := h.Service.GetTree(recipeID)
	if err != nil {
		logger.Get().Error("failed to get recipe tree", zap.String("recipe_id", recipeIDStr), zap.Error(err))
		switch e := err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": e.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve recipe tree"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"tree": treeResponse})
}

// CreateBranch creates a new branch node on a recipe's tree.
// POST /v1/recipes/:recipe_id/branch
func (h *RecipeTreeHandler) CreateBranch(c *gin.Context) {
	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Verify recipe ownership before allowing branch creation
	recipe, err := h.Service.Repo.GetRecipeByID(recipeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	if recipe.CreatedByID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only modify your own recipes"})
		return
	}

	var request struct {
		ParentNodeID uint   `json:"parent_node_id" binding:"required"`
		BranchName   string `json:"branch_name" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	node, err := h.Service.CreateBranch(recipeID, request.ParentNodeID, request.BranchName, user.ID)
	if err != nil {
		logger.Get().Error("failed to create branch",
			zap.String("recipe_id", recipeIDStr),
			zap.Uint("parent_node_id", request.ParentNodeID),
			zap.Error(err))
		switch e := err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": e.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create branch"})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{"node": node})
}

// SetActiveNode sets a specific node as the active node for a recipe's tree.
// PUT /v1/recipes/:recipe_id/tree/active/:node_id
func (h *RecipeTreeHandler) SetActiveNode(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	recipeIDStr := c.Param("recipe_id")
	recipeID, err := parseUintParam(recipeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	// Verify recipe ownership before allowing mutation
	recipe, err := h.Service.Repo.GetRecipeByID(recipeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	if recipe.CreatedByID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only modify your own recipes"})
		return
	}

	nodeIDStr := c.Param("node_id")
	nodeID, err := parseUintParam(nodeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid node ID"})
		return
	}

	if err := h.Service.SetActiveNode(recipeID, nodeID); err != nil {
		logger.Get().Error("failed to set active node",
			zap.String("recipe_id", recipeIDStr),
			zap.String("node_id", nodeIDStr),
			zap.Error(err))
		switch e := err.(type) {
		case repository.NotFoundError:
			c.JSON(http.StatusNotFound, gin.H{"error": e.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to set active node"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Active node updated"})
}
