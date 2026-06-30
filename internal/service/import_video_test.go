package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"github.com/windoze95/saltybytes-api/internal/video"
)

// --- fakes for the video fetcher and frame sampler ---

type fakeVideoFetcher struct {
	meta  *video.VideoMeta
	err   error
	calls int
}

func (f *fakeVideoFetcher) FetchVideo(ctx context.Context, rawURL string) (*video.VideoMeta, error) {
	f.calls++
	return f.meta, f.err
}

type fakeFrameSampler struct {
	frames [][]byte
	err    error
	calls  int
}

func (f *fakeFrameSampler) Sample(ctx context.Context, mediaURL string, durationMS int) ([][]byte, error) {
	f.calls++
	return f.frames, f.err
}

func tiktokMeta() *video.VideoMeta {
	return &video.VideoMeta{
		Platform:   video.PlatformTikTok,
		VideoID:    "7499229683859426602",
		Caption:    "easy fluffy pancakes #recipe",
		Transcript: "First mix the flour and eggs, then cook on a griddle.",
		// A public TEST-NET-3 IP literal (RFC 5737): it passes the SSRF guard's
		// private-address check, and LookupHost short-circuits IP literals so no
		// DNS query is made — keeping the test offline.
		MediaURL:   "https://203.0.113.10/nowm.mp4",
		DurationMS: 45000,
	}
}

func newVideoTestService(repo *testutil.MockRecipeRepo, vrepo *testutil.MockVideoImportRepo) *ImportService {
	svc := newTestImportService(repo, nil, nil)
	svc.VideoRepo = vrepo
	return svc
}

// waitForVideoJob polls until the job reaches a terminal state or times out.
func waitForVideoJob(t *testing.T, svc *ImportService, id uint) *models.VideoImport {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := svc.GetVideoImport(id)
		if err == nil && (job.Status == models.VideoImportDone || job.Status == models.VideoImportFailed) {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("video import job %d did not finish in time", id)
	return nil
}

func TestStartVideoImport_NotConfigured(t *testing.T) {
	svc := newTestImportService(testutil.NewMockRecipeRepo(), nil, nil)
	_, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@x/video/1", testutil.TestUser())
	var extractErr *ExtractionError
	if err == nil || !errors.As(err, &extractErr) || extractErr.Code != "video_unavailable" {
		t.Fatalf("expected video_unavailable ExtractionError, got %v", err)
	}
}

func TestStartVideoImport_UnsupportedPlatform(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)
	svc.VideoFetcher = &fakeVideoFetcher{meta: tiktokMeta()}
	svc.VideoFrameSampler = &fakeFrameSampler{}
	svc.VisionProvider = &testutil.MockVisionProvider{}

	_, err := svc.StartVideoImport(context.Background(), "https://example.com/not-a-video", testutil.TestUser())
	var extractErr *ExtractionError
	if err == nil || !errors.As(err, &extractErr) || extractErr.Code != "unsupported_platform" {
		t.Fatalf("expected unsupported_platform ExtractionError, got %v", err)
	}
}

