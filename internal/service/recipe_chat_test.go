package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// newGenRecipeService builds a RecipeService with the given AI providers and
// embedding deps wired in (any may be nil for "not configured").
func newGenRecipeService(repo repository.RecipeRepo, text ai.TextProvider, image ai.ImageProvider, embed ai.EmbeddingProvider, vector repository.VectorRepo) *RecipeService {
	return &RecipeService{
		Cfg:           &config.Config{},
		Repo:          repo,
		TextProvider:  text,
		ImageProvider: image,
		EmbedProvider: embed,
		VectorRepo:    vector,
	}
}

// gatedTextProvider returns a MockTextProvider whose GenerateRecipe blocks
// until the test finishes, so Init* tests can assert on placeholder state
// without racing the background finish goroutine.
func gatedTextProvider(t *testing.T) (*testutil.MockTextProvider, chan struct{}) {
	t.Helper()
	gate := make(chan struct{})
	entered := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	provider := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			close(entered)
			<-gate
			return nil, errors.New("aborted by test teardown")
		},
	}
	return provider, entered
}

// waitForCondition polls cond until it returns true or the timeout elapses.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// drainEvents closes and drains a stream event channel into a slice.
func drainEvents(events chan ai.StreamEvent) []ai.StreamEvent {
	close(events)
	var out []ai.StreamEvent
	for e := range events {
		out = append(out, e)
	}
	return out
}

// findEvent returns the first event of the given type, or nil.
func findEvent(events []ai.StreamEvent, eventType ai.StreamEventType) *ai.StreamEvent {
	for i := range events {
		if events[i].Type == eventType {
			return &events[i]
		}
	}
	return nil
}

// --- InitGenerateRecipe ---

func TestInitGenerateRecipe_CreatesGeneratingPlaceholder(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	provider, entered := gatedTextProvider(t)

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	resp, err := svc.InitGenerateRecipe(user, "make pancakes", false)
	if err != nil {
		t.Fatalf("InitGenerateRecipe() error = %v", err)
	}

	if resp.ID != "1" {
		t.Errorf("response ID = %q, want '1'", resp.ID)
	}
	if resp.Status != "generating" {
		t.Errorf("response Status = %q, want 'generating'", resp.Status)
	}

	stored := repo.RecipeSnapshot(1)
	if stored == nil {
		t.Fatal("placeholder recipe should exist in the repo")
	}
	if stored.Status != "generating" {
		t.Errorf("stored Status = %q, want 'generating'", stored.Status)
	}
	if stored.PersonalizationUID != user.Personalization.UID {
		t.Errorf("stored PersonalizationUID = %v, want %v", stored.PersonalizationUID, user.Personalization.UID)
	}

	// The async finish goroutine must actually start.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("FinishGenerateRecipe goroutine never called the text provider")
	}
}

