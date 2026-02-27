package testutil

import (
	"context"
	"fmt"
	"sync"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"gorm.io/gorm"
)

// --- MockTextProvider ---

// MockTextProvider is a mock implementation of ai.TextProvider.
type MockTextProvider struct {
	GenerateRecipeFunc          func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error)
	RegenerateRecipeFunc        func(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error)
	ForkRecipeFunc              func(ctx context.Context, req ai.ForkRequest) (*ai.RecipeResult, error)
	AnalyzeAllergensFunc        func(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error)
	ClassifyVoiceIntentFunc     func(ctx context.Context, transcript string) (*ai.VoiceIntent, error)
	NormalizeMeasurementsFunc   func(ctx context.Context, ingredients []ai.IngredientInput) ([]ai.NormalizedIngredient, error)
	EstimatePortionsFunc        func(ctx context.Context, recipeDef interface{}) (*ai.PortionEstimate, error)
	ExtractRecipeFromTextFunc   func(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error)
	CookingQAFunc               func(ctx context.Context, question string, recipeContext string) (string, error)
	DietaryInterviewFunc        func(ctx context.Context, messages []ai.Message, memberName string) (string, error)
}

func (m *MockTextProvider) GenerateRecipe(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
	if m.GenerateRecipeFunc != nil {
		return m.GenerateRecipeFunc(ctx, req)
	}
	return nil, fmt.Errorf("GenerateRecipe not configured")
}

func (m *MockTextProvider) RegenerateRecipe(ctx context.Context, req ai.RegenerateRequest) (*ai.RecipeResult, error) {
	if m.RegenerateRecipeFunc != nil {
		return m.RegenerateRecipeFunc(ctx, req)
	}
	return nil, fmt.Errorf("RegenerateRecipe not configured")
}

func (m *MockTextProvider) ForkRecipe(ctx context.Context, req ai.ForkRequest) (*ai.RecipeResult, error) {
	if m.ForkRecipeFunc != nil {
		return m.ForkRecipeFunc(ctx, req)
	}
	return nil, fmt.Errorf("ForkRecipe not configured")
}

func (m *MockTextProvider) AnalyzeAllergens(ctx context.Context, req ai.AllergenRequest) (*ai.AllergenResult, error) {
	if m.AnalyzeAllergensFunc != nil {
		return m.AnalyzeAllergensFunc(ctx, req)
	}
	return nil, fmt.Errorf("AnalyzeAllergens not configured")
}

func (m *MockTextProvider) ClassifyVoiceIntent(ctx context.Context, transcript string) (*ai.VoiceIntent, error) {
	if m.ClassifyVoiceIntentFunc != nil {
		return m.ClassifyVoiceIntentFunc(ctx, transcript)
	}
	return nil, fmt.Errorf("ClassifyVoiceIntent not configured")
}

func (m *MockTextProvider) NormalizeMeasurements(ctx context.Context, ingredients []ai.IngredientInput) ([]ai.NormalizedIngredient, error) {
	if m.NormalizeMeasurementsFunc != nil {
		return m.NormalizeMeasurementsFunc(ctx, ingredients)
	}
	return nil, fmt.Errorf("NormalizeMeasurements not configured")
}

func (m *MockTextProvider) EstimatePortions(ctx context.Context, recipeDef interface{}) (*ai.PortionEstimate, error) {
	if m.EstimatePortionsFunc != nil {
		return m.EstimatePortionsFunc(ctx, recipeDef)
	}
	return nil, fmt.Errorf("EstimatePortions not configured")
}

func (m *MockTextProvider) ExtractRecipeFromText(ctx context.Context, text string, unitSystem string) (*ai.RecipeResult, error) {
	if m.ExtractRecipeFromTextFunc != nil {
		return m.ExtractRecipeFromTextFunc(ctx, text, unitSystem)
	}
	return nil, fmt.Errorf("ExtractRecipeFromText not configured")
}

func (m *MockTextProvider) CookingQA(ctx context.Context, question string, recipeContext string) (string, error) {
	if m.CookingQAFunc != nil {
		return m.CookingQAFunc(ctx, question, recipeContext)
	}
	return "", fmt.Errorf("CookingQA not configured")
}

