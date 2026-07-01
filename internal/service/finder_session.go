package service

import (
	"context"
	"errors"

	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// ErrFinderSessionNotOwned is returned when a user references a finder session
// that belongs to someone else.
var ErrFinderSessionNotOwned = errors.New("finder session not owned by user")

// FinderSessionService manages saved recipe-finder runs. History is ungated
// (no subscription check) — it just records what the finder already did.
type FinderSessionService struct {
	Repo repository.FinderSessionRepo
}

// NewFinderSessionService creates a new FinderSessionService.
func NewFinderSessionService(repo repository.FinderSessionRepo) *FinderSessionService {
	return &FinderSessionService{Repo: repo}
}

// Save persists a completed finder run.
func (s *FinderSessionService) Save(ctx context.Context, session *models.FinderSession) error {
	return s.Repo.Create(ctx, session)
}

// List returns a page of the user's sessions (newest first) and the total count.
func (s *FinderSessionService) List(ctx context.Context, userID uint, limit, offset int) ([]models.FinderSession, int64, error) {
	return s.Repo.ListByUser(ctx, userID, limit, offset)
}

// Get returns one session, enforcing ownership.
func (s *FinderSessionService) Get(ctx context.Context, userID, sessionID uint) (*models.FinderSession, error) {
	session, err := s.Repo.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.UserID != userID {
		return nil, ErrFinderSessionNotOwned
	}
	return session, nil
}

// Delete removes one session, enforcing ownership.
func (s *FinderSessionService) Delete(ctx context.Context, userID, sessionID uint) error {
	session, err := s.Repo.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.UserID != userID {
		return ErrFinderSessionNotOwned
	}
	return s.Repo.Delete(ctx, sessionID)
}