func TestInitGenerateRecipe_NilPersonalization(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	user.Personalization = &models.Personalization{} // ID == 0

	svc := newGenRecipeService(repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	if _, err := svc.InitGenerateRecipe(user, "make pancakes", false); err == nil {
		t.Fatal("InitGenerateRecipe() error = nil, want error for missing personalization")
	}
	if len(repo.Recipes) != 0 {
		t.Errorf("no recipe should be created, got %d", len(repo.Recipes))
	}
}

func TestInitGenerateRecipe_CreateRecipeError(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	repo.CreateRecipeErr = errors.New("db down")

	svc := newGenRecipeService(repo, &testutil.MockTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	if _, err := svc.InitGenerateRecipe(testutil.TestUser(), "make pancakes", false); err == nil {
		t.Fatal("InitGenerateRecipe() error = nil, want error when the repo create fails")
	}
}

// --- FinishGenerateRecipe ---

// seedGeneratingRecipe creates a placeholder recipe through the repo the same
// way InitGenerateRecipe does.
func seedGeneratingRecipe(t *testing.T, repo *testutil.MockRecipeRepo, user *models.User) *models.Recipe {
	t.Helper()
	recipe := &models.Recipe{
		CreatedBy:          user,
		CreatedByID:        user.ID,
		PersonalizationUID: user.Personalization.UID,
		Status:             "generating",
	}
	if err := repo.CreateRecipe(recipe); err != nil {
		t.Fatalf("CreateRecipe() error = %v", err)
	}
	return recipe
}

func TestFinishGenerateRecipe_HappyPathWithImage(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	var gotReq ai.RecipeRequest
	result := testutil.TestRecipeResult()
	result.UnitSystem = "metric"
	result.PromptVersion = "pv-123"
	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			gotReq = req
			return result, nil
		},
	}

	var imagePrompt string
	image := &testutil.MockImageProvider{
		GenerateImageFunc: func(ctx context.Context, prompt string) ([]byte, error) {
			imagePrompt = prompt
			return []byte("png-bytes"), nil
		},
	}
	var gotKey, gotContentType string
	newURL := "https://bucket.s3.us-east-2.amazonaws.com/recipes/1/images/recipe_image_1_1700000001.png"
	stubS3Upload(t, &gotKey, &gotContentType, newURL, nil)
	stubS3Delete(t, nil)

	vector := &testutil.MockVectorRepo{}
	embed := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.1, 0.2}, nil
		},
	}

	svc := newGenRecipeService(repo, text, image, embed, vector)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", true)

	// Request carried the user's personalization.
	if gotReq.UserPrompt != "make pancakes" {
		t.Errorf("req.UserPrompt = %q", gotReq.UserPrompt)
	}
	if gotReq.UnitSystem != "US Customary" {
		t.Errorf("req.UnitSystem = %q, want 'US Customary'", gotReq.UnitSystem)
	}
	if gotReq.Requirements != "No peanuts" {
		t.Errorf("req.Requirements = %q, want 'No peanuts'", gotReq.Requirements)
	}

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil {
		t.Fatal("recipe should still exist")
	}
	if stored.Title != "Classic Pancakes" {
		t.Errorf("persisted Title = %q, want 'Classic Pancakes'", stored.Title)
	}
	if stored.UnitSystem != "metric" {
		t.Errorf("persisted UnitSystem = %q, want 'metric' (from tool result)", stored.UnitSystem)
	}
	if stored.Status != "ready" {
		t.Errorf("Status = %q, want 'ready'", stored.Status)
	}
	if recipe.PromptVersion != "pv-123" {
		t.Errorf("PromptVersion = %q, want 'pv-123'", recipe.PromptVersion)
	}

	// Tree initialized with the chat root node.
	tree, err := repo.GetTreeByRecipeID(recipe.ID)
	if err != nil {
		t.Fatalf("GetTreeByRecipeID() error = %v", err)
	}
	root, err := repo.GetActiveNode(tree.ID)
	if err != nil {
		t.Fatalf("GetActiveNode() error = %v", err)
	}
	if root.Type != models.RecipeTypeChat {
		t.Errorf("root node Type = %q, want %q", root.Type, models.RecipeTypeChat)
	}
	if root.Prompt != "make pancakes" {
		t.Errorf("root node Prompt = %q", root.Prompt)
	}
	if root.BranchName != "original" {
		t.Errorf("root node BranchName = %q, want 'original'", root.BranchName)
	}
	if root.Response == nil || root.Response.Title != "Classic Pancakes" {
		t.Error("root node Response should carry the generated def")
	}

	// Embedding attempted.
	if len(vector.UpdateEmbeddingCalls) != 1 || vector.UpdateEmbeddingCalls[0] != recipe.ID {
		t.Errorf("UpdateEmbeddingCalls = %v, want [%d]", vector.UpdateEmbeddingCalls, recipe.ID)
	}

	// Image generated and persisted.
	if imagePrompt != result.ImagePrompt {
		t.Errorf("image prompt = %q, want %q", imagePrompt, result.ImagePrompt)
	}
	updates := repo.ImageURLUpdates()
	if len(updates) != 1 || updates[0].ImageURL != newURL {
		t.Errorf("ImageURLUpdates = %v, want one update to %q", updates, newURL)
	}
	if gotContentType != "image/png" {
		t.Errorf("upload content type = %q, want 'image/png'", gotContentType)
	}

	// Tags associated.
	if len(repo.Tags) != 3 {
		t.Errorf("tags created = %d, want 3", len(repo.Tags))
	}
}

func TestFinishGenerateRecipe_UnitSystemFallbackToPersonalization(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	result := testutil.TestRecipeResult()
	result.UnitSystem = "" // tool result omitted unit_system
	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, text, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", false)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil {
		t.Fatal("recipe should still exist")
	}
	if stored.UnitSystem != "us_customary" {
		t.Errorf("persisted UnitSystem = %q, want personalization fallback 'us_customary'", stored.UnitSystem)
	}
}

func TestFinishGenerateRecipe_AIFailureMarksFailedAndDeletes(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return nil, errors.New("claude unavailable")
		},
	}

	svc := newGenRecipeService(repo, text, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", true)

	if repo.RecipeSnapshot(recipe.ID) != nil {
		t.Error("recipe should be deleted after a generation failure")
	}
	statuses := repo.StatusUpdates()
	if len(statuses) != 1 || statuses[0].Status != "failed" {
		t.Errorf("StatusUpdates = %v, want a single 'failed' update", statuses)
	}
}

