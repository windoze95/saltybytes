package repository

import (
	"errors"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AllergenRepository is a repository for interacting with allergen analyses.
type AllergenRepository struct {
	DB *gorm.DB
}

// NewAllergenRepository creates a new AllergenRepository.
func NewAllergenRepository(db *gorm.DB) *AllergenRepository {
	return &AllergenRepository{DB: db}
}

// CreateAnalysis creates a new allergen analysis.
func (r *AllergenRepository) CreateAnalysis(analysis *models.AllergenAnalysis) error {
	if err := r.DB.Create(analysis).Error; err != nil {
		logger.Get().Error("failed to create allergen analysis", zap.Uint("recipe_id", analysis.RecipeID), zap.Error(err))
		return err
	}
	return nil
}

// GetAnalysisByRecipeID retrieves an allergen analysis by recipe ID.
func (r *AllergenRepository) GetAnalysisByRecipeID(recipeID uint) (*models.AllergenAnalysis, error) {
	var analysis models.AllergenAnalysis
	if err := r.DB.Where("recipe_id = ?", recipeID).First(&analysis).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFoundError{message: "allergen analysis not found"}
		}
		logger.Get().Error("failed to get allergen analysis", zap.Uint("recipe_id", recipeID), zap.Error(err))
		return nil, err
	}
	return &analysis, nil
}

// GetAnalysisByNodeID retrieves an allergen analysis by node ID.
func (r *AllergenRepository) GetAnalysisByNodeID(nodeID uint) (*models.AllergenAnalysis, error) {
	var analysis models.AllergenAnalysis
	if err := r.DB.Where("node_id = ?", nodeID).First(&analysis).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFoundError{message: "allergen analysis not found"}
		}
		logger.Get().Error("failed to get allergen analysis by node ID", zap.Uint("node_id", nodeID), zap.Error(err))
		return nil, err
	}
	return &analysis, nil
}

// UpdateAnalysis updates an existing allergen analysis.
func (r *AllergenRepository) UpdateAnalysis(analysis *models.AllergenAnalysis) error {
	if err := r.DB.Save(analysis).Error; err != nil {
		logger.Get().Error("failed to update allergen analysis", zap.Uint("id", analysis.ID), zap.Error(err))
		return err
	}
	return nil
}

// DeleteAnalysisByRecipeID deletes an allergen analysis by recipe ID.
func (r *AllergenRepository) DeleteAnalysisByRecipeID(recipeID uint) error {
	if err := r.DB.Where("recipe_id = ?", recipeID).Delete(&models.AllergenAnalysis{}).Error; err != nil {
		logger.Get().Error("failed to delete allergen analysis", zap.Uint("recipe_id", recipeID), zap.Error(err))
		return err
	}
	return nil
}
