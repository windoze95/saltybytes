package service

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// stubS3Delete replaces the package-level S3 delete indirection for the test,
// restoring it afterwards. It returns a pointer to the keys it was called with.
func stubS3Delete(t *testing.T, err error) *[]string {
	t.Helper()
	var keys []string
	orig := deleteImageFromS3
	deleteImageFromS3 = func(_ context.Context, _ *config.Config, s3Key string) error {
		keys = append(keys, s3Key)
		return err
	}
	t.Cleanup(func() { deleteImageFromS3 = orig })
	return &keys
}

// stubS3Upload replaces the package-level S3 upload indirection for the test,
// restoring it afterwards.
func stubS3Upload(t *testing.T, gotKey, gotContentType *string, returnURL string, err error) {
	t.Helper()
	orig := uploadImageToS3
	uploadImageToS3 = func(_ context.Context, _ *config.Config, _ []byte, s3Key string, contentType string) (string, error) {
		*gotKey = s3Key
		*gotContentType = contentType
		return returnURL, err
	}
	t.Cleanup(func() { uploadImageToS3 = orig })
}

func TestDeleteRecipe_S3FailureStillSucceeds(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.ImageURL = "https://bucket.s3.us-east-2.amazonaws.com/recipes/1/images/recipe_image_1_1700000000.png"
	repo.Recipes[recipe.ID] = recipe

	deletedKeys := stubS3Delete(t, errors.New("s3 unavailable"))

	svc := newTestRecipeService(repo)
	if err := svc.DeleteRecipe(context.Background(), recipe.ID); err != nil {
		t.Fatalf("DeleteRecipe() error = %v, want nil (S3 failure must be best-effort)", err)
	}

	if _, ok := repo.Recipes[recipe.ID]; ok {
		t.Error("recipe should have been deleted from the repo")
	}
	if len(*deletedKeys) != 1 || (*deletedKeys)[0] != "recipes/1/images/recipe_image_1_1700000000.png" {
		t.Errorf("S3 delete keys = %v, want the key derived from the recipe's ImageURL", *deletedKeys)
	}
}

func TestDeleteRecipe_CleansUpTreeAndNodes(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	recipe.ImageURL = ""
	repo.Recipes[recipe.ID] = recipe

	def := testutil.TestRecipeDef()
	rootNode := &models.RecipeNode{
		Prompt:      "make pancakes",
		Response:    &def,
		Type:        models.RecipeTypeChat,
		BranchName:  "original",
		CreatedByID: recipe.CreatedByID,
		IsActive:    true,
	}
	tree, err := repo.CreateRecipeTree(recipe.ID, rootNode)
	if err != nil {
		t.Fatalf("CreateRecipeTree() error = %v", err)
	}
	childNode := &models.RecipeNode{
		TreeID:      tree.ID,
		ParentID:    &rootNode.ID,
		Prompt:      "make them fluffier",
		Response:    &def,
		Type:        models.RecipeTypeRegenChat,
		CreatedByID: recipe.CreatedByID,
	}
	if err := repo.AddNodeToTree(childNode, true); err != nil {
		t.Fatalf("AddNodeToTree() error = %v", err)
	}

	deletedKeys := stubS3Delete(t, nil)

	svc := newTestRecipeService(repo)
	if err := svc.DeleteRecipe(context.Background(), recipe.ID); err != nil {
		t.Fatalf("DeleteRecipe() error = %v", err)
	}

	if len(repo.Trees) != 0 {
		t.Errorf("trees remaining after delete = %d, want 0", len(repo.Trees))
	}
	if len(repo.Nodes) != 0 {
		t.Errorf("nodes remaining after delete = %d, want 0", len(repo.Nodes))
	}
	if len(*deletedKeys) != 0 {
		t.Errorf("S3 delete should not be called for a recipe with no image, got keys %v", *deletedKeys)
	}
}

func TestDeleteRecipe_RepoErrorFails(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	recipe := testutil.TestRecipe()
	repo.Recipes[recipe.ID] = recipe
	repo.DeleteRecipeErr = errors.New("db down")

	deletedKeys := stubS3Delete(t, nil)

	svc := newTestRecipeService(repo)
	if err := svc.DeleteRecipe(context.Background(), recipe.ID); err == nil {
		t.Fatal("DeleteRecipe() error = nil, want error when the repo delete fails")
	}
	if len(*deletedKeys) != 0 {
		t.Errorf("S3 delete should not run when the repo delete fails, got keys %v", *deletedKeys)
	}
}

func TestUploadRecipeImage_VersionedPNGKeyAndOldObjectCleanup(t *testing.T) {
	var gotKey, gotContentType string
	newURL := "https://bucket.s3.us-east-2.amazonaws.com/recipes/1/images/recipe_image_1_1700000001.png"
	stubS3Upload(t, &gotKey, &gotContentType, newURL, nil)
	deletedKeys := stubS3Delete(t, nil)

	oldURL := "https://bucket.s3.us-east-2.amazonaws.com/recipes/1/images/recipe_image_1.jpg"
	url, err := uploadRecipeImage(context.Background(), 1, oldURL, []byte("png-bytes"), &config.Config{})
	if err != nil {
		t.Fatalf("uploadRecipeImage() error = %v", err)
	}
	if url != newURL {
		t.Errorf("uploadRecipeImage() url = %q, want %q", url, newURL)
	}

	keyPattern := regexp.MustCompile(`^recipes/1/images/recipe_image_1_\d+\.png$`)
	if !keyPattern.MatchString(gotKey) {
		t.Errorf("upload key = %q, want match for %q", gotKey, keyPattern)
	}
	if gotContentType != "image/png" {
		t.Errorf("upload content type = %q, want %q", gotContentType, "image/png")
	}
	if len(*deletedKeys) != 1 || (*deletedKeys)[0] != "recipes/1/images/recipe_image_1.jpg" {
		t.Errorf("old object delete keys = %v, want the legacy key derived from the old URL", *deletedKeys)
	}
}

func TestUploadRecipeImage_OldObjectDeleteFailureNonFatal(t *testing.T) {
	var gotKey, gotContentType string
	newURL := "https://bucket.s3.us-east-2.amazonaws.com/recipes/2/images/recipe_image_2_1700000002.png"
	stubS3Upload(t, &gotKey, &gotContentType, newURL, nil)
	stubS3Delete(t, errors.New("s3 unavailable"))

	oldURL := "https://bucket.s3.us-east-2.amazonaws.com/recipes/2/images/recipe_image_2_1600000000.png"
	url, err := uploadRecipeImage(context.Background(), 2, oldURL, []byte("png-bytes"), &config.Config{})
	if err != nil {
		t.Fatalf("uploadRecipeImage() error = %v, want nil (old-object cleanup is best-effort)", err)
	}
	if url != newURL {
		t.Errorf("uploadRecipeImage() url = %q, want %q", url, newURL)
	}
}

func TestUploadRecipeImage_NoOldImageSkipsDelete(t *testing.T) {
	var gotKey, gotContentType string
	stubS3Upload(t, &gotKey, &gotContentType, "https://bucket.s3.us-east-2.amazonaws.com/new.png", nil)
	deletedKeys := stubS3Delete(t, nil)

	if _, err := uploadRecipeImage(context.Background(), 3, "", []byte("png-bytes"), &config.Config{}); err != nil {
		t.Fatalf("uploadRecipeImage() error = %v", err)
	}
	if len(*deletedKeys) != 0 {
		t.Errorf("no old image: delete should not be called, got keys %v", *deletedKeys)
	}
}
