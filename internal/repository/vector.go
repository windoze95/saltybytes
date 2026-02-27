package repository

import (
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
)

// VectorRepository handles pgvector similarity search operations.
type VectorRepository struct {
	DB *gorm.DB
}

// NewVectorRepository creates a new VectorRepository.
func NewVectorRepository(db *gorm.DB) *VectorRepository {
	return &VectorRepository{DB: db}
}

// FindSimilar finds recipes similar to the given embedding vector using cosine similarity.
func (r *VectorRepository) FindSimilar(embedding []float32, limit int) ([]models.Recipe, error) {
	if limit <= 0 {
		limit = 10
	}

	var recipes []models.Recipe
	err := r.DB.
		Preload("CreatedBy").
		Preload("Hashtags").
		Where("embedding IS NOT NULL").
		Order(fmt.Sprintf("embedding <=> '%v'", pgvectorLiteral(embedding))).
		Limit(limit).
		Find(&recipes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to find similar recipes: %w", err)
	}

	return recipes, nil
}

// UpdateEmbedding sets the embedding vector for a recipe.
func (r *VectorRepository) UpdateEmbedding(recipeID uint, embedding []float32) error {
	err := r.DB.Model(&models.Recipe{}).
		Where("id = ?", recipeID).
		Update("embedding", pgvectorLiteral(embedding)).Error
	if err != nil {
		return fmt.Errorf("failed to update embedding: %w", err)
	}
	return nil
}

// pgvectorLiteral formats a float32 slice as a pgvector literal string: [0.1,0.2,0.3]
func pgvectorLiteral(v []float32) string {
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
