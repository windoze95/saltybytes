package testutil

import (
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// MockAllergenRepo mocks repository.AllergenRepo for testing.
type MockAllergenRepo struct {
	CreateAnalysisFunc           func(analysis *models.AllergenAnalysis) error
	GetAnalysisByRecipeIDFunc    func(recipeID uint) (*models.AllergenAnalysis, error)
	GetAnalysisByNodeIDFunc      func(nodeID uint) (*models.AllergenAnalysis, error)
	UpdateAnalysisFunc           func(analysis *models.AllergenAnalysis) error
	DeleteAnalysisByRecipeIDFunc func(recipeID uint) error
}

func (m *MockAllergenRepo) CreateAnalysis(analysis *models.AllergenAnalysis) error {
	if m.CreateAnalysisFunc != nil {
		return m.CreateAnalysisFunc(analysis)
	}
	return nil
}

func (m *MockAllergenRepo) GetAnalysisByRecipeID(recipeID uint) (*models.AllergenAnalysis, error) {
	if m.GetAnalysisByRecipeIDFunc != nil {
		return m.GetAnalysisByRecipeIDFunc(recipeID)
	}
	return nil, repository.NotFoundError{}
}

func (m *MockAllergenRepo) GetAnalysisByNodeID(nodeID uint) (*models.AllergenAnalysis, error) {
	if m.GetAnalysisByNodeIDFunc != nil {
		return m.GetAnalysisByNodeIDFunc(nodeID)
	}
	return nil, repository.NotFoundError{}
}

func (m *MockAllergenRepo) UpdateAnalysis(analysis *models.AllergenAnalysis) error {
	if m.UpdateAnalysisFunc != nil {
		return m.UpdateAnalysisFunc(analysis)
	}
	return nil
}

func (m *MockAllergenRepo) DeleteAnalysisByRecipeID(recipeID uint) error {
	if m.DeleteAnalysisByRecipeIDFunc != nil {
		return m.DeleteAnalysisByRecipeIDFunc(recipeID)
	}
	return nil
}

// Compile-time interface check.
var _ repository.AllergenRepo = (*MockAllergenRepo)(nil)
