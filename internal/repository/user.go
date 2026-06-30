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
				return nil, ErrUsernameTaken
			} else if strings.Contains(pgErr.Error(), "email") {
				return nil, ErrEmailTaken
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

// GetUserWithAuthByID retrieves a user with their auth record preloaded.
// Used by the refresh-token flow to check the current token version.
func (r *UserRepository) GetUserWithAuthByID(userID uint) (*models.User, error) {
	var user models.User
	if err := r.DB.Preload("Auth").
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

// UpdatePersonalization partially updates a user's personalization settings.
// Only non-nil fields in the update are written; nil fields keep their
// current values.
func (r *UserRepository) UpdatePersonalization(userID uint, update *models.PersonalizationUpdate) error {
	var existingPersonalization models.Personalization

	// First, find the existing record
	err := r.DB.Where("user_id = ?", userID).
		First(&existingPersonalization).Error
	if err != nil {
		logger.Get().Error("failed to retrieve existing personalization", zap.Uint("user_id", userID), zap.Error(err))
		return err
	}

	// Apply only the fields present in the update
	if update.UnitSystem != nil {
		existingPersonalization.UnitSystem = *update.UnitSystem
	}
	if update.Requirements != nil {
		existingPersonalization.Requirements = *update.Requirements
	}
	if update.CookingContext != nil {
		existingPersonalization.CookingContext = *update.CookingContext
	}
	if update.UID != nil {
		existingPersonalization.UID = *update.UID
	}

	// Perform the update
	err = r.DB.Save(&existingPersonalization).Error
	if err != nil {
		logger.Get().Error("failed to save updated personalization", zap.Uint("user_id", userID), zap.Error(err))
	}

	return err
}

// IncrementTokenVersion atomically increments a user's refresh-token version,
// revoking all outstanding refresh tokens. UpdateColumn skips GORM hooks so
// the UserAuth BeforeUpdate AuthType validation does not apply.
func (r *UserRepository) IncrementTokenVersion(userID uint) error {
	result := r.DB.Model(&models.UserAuth{}).
		Where("user_id = ?", userID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1"))
	if result.Error != nil {
		logger.Get().Error("failed to increment token version", zap.Uint("user_id", userID), zap.Error(result.Error))
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("no auth record found for user")
	}
	return nil
}

// CreateSubscription creates a subscription row for a user. Used to backfill
// users that predate subscription rows being created at signup.
func (r *UserRepository) CreateSubscription(sub *models.Subscription) error {
	if err := r.DB.Create(sub).Error; err != nil {
		logger.Get().Error("failed to create subscription", zap.Uint("user_id", sub.UserID), zap.Error(err))
		return err
	}
	return nil
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
			"video_imports_used":     0,
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
