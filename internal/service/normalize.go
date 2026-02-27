package service

import (
	"context"
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
)

// NormalizeService handles measurement normalization via AI.
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

// NormalizeMeasurements normalizes vague or non-standard measurements in ingredients.
func (s *NormalizeService) NormalizeMeasurements(ctx context.Context, ingredients []models.Ingredient) ([]models.Ingredient, error) {
	inputs := make([]ai.IngredientInput, len(ingredients))
	for i, ing := range ingredients {
		inputs[i] = ai.IngredientInput{
			Name:   ing.Name,
			Unit:   ing.Unit,
			Amount: ing.Amount,
		}
	}

	normalized, err := s.AIProvider.NormalizeMeasurements(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize measurements: %w", err)
	}

	if len(normalized) != len(ingredients) {
		return nil, fmt.Errorf("normalization returned %d results for %d ingredients", len(normalized), len(ingredients))
	}

	result := make([]models.Ingredient, len(ingredients))
	for i, ing := range ingredients {
		result[i] = ing
		result[i].OriginalText = fmt.Sprintf("%g %s %s", ing.Amount, ing.Unit, ing.Name)
		result[i].NormalizedAmount = normalized[i].NormalizedAmount
		result[i].NormalizedUnit = normalized[i].NormalizedUnit
		result[i].IsEstimated = normalized[i].IsEstimated
	}

	return result, nil
}

// EstimatePortions estimates serving count if not provided.
func (s *NormalizeService) EstimatePortions(ctx context.Context, recipeDef *models.RecipeDef) (*ai.PortionEstimate, error) {
	return s.AIProvider.EstimatePortions(ctx, recipeDef)
}