func TestFinishGenerateRecipe_ValidationFailureDeletes(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	result := testutil.TestRecipeResult()
	result.Title = "" // fails validateRecipeCoreFields
	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, text, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", false)

	if repo.RecipeSnapshot(recipe.ID) != nil {
		t.Error("recipe should be deleted after a validation failure")
	}
	statuses := repo.StatusUpdates()
	if len(statuses) != 1 || statuses[0].Status != "failed" {
		t.Errorf("StatusUpdates = %v, want a single 'failed' update", statuses)
	}
}

func TestFinishGenerateRecipe_PersistFailureDeletes(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	repo.UpdateRecipeDefErr = errors.New("db down")
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, text, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", false)

	if repo.RecipeSnapshot(recipe.ID) != nil {
		t.Error("recipe should be deleted when persisting the def fails")
	}
}

func TestFinishGenerateRecipe_NoImageWhenGenImageFalse(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	imageCalled := false
	image := &testutil.MockImageProvider{
		GenerateImageFunc: func(ctx context.Context, prompt string) ([]byte, error) {
			imageCalled = true
			return []byte("png"), nil
		},
	}
	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, text, image, nil, nil)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", false)

	if imageCalled {
		t.Error("image provider should not be called when genImage is false")
	}
	if updates := repo.ImageURLUpdates(); len(updates) != 0 {
		t.Errorf("ImageURLUpdates = %v, want none", updates)
	}
	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil || stored.Status != "ready" {
		t.Error("recipe should still finish as ready")
	}
}

func TestFinishGenerateRecipe_ImageFailureNonFatal(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	image := &testutil.MockImageProvider{
		GenerateImageFunc: func(ctx context.Context, prompt string) ([]byte, error) {
			return nil, errors.New("dall-e unavailable")
		},
	}
	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, text, image, nil, nil)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", true)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil {
		t.Fatal("recipe must survive an image generation failure")
	}
	if stored.Status != "ready" {
		t.Errorf("Status = %q, want 'ready' (image failure is non-fatal)", stored.Status)
	}
	if updates := repo.ImageURLUpdates(); len(updates) != 0 {
		t.Errorf("ImageURLUpdates = %v, want none", updates)
	}
}

func TestFinishGenerateRecipe_TreeCreateFailureNonFatal(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	repo.CreateRecipeTreeErr = errors.New("db down")
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, text, &testutil.MockImageProvider{}, nil, nil)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", false)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil || stored.Status != "ready" {
		t.Error("recipe should be ready even when tree creation fails (supplementary)")
	}
}

func TestFinishGenerateRecipe_EmbeddingFailureNonFatal(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	recipe := seedGeneratingRecipe(t, repo, user)

	text := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	embed := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return nil, errors.New("embedding api down")
		},
	}
	vector := &testutil.MockVectorRepo{}

	svc := newGenRecipeService(repo, text, &testutil.MockImageProvider{}, embed, vector)
	svc.FinishGenerateRecipe(recipe, user, "make pancakes", false)

	stored := repo.RecipeSnapshot(recipe.ID)
	if stored == nil || stored.Status != "ready" {
		t.Error("recipe should be ready even when embedding generation fails")
	}
	if len(vector.UpdateEmbeddingCalls) != 0 {
		t.Errorf("UpdateEmbeddingCalls = %v, want none after embed failure", vector.UpdateEmbeddingCalls)
	}
}

// --- StreamGenerateRecipe / finishStreamedRecipe ---

