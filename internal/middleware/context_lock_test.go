package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// A locked account must be rejected at the auth layer — nothing
// authenticated works until the lock is lifted.
func TestAttachUserToContext_RejectsLockedAccounts(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	now := time.Now()
	user.LockedAt = &now
	user.LockReason = "runaway guard"
	repo.Users[user.ID] = user

	svc := service.NewUserService(&config.Config{}, repo)

	r := gin.New()
	r.GET("/anything",
		func(c *gin.Context) { c.Set("user_id", user.ID); c.Next() },
		AttachUserToContext(svc),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) },
	)

	req := httptest.NewRequest("GET", "/anything", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403. body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "account_locked") {
		t.Errorf("body missing account_locked: %s", w.Body.String())
	}
}
