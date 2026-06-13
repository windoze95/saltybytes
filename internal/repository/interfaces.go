package repository

import (
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
)

// RecipeRepo is the interface for recipe repository operations.
type RecipeRepo interface {
	GetUserRecipes(userID uint, page, pageSize int) ([]models.Recipe, int64, error)
	GetRecipeByID(recipeID uint) (*models.Recipe, error)
	CreateRecipe(recipe *models.Recipe) error
	DeleteRecipe(recipeID uint) error
	UpdateRecipeTitle(recipe *models.Recipe, title string) error
	UpdateRecipeImageURL(recipeID uint, imageURL string) error
	UpdateRecipeStatus(recipeID uint, status string) error
	UpdateRecipeDef(recipe *models.Recipe) error
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

// VectorRepo is the interface for pgvector similarity search operations.
type VectorRepo interface {
	FindSimilar(embeddingLiteral string, excludeRecipeID uint, limit int) ([]models.Recipe, error)
	GetRecipeEmbedding(recipeID uint) (*string, error)
	UpdateEmbedding(recipeID uint, embedding []float32) error
	SearchUserRecipesByEmbedding(userID uint, embeddingLiteral string, limit int) ([]models.Recipe, error)
	SearchUserRecipesByTitle(userID uint, query string, onlyMissingEmbedding bool, limit int) ([]models.Recipe, error)
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

// FamilyRepo is the interface for family repository operations.
type FamilyRepo interface {
	CreateFamily(family *models.Family) error
	GetFamilyByOwnerID(ownerID uint) (*models.Family, error)
	CreateFamilyMember(member *models.FamilyMember) error
	GetFamilyMemberByID(id uint) (*models.FamilyMember, error)
	UpdateFamilyMember(member *models.FamilyMember) error
	DeleteFamilyMember(id uint) error
	UpdateDietaryProfile(profile *models.DietaryProfile) error
	GetOrCreateDietaryProfile(memberID uint) (*models.DietaryProfile, error)
}

// AllergenRepo is the interface for allergen analysis repository operations.
type AllergenRepo interface {
	CreateAnalysis(analysis *models.AllergenAnalysis) error
	GetAnalysisByRecipeID(recipeID uint) (*models.AllergenAnalysis, error)
	GetAnalysisByNodeID(nodeID uint) (*models.AllergenAnalysis, error)
	UpdateAnalysis(analysis *models.AllergenAnalysis) error
	DeleteAnalysisByRecipeID(recipeID uint) error
}

// UserRepo is the interface for user repository operations.
type UserRepo interface {
	CreateUser(user *models.User) (*models.User, error)
	GetUserByID(userID uint) (*models.User, error)
	GetUserWithAuthByID(userID uint) (*models.User, error)
	GetUserAuthByUsername(username string) (*models.User, error)
	UpdateUserFirstName(userID uint, firstName string) error
	UpdateUserEmail(userID uint, email string) error
	UpdateUserSettingsKeepScreenAwake(userID uint, keepScreenAwake bool) error
	UpdatePersonalization(userID uint, update *models.PersonalizationUpdate) error
	UsernameExists(username string) (bool, error)
	IncrementTokenVersion(userID uint) error
	CreateSubscription(sub *models.Subscription) error
	IncrementSubscriptionUsage(userID uint, column string) error
	ResetSubscriptionUsage(userID uint, nextReset time.Time) error
}

// Compile-time check that the concrete repository satisfies the interface.
var _ UserRepo = (*UserRepository)(nil)
var _ FamilyRepo = (*FamilyRepository)(nil)
var _ AllergenRepo = (*AllergenRepository)(nil)
