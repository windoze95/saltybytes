package repository

import (
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
)

// RecipeRepo is the interface for recipe repository operations.
type RecipeRepo interface {
	GetUserRecipes(userID uint, page, pageSize int) ([]models.Recipe, int64, error)
	GetRecipeByID(recipeID uint) (*models.Recipe, error)
	GetHistoryByID(historyID uint) (*models.RecipeHistory, error)
	GetRecipeByHistoryID(historyID uint) (*models.Recipe, error)
	GetRecipeHistoryEntriesAfterID(historyID uint, afterID uint) ([]models.RecipeHistoryEntry, error)
	CreateRecipe(recipe *models.Recipe) error
	DeleteRecipe(recipeID uint) error
	UpdateRecipeTitle(recipe *models.Recipe, title string) error
	UpdateRecipeImageURL(recipeID uint, imageURL string) error
	UpdateRecipeDef(recipe *models.Recipe, newRecipeHistoryEntry models.RecipeHistoryEntry) error
	UpdateRecipeWithHistoryEntry(recipeID uint, newActiveEntryID uint, updatedResponse models.RecipeDef) error
	FindTagByName(tagName string) (*models.Tag, error)
	CreateTag(tag *models.Tag) error
	UpdateRecipeTagsAssociation(recipeID uint, newTags []models.Tag) error
	CreateRecipeTree(recipeID uint, rootNode *models.RecipeNode) (*models.RecipeTree, error)
	GetTreeByRecipeID(recipeID uint) (*models.RecipeTree, error)
	GetTreeWithNodes(treeID uint) (*models.RecipeTree, error)
	GetActiveNode(treeID uint) (*models.RecipeNode, error)
	GetNodeByID(nodeID uint) (*models.RecipeNode, error)
	GetNodeChildren(nodeID uint) ([]models.RecipeNode, error)
	GetNodeAncestors(nodeID uint) ([]models.RecipeNode, error)
	AddNodeToTree(node *models.RecipeNode, setActive bool) error
	SetActiveNode(treeID uint, nodeID uint) error
	UpdateRecipeFromNode(recipeID uint, node *models.RecipeNode) error
	MaterializeRecipeFromCanonical(recipeID uint, data models.RecipeDef) error
}

// CanonicalRecipeRepo is the interface for canonical recipe repository operations.
type CanonicalRecipeRepo interface {
	GetByID(id uint) (*models.CanonicalRecipe, error)
	GetByNormalizedURL(normalizedURL string) (*models.CanonicalRecipe, error)
	Upsert(entry *models.CanonicalRecipe) error
	IncrementHitCount(id uint) error
	GetStaleEntries(maxAge time.Duration) ([]models.CanonicalRecipe, error)
}

// SearchCacheRepo is the interface for search cache repository operations.
type SearchCacheRepo interface {
	GetByNormalizedQuery(query string) (*models.SearchCache, error)
	Upsert(entry *models.SearchCache) error
	IncrementHitCount(id uint) error
	FindSimilar(embedding []float32, threshold float64, limit int) ([]models.SearchCache, error)
	GetHotQueries(minHits int, maxAge, refreshWindow time.Duration) ([]models.SearchCache, error)
	DeleteStale(maxAge time.Duration) (int64, error)
}

// UserRepo is the interface for user repository operations.
type UserRepo interface {
	CreateUser(user *models.User) (*models.User, error)
	GetUserByID(userID uint) (*models.User, error)
	GetUserAuthByUsername(username string) (*models.User, error)
	UpdateUserFirstName(userID uint, firstName string) error
	UpdateUserEmail(userID uint, email string) error
	UpdateUserSettingsKeepScreenAwake(userID uint, keepScreenAwake bool) error
	UpdatePersonalization(userID uint, updatedPersonalization *models.Personalization) error
	UsernameExists(username string) (bool, error)
}