func (m *MockTextProvider) DietaryInterview(ctx context.Context, messages []ai.Message, memberName string) (string, error) {
	if m.DietaryInterviewFunc != nil {
		return m.DietaryInterviewFunc(ctx, messages, memberName)
	}
	return "", fmt.Errorf("DietaryInterview not configured")
}

// --- MockVisionProvider ---

// MockVisionProvider is a mock implementation of ai.VisionProvider.
type MockVisionProvider struct {
	ExtractRecipeFromImageFunc func(ctx context.Context, imageData []byte, unitSystem string, requirements string) (*ai.RecipeResult, error)
}

func (m *MockVisionProvider) ExtractRecipeFromImage(ctx context.Context, imageData []byte, unitSystem string, requirements string) (*ai.RecipeResult, error) {
	if m.ExtractRecipeFromImageFunc != nil {
		return m.ExtractRecipeFromImageFunc(ctx, imageData, unitSystem, requirements)
	}
	return nil, fmt.Errorf("ExtractRecipeFromImage not configured")
}

// --- MockImageProvider ---

// MockImageProvider is a mock implementation of ai.ImageProvider.
type MockImageProvider struct {
	GenerateImageFunc func(ctx context.Context, prompt string) ([]byte, error)
}

func (m *MockImageProvider) GenerateImage(ctx context.Context, prompt string) ([]byte, error) {
	if m.GenerateImageFunc != nil {
		return m.GenerateImageFunc(ctx, prompt)
	}
	return nil, fmt.Errorf("GenerateImage not configured")
}

// --- MockSpeechProvider ---

// MockSpeechProvider is a mock implementation of ai.SpeechProvider.
type MockSpeechProvider struct {
	TranscribeAudioFunc func(ctx context.Context, audioData []byte) (string, error)
}

func (m *MockSpeechProvider) TranscribeAudio(ctx context.Context, audioData []byte) (string, error) {
	if m.TranscribeAudioFunc != nil {
		return m.TranscribeAudioFunc(ctx, audioData)
	}
	return "", fmt.Errorf("TranscribeAudio not configured")
}

// --- MockRecipeRepo ---

// MockRecipeRepo is an in-memory mock implementation of repository.RecipeRepo.
type MockRecipeRepo struct {
	mu       sync.Mutex
	Recipes  map[uint]*models.Recipe
	Tags     map[string]*models.Tag
	Trees    map[uint]*models.RecipeTree
	Nodes    map[uint]*models.RecipeNode
	NextID   uint
	NextTagID uint
	NextTreeID uint
	NextNodeID uint

	// Error overrides: set these to force specific methods to return errors.
	CreateRecipeErr              error
	GetRecipeByIDErr             error
	DeleteRecipeErr              error
	UpdateRecipeTitleErr         error
	UpdateRecipeImageURLErr      error
	UpdateRecipeDefErr           error
	CreateRecipeTreeErr          error
	AddNodeToTreeErr             error
}

// NewMockRecipeRepo creates a new MockRecipeRepo with initialized maps.
func NewMockRecipeRepo() *MockRecipeRepo {
	return &MockRecipeRepo{
		Recipes:    make(map[uint]*models.Recipe),
		Tags:       make(map[string]*models.Tag),
		Trees:      make(map[uint]*models.RecipeTree),
		Nodes:      make(map[uint]*models.RecipeNode),
		NextID:     1,
		NextTagID:  1,
		NextTreeID: 1,
		NextNodeID: 1,
	}
}

func (m *MockRecipeRepo) GetUserRecipes(userID uint, page, pageSize int) ([]models.Recipe, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var recipes []models.Recipe
	for _, r := range m.Recipes {
		if r.CreatedByID == userID {
			recipes = append(recipes, *r)
		}
	}
	total := int64(len(recipes))

	start := (page - 1) * pageSize
	if start >= len(recipes) {
		return []models.Recipe{}, total, nil
	}
	end := start + pageSize
	if end > len(recipes) {
		end = len(recipes)
	}
	return recipes[start:end], total, nil
}

