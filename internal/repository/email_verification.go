package repository

import (
	"errors"

	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EmailVerificationRepository persists pending signup verification codes.
type EmailVerificationRepository struct {
	DB *gorm.DB
}

// NewEmailVerificationRepository creates a new EmailVerificationRepository.
func NewEmailVerificationRepository(db *gorm.DB) *EmailVerificationRepository {
	return &EmailVerificationRepository{DB: db}
}

// Upsert writes the user's pending verification, replacing any existing row
// (one pending code per user).
func (r *EmailVerificationRepository) Upsert(v *models.EmailVerification) error {
	return r.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"code_hash", "expires_at", "attempts", "send_count", "last_sent_at", "updated_at",
		}),
	}).Create(v).Error
}

// GetByUserID returns the user's pending verification, or nil when none
// exists.
func (r *EmailVerificationRepository) GetByUserID(userID uint) (*models.EmailVerification, error) {
	var v models.EmailVerification
	if err := r.DB.Where("user_id = ?", userID).First(&v).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

// IncrementAttempts counts one wrong code entry.
func (r *EmailVerificationRepository) IncrementAttempts(userID uint) error {
	return r.DB.Model(&models.EmailVerification{}).
		Where("user_id = ?", userID).
		UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error
}

// DeleteByUserID removes the user's pending verification (after success, or
// when their email is released).
func (r *EmailVerificationRepository) DeleteByUserID(userID uint) error {
	return r.DB.Unscoped().
		Where("user_id = ?", userID).
		Delete(&models.EmailVerification{}).Error
}
