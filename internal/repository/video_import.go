package repository

import (
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// VideoImportRepository handles the video extraction cache and async import jobs.
type VideoImportRepository struct {
	DB *gorm.DB
}

// NewVideoImportRepository creates a new VideoImportRepository.
func NewVideoImportRepository(db *gorm.DB) *VideoImportRepository {
	return &VideoImportRepository{DB: db}
}

// GetCacheByVideoKey returns the cached extraction for a video key.
func (r *VideoImportRepository) GetCacheByVideoKey(videoKey string) (*models.VideoExtractionCache, error) {
	var entry models.VideoExtractionCache
	if err := r.DB.Where("video_key = ?", videoKey).First(&entry).Error; err != nil {
		return nil, err
	}
	return &entry, nil
}

// UpsertCache inserts or updates a video extraction cache entry by video key.
func (r *VideoImportRepository) UpsertCache(entry *models.VideoExtractionCache) error {
	return r.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "video_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"recipe_data", "original_url", "platform", "fetched_at", "last_accessed_at", "prompt_version"}),
	}).Create(entry).Error
}

// IncrementCacheHit atomically bumps the hit count and last-accessed time.
func (r *VideoImportRepository) IncrementCacheHit(id uint) error {
	return r.DB.Model(&models.VideoExtractionCache{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"hit_count":        gorm.Expr("hit_count + 1"),
			"last_accessed_at": time.Now(),
		}).Error
}

// CreateImport inserts a new async video-import job.
func (r *VideoImportRepository) CreateImport(job *models.VideoImport) error {
	return r.DB.Create(job).Error
}

// GetImportByID returns a video-import job by ID.
func (r *VideoImportRepository) GetImportByID(id uint) (*models.VideoImport, error) {
	var job models.VideoImport
	if err := r.DB.First(&job, id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// UpdateImport persists changes to a video-import job.
func (r *VideoImportRepository) UpdateImport(job *models.VideoImport) error {
	return r.DB.Save(job).Error
}

// SumImportCostSince returns the total metered cost of video imports created at
// or after t — used to enforce the global daily budget kill switch.
func (r *VideoImportRepository) SumImportCostSince(t time.Time) (float64, error) {
	var total float64
	err := r.DB.Model(&models.VideoImport{}).
		Where("created_at >= ?", t).
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&total).Error
	return total, err
}

// Compile-time check that VideoImportRepository satisfies VideoImportRepo.
var _ VideoImportRepo = (*VideoImportRepository)(nil)