func (m *MockRecipeRepo) GetRecipeByID(recipeID uint) (*models.Recipe, error) {
	if m.GetRecipeByIDErr != nil {
		return nil, m.GetRecipeByIDErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.Recipes[recipeID]
	if !ok {
		return nil, repository.NotFoundError{}
	}
	return r, nil
}

func (m *MockRecipeRepo) GetHistoryByID(historyID uint) (*models.RecipeHistory, error) {
	return &models.RecipeHistory{}, nil
}

func (m *MockRecipeRepo) GetRecipeHistoryEntriesAfterID(historyID uint, afterID uint) ([]models.RecipeHistoryEntry, error) {
	return nil, nil
}

func (m *MockRecipeRepo) CreateRecipe(recipe *models.Recipe) error {
	if m.CreateRecipeErr != nil {
		return m.CreateRecipeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	recipe.ID = m.NextID
	m.NextID++
	m.Recipes[recipe.ID] = recipe
	return nil
}

func (m *MockRecipeRepo) DeleteRecipe(recipeID uint) error {
	if m.DeleteRecipeErr != nil {
		return m.DeleteRecipeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.Recipes, recipeID)
	return nil
}

func (m *MockRecipeRepo) UpdateRecipeTitle(recipe *models.Recipe, title string) error {
	if m.UpdateRecipeTitleErr != nil {
		return m.UpdateRecipeTitleErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.Recipes[recipe.ID]; ok {
		r.Title = title
	}
	return nil
}

func (m *MockRecipeRepo) UpdateRecipeImageURL(recipeID uint, imageURL string) error {
	if m.UpdateRecipeImageURLErr != nil {
		return m.UpdateRecipeImageURLErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.Recipes[recipeID]; ok {
		r.ImageURL = imageURL
	}
	return nil
}

func (m *MockRecipeRepo) UpdateRecipeDef(recipe *models.Recipe, newRecipeHistoryEntry models.RecipeHistoryEntry) error {
	if m.UpdateRecipeDefErr != nil {
		return m.UpdateRecipeDefErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.Recipes[recipe.ID]; ok {
		r.RecipeDef = recipe.RecipeDef
	}
	return nil
}

func (m *MockRecipeRepo) UpdateRecipeWithHistoryEntry(recipeID uint, newActiveEntryID uint, updatedResponse models.RecipeDef) error {
	return nil
}

func (m *MockRecipeRepo) FindTagByName(tagName string) (*models.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tag, ok := m.Tags[tagName]; ok {
		return tag, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *MockRecipeRepo) CreateTag(tag *models.Tag) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tag.ID = m.NextTagID
	m.NextTagID++
	m.Tags[tag.Hashtag] = tag
	return nil
}

func (m *MockRecipeRepo) UpdateRecipeTagsAssociation(recipeID uint, newTags []models.Tag) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.Recipes[recipeID]; ok {
		tags := make([]*models.Tag, len(newTags))
		for i := range newTags {
			tags[i] = &newTags[i]
		}
		r.Hashtags = tags
	}
	return nil
}

func (m *MockRecipeRepo) CreateRecipeTree(recipeID uint, rootNode *models.RecipeNode) (*models.RecipeTree, error) {
	if m.CreateRecipeTreeErr != nil {
		return nil, m.CreateRecipeTreeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	tree := &models.RecipeTree{
		RecipeID: recipeID,
	}
	tree.ID = m.NextTreeID
	m.NextTreeID++

	rootNode.ID = m.NextNodeID
	m.NextNodeID++
	rootNode.TreeID = tree.ID

	tree.RootNodeID = &rootNode.ID
	m.Trees[tree.ID] = tree
	m.Nodes[rootNode.ID] = rootNode

	return tree, nil
}

func (m *MockRecipeRepo) GetTreeByRecipeID(recipeID uint) (*models.RecipeTree, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.Trees {
		if t.RecipeID == recipeID {
			return t, nil
		}
	}
	return nil, repository.NotFoundError{}
}

func (m *MockRecipeRepo) GetTreeWithNodes(treeID uint) (*models.RecipeTree, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tree, ok := m.Trees[treeID]
	if !ok {
		return nil, repository.NotFoundError{}
	}
	var nodes []models.RecipeNode
	for _, n := range m.Nodes {
		if n.TreeID == treeID {
			nodes = append(nodes, *n)
		}
	}
	tree.Nodes = nodes
	return tree, nil
}

func (m *MockRecipeRepo) GetActiveNode(treeID uint) (*models.RecipeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, n := range m.Nodes {
		if n.TreeID == treeID && n.IsActive {
			return n, nil
		}
	}
	return nil, repository.NotFoundError{}
}

func (m *MockRecipeRepo) GetNodeByID(nodeID uint) (*models.RecipeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.Nodes[nodeID]
	if !ok {
		return nil, repository.NotFoundError{}
	}
	return n, nil
}

func (m *MockRecipeRepo) GetNodeChildren(nodeID uint) ([]models.RecipeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var children []models.RecipeNode
	for _, n := range m.Nodes {
		if n.ParentID != nil && *n.ParentID == nodeID {
			children = append(children, *n)
		}
	}
	return children, nil
}

func (m *MockRecipeRepo) GetNodeAncestors(nodeID uint) ([]models.RecipeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var ancestors []models.RecipeNode
	currentID := nodeID
	for {
		n, ok := m.Nodes[currentID]
		if !ok {
			return nil, fmt.Errorf("node %d not found", currentID)
		}
		ancestors = append([]models.RecipeNode{*n}, ancestors...)
		if n.ParentID == nil {
			break
		}
		currentID = *n.ParentID
	}
	return ancestors, nil
}

func (m *MockRecipeRepo) AddNodeToTree(node *models.RecipeNode, setActive bool) error {
	if m.AddNodeToTreeErr != nil {
		return m.AddNodeToTreeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	node.ID = m.NextNodeID
	m.NextNodeID++
	if setActive {
		for _, n := range m.Nodes {
			if n.TreeID == node.TreeID {
				n.IsActive = false
			}
		}
		node.IsActive = true
	}
	m.Nodes[node.ID] = node
	return nil
}

func (m *MockRecipeRepo) SetActiveNode(treeID uint, nodeID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, n := range m.Nodes {
		if n.TreeID == treeID {
			n.IsActive = n.ID == nodeID
		}
	}
	return nil
}

func (m *MockRecipeRepo) UpdateRecipeFromNode(recipeID uint, node *models.RecipeNode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.Recipes[recipeID]; ok && node.Response != nil {
		r.RecipeDef = *node.Response
	}
	return nil
}

// --- MockUserRepo ---

// MockUserRepo is an in-memory mock implementation of repository.UserRepo.
type MockUserRepo struct {
	mu     sync.Mutex
	Users  map[uint]*models.User
	NextID uint

	CreateUserErr error
}

// NewMockUserRepo creates a new MockUserRepo with initialized maps.
func NewMockUserRepo() *MockUserRepo {
	return &MockUserRepo{
		Users:  make(map[uint]*models.User),
		NextID: 1,
	}
}

func (m *MockUserRepo) CreateUser(user *models.User) (*models.User, error) {
	if m.CreateUserErr != nil {
		return nil, m.CreateUserErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	user.ID = m.NextID
	m.NextID++
	m.Users[user.ID] = user
	return user, nil
}

func (m *MockUserRepo) GetUserByID(userID uint) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.Users[userID]
	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	return u, nil
}

func (m *MockUserRepo) GetUserAuthByUsername(username string) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, u := range m.Users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (m *MockUserRepo) UpdateUserFirstName(userID uint, firstName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, ok := m.Users[userID]; ok {
		u.FirstName = firstName
	}
	return nil
}

func (m *MockUserRepo) UpdateUserEmail(userID uint, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, ok := m.Users[userID]; ok {
		u.Email = email
	}
	return nil
}

func (m *MockUserRepo) UpdateUserSettingsKeepScreenAwake(userID uint, keepScreenAwake bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, ok := m.Users[userID]; ok && u.Settings != nil {
		u.Settings.KeepScreenAwake = keepScreenAwake
	}
	return nil
}

func (m *MockUserRepo) UpdatePersonalization(userID uint, updatedPersonalization *models.Personalization) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, ok := m.Users[userID]; ok {
		u.Personalization = updatedPersonalization
	}
	return nil
}

func (m *MockUserRepo) UsernameExists(username string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, u := range m.Users {
		if u.Username == username {
			return true, nil
		}
	}
	return false, nil
}

// Compile-time interface checks.
var _ ai.TextProvider = (*MockTextProvider)(nil)
var _ ai.VisionProvider = (*MockVisionProvider)(nil)
var _ ai.ImageProvider = (*MockImageProvider)(nil)
var _ ai.SpeechProvider = (*MockSpeechProvider)(nil)
var _ repository.RecipeRepo = (*MockRecipeRepo)(nil)
var _ repository.UserRepo = (*MockUserRepo)(nil)
