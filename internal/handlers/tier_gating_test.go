package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

// AI-powered imports (photo/files/voice/text) are metered under the
// ai_import counter; these pin the gate.

func multipartImage(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("image", "r.jpg")
	fw.Write([]byte("fake-image-bytes"))
	w.Close()
	return body, w.FormDataContentType()
}

func newImportGateFixture(t *testing.T, tier models.SubscriptionTier, importsUsed int) (*gin.Engine, *models.User) {
	t.Helper()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:          gorm.Model{ID: 1},
		UserID:         user.ID,
		Tier:           tier,
		AIImportsUsed:  importsUsed,
		MonthlyResetAt: time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	importSvc := newImportService(testutil.NewMockRecipeRepo(), &testutil.MockTextProvider{})
	handler := NewImportHandler(importSvc)
	handler.SubService = service.NewSubscriptionService(&config.Config{}, userRepo)
	r := gin.New()
	r.POST("/recipes/import/photo", setUser(user), handler.ImportFromPhoto)
	return r, user
}

func TestImportFromPhoto_GatedAtAIImportCap(t *testing.T) {
	r, _ := newImportGateFixture(t, models.TierFree, 10) // free cap = 10

	body, ctype := multipartImage(t)
	req := httptest.NewRequest("POST", "/recipes/import/photo", body)
	req.Header.Set("Content-Type", ctype)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 at cap. body: %s", w.Code, w.Body.String())
	}
}

func TestImportFromPhoto_UnlimitedTierNeverGated(t *testing.T) {
	r, _ := newImportGateFixture(t, models.TierUnlimited, 100000)

	body, ctype := multipartImage(t)
	req := httptest.NewRequest("POST", "/recipes/import/photo", body)
	req.Header.Set("Content-Type", ctype)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Passes the gate; downstream may fail on the fake image, but it must
	// not be a 403.
	if w.Code == http.StatusForbidden {
		t.Fatalf("unlimited tier must never hit the quota gate. body: %s", w.Body.String())
	}
}
