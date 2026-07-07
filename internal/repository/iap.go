package repository

import (
	"errors"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
)

// StoreSubscriptionRepository persists store-side subscription state.
type StoreSubscriptionRepository struct {
	DB *gorm.DB
}

// NewStoreSubscriptionRepository creates a new StoreSubscriptionRepository.
func NewStoreSubscriptionRepository(db *gorm.DB) *StoreSubscriptionRepository {
	return &StoreSubscriptionRepository{DB: db}
}

// GetByExternalKey returns the row for a store identity (apple original
// transaction ID / google purchase token), or nil when none exists.
func (r *StoreSubscriptionRepository) GetByExternalKey(key string) (*models.StoreSubscription, error) {
	var sub models.StoreSubscription
	if err := r.DB.Where("external_key = ?", key).First(&sub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

// ListByUser returns all store subscriptions bound to a user.
func (r *StoreSubscriptionRepository) ListByUser(userID uint) ([]models.StoreSubscription, error) {
	var subs []models.StoreSubscription
	if err := r.DB.Where("user_id = ?", userID).Find(&subs).Error; err != nil {
		return nil, err
	}
	return subs, nil
}

// Save creates or updates a row (by primary key).
func (r *StoreSubscriptionRepository) Save(sub *models.StoreSubscription) error {
	return r.DB.Save(sub).Error
}
