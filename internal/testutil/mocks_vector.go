package testutil

import (
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// --- MockVectorRepo ---

// MockVectorRepo is a mock implementation of repository.VectorRepo.
// Set the Func fields to control behavior; unset funcs return zero values
// (or an error where a result is required).
type MockVectorRepo struct {
	FindSimilarFunc                  func(embeddingLiteral string, excludeRecipeID uint, limit int) ([]models.Recipe, error)
	GetRecipeEmbeddingFunc           func(recipeID uint) (*string, error)
	UpdateEmbeddingFunc              func(recipeID uint, embedding []float32) error
	SearchUserRecipesByEmbeddingFunc func(userID uint, embeddingLiteral string, limit int) ([]models.Recipe, error)
	SearchUserRecipesByTitleFunc     func(userID uint, query string, onlyMissingEmbedding bool, limit int) ([]models.Recipe, error)

	// Call records for assertions.
	FindSimilarCalls                  []MockFindSimilarCall
	UpdateEmbeddingCalls              []uint
	GetRecipeEmbeddingCalls           []uint
	SearchUserRecipesByEmbeddingCalls []uint
	SearchUserRecipesByTitleCalls     []MockTitleSearchCall
}

// MockFindSimilarCall records the arguments of a FindSimilar invocation.
type MockFindSimilarCall struct {
	EmbeddingLiteral string
	ExcludeRecipeID  uint
	Limit            int
}

// MockTitleSearchCall records the arguments of a SearchUserRecipesByTitle invocation.
type MockTitleSearchCall struct {
	UserID               uint
	Query                string
	OnlyMissingEmbedding bool
	Limit                int
}

func (m *MockVectorRepo) FindSimilar(embeddingLiteral string, excludeRecipeID uint, limit int) ([]models.Recipe, error) {
	m.FindSimilarCalls = append(m.FindSimilarCalls, MockFindSimilarCall{
		EmbeddingLiteral: embeddingLiteral,
		ExcludeRecipeID:  excludeRecipeID,
		Limit:            limit,
	})
	if m.FindSimilarFunc != nil {
		return m.FindSimilarFunc(embeddingLiteral, excludeRecipeID, limit)
	}
	return []models.Recipe{}, nil
}

func (m *MockVectorRepo) GetRecipeEmbedding(recipeID uint) (*string, error) {
	m.GetRecipeEmbeddingCalls = append(m.GetRecipeEmbeddingCalls, recipeID)
	if m.GetRecipeEmbeddingFunc != nil {
		return m.GetRecipeEmbeddingFunc(recipeID)
	}
	return nil, fmt.Errorf("GetRecipeEmbedding not configured")
}

func (m *MockVectorRepo) UpdateEmbedding(recipeID uint, embedding []float32) error {
	m.UpdateEmbeddingCalls = append(m.UpdateEmbeddingCalls, recipeID)
	if m.UpdateEmbeddingFunc != nil {
		return m.UpdateEmbeddingFunc(recipeID, embedding)
	}
	return nil
}

func (m *MockVectorRepo) SearchUserRecipesByEmbedding(userID uint, embeddingLiteral string, limit int) ([]models.Recipe, error) {
	m.SearchUserRecipesByEmbeddingCalls = append(m.SearchUserRecipesByEmbeddingCalls, userID)
	if m.SearchUserRecipesByEmbeddingFunc != nil {
		return m.SearchUserRecipesByEmbeddingFunc(userID, embeddingLiteral, limit)
	}
	return []models.Recipe{}, nil
}

func (m *MockVectorRepo) SearchUserRecipesByTitle(userID uint, query string, onlyMissingEmbedding bool, limit int) ([]models.Recipe, error) {
	m.SearchUserRecipesByTitleCalls = append(m.SearchUserRecipesByTitleCalls, MockTitleSearchCall{
		UserID:               userID,
		Query:                query,
		OnlyMissingEmbedding: onlyMissingEmbedding,
		Limit:                limit,
	})
	if m.SearchUserRecipesByTitleFunc != nil {
		return m.SearchUserRecipesByTitleFunc(userID, query, onlyMissingEmbedding, limit)
	}
	return []models.Recipe{}, nil
}

// Compile-time interface check.
var _ repository.VectorRepo = (*MockVectorRepo)(nil)
