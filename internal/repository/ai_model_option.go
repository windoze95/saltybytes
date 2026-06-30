package repository

import (
	"errors"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AIModelOptionRepository persists the swappable light-tier model registry
// (ai_model_options) and the single active-selection row (ai_config).
type AIModelOptionRepository struct {
	DB *gorm.DB
}

// NewAIModelOptionRepository creates a new AIModelOptionRepository.
func NewAIModelOptionRepository(db *gorm.DB) *AIModelOptionRepository {
	return &AIModelOptionRepository{DB: db}
}

// ListOptions returns every registered model option, newest first.
func (r *AIModelOptionRepository) ListOptions() ([]models.AIModelOption, error) {
	var opts []models.AIModelOption
	if err := r.DB.Order("created_at DESC").Find(&opts).Error; err != nil {
		return nil, err
	}
	return opts, nil
}

// GetOption returns a single option by ID, or (nil, nil) if it does not exist.
func (r *AIModelOptionRepository) GetOption(id uint) (*models.AIModelOption, error) {
	var opt models.AIModelOption
	err := r.DB.First(&opt, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &opt, nil
}

// CreateOption inserts a new option.
func (r *AIModelOptionRepository) CreateOption(opt *models.AIModelOption) error {
	return r.DB.Create(opt).Error
}

// UpdateOption persists all fields of an existing option.
func (r *AIModelOptionRepository) UpdateOption(opt *models.AIModelOption) error {
	return r.DB.Save(opt).Error
}

// DeleteOption removes an option by ID.
func (r *AIModelOptionRepository) DeleteOption(id uint) error {
	return r.DB.Delete(&models.AIModelOption{}, id).Error
}

// GetConfig returns the single active-config row, or (nil, nil) if unset.
func (r *AIModelOptionRepository) GetConfig() (*models.AIConfig, error) {
	var cfg models.AIConfig
	err := r.DB.Order("id").First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// UpsertConfig writes the single active-config row (id=1), inserting or updating.
func (r *AIModelOptionRepository) UpsertConfig(cfg *models.AIConfig) error {
	cfg.ID = 1
	return r.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"active_provider", "active_model", "active_base_url", "updated_at"}),
	}).Create(cfg).Error
}