func TestVideoImport_FreshExtraction(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)

	fetcher := &fakeVideoFetcher{meta: tiktokMeta()}
	sampler := &fakeFrameSampler{frames: [][]byte{{0xFF, 0xD8, 0x01}, {0xFF, 0xD8, 0x02}, {0xFF, 0xD8, 0x03}}}
	svc.VideoFetcher = fetcher
	svc.VideoFrameSampler = sampler

	var gotMedia []ai.MediaInput
	var gotContext string
	svc.VisionProvider = &testutil.MockVisionProvider{
		ExtractRecipesFromMediaFunc: func(ctx context.Context, media []ai.MediaInput, contextText, unitSystem, requirements string) ([]*ai.RecipeResult, error) {
			gotMedia = media
			gotContext = contextText
			return []*ai.RecipeResult{testutil.TestRecipeResult()}, nil
		},
	}

	job, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@chef/video/7499229683859426602", testutil.TestUser())
	if err != nil {
		t.Fatalf("StartVideoImport error: %v", err)
	}
	if job.Status != models.VideoImportQueued {
		t.Errorf("initial status = %q, want queued", job.Status)
	}

	done := waitForVideoJob(t, svc, job.ID)
	if done.Status != models.VideoImportDone {
		t.Fatalf("final status = %q (error %q), want done", done.Status, done.Error)
	}
	if done.RecipeID == nil {
		t.Error("expected a RecipeID on the completed job")
	}
	if done.CacheHit {
		t.Error("fresh extraction should not be a cache hit")
	}
	if done.CostUSD <= 0 {
		t.Errorf("expected a positive metered cost, got %v", done.CostUSD)
	}
	if len(repo.Recipes) != 1 {
		t.Errorf("expected 1 recipe saved, got %d", len(repo.Recipes))
	}
	if len(gotMedia) != 3 {
		t.Errorf("vision got %d frames, want 3", len(gotMedia))
	}
	for i, m := range gotMedia {
		if m.Kind != ai.MediaImage {
			t.Errorf("frame %d kind = %q, want image", i, m.Kind)
		}
	}
	if !strings.Contains(gotContext, "Transcript") || !strings.Contains(gotContext, "flour and eggs") {
		t.Errorf("context text missing transcript: %q", gotContext)
	}
	// The extraction should be cached for the next importer.
	if _, err := vrepo.GetCacheByVideoKey("tiktok:7499229683859426602"); err != nil {
		t.Errorf("expected cache entry for the video, got %v", err)
	}
}

func TestVideoImport_CacheHit(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)

	// Seed the cache so the video is served without sampling or extraction.
	cached := recipeResultToRecipeDef(testutil.TestRecipeResult())
	if err := vrepo.UpsertCache(&models.VideoExtractionCache{
		VideoKey:    "tiktok:7499229683859426602",
		Platform:    "tiktok",
		OriginalURL: "https://www.tiktok.com/@chef/video/7499229683859426602",
		RecipeData:  cached,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	fetcher := &fakeVideoFetcher{meta: tiktokMeta()}
	sampler := &fakeFrameSampler{frames: [][]byte{{0xFF}}}
	svc.VideoFetcher = fetcher
	svc.VideoFrameSampler = sampler
	visionCalls := 0
	svc.VisionProvider = &testutil.MockVisionProvider{
		ExtractRecipesFromMediaFunc: func(ctx context.Context, media []ai.MediaInput, contextText, unitSystem, requirements string) ([]*ai.RecipeResult, error) {
			visionCalls++
			return []*ai.RecipeResult{testutil.TestRecipeResult()}, nil
		},
	}

	job, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@chef/video/7499229683859426602", testutil.TestUser())
	if err != nil {
		t.Fatalf("StartVideoImport error: %v", err)
	}

	done := waitForVideoJob(t, svc, job.ID)
	if done.Status != models.VideoImportDone {
		t.Fatalf("final status = %q (error %q), want done", done.Status, done.Error)
	}
	if !done.CacheHit {
		t.Error("expected CacheHit=true")
	}
	if done.CostUSD != 0 {
		t.Errorf("cache hit cost = %v, want 0", done.CostUSD)
	}
	if done.RecipeID == nil {
		t.Error("expected a RecipeID on the cache-hit job")
	}
	if sampler.calls != 0 {
		t.Errorf("sampler called %d times on cache hit, want 0", sampler.calls)
	}
	if visionCalls != 0 {
		t.Errorf("vision called %d times on cache hit, want 0", visionCalls)
	}
}

func TestVideoImport_BudgetExceeded(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	vrepo.SumImportCostSinceFunc = func(t time.Time) (float64, error) { return 50.0, nil }

	svc := newVideoTestService(repo, vrepo)
	svc.Cfg = &config.Config{EnvVars: config.EnvVars{VideoImportDailyBudgetUSD: 25.0}}

	fetcher := &fakeVideoFetcher{meta: tiktokMeta()}
	sampler := &fakeFrameSampler{frames: [][]byte{{0xFF}}}
	svc.VideoFetcher = fetcher
	svc.VideoFrameSampler = sampler
	svc.VisionProvider = &testutil.MockVisionProvider{}

	job, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@chef/video/7499229683859426602", testutil.TestUser())
	if err != nil {
		t.Fatalf("StartVideoImport error: %v", err)
	}

	done := waitForVideoJob(t, svc, job.ID)
	if done.Status != models.VideoImportFailed {
		t.Fatalf("final status = %q, want failed", done.Status)
	}
	if !strings.Contains(strings.ToLower(done.Error), "capacity") {
		t.Errorf("error = %q, want an at-capacity message", done.Error)
	}
	if sampler.calls != 0 {
		t.Errorf("sampler called %d times when over budget, want 0 (fail before sampling)", sampler.calls)
	}
}

func TestVideoImport_SetsThumbnail(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)
	svc.VideoFetcher = &fakeVideoFetcher{meta: tiktokMeta()}
	svc.VideoFrameSampler = &fakeFrameSampler{frames: [][]byte{{0xFF, 1}, {0xFF, 2}, {0xFF, 3}}}
	svc.VisionProvider = &testutil.MockVisionProvider{
		ExtractRecipesFromMediaFunc: func(ctx context.Context, media []ai.MediaInput, contextText, unitSystem, requirements string) ([]*ai.RecipeResult, error) {
			return []*ai.RecipeResult{testutil.TestRecipeResult()}, nil
		},
	}
	var gotKey string
	var gotFrame []byte
	svc.ThumbnailUploader = func(ctx context.Context, frame []byte, videoKey string) (string, error) {
		gotKey, gotFrame = videoKey, frame
		return "https://s3.example/thumb.jpg", nil
	}

	job, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@chef/video/7499229683859426602", testutil.TestUser())
	if err != nil {
		t.Fatalf("StartVideoImport: %v", err)
	}
	done := waitForVideoJob(t, svc, job.ID)
	if done.Status != models.VideoImportDone || done.RecipeID == nil {
		t.Fatalf("status=%q recipe=%v, want done with a recipe", done.Status, done.RecipeID)
	}
	if gotKey != "tiktok:7499229683859426602" {
		t.Errorf("thumbnail keyed by %q", gotKey)
	}
	if len(gotFrame) < 2 || gotFrame[1] != 2 {
		t.Errorf("uploaded frame = %v, want the middle frame (marker 2)", gotFrame)
	}
	if got := repo.Recipes[*done.RecipeID].ImageURL; got != "https://s3.example/thumb.jpg" {
		t.Errorf("recipe ImageURL = %q, want the thumbnail", got)
	}
	entry, err := vrepo.GetCacheByVideoKey("tiktok:7499229683859426602")
	if err != nil {
		t.Fatalf("cache lookup: %v", err)
	}
	if entry.ThumbnailURL != "https://s3.example/thumb.jpg" {
		t.Errorf("cache ThumbnailURL = %q, want the thumbnail", entry.ThumbnailURL)
	}
}

