package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"github.com/windoze95/saltybytes-api/internal/video"
	"gorm.io/gorm"
)

// --- fakes implementing the service video interfaces ---

type fakeVideoFetcher struct{ meta *video.VideoMeta }

func (f *fakeVideoFetcher) FetchVideo(ctx context.Context, rawURL string) (*video.VideoMeta, error) {
	return f.meta, nil
}

type fakeFrameSampler struct{}

func (f *fakeFrameSampler) Sample(ctx context.Context, mediaURL string, durationMS int) ([][]byte, error) {
	return [][]byte{{0xFF, 0xD8, 0x01}, {0xFF, 0xD8, 0x02}}, nil
}

// videoEnabledHandler builds an ImportHandler whose service has the video
// dependencies wired with fakes, plus a SubscriptionService for gating.
func videoEnabledHandler(repo *testutil.MockRecipeRepo, vrepo *testutil.MockVideoImportRepo, userRepo *testutil.MockUserRepo) *ImportHandler {
	importSvc := newImportService(repo, nil)
	importSvc.VideoRepo = vrepo
	importSvc.VideoFetcher = &fakeVideoFetcher{meta: &video.VideoMeta{
		Platform:   video.PlatformTikTok,
		VideoID:    "7499229683859426602",
		Caption:    "pancakes #recipe",
		Transcript: "Mix flour and eggs.",
		// Public TEST-NET-3 IP literal (RFC 5737): passes the SSRF guard offline
		// (LookupHost short-circuits IP literals, so no DNS query is made).
		MediaURL:   "https://203.0.113.10/v.mp4",
		DurationMS: 30000,
	}}
	importSvc.VideoFrameSampler = &fakeFrameSampler{}
	importSvc.VisionProvider = &testutil.MockVisionProvider{
		ExtractRecipesFromMediaFunc: func(ctx context.Context, media []ai.MediaInput, contextText, unitSystem, requirements string) ([]*ai.RecipeResult, error) {
			return []*ai.RecipeResult{testutil.TestRecipeResult()}, nil
		},
	}
	handler := NewImportHandler(importSvc)
	if userRepo != nil {
		handler.SubService = service.NewSubscriptionService(&config.Config{}, userRepo)
	}
	return handler
}

func freeUserWithVideoUsage(used int) (*models.User, *testutil.MockUserRepo) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:            gorm.Model{ID: 1},
		UserID:           user.ID,
		Tier:             models.TierFree,
		VideoImportsUsed: used,
		MonthlyResetAt:   time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user
	return user, userRepo
}

func TestImportFromVideo_FreeUserAtLimit_403(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	user, userRepo := freeUserWithVideoUsage(2) // free limit is 2
	handler := videoEnabledHandler(repo, vrepo, userRepo)

	r := gin.New()
	r.POST("/recipes/import/video", setUser(user), handler.ImportFromVideo)

	req := httptest.NewRequest("POST", "/recipes/import/video", strings.NewReader(`{"url":"https://www.tiktok.com/@chef/video/7499229683859426602"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403. body: %s", w.Code, w.Body.String())
	}
}

func TestImportFromVideo_Unconfigured_503(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	// No video deps wired on the service → StartVideoImport returns video_unavailable.
	importSvc := newImportService(repo, nil)
	handler := NewImportHandler(importSvc)

	user := testutil.TestUser()
	r := gin.New()
	r.POST("/recipes/import/video", setUser(user), handler.ImportFromVideo)

	req := httptest.NewRequest("POST", "/recipes/import/video", strings.NewReader(`{"url":"https://www.tiktok.com/@chef/video/123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503. body: %s", w.Code, w.Body.String())
	}
}

func TestImportFromVideo_UnsupportedPlatform_400(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	user, userRepo := freeUserWithVideoUsage(0)
	handler := videoEnabledHandler(repo, vrepo, userRepo)

	r := gin.New()
	r.POST("/recipes/import/video", setUser(user), handler.ImportFromVideo)

	req := httptest.NewRequest("POST", "/recipes/import/video", strings.NewReader(`{"url":"https://example.com/not-a-video"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400. body: %s", w.Code, w.Body.String())
	}
}

func TestImportFromVideo_Accepted_AndPoll(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	user, userRepo := freeUserWithVideoUsage(0)
	handler := videoEnabledHandler(repo, vrepo, userRepo)

	r := gin.New()
	r.POST("/recipes/import/video", setUser(user), handler.ImportFromVideo)
	r.GET("/recipes/import/video/:id", setUser(user), handler.GetVideoImportStatus)

	// Accept the job.
	req := httptest.NewRequest("POST", "/recipes/import/video", strings.NewReader(`{"url":"https://www.tiktok.com/@chef/video/7499229683859426602"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202. body: %s", w.Code, w.Body.String())
	}
	var accepted struct {
		Job struct {
			ID     uint   `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted body: %v", err)
	}
	if accepted.Job.ID == 0 {
		t.Fatal("expected a job id in the 202 response")
	}

	// The accepted job should have consumed one unit of quota.
	if user.Subscription.VideoImportsUsed != 1 {
		t.Errorf("VideoImportsUsed = %d, want 1 after accepting a job", user.Subscription.VideoImportsUsed)
	}

	// Poll the status endpoint until the async job completes.
	path := "/recipes/import/video/" + itoa(accepted.Job.ID)
	deadline := time.Now().Add(3 * time.Second)
	var lastStatus string
	for time.Now().Before(deadline) {
		gw := httptest.NewRecorder()
		greq := httptest.NewRequest("GET", path, nil)
		r.ServeHTTP(gw, greq)
		if gw.Code != http.StatusOK {
			t.Fatalf("poll status = %d, want 200. body: %s", gw.Code, gw.Body.String())
		}
		var polled struct {
			Job struct {
				Status   string `json:"status"`
				RecipeID uint   `json:"recipe_id"`
			} `json:"job"`
		}
		json.Unmarshal(gw.Body.Bytes(), &polled)
		lastStatus = polled.Job.Status
		if polled.Job.Status == string(models.VideoImportDone) {
			if polled.Job.RecipeID == 0 {
				t.Error("completed job should carry a recipe_id")
			}
			return
		}
		if polled.Job.Status == string(models.VideoImportFailed) {
			t.Fatalf("job failed unexpectedly: %s", gw.Body.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job did not complete; last status %q", lastStatus)
}

func TestGetVideoImportStatus_NotOwner_404(t *testing.T) {
	repo := testutil.NewMockRecipeRepo()
	vrepo := testutil.NewMockVideoImportRepo()
	owner, userRepo := freeUserWithVideoUsage(0)
	handler := videoEnabledHandler(repo, vrepo, userRepo)

	// A job owned by `owner`.
	job := &models.VideoImport{UserID: owner.ID, SourceURL: "https://www.tiktok.com/@x/video/1", Platform: "tiktok", Status: models.VideoImportDone}
	if err := vrepo.CreateImport(job); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// Another user tries to read it.
	other := testutil.TestUser()
	other.ID = owner.ID + 999

	r := gin.New()
	r.GET("/recipes/import/video/:id", setUser(other), handler.GetVideoImportStatus)

	greq := httptest.NewRequest("GET", "/recipes/import/video/"+itoa(job.ID), nil)
	gw := httptest.NewRecorder()
	r.ServeHTTP(gw, greq)

	if gw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for non-owner. body: %s", gw.Code, gw.Body.String())
	}
}

func itoa(u uint) string {
	return strconv.FormatUint(uint64(u), 10)
}
