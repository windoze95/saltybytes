package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/models"
	"gorm.io/gorm"
)

func runawayRouter(capUSD float64, user *models.User,
	sum func(uint, time.Time) (float64, error),
	lock func(uint, string) error,
) *gin.Engine {
	r := gin.New()
	r.POST("/ai",
		func(c *gin.Context) { c.Set("user", user); c.Next() },
		UnlimitedRunawayGuard(capUSD, sum, lock),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) },
	)
	return r
}

func unlimitedUser() *models.User {
	return &models.User{
		Model:    gorm.Model{ID: 4},
		Username: "julian",
		Subscription: &models.Subscription{
			Tier: models.TierUnlimited,
		},
	}
}

func hitRunaway(r *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/ai", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRunawayGuard_UnderCapPasses(t *testing.T) {
	locked := int32(0)
	r := runawayRouter(10, unlimitedUser(),
		func(uint, time.Time) (float64, error) { return 9.99, nil },
		func(uint, string) error { atomic.AddInt32(&locked, 1); return nil })

	if w := hitRunaway(r); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if atomic.LoadInt32(&locked) != 0 {
		t.Error("must not lock under the cap")
	}
}

func TestRunawayGuard_OverCapLocksAndBlocks(t *testing.T) {
	var lockedID uint
	var lockedReason string
	r := runawayRouter(10, unlimitedUser(),
		func(uint, time.Time) (float64, error) { return 12.40, nil },
		func(id uint, reason string) error { lockedID = id; lockedReason = reason; return nil })

	w := hitRunaway(r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403. body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "account_locked") {
		t.Errorf("body missing account_locked code: %s", w.Body.String())
	}
	if lockedID != 4 {
		t.Errorf("locked user %d, want 4", lockedID)
	}
	if !strings.Contains(lockedReason, "$12.40") || !strings.Contains(lockedReason, "$10.00") {
		t.Errorf("lock reason should carry spend and cap: %q", lockedReason)
	}
}

func TestRunawayGuard_CappedTiersPassWithoutSpendCheck(t *testing.T) {
	sums := int32(0)
	premium := unlimitedUser()
	premium.Subscription.Tier = models.TierPremium
	r := runawayRouter(10, premium,
		func(uint, time.Time) (float64, error) { atomic.AddInt32(&sums, 1); return 999, nil },
		func(uint, string) error { return nil })

	if w := hitRunaway(r); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (capped tiers are already bounded)", w.Code)
	}
	if atomic.LoadInt32(&sums) != 0 {
		t.Error("capped tiers must not incur spend queries")
	}
}

func TestRunawayGuard_DisabledPasses(t *testing.T) {
	r := runawayRouter(0, unlimitedUser(),
		func(uint, time.Time) (float64, error) { return 99999, nil },
		func(uint, string) error { return nil })
	if w := hitRunaway(r); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when disabled", w.Code)
	}
}

func TestRunawayGuard_FailsOpenOnQueryError(t *testing.T) {
	r := runawayRouter(10, unlimitedUser(),
		func(uint, time.Time) (float64, error) { return 0, errors.New("db down") },
		func(uint, string) error { return nil })
	if w := hitRunaway(r); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail open)", w.Code)
	}
}

func TestRunawayGuard_CachesPerUserSpend(t *testing.T) {
	sums := int32(0)
	r := runawayRouter(10, unlimitedUser(),
		func(uint, time.Time) (float64, error) { atomic.AddInt32(&sums, 1); return 1, nil },
		func(uint, string) error { return nil })
	hitRunaway(r)
	hitRunaway(r)
	hitRunaway(r)
	if got := atomic.LoadInt32(&sums); got != 1 {
		t.Errorf("spend queried %d times for 3 quick requests, want 1", got)
	}
}