func TestStreamGenerateRecipe_HappyPathStreaming(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()

	result := testutil.TestRecipeResult()
	result.UnitSystem = "metric"
	provider := &testutil.MockStreamingTextProvider{
		StreamGenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error) {
			ai.TrySendEvent(ctx, events, ai.StreamEvent{Type: ai.StreamEventGenerating})
			ai.TrySendEvent(ctx, events, ai.StreamEvent{Type: ai.StreamEventProgress, TokensSoFar: 128})
			return result, nil
		},
	}
	vector := &testutil.MockVectorRepo{}
	embed := &testutil.MockEmbeddingProvider{
		GenerateEmbeddingFunc: func(ctx context.Context, text string) ([]float32, error) {
			return []float32{0.5}, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, embed, vector)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	if len(got) == 0 || got[0].Type != ai.StreamEventStarted {
		t.Fatalf("first event = %+v, want recipe.started", got)
	}
	recipeID := got[0].RecipeID
	if recipeID == 0 {
		t.Error("started event should carry the placeholder recipe ID")
	}
	if findEvent(got, ai.StreamEventProgress) == nil {
		t.Error("expected the provider's progress event to pass through")
	}
	complete := findEvent(got, ai.StreamEventComplete)
	if complete == nil {
		t.Fatalf("no complete event, got %+v", got)
	}
	if got[len(got)-1].Type != ai.StreamEventComplete {
		t.Errorf("last event = %q, want recipe.complete", got[len(got)-1].Type)
	}
	if complete.Result == nil || complete.Result.Title != "Classic Pancakes" {
		t.Errorf("complete.Result = %+v, want the generated recipe", complete.Result)
	}
	if complete.Result.UnitSystem != "metric" {
		t.Errorf("complete.Result.UnitSystem = %q, want 'metric'", complete.Result.UnitSystem)
	}

	stored := repo.RecipeSnapshot(recipeID)
	if stored == nil {
		t.Fatal("recipe should be persisted")
	}
	if stored.Title != "Classic Pancakes" || stored.Status != "ready" {
		t.Errorf("stored recipe = title %q status %q, want persisted+ready", stored.Title, stored.Status)
	}
	if _, err := repo.GetTreeByRecipeID(recipeID); err != nil {
		t.Errorf("tree should be created for the streamed recipe: %v", err)
	}
	if len(vector.UpdateEmbeddingCalls) != 1 {
		t.Errorf("UpdateEmbeddingCalls = %v, want one embedding attempt", vector.UpdateEmbeddingCalls)
	}
}