func TestVideoImport_CacheHitReusesThumbnail(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)

	cached := recipeResultToRecipeDef(testutil.TestRecipeResult())
	if err := vrepo.UpsertCache(&models.VideoExtractionCache{
		VideoKey: "tiktok:7499229683859426602", Platform: "tiktok",
		OriginalURL: "https://x", RecipeData: cached,
		ThumbnailURL: "https://s3.example/cached-thumb.jpg",
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	svc.VideoFetcher = &fakeVideoFetcher{meta: tiktokMeta()}
	svc.VideoFrameSampler = &fakeFrameSampler{}
	svc.VisionProvider = &testutil.MockVisionProvider{}

	job, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@chef/video/7499229683859426602", testutil.TestUser())
	if err != nil {
		t.Fatalf("StartVideoImport: %v", err)
	}
	done := waitForVideoJob(t, svc, job.ID)
	if !done.CacheHit || done.RecipeID == nil {
		t.Fatalf("want cache hit with a recipe, got hit=%v recipe=%v", done.CacheHit, done.RecipeID)
	}
	if got := repo.Recipes[*done.RecipeID].ImageURL; got != "https://s3.example/cached-thumb.jpg" {
		t.Errorf("recipe ImageURL = %q, want the cached thumbnail", got)
	}
}

func TestVideoImport_RefundsQuotaOnFailure(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)

	// The handler increments quota on acceptance; model that as Used=1.
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		UserID: user.ID, Tier: models.TierFree,
		VideoImportsUsed: 1, MonthlyResetAt: time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user
	svc.SubService = NewSubscriptionService(&config.Config{}, userRepo)

	// Fail on our side (fetch error) → quota should be refunded.
	svc.VideoFetcher = &fakeVideoFetcher{err: context.DeadlineExceeded}
	svc.VideoFrameSampler = &fakeFrameSampler{}
	svc.VisionProvider = &testutil.MockVisionProvider{}

	job, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@x/video/1", user)
	if err != nil {
		t.Fatalf("StartVideoImport: %v", err)
	}
	done := waitForVideoJob(t, svc, job.ID)
	if done.Status != models.VideoImportFailed {
		t.Fatalf("want failed, got %s", done.Status)
	}
	// The refund runs in the goroutine after the job is marked failed; poll the
	// lock-synchronized counter until it lands.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && userRepo.SubscriptionUsage(user.ID, "video_imports_used") != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := userRepo.SubscriptionUsage(user.ID, "video_imports_used"); got != 0 {
		t.Errorf("VideoImportsUsed = %d, want 0 after refund", got)
	}
}

