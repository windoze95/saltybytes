package repository

import (
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
)

// SimilarRecipeDistanceThreshold is the maximum cosine distance for a recipe
// to be considered similar to another recipe.
const SimilarRecipeDistanceThreshold = 0.65

// UserSearchDistanceThreshold is the maximum cosine distance for a recipe to
// match a user's semantic search query. It is looser than the similar-recipe
// threshold because query text is much shorter than recipe text.
const UserSearchDistanceThreshold = 0.75

// VectorRepository handles pgvector similarity search operations.
type VectorRepository struct {
	DB *gorm.DB
}

// NewVectorRepository creates a new VectorRepository.
func NewVectorRepository(db *gorm.DB) *VectorRepository {
	return &VectorRepository{DB: db}
}

// Compile-time interface check.
var _ VectorRepo = (*VectorRepository)(nil)

// FindSimilar finds recipes similar to the given embedding (a pgvector literal)
// using cosine distance. The source recipe is excluded and only matches within
// SimilarRecipeDistanceThreshold are returned.
func (r *VectorRepository) FindSimilar(embeddingLiteral string, excludeRecipeID uint, limit int) ([]models.Recipe, error) {
	if limit <= 0 {
		limit = 10
	}

	distanceExpr := fmt.Sprintf("embedding <=> '%s'", embeddingLiteral)

	var recipes []models.Recipe
	err := r.DB.
		Preload("Hashtags").
		Preload("Canonical").
		Preload("CreatedBy", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID", "Username")
		}).
		Where("embedding IS NOT NULL").
		Where("id != ?", excludeRecipeID).
		Where(distanceExpr+" < ?", SimilarRecipeDistanceThreshold).
		Order(distanceExpr).
		Limit(limit).
		Find(&recipes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to find similar recipes: %w", err)
	}

	return recipes, nil
}

// GetRecipeEmbedding returns the stored embedding literal for a recipe, or nil
// when the recipe has no embedding.
func (r *VectorRepository) GetRecipeEmbedding(recipeID uint) (*string, error) {
	var row struct {
		Embedding *string
	}
	err := r.DB.Model(&models.Recipe{}).
		Select("embedding").
		Where("id = ?", recipeID).
		First(&row).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe embedding: %w", err)
	}
	return row.Embedding, nil
}

// UpdateEmbedding sets the embedding vector for a recipe.
func (r *VectorRepository) UpdateEmbedding(recipeID uint, embedding []float32) error {
	err := r.DB.Model(&models.Recipe{}).
		Where("id = ?", recipeID).
		Update("embedding", PgvectorLiteral(embedding)).Error
	if err != nil {
		return fmt.Errorf("failed to update embedding: %w", err)
	}
	return nil
}

// SearchUserRecipesByEmbedding performs a semantic search over a user's own
// recipes using cosine distance against the given embedding literal.
func (r *VectorRepository) SearchUserRecipesByEmbedding(userID uint, embeddingLiteral string, limit int) ([]models.Recipe, error) {
	if limit <= 0 {
		limit = 10
	}

	distanceExpr := fmt.Sprintf("embedding <=> '%s'", embeddingLiteral)

	var recipes []models.Recipe
	err := r.DB.
		Preload("Hashtags").
		Preload("Canonical").
		Preload("CreatedBy", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID", "Username")
		}).
		Where("created_by_id = ?", userID).
		Where("embedding IS NOT NULL").
		Where(distanceExpr+" < ?", UserSearchDistanceThreshold).
		Order(distanceExpr).
		Limit(limit).
		Find(&recipes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to search user recipes by embedding: %w", err)
	}

	return recipes, nil
}

// SearchUserRecipesByTitle performs an ILIKE title match over a user's own
// recipes. When onlyMissingEmbedding is true, only recipes without an
// embedding are considered (used to complement a vector search).
func (r *VectorRepository) SearchUserRecipesByTitle(userID uint, query string, onlyMissingEmbedding bool, limit int) ([]models.Recipe, error) {
	if limit <= 0 {
		limit = 10
	}

	q := r.DB.
		Preload("Hashtags").
		Preload("Canonical").
		Preload("CreatedBy", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID", "Username")
		}).
		Where("created_by_id = ?", userID).
		Where("title ILIKE ?", "%"+query+"%")

	if onlyMissingEmbedding {
		q = q.Where("embedding IS NULL")
	}

	var recipes []models.Recipe
	err := q.Order("created_at DESC").
		Limit(limit).
		Find(&recipes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to search user recipes by title: %w", err)
	}

	return recipes, nil
}

// ListRecipesMissingEmbedding returns a batch of recipes without an embedding,
// ordered by ID, starting after afterID. Used by the embedding backfill task.
func (r *VectorRepository) ListRecipesMissingEmbedding(afterID uint, limit int) ([]models.Recipe, error) {
	if limit <= 0 {
		limit = 25
	}

	var recipes []models.Recipe
	err := r.DB.
		Preload("Canonical").
		Where("embedding IS NULL").
		Where("id > ?", afterID).
		Order("id ASC").
		Limit(limit).
		Find(&recipes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recipes missing embedding: %w", err)
	}

	return recipes, nil
}

// ListCanonicalsMissingEmbedding returns a batch of canonical recipes without
// an embedding, ordered by ID, starting after afterID. Used by the embedding
// backfill task.
func (r *VectorRepository) ListCanonicalsMissingEmbedding(afterID uint, limit int) ([]models.CanonicalRecipe, error) {
	if limit <= 0 {
		limit = 25
	}

	var entries []models.CanonicalRecipe
	err := r.DB.
		Where("embedding IS NULL").
		Where("id > ?", afterID).
		Order("id ASC").
		Limit(limit).
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list canonicals missing embedding: %w", err)
	}

	return entries, nil
}

// UpdateCanonicalEmbedding sets the embedding vector for a canonical recipe.
func (r *VectorRepository) UpdateCanonicalEmbedding(canonicalID uint, embedding []float32) error {
	err := r.DB.Model(&models.CanonicalRecipe{}).
		Where("id = ?", canonicalID).
		Update("embedding", PgvectorLiteral(embedding)).Error
	if err != nil {
		return fmt.Errorf("failed to update canonical embedding: %w", err)
	}
	return nil
}

// PgvectorLiteral formats a float32 slice as a pgvector literal string: [0.1,0.2,0.3]
func PgvectorLiteral(v []float32) string {
	s := "["
	for i, f := range v {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf("%g", f)
	}
	s += "]"
	return s
}
