package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/s3"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// RecipeService is the business logic layer for recipe-related operations.
type RecipeService struct {
	Cfg           *config.Config
	Repo          repository.RecipeRepo
	TextProvider  ai.TextProvider
	ImageProvider ai.ImageProvider
	// Optional: set these to enable embedding generation on recipe
	// create/update and semantic search over a user's recipes.
	EmbedProvider ai.EmbeddingProvider
	VectorRepo    repository.VectorRepo
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
	UnitSystem      string  `json:"unitSystem"`
	Status          string  `json:"status"`
	// Additional detail fields
	ParentRecipeID *string `json:"parentRecipeId,omitempty"`
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
	Status          string   `json:"status"`
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

// embeddingText builds the text used to embed a recipe: its title followed by
// its ingredient names.
func embeddingText(recipeDef *models.RecipeDef) string {
	text := recipeDef.Title
	for _, ing := range recipeDef.Ingredients {
		text += " " + ing.Name
	}
	return text
}

// generateAndStoreRecipeEmbedding creates a vector embedding for a recipe and
// persists it. This is best-effort: failures are logged but do not block
// recipe operations.
func generateAndStoreRecipeEmbedding(ctx context.Context, embedProvider ai.EmbeddingProvider, vectorRepo repository.VectorRepo, recipeID uint, recipeDef *models.RecipeDef) {
	if embedProvider == nil || vectorRepo == nil {
		return
	}

	embedding, err := embedProvider.GenerateEmbedding(ctx, embeddingText(recipeDef))
	if err != nil {
		logger.Get().Warn("failed to generate recipe embedding", zap.Uint("recipe_id", recipeID), zap.Error(err))
		return
	}

	if err := vectorRepo.UpdateEmbedding(recipeID, embedding); err != nil {
		logger.Get().Warn("failed to store recipe embedding", zap.Uint("recipe_id", recipeID), zap.Error(err))
	}
}

// generateAndStoreEmbedding creates a vector embedding for a recipe and persists it.
// This is best-effort: failures are logged but do not block recipe operations.
func (s *RecipeService) generateAndStoreEmbedding(ctx context.Context, recipeID uint, recipeDef *models.RecipeDef) {
	generateAndStoreRecipeEmbedding(ctx, s.EmbedProvider, s.VectorRepo, recipeID, recipeDef)
}

// GetUserRecipes returns a paginated list of recipes for a user.
func (s *RecipeService) GetUserRecipes(userID uint, page, pageSize int) ([]RecipeListItem, int64, error) {
	recipes, total, err := s.Repo.GetUserRecipes(userID, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get user recipes: %w", err)
	}

	return s.ToRecipeListItems(recipes), total, nil
}

// ToRecipeListItem converts a Recipe to the lightweight RecipeListItem DTO.
func (s *RecipeService) ToRecipeListItem(r *models.Recipe) RecipeListItem {
	effectiveDef := effectiveRecipeDef(r)

	tags := make([]string, 0, len(r.Hashtags))
	for _, t := range r.Hashtags {
		tags = append(tags, t.Hashtag)
	}

	return RecipeListItem{
		ID:              fmt.Sprintf("%d", r.ID),
		Title:           effectiveDef.Title,
		OwnerID:         fmt.Sprintf("%d", r.CreatedByID),
		ImageURL:        r.ImageURL,
		CookTimeMinutes: effectiveDef.CookTime,
		Tags:            tags,
		SourceURL:       effectiveDef.SourceURL,
		Status:          r.Status,
		CreatedAt:       r.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       r.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// ToRecipeListItems converts a slice of Recipes to RecipeListItem DTOs.
// Always returns a non-nil slice so JSON serializes as [] rather than null.
func (s *RecipeService) ToRecipeListItems(recipes []models.Recipe) []RecipeListItem {
	items := make([]RecipeListItem, len(recipes))
	for i := range recipes {
		items[i] = s.ToRecipeListItem(&recipes[i])
	}
	return items
}

// userSearchCandidateCap bounds how many candidates each search strategy
// (vector, ILIKE) contributes before merging and paginating.
const userSearchCandidateCap = 100

// SearchUserRecipes performs a semantic search over the user's own recipes,
// merged with an ILIKE title fallback for recipes lacking embeddings. Results
// are deduped by ID (vector-rank first) and paginated in memory. If embedding
// generation fails, it falls back to a pure ILIKE title search.
func (s *RecipeService) SearchUserRecipes(ctx context.Context, userID uint, query string, page, pageSize int) ([]RecipeListItem, int64, error) {
	if s.VectorRepo == nil {
		return nil, 0, errors.New("vector repository not configured")
	}

	var vectorHits []models.Recipe
	vectorOK := false
	if s.EmbedProvider != nil {
		embedding, err := s.EmbedProvider.GenerateEmbedding(ctx, query)
		if err != nil {
			logger.Get().Warn("failed to embed search query, falling back to title search",
				zap.Uint("user_id", userID), zap.Error(err))
		} else {
			hits, searchErr := s.VectorRepo.SearchUserRecipesByEmbedding(userID, repository.PgvectorLiteral(embedding), userSearchCandidateCap)
			if searchErr != nil {
				logger.Get().Warn("vector search failed, falling back to title search",
					zap.Uint("user_id", userID), zap.Error(searchErr))
			} else {
				vectorHits = hits
				vectorOK = true
			}
		}
	}

	// When the vector search succeeded, the ILIKE pass only needs to cover
	// recipes lacking embeddings; otherwise it is the sole search strategy.
	titleHits, err := s.VectorRepo.SearchUserRecipesByTitle(userID, query, vectorOK, userSearchCandidateCap)
	if err != nil {
		if !vectorOK {
			return nil, 0, fmt.Errorf("failed to search user recipes: %w", err)
		}
		logger.Get().Warn("title fallback search failed", zap.Uint("user_id", userID), zap.Error(err))
	}

	// Dedupe by ID, preserving vector-rank-first order.
	seen := make(map[uint]bool, len(vectorHits)+len(titleHits))
	merged := make([]models.Recipe, 0, len(vectorHits)+len(titleHits))
	for _, r := range vectorHits {
		if !seen[r.ID] {
			seen[r.ID] = true
			merged = append(merged, r)
		}
	}
	for _, r := range titleHits {
		if !seen[r.ID] {
			seen[r.ID] = true
			merged = append(merged, r)
		}
	}

	total := int64(len(merged))

	// Paginate the merged result.
	start := (page - 1) * pageSize
	if start >= len(merged) {
		return []RecipeListItem{}, total, nil
	}
	end := start + pageSize
	if end > len(merged) {
		end = len(merged)
	}

	return s.ToRecipeListItems(merged[start:end]), total, nil
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

// recipeResultToRecipeDef converts an ai.RecipeResult to a models.RecipeDef.
func recipeResultToRecipeDef(r *ai.RecipeResult) models.RecipeDef {
	ingredients := make(models.Ingredients, len(r.Ingredients))
	for i, ing := range r.Ingredients {
		ingredients[i] = models.Ingredient{
			Name:         ing.Name,
			Unit:         ing.Unit,
			Amount:       ing.Amount,
			MetricUnit:   ing.MetricUnit,
			MetricAmount: ing.MetricAmount,
			OriginalText: ing.OriginalText,
		}
	}
	return models.RecipeDef{
		Title:             r.Title,
		Ingredients:       ingredients,
		Instructions:      r.Instructions,
		CookTime:          r.CookTime,
		ImagePrompt:       r.ImagePrompt,
		LinkedSuggestions: r.LinkedSuggestions,
		Portions:          r.Portions,
		PortionSize:       r.PortionSize,
		SourceURL:         r.SourceURL,
		UnitSystem:        r.UnitSystem,
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
// When the recipe has not diverged and references a canonical, the canonical's
// RecipeData is used instead of the recipe's own fields.
func (s *RecipeService) ToRecipeResponse(r *models.Recipe) *RecipeResponse {
	effectiveDef := effectiveRecipeDef(r)

	tags := make([]string, 0, len(r.Hashtags))
	for _, t := range r.Hashtags {
		tags = append(tags, t.Hashtag)
	}

	var parentRecipeID *string
	if r.ForkedFromID != nil && *r.ForkedFromID != 0 {
		s := fmt.Sprintf("%d", *r.ForkedFromID)
		parentRecipeID = &s
	}

	unitSystem := effectiveDef.UnitSystem
	if unitSystem == "" {
		unitSystem = "us_customary"
	}

	status := r.Status
	if status == "" {
		status = "ready"
	}

	resp := &RecipeResponse{
		ID:              fmt.Sprintf("%d", r.ID),
		Title:           effectiveDef.Title,
		OwnerID:         fmt.Sprintf("%d", r.CreatedByID),
		ImageURL:        r.ImageURL,
		Ingredients:     effectiveDef.Ingredients,
		Instructions:    effectiveDef.Instructions,
		Tags:            tags,
		CookTimeMinutes: effectiveDef.CookTime,
		SourceURL:       effectiveDef.SourceURL,
		UnitSystem:      unitSystem,
		Status:          status,
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

	if treeWithNodes.Nodes == nil {
		treeWithNodes.Nodes = []models.RecipeNode{}
	}

	return &TreeResponse{
		TreeID:       treeWithNodes.ID,
		RecipeID:     treeWithNodes.RecipeID,
		RootNodeID:   treeWithNodes.RootNodeID,
		ActiveNodeID: activeNodeID(treeWithNodes.Nodes),
		Nodes:        treeWithNodes.Nodes,
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

// TreeResponse is the response object for recipe tree operations. Nodes is a
// flat array; clients rebuild the tree structure from each node's parent_id.
type TreeResponse struct {
	TreeID       uint                `json:"tree_id"`
	RecipeID     uint                `json:"recipe_id"`
	RootNodeID   *uint               `json:"root_node_id"`
	ActiveNodeID *uint               `json:"active_node_id"`
	Nodes        []models.RecipeNode `json:"nodes"`
}

// activeNodeID returns the ID of the active node in a flat node list, or nil.
func activeNodeID(nodes []models.RecipeNode) *uint {
	for i := range nodes {
		if nodes[i].IsActive {
			id := nodes[i].ID
			return &id
		}
	}
	return nil
}

// effectiveRecipeDef resolves the effective RecipeDef for a recipe, using the
// canonical's data when the recipe hasn't diverged.
func effectiveRecipeDef(r *models.Recipe) models.RecipeDef {
	if !r.HasDiverged && r.Canonical != nil {
		def := r.Canonical.RecipeData
		if def.SourceURL == "" {
			def.SourceURL = r.SourceURL
		}
		return def
	}
	return r.RecipeDef
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
