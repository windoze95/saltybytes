package repository

import (
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CanonicalRecipeRepository handles canonical recipe CRUD operations.
type CanonicalRecipeRepository struct {
	DB *gorm.DB
}

// NewCanonicalRecipeRepository creates a new CanonicalRecipeRepository.
func NewCanonicalRecipeRepository(db *gorm.DB) *CanonicalRecipeRepository {
	return &CanonicalRecipeRepository{DB: db}
}

// GetByID retrieves a canonical recipe by its ID.
func (r *CanonicalRecipeRepository) GetByID(id uint) (*models.CanonicalRecipe, error) {
	var entry models.CanonicalRecipe
	err := r.DB.First(&entry, id).Error
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// GetByNormalizedURL performs an exact-match lookup on the normalized URL.
func (r *CanonicalRecipeRepository) GetByNormalizedURL(normalizedURL string) (*models.CanonicalRecipe, error) {
	var entry models.CanonicalRecipe
	err := r.DB.Where("normalized_url = ?", normalizedURL).First(&entry).Error
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// Upsert creates or updates a canonical recipe entry, handling race conditions via ON CONFLICT.
func (r *CanonicalRecipeRepository) Upsert(entry *models.CanonicalRecipe) error {
	return r.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "normalized_url"}},
		DoUpdates: clause.AssignmentColumns([]string{"recipe_data", "extraction_method", "fetched_at", "last_accessed_at", "original_url", "embedding", "prompt_version"}),
	}).Create(entry).Error
}

// IncrementHitCount atomically increments hit_count and updates last_accessed_at.
func (r *CanonicalRecipeRepository) IncrementHitCount(id uint) error {
	return r.DB.Model(&models.CanonicalRecipe{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"hit_count":        gorm.Expr("hit_count + 1"),
			"last_accessed_at": time.Now(),
		}).Error
}

// GetStaleEntries returns canonical entries whose FetchedAt is older than maxAge.
func (r *CanonicalRecipeRepository) GetStaleEntries(maxAge time.Duration) ([]models.CanonicalRecipe, error) {
	cutoff := time.Now().Add(-maxAge)
	var entries []models.CanonicalRecipe
	err := r.DB.Where("fetched_at < ?", cutoff).Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get stale canonical entries: %w", err)
	}
	return entries, nil
}
