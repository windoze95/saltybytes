package repository

import (
	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
)

// AIUsageRepository persists AI call usage/cost records.
type AIUsageRepository struct {
	DB *gorm.DB
}

// NewAIUsageRepository creates a new AIUsageRepository.
func NewAIUsageRepository(db *gorm.DB) *AIUsageRepository {
	return &AIUsageRepository{DB: db}
}

// Insert records a single AI usage row.
func (r *AIUsageRepository) Insert(log *models.AIUsageLog) error {
	return r.DB.Create(log).Error
}
