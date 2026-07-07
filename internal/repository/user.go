package repository

import (
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

// mapUserUniqueViolation converts Postgres unique-constraint violations on
// the users table into the sentinel errors handlers match with errors.Is.
// Any other error is returned unchanged. The pgx driver (used by
// gorm.io/driver/postgres) surfaces these as *pgconn.PgError with SQLSTATE
// 23505; the violation fires on the INSERT itself, not on commit.
func mapUserUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		constraint := pgErr.ConstraintName
		if constraint == "" {
			constraint = pgErr.Message
		}
		if strings.Contains(constraint, "username") {
			return ErrUsernameTaken
		}
		if strings.Contains(constraint, "email") {
			return ErrEmailTaken
		}
	}
	return err
}

// CreateUser creates a new user.
func (r *UserRepository) CreateUser(user *models.User) (*models.User, error) {
	tx := r.DB.Begin()
	if err := tx.Create(user).Error; err != nil {
		tx.Rollback()
		return nil, mapUserUniqueViolation(err)
	}
	if err := tx.Commit().Error; err != nil {
		return nil, mapUserUniqueViolation(err)
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

// GetUserAuthByUsername retrieves a user's authentication information by
// their username. The match is case-insensitive: signup stores the username
// as typed (mobile keyboards autocapitalize), so an exact match would lock
// out anyone who types their name in a different case at login.
func (r *UserRepository) GetUserAuthByUsername(username string) (*models.User, error) {
	var user models.User
	if err := r.DB.Preload("Auth").Preload("Settings").Preload("Personalization").
		Where("LOWER(username) = LOWER(?)", username).
		First(&user).Error; err != nil {
		return nil, err
	}

	return &user, nil
}

// GetUserAuthByEmail retrieves a user's authentication information by their
// email address, case-insensitively.
func (r *UserRepository) GetUserAuthByEmail(email string) (*models.User, error) {
	var user models.User
	if err := r.DB.Preload("Auth").Preload("Settings").Preload("Personalization").
		Where("LOWER(email) = LOWER(?)", email).
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
		return mapUserUniqueViolation(err)
	}

	return nil
}

// SetEmailVerified stamps the user's email as verified now.
func (r *UserRepository) SetEmailVerified(userID uint) error {
	err := r.DB.Model(&models.User{}).
		Where("id = ?", userID).
		Update("email_verified_at", time.Now()).Error
	if err != nil {
		logger.Get().Error("failed to set email verified", zap.Uint("user_id", userID), zap.Error(err))
	}
	return err
}

// ClearUserEmail releases a user's email address (used when a stale
// unverified signup is squatting an address someone else wants to register
// with). The account keeps working via username login.
func (r *UserRepository) ClearUserEmail(userID uint) error {
	err := r.DB.Model(&models.User{}).
		Where("id = ?", userID).
		Update("email", nil).Error
	if err != nil {
		logger.Get().Error("failed to clear user email", zap.Uint("user_id", userID), zap.Error(err))
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

// DecrementSubscriptionUsage atomically decrements a usage counter on the
// subscription row, flooring at zero. Used to refund a counted action that
// later failed on our side (e.g. a video import that errored after acceptance).
func (r *UserRepository) DecrementSubscriptionUsage(userID uint, column string) error {
	result := r.DB.Model(&models.Subscription{}).
		Where("user_id = ?", userID).
		UpdateColumn(column, gorm.Expr("GREATEST("+column+" - 1, 0)"))
	if result.Error != nil {
		logger.Get().Error("failed to decrement subscription usage", zap.Uint("user_id", userID), zap.String("column", column), zap.Error(result.Error))
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
			"ai_imports_used":        0,
			"monthly_reset_at":       nextReset,
		})
	if result.Error != nil {
		logger.Get().Error("failed to reset subscription usage", zap.Uint("user_id", userID), zap.Error(result.Error))
		return result.Error
	}
	return nil
}

// LockUser administratively locks an account (runaway guard / suspected
// compromise): every authenticated request 403s until locked_at is cleared
// manually.
func (r *UserRepository) LockUser(userID uint, reason string) error {
	err := r.DB.Model(&models.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"locked_at":   time.Now(),
			"lock_reason": reason,
		}).Error
	if err != nil {
		logger.Get().Error("failed to lock user", zap.Uint("user_id", userID), zap.Error(err))
	}
	return err
}

// DeleteAbandonedUnverifiedUsers hard-deletes "empty husk" accounts: never
// email-verified, older than the cutoff, and with zero durable content (no
// recipes created or collected, no family owned). These are abandoned
// signups squatting usernames; hard deletion frees the username and email
// for reuse (the unique constraints see soft-deleted rows, so soft delete
// wouldn't). Accounts with any content are never touched — under the soft
// gate an unverified account can still be a real, active user.
func (r *UserRepository) DeleteAbandonedUnverifiedUsers(olderThan time.Time) (int64, error) {
	var deleted int64
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Raw(`
			SELECT id FROM users
			WHERE email_verified_at IS NULL
			  AND created_at < ?
			  AND NOT EXISTS (SELECT 1 FROM recipes rc WHERE rc.created_by_id = users.id)
			  AND NOT EXISTS (SELECT 1 FROM user_collected_recipes uc WHERE uc.user_id = users.id)
			  AND NOT EXISTS (SELECT 1 FROM families f WHERE f.owner_id = users.id)`,
			olderThan).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		// Child rows first (the association FKs restrict deletes), then the
		// user rows themselves — unscoped so the row is truly gone.
		for _, table := range []string{
			"email_verifications", "o_auth_auth_codes", "o_auth_tokens",
			"finder_sessions", "video_imports",
			"user_auths", "subscriptions", "user_settings", "personalizations",
		} {
			if err := tx.Exec(`DELETE FROM `+table+` WHERE user_id IN ?`, ids).Error; err != nil {
				return err
			}
		}
		res := tx.Exec(`DELETE FROM users WHERE id IN ?`, ids)
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		return nil
	})
	return deleted, err
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

// EmailExists checks if an email address is already registered, ignoring
// case. The DB unique constraint on email is case-sensitive, so this check
// is also what keeps Foo@x.com and foo@x.com from becoming two accounts.
func (r *UserRepository) EmailExists(email string) (bool, error) {
	var user models.User
	err := r.DB.Where("LOWER(email) = LOWER(?)", email).
		First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
