package repository

import (
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// UserRepository is a repository for interacting with users.
type UserRepository struct {
	DB *gorm.DB
}

// NewUserRepository creates a new UserRepository.
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{DB: db}
}

// CreateUser creates a new user.
func (r *UserRepository) CreateUser(user *models.User) (*models.User, error) {
	tx := r.DB.Begin()
	if err := tx.Create(user).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		// Check for unique constraints
		if pgErr, ok := err.(*pq.Error); ok && pgErr.Code == "23505" {
			if strings.Contains(pgErr.Error(), "username") {
				return nil, errors.New("username already in use")
			} else if strings.Contains(pgErr.Error(), "email") {
				return nil, errors.New("email already in use")
			}
		}
		return nil, err
	}

	return user, nil
}

// GetUserByID retrieves a user by their ID.
func (r *UserRepository) GetUserByID(userID uint) (*models.User, error) {
	var user models.User
	if err := r.DB.Preload("Settings").
		Preload("Personalization").
		Preload("Subscription").
		Where("id = ?", userID).
		First(&user).Error; err != nil {
		return nil, err
	}

	return &user, nil
}

// GetUserAuthByUsername retrieves a user's authentication information by their username.
func (r *UserRepository) GetUserAuthByUsername(username string) (*models.User, error) {
	var user models.User
	if err := r.DB.Preload("Auth").Preload("Settings").Preload("Personalization").
		Where("username = ?", username).
		First(&user).Error; err != nil {
		return nil, err
	}

	return &user, nil
}

// UpdateUserFirstName updates a user's first name.
func (r *UserRepository) UpdateUserFirstName(userID uint, firstName string) error {
	err := r.DB.Model(&models.User{}).
		Where("id = ?", userID).
		Update("first_name", firstName).Error
	if err != nil {
		logger.Get().Error("failed to update user first name", zap.Uint("user_id", userID), zap.Error(err))
	}
	return err
}

// UpdateUserEmail updates a user's email address.
func (r *UserRepository) UpdateUserEmail(userID uint, email string) error {
	err := r.DB.Model(&models.User{}).
		Where("id = ?", userID).
		Update("Email", email).Error
	if err != nil {
		logger.Get().Error("failed to update user email", zap.Uint("user_id", userID), zap.Error(err))
	}

	return err
}

// UpdateUserSettingsKeepScreenAwake updates a user's KeepScreenAwake setting.
func (r *UserRepository) UpdateUserSettingsKeepScreenAwake(userID uint, keepScreenAwake bool) error {
	err := r.DB.Model(&models.UserSettings{}).
		Where("user_id = ?", userID).
		Update("KeepScreenAwake", keepScreenAwake).Error
	if err != nil {
		logger.Get().Error("failed to update user settings", zap.Uint("user_id", userID), zap.Error(err))
	}

	return err
}

// UpdatePersonalization updates a user's personalization settings.
func (r *UserRepository) UpdatePersonalization(userID uint, updatedPersonalization *models.Personalization) error {
	var existingPersonalization models.Personalization

	// First, find the existing record
	err := r.DB.Where("user_id = ?", userID).
		First(&existingPersonalization).Error
	if err != nil {
		logger.Get().Error("failed to retrieve existing personalization", zap.Uint("user_id", userID), zap.Error(err))
		return err
	}

	// Update fields
	existingPersonalization.UnitSystem = updatedPersonalization.UnitSystem
	existingPersonalization.Requirements = updatedPersonalization.Requirements
	existingPersonalization.UID = updatedPersonalization.UID

	// Perform the update
	err = r.DB.Save(&existingPersonalization).Error
	if err != nil {
		logger.Get().Error("failed to save updated personalization", zap.Uint("user_id", userID), zap.Error(err))
	}

	return err
}

// IncrementSubscriptionUsage atomically increments a usage counter on the
// subscription row for the given user. column must be one of:
// "allergen_analyses_used", "web_searches_used", "ai_generations_used".
func (r *UserRepository) IncrementSubscriptionUsage(userID uint, column string) error {
	result := r.DB.Model(&models.Subscription{}).
		Where("user_id = ?", userID).
		UpdateColumn(column, gorm.Expr(column+" + 1"))
	if result.Error != nil {
		logger.Get().Error("failed to increment subscription usage", zap.Uint("user_id", userID), zap.String("column", column), zap.Error(result.Error))
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("no subscription found for user")
	}
	return nil
}

// ResetSubscriptionUsage zeroes all usage counters and advances the monthly
// reset timestamp for the given user's subscription.
func (r *UserRepository) ResetSubscriptionUsage(userID uint, nextReset time.Time) error {
	result := r.DB.Model(&models.Subscription{}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{
			"allergen_analyses_used": 0,
			"web_searches_used":      0,
			"ai_generations_used":    0,
			"monthly_reset_at":       nextReset,
		})
	if result.Error != nil {
		logger.Get().Error("failed to reset subscription usage", zap.Uint("user_id", userID), zap.Error(result.Error))
		return result.Error
	}
	return nil
}

// UsernameExists checks if a username already exists.
func (r *UserRepository) UsernameExists(username string) (bool, error) {
	lowercaseUsername := strings.ToLower(username)
	var user models.User
	err := r.DB.Where("LOWER(username) = ?", lowercaseUsername).
		First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
