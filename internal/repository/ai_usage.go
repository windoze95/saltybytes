package repository

import (
	"time"

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

// SumCostSince returns the total metered AI cost recorded at or after the
// given instant. Backs the app-wide daily AI budget kill switch.
func (r *AIUsageRepository) SumCostSince(since time.Time) (float64, error) {
	var total float64
	err := r.DB.Model(&models.AIUsageLog{}).
		Where("created_at >= ?", since).
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&total).Error
	return total, err
}
