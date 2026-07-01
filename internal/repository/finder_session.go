package repository

import (
	"context"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FinderSessionRepository persists saved Recipe Finder runs.
type FinderSessionRepository struct {
	DB *gorm.DB
}

// NewFinderSessionRepository creates a new FinderSessionRepository.
func NewFinderSessionRepository(db *gorm.DB) *FinderSessionRepository {
	return &FinderSessionRepository{DB: db}
}

// Create inserts a new finder session.
func (r *FinderSessionRepository) Create(ctx context.Context, session *models.FinderSession) error {
	if err := r.DB.WithContext(ctx).Create(session).Error; err != nil {
		logger.Get().Error("failed to create finder session", zap.Uint("user_id", session.UserID), zap.Error(err))
		return err
	}
	return nil
}

// ListByUser returns a page of a user's sessions, newest first, plus the total count.
func (r *FinderSessionRepository) ListByUser(ctx context.Context, userID uint, limit, offset int) ([]models.FinderSession, int64, error) {
	var (
		sessions []models.FinderSession
		total    int64
	)
	if err := r.DB.WithContext(ctx).Model(&models.FinderSession{}).
		Where("user_id = ?", userID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := r.DB.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&sessions).Error; err != nil {
		return nil, 0, err
	}
	return sessions, total, nil
}

// GetByID returns a session by ID (ownership is enforced by the caller).
func (r *FinderSessionRepository) GetByID(ctx context.Context, id uint) (*models.FinderSession, error) {
	var session models.FinderSession
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// Delete soft-deletes a session by ID.
func (r *FinderSessionRepository) Delete(ctx context.Context, id uint) error {
	if err := r.DB.WithContext(ctx).Delete(&models.FinderSession{}, id).Error; err != nil {
		logger.Get().Error("failed to delete finder session", zap.Uint("session_id", id), zap.Error(err))
		return err
	}
	return nil
}