func TestVideoImport_TextPath(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)

	// A YouTube-style result: no downloadable media, recipe in the text.
	svc.VideoFetcher = &fakeVideoFetcher{meta: &video.VideoMeta{
		Platform: video.PlatformYouTube,
		VideoID:  "yt123",
		Caption:  "Honey Garlic Chicken\n\n5 ingredients: chicken, honey, garlic, soy, butter.",
		MediaURL: "", // text path
	}}
	sampler := &fakeFrameSampler{}
	svc.VideoFrameSampler = sampler

	visionCalls := 0
	svc.VisionProvider = &testutil.MockVisionProvider{
		ExtractRecipesFromMediaFunc: func(ctx context.Context, media []ai.MediaInput, contextText, unitSystem, requirements string) ([]*ai.RecipeResult, error) {
			visionCalls++
			return nil, nil
		},
	}
	var gotText string
	svc.TextProvider = &testutil.MockTextProvider{
		ExtractRecipeFromTextFunc: func(ctx context.Context, text, unitSystem string) (*ai.RecipeResult, error) {
			gotText = text
			return testutil.TestRecipeResult(), nil
		},
	}

	job, err := svc.StartVideoImport(context.Background(), "https://www.youtube.com/watch?v=yt123", testutil.TestUser())
	if err != nil {
		t.Fatalf("StartVideoImport: %v", err)
	}
	done := waitForVideoJob(t, svc, job.ID)
	if done.Status != models.VideoImportDone || done.RecipeID == nil {
		t.Fatalf("status=%q recipe=%v, want done with a recipe", done.Status, done.RecipeID)
	}
	if sampler.calls != 0 {
		t.Errorf("frame sampler called %d times on text path, want 0", sampler.calls)
	}
	if visionCalls != 0 {
		t.Errorf("vision called %d times on text path, want 0", visionCalls)
	}
	if !strings.Contains(gotText, "Honey Garlic Chicken") {
		t.Errorf("text extraction input = %q, want the caption", gotText)
	}
	if got := repo.Recipes[*done.RecipeID].ImageURL; got != "" {
		t.Errorf("text-path recipe should have no thumbnail, got %q", got)
	}
	if done.CostUSD <= 0 || done.CostUSD > 0.05 {
		t.Errorf("text-path cost = %v, want ~the flat text overhead", done.CostUSD)
	}
}

func TestVideoImport_FetchFailed(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	svc := newVideoTestService(repo, vrepo)

	svc.VideoFetcher = &fakeVideoFetcher{err: context.DeadlineExceeded}
	svc.VideoFrameSampler = &fakeFrameSampler{}
	svc.VisionProvider = &testutil.MockVisionProvider{}

	job, err := svc.StartVideoImport(context.Background(), "https://www.tiktok.com/@chef/video/1", testutil.TestUser())
	if err != nil {
		t.Fatalf("StartVideoImport error: %v", err)
	}

	done := waitForVideoJob(t, svc, job.ID)
	if done.Status != models.VideoImportFailed {
		t.Fatalf("final status = %q, want failed", done.Status)
	}
	if done.Error == "" {
		t.Error("expected an error message on the failed job")
	}
}
