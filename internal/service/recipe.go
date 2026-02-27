package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/s3"
)

// RecipeService is the business logic layer for recipe-related operations.
type RecipeService struct {
	Cfg           *config.Config
	Repo          repository.RecipeRepo
	TextProvider  ai.TextProvider
	ImageProvider ai.ImageProvider
}

// RecipeResponse is the response object for recipe-related operations.
// Field names match RecipeListItem / Flutter Recipe model.
type RecipeResponse struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	OwnerID         string             `json:"ownerId"`
	ImageURL        string             `json:"imageUrl"`
	Ingredients     models.Ingredients `json:"ingredients"`
	Instructions    []string           `json:"instructions"`
	Tags            []string           `json:"tags"`
	CookTimeMinutes int                `json:"cookTimeMinutes"`
	SourceURL       string             `json:"sourceUrl,omitempty"`
	CreatedAt       string             `json:"createdAt"`
	UpdatedAt       string             `json:"updatedAt"`
	// Additional detail fields
	ParentRecipeID *string `json:"parentRecipeId,omitempty"`
}

// HistoryResponse is the response object for recipe history-related operations.
type HistoryResponse struct {
	Entries []models.RecipeHistoryEntry `json:"entries"`
}

// RecipeListItem is a lightweight response object for recipe listing.
type RecipeListItem struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	OwnerID         string   `json:"ownerId"`
	ImageURL        string   `json:"imageUrl"`
	CookTimeMinutes int      `json:"cookTimeMinutes"`
	Tags            []string `json:"tags"`
	SourceURL       string   `json:"sourceUrl,omitempty"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

// NewRecipeService is the constructor function for initializing a new RecipeService
func NewRecipeService(cfg *config.Config, repo repository.RecipeRepo, textProvider ai.TextProvider, imageProvider ai.ImageProvider) *RecipeService {
	return &RecipeService{
		Cfg:           cfg,
		Repo:          repo,
		TextProvider:  textProvider,
		ImageProvider: imageProvider,
	}
}

// GetUserRecipes returns a paginated list of recipes for a user.
func (s *RecipeService) GetUserRecipes(userID uint, page, pageSize int) ([]RecipeListItem, int64, error) {
	recipes, total, err := s.Repo.GetUserRecipes(userID, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get user recipes: %w", err)
	}

	items := make([]RecipeListItem, len(recipes))
	for i, r := range recipes {
		tags := make([]string, 0, len(r.Hashtags))
		for _, t := range r.Hashtags {
			tags = append(tags, t.Hashtag)
		}

		items[i] = RecipeListItem{
			ID:              fmt.Sprintf("%d", r.ID),
			Title:           r.Title,
			OwnerID:         fmt.Sprintf("%d", r.CreatedByID),
			ImageURL:        r.ImageURL,
			CookTimeMinutes: r.CookTime,
			Tags:            tags,
			SourceURL:       r.SourceURL,
			CreatedAt:       r.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:       r.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	return items, total, nil
}

// GetRecipeByID fetches a recipe by its ID.
func (s *RecipeService) GetRecipeByID(recipeID uint) (*RecipeResponse, error) {
	// Fetch the recipe by its ID from the repository
	recipe, err := s.Repo.GetRecipeByID(recipeID)
	if err != nil {
		return nil, err
	}

	// Create a RecipeResponse from the Recipe
	recipeResponse := s.ToRecipeResponse(recipe)

	return recipeResponse, nil
}

// GetRecipeHistoryByID fetches a recipe history by its ID.
func (s *RecipeService) GetRecipeHistoryByID(historyID uint) (*HistoryResponse, error) {
	// Fetch the recipe by its ID from the repository
	history, err := s.Repo.GetHistoryByID(historyID)
	if err != nil {
		return nil, err
	}

	historyResponse := &HistoryResponse{Entries: history.Entries}

	return historyResponse, nil
}

// DeleteRecipe deletes a recipe by its ID.
func (s *RecipeService) DeleteRecipe(ctx context.Context, recipeID uint) error {
	// Delete the recipe from the database
	if err := s.Repo.DeleteRecipe(recipeID); err != nil {
		return fmt.Errorf("failed to delete recipe: %w", err)
	}

	// Delete the recipe image from S3
	s3Key := s3.GenerateS3Key(recipeID)
	if err := s3.DeleteRecipeImageFromS3(ctx, s.Cfg, s3Key); err != nil {
		return fmt.Errorf("failed to delete recipe image from S3: %w", err)
	}

	return nil
}

// populateRecipeCoreFields populates the recipe's core fields from an AI result.
func populateRecipeCoreFields(recipe *models.Recipe, result *ai.RecipeResult, historyEntry models.RecipeHistoryEntry) error {
	recipe.RecipeDef = recipeResultToRecipeDef(result)

	if recipe.History == nil {
		return errors.New("recipe history is nil")
	}

	recipe.History.Entries = append(recipe.History.Entries, historyEntry)

	return validateRecipeCoreFields(recipe)
}

// recipeResultToRecipeDef converts an ai.RecipeResult to a models.RecipeDef.
func recipeResultToRecipeDef(r *ai.RecipeResult) models.RecipeDef {
	ingredients := make(models.Ingredients, len(r.Ingredients))
	for i, ing := range r.Ingredients {
		ingredients[i] = models.Ingredient{
			Name:             ing.Name,
			Unit:             ing.Unit,
			Amount:           ing.Amount,
			OriginalText:     ing.OriginalText,
			NormalizedAmount: ing.NormalizedAmount,
			NormalizedUnit:   ing.NormalizedUnit,
			IsEstimated:      ing.IsEstimated,
		}
	}
	return models.RecipeDef{
		Title:             r.Title,
		Ingredients:       ingredients,
		Instructions:      r.Instructions,
		CookTime:          r.CookTime,
		ImagePrompt:       r.ImagePrompt,
		Hashtags:          r.Hashtags,
		LinkedSuggestions: r.LinkedSuggestions,
		Portions:          r.Portions,
		PortionSize:       r.PortionSize,
		SourceURL:         r.SourceURL,
	}
}

// validateRecipeFields validates that the Recipe's required fields are populated.
func validateRecipeCoreFields(recipe *models.Recipe) error {
	if recipe.Title == "" ||
		recipe.Ingredients == nil ||
		recipe.Instructions == nil ||
		recipe.ImagePrompt == "" {
		return errors.New("missing required fields in Recipe")
	}

	return nil
}

// uploadRecipeImage uploads the recipe image to S3 and returns the new image URL.
func uploadRecipeImage(ctx context.Context, recipeID uint, imageBytes []byte, cfg *config.Config) (string, error) {
	s3Key := s3.GenerateS3Key(recipeID)
	imageURL, err := s3.UploadRecipeImageToS3(ctx, cfg, imageBytes, s3Key)
	if err != nil {
		return "", errors.New("failed to upload image to S3: " + err.Error())
	}

	return imageURL, nil
}

// AssociateTagsWithRecipe checks if each hashtag exists as a Tag in the database.
// If it does, it uses the existing Tag's ID and Name.
func (s *RecipeService) AssociateTagsWithRecipe(recipe *models.Recipe, tags []string) error {
	var associatedTags []models.Tag

	for _, hashtag := range tags {
		cleanedHashtag := cleanHashtag(hashtag)

		// Search for the tag by the cleaned name
		existingTag, err := s.Repo.FindTagByName(cleanedHashtag)
		if err == nil {
			associatedTags = append(associatedTags, *existingTag)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			newTag := models.Tag{Hashtag: cleanedHashtag}
			if err := s.Repo.CreateTag(&newTag); err != nil {
				return fmt.Errorf("failed to create new tag: %v", err)
			}
			associatedTags = append(associatedTags, newTag)
		} else {
			return fmt.Errorf("database error while searching for tag: %v", err)
		}
	}

	if err := s.Repo.UpdateRecipeTagsAssociation(recipe.ID, associatedTags); err != nil {
		return fmt.Errorf("failed to update recipe with tags: %v", err)
	}
	// recipe.Hashtags = associatedTags

	return nil
}

// ToRecipeResponse converts a Recipe to a RecipeResponse.
// Field names match RecipeListItem / Flutter Recipe model.
func (s *RecipeService) ToRecipeResponse(r *models.Recipe) *RecipeResponse {
	tags := make([]string, 0, len(r.Hashtags))
	for _, t := range r.Hashtags {
		tags = append(tags, t.Hashtag)
	}

	var parentRecipeID *string
	if r.ForkedFromID != nil && *r.ForkedFromID != 0 {
		s := fmt.Sprintf("%d", *r.ForkedFromID)
		parentRecipeID = &s
	}

	resp := &RecipeResponse{
		ID:              fmt.Sprintf("%d", r.ID),
		Title:           r.Title,
		OwnerID:         fmt.Sprintf("%d", r.CreatedByID),
		ImageURL:        r.ImageURL,
		Ingredients:     r.Ingredients,
		Instructions:    r.Instructions,
		Tags:            tags,
		CookTimeMinutes: r.CookTime,
		SourceURL:       r.SourceURL,
		CreatedAt:       r.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       r.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		ParentRecipeID:  parentRecipeID,
	}

	return resp
}

// --- RecipeTree/RecipeNode service methods ---

// GetRecipeTree returns the tree structure for a recipe.
func (s *RecipeService) GetRecipeTree(recipeID uint) (*TreeResponse, error) {
	tree, err := s.Repo.GetTreeByRecipeID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe tree: %w", err)
	}

	treeWithNodes, err := s.Repo.GetTreeWithNodes(tree.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree nodes: %w", err)
	}

	return &TreeResponse{
		TreeID:     treeWithNodes.ID,
		RecipeID:   treeWithNodes.RecipeID,
		RootNodeID: treeWithNodes.RootNodeID,
		Nodes:      treeWithNodes.Nodes,
	}, nil
}

// GetActiveNode returns the active node for a recipe's tree.
func (s *RecipeService) GetActiveNode(recipeID uint) (*models.RecipeNode, error) {
	tree, err := s.Repo.GetTreeByRecipeID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe tree: %w", err)
	}

	node, err := s.Repo.GetActiveNode(tree.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get active node: %w", err)
	}

	return node, nil
}

// GetNodeHistory returns the chain of nodes from root to the specified node.
func (s *RecipeService) GetNodeHistory(nodeID uint) ([]models.RecipeNode, error) {
	ancestors, err := s.Repo.GetNodeAncestors(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get node history: %w", err)
	}
	return ancestors, nil
}

// SwitchToNode sets a node as active and updates the recipe's fields from the node's response.
func (s *RecipeService) SwitchToNode(recipeID uint, nodeID uint) error {
	node, err := s.Repo.GetNodeByID(nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	if node.Response == nil {
		return errors.New("cannot switch to node with no response")
	}

	if err := s.Repo.UpdateRecipeFromNode(recipeID, node); err != nil {
		return fmt.Errorf("failed to update recipe from node: %w", err)
	}

	return nil
}

// NodeHistoryToEntries converts a chain of tree nodes into RecipeHistoryEntry format
// for compatibility with ProcessExistingRecipeHistoryEntries. This bridges the tree
// structure with existing AI generation code.
func NodeHistoryToEntries(nodes []models.RecipeNode) []models.RecipeHistoryEntry {
	entries := make([]models.RecipeHistoryEntry, 0, len(nodes))
	for i, node := range nodes {
		entries = append(entries, models.RecipeHistoryEntry{
			Prompt:   node.Prompt,
			Response: node.Response,
			Summary:  node.Summary,
			Type:     node.Type,
			Order:    i,
		})
	}
	return entries
}

// TreeResponse is the response object for recipe tree operations.
type TreeResponse struct {
	TreeID     uint                `json:"tree_id"`
	RecipeID   uint                `json:"recipe_id"`
	RootNodeID *uint               `json:"root_node_id"`
	Nodes      []models.RecipeNode `json:"nodes"`
}

// cleanHashtag formats a hashtag string.
func cleanHashtag(hashtag string) string {
	// Convert to lowercase
	hashtag = strings.ToLower(hashtag)

	// Remove spaces
	hashtag = strings.ReplaceAll(hashtag, " ", "")

	// Remove '#' if present
	hashtag = strings.TrimPrefix(hashtag, "#")

	return hashtag
}
