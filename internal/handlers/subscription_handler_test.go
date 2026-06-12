package handlers

import (
	"encoding/json"
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

func newSubscriptionRouter(userRepo *testutil.MockUserRepo, user *models.User) *gin.Engine {
	svc := service.NewSubscriptionService(&config.Config{}, userRepo)
	handler := NewSubscriptionHandler(svc)

	r := gin.New()
	r.GET("/subscription", setUser(user), handler.GetSubscription)
	r.POST("/subscription/upgrade", setUser(user), handler.UpgradeSubscription)
	return r
}

func TestGetSubscription_Handler_Envelope(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:                gorm.Model{ID: 1},
		UserID:               user.ID,
		Tier:                 models.TierFree,
		AllergenAnalysesUsed: 2,
		MonthlyResetAt:       time.Now().Add(time.Hour),
	}
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	r := newSubscriptionRouter(userRepo, user)

	req := httptest.NewRequest("GET", "/subscription", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	sub, ok := resp["subscription"]
	if !ok {
		t.Fatalf("response missing 'subscription' envelope key. body: %s", w.Body.String())
	}
	// models.Subscription has no json tags, so fields serialize PascalCase.
	if sub["Tier"] != "free" {
		t.Errorf("subscription.Tier = %v, want 'free'", sub["Tier"])
	}
	if sub["AllergenAnalysesUsed"] != float64(2) {
		t.Errorf("subscription.AllergenAnalysesUsed = %v, want 2", sub["AllergenAnalysesUsed"])
	}
}

func TestGetSubscription_Handler_NilSubscription_CreatesFreeRow(t *testing.T) {
	user := testutil.TestUser()
	user.Subscription = nil
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	r := newSubscriptionRouter(userRepo, user)

	req := httptest.NewRequest("GET", "/subscription", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["subscription"]["Tier"] != "free" {
		t.Errorf("subscription.Tier = %v, want default 'free'", resp["subscription"]["Tier"])
	}
	if userRepo.Users[user.ID].Subscription == nil {
		t.Error("free-tier subscription row should have been created on the fly")
	}
}

func TestUpgradeSubscription_Handler_NotImplemented_501(t *testing.T) {
	user := testutil.TestUser()
	userRepo := testutil.NewMockUserRepo()
	userRepo.Users[user.ID] = user

	r := newSubscriptionRouter(userRepo, user)

	req := httptest.NewRequest("POST", "/subscription/upgrade", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d (paid plans not wired up). body: %s", w.Code, http.StatusNotImplemented, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["error"] == nil {
		t.Error("response should contain 'error' explaining upgrade is unavailable")
	}
	// The user must not be silently upgraded.
	if sub := userRepo.Users[user.ID].Subscription; sub != nil && sub.Tier == models.TierPremium {
		t.Error("user must not be upgraded to premium by a failed upgrade")
	}
}