func TestStreamGenerateRecipe_StreamFailureDeletesRecipe(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()

	provider := &testutil.MockStreamingTextProvider{
		StreamGenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error) {
			// The real provider emits the error event itself before returning.
			ai.TrySendEvent(ctx, events, ai.StreamEvent{Type: ai.StreamEventError, Error: "overloaded", ErrorKind: "rate_limit"})
			return nil, errors.New("overloaded")
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	if len(got) == 0 || got[0].Type != ai.StreamEventStarted {
		t.Fatalf("first event = %+v, want recipe.started", got)
	}
	recipeID := got[0].RecipeID
	if findEvent(got, ai.StreamEventComplete) != nil {
		t.Error("no complete event should be emitted on stream failure")
	}
	if repo.RecipeSnapshot(recipeID) != nil {
		t.Error("placeholder recipe should be deleted after a premature stream failure")
	}
	statuses := repo.StatusUpdates()
	if len(statuses) != 1 || statuses[0].Status != "failed" {
		t.Errorf("StatusUpdates = %v, want a single 'failed' update", statuses)
	}
}

func TestStreamGenerateRecipe_FallbackToSyncProvider(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()

	syncCalled := false
	// Plain MockTextProvider does not implement ai.StreamingTextProvider,
	// forcing the sync fallback path.
	provider := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			syncCalled = true
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	if !syncCalled {
		t.Fatal("sync GenerateRecipe should be used when the provider cannot stream")
	}
	complete := findEvent(got, ai.StreamEventComplete)
	if complete == nil {
		t.Fatalf("no complete event in fallback path, got %+v", got)
	}
	stored := repo.RecipeSnapshot(complete.RecipeID)
	if stored == nil || stored.Status != "ready" {
		t.Error("fallback-generated recipe should be persisted and ready")
	}
}

func TestStreamGenerateRecipe_FallbackSyncFailure(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()

	provider := &testutil.MockTextProvider{
		GenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest) (*ai.RecipeResult, error) {
			return nil, errors.New("claude unavailable")
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	errEvent := findEvent(got, ai.StreamEventError)
	if errEvent == nil {
		t.Fatalf("no error event, got %+v", got)
	}
	if errEvent.RecipeID == 0 {
		t.Error("error event should reference the placeholder recipe")
	}
	if repo.RecipeSnapshot(errEvent.RecipeID) != nil {
		t.Error("placeholder recipe should be deleted after sync fallback failure")
	}
}

func TestStreamGenerateRecipe_NilPersonalization(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()
	user.Personalization = &models.Personalization{}

	svc := newGenRecipeService(repo, &testutil.MockStreamingTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	if len(got) != 1 || got[0].Type != ai.StreamEventError {
		t.Fatalf("events = %+v, want a single error event", got)
	}
	if got[0].ErrorKind != "content_quality" {
		t.Errorf("ErrorKind = %q, want 'content_quality'", got[0].ErrorKind)
	}
	if len(repo.Recipes) != 0 {
		t.Error("no placeholder recipe should be created")
	}
}

func TestStreamGenerateRecipe_CreateRecipeError(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	repo.CreateRecipeErr = errors.New("db down")

	svc := newGenRecipeService(repo, &testutil.MockStreamingTextProvider{}, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), testutil.TestUser(), "make pancakes", false, events)
	got := drainEvents(events)

	if len(got) != 1 || got[0].Type != ai.StreamEventError {
		t.Fatalf("events = %+v, want a single error event (no started)", got)
	}
}

func TestFinishStreamedRecipe_ValidationFailure(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()

	result := testutil.TestRecipeResult()
	result.ImagePrompt = "" // fails validateRecipeCoreFields
	provider := &testutil.MockStreamingTextProvider{
		StreamGenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error) {
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	errEvent := findEvent(got, ai.StreamEventError)
	if errEvent == nil {
		t.Fatalf("no error event, got %+v", got)
	}
	if errEvent.ErrorKind != "content_quality" {
		t.Errorf("ErrorKind = %q, want 'content_quality'", errEvent.ErrorKind)
	}
	if repo.RecipeSnapshot(errEvent.RecipeID) != nil {
		t.Error("recipe should be deleted after streamed validation failure")
	}
}

func TestFinishStreamedRecipe_PersistFailureKeepsRecipeMarkedFailed(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	repo.UpdateRecipeDefErr = errors.New("db down")
	user := testutil.TestUser()

	provider := &testutil.MockStreamingTextProvider{
		StreamGenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	errEvent := findEvent(got, ai.StreamEventError)
	if errEvent == nil {
		t.Fatalf("no error event, got %+v", got)
	}
	stored := repo.RecipeSnapshot(errEvent.RecipeID)
	if stored == nil {
		t.Fatal("recipe row should NOT be deleted on a persist failure")
	}
	if stored.Status != "failed" {
		t.Errorf("Status = %q, want 'failed'", stored.Status)
	}
}

func TestFinishStreamedRecipe_UnitSystemFallbackToPersonalization(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()

	result := testutil.TestRecipeResult()
	result.UnitSystem = ""
	provider := &testutil.MockStreamingTextProvider{
		StreamGenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error) {
			return result, nil
		},
	}

	svc := newGenRecipeService(repo, provider, &testutil.MockImageProvider{}, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", false, events)
	got := drainEvents(events)

	complete := findEvent(got, ai.StreamEventComplete)
	if complete == nil {
		t.Fatalf("no complete event, got %+v", got)
	}
	if complete.Result.UnitSystem != "us_customary" {
		t.Errorf("complete.Result.UnitSystem = %q, want personalization fallback 'us_customary'", complete.Result.UnitSystem)
	}
	stored := repo.RecipeSnapshot(complete.RecipeID)
	if stored == nil || stored.UnitSystem != "us_customary" {
		t.Error("persisted def should carry the fallback unit system")
	}
}

func TestFinishStreamedRecipe_BackgroundImageGeneration(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	user := testutil.TestUser()

	provider := &testutil.MockStreamingTextProvider{
		StreamGenerateRecipeFunc: func(ctx context.Context, req ai.RecipeRequest, events chan<- ai.StreamEvent) (*ai.RecipeResult, error) {
			return testutil.TestRecipeResult(), nil
		},
	}
	image := &testutil.MockImageProvider{
		GenerateImageFunc: func(ctx context.Context, prompt string) ([]byte, error) {
			return []byte("png"), nil
		},
	}
	var gotKey, gotContentType string
	newURL := "https://bucket.s3.us-east-2.amazonaws.com/recipes/1/images/recipe_image_1_1700000002.png"
	stubS3Upload(t, &gotKey, &gotContentType, newURL, nil)
	stubS3Delete(t, nil)

	svc := newGenRecipeService(repo, provider, image, nil, nil)
	events := make(chan ai.StreamEvent, 32)
	svc.StreamGenerateRecipe(context.Background(), user, "make pancakes", true, events)
	got := drainEvents(events)

	complete := findEvent(got, ai.StreamEventComplete)
	if complete == nil {
		t.Fatalf("no complete event, got %+v", got)
	}

	// Image generation runs in the background after the complete event.
	waitForCondition(t, 2*time.Second, func() bool {
		updates := repo.ImageURLUpdates()
		return len(updates) == 1 && updates[0].ImageURL == newURL
	}, "background image generation never updated the recipe image URL")
}
