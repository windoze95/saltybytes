package service

import (
	"context"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
)

// NormalizeService handles portion estimation via AI.
type NormalizeService struct {
	Cfg        *config.Config
	AIProvider ai.TextProvider
}

// NewNormalizeService creates a new NormalizeService.
func NewNormalizeService(cfg *config.Config, aiProvider ai.TextProvider) *NormalizeService {
	return &NormalizeService{
		Cfg:        cfg,
		AIProvider: aiProvider,
	}
}

// EstimatePortions estimates serving count if not provided.
func (s *NormalizeService) EstimatePortions(ctx context.Context, recipeDef *models.RecipeDef) (*ai.PortionEstimate, error) {
	return s.AIProvider.EstimatePortions(ctx, recipeDef)
}
