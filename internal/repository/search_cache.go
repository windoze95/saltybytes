package repository

import (
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SearchCacheRepository handles search cache CRUD operations.
type SearchCacheRepository struct {
	DB *gorm.DB
}

// NewSearchCacheRepository creates a new SearchCacheRepository.
func NewSearchCacheRepository(db *gorm.DB) *SearchCacheRepository {
	return &SearchCacheRepository{DB: db}
}

// GetByNormalizedQuery performs an exact-match lookup on the normalized query.
func (r *SearchCacheRepository) GetByNormalizedQuery(query string) (*models.SearchCache, error) {
	var entry models.SearchCache
	err := r.DB.Where("normalized_query = ?", query).First(&entry).Error
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// Upsert creates or updates a cache entry, handling race conditions via ON CONFLICT.
func (r *SearchCacheRepository) Upsert(entry *models.SearchCache) error {
	return r.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "normalized_query"}},
		DoUpdates: clause.AssignmentColumns([]string{"results", "result_count", "fetched_at", "last_accessed_at", "embedding"}),
	}).Create(entry).Error
}

// IncrementHitCount atomically increments hit_count and updates last_accessed_at.
func (r *SearchCacheRepository) IncrementHitCount(id uint) error {
	return r.DB.Model(&models.SearchCache{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"hit_count":      gorm.Expr("hit_count + 1"),
			"last_accessed_at": time.Now(),
		}).Error
}

// FindSimilar finds cached entries with embeddings similar to the given vector.
func (r *SearchCacheRepository) FindSimilar(embedding []float32, threshold float64, limit int) ([]models.SearchCache, error) {
	maxDistance := 1.0 - threshold
	literal := PgvectorLiteral(embedding)

	var entries []models.SearchCache
	err := r.DB.
		Where("embedding IS NOT NULL AND (embedding <=> ?) < ?", literal, maxDistance).
		Order(fmt.Sprintf("embedding <=> '%s'", literal)).
		Limit(limit).
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("failed to find similar cache entries: %w", err)
	}
	return entries, nil
}

// GetHotQueries returns frequently accessed entries that are approaching staleness.
func (r *SearchCacheRepository) GetHotQueries(minHits int, maxAge, refreshWindow time.Duration) ([]models.SearchCache, error) {
	now := time.Now()
	staleAt := now.Add(-maxAge)
	refreshAt := now.Add(-maxAge + refreshWindow)

	var entries []models.SearchCache
	err := r.DB.
		Where("hit_count >= ? AND fetched_at > ? AND fetched_at < ?", minHits, staleAt, refreshAt).
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get hot queries: %w", err)
	}
	return entries, nil
}

// DeleteStale removes entries that haven't been accessed within maxAge.
func (r *SearchCacheRepository) DeleteStale(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge)
	result := r.DB.Where("last_accessed_at < ?", cutoff).Delete(&models.SearchCache{})
	return result.RowsAffected, result.Error
}
