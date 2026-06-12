package service

import (
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

func newTestSubscriptionService(repo *testutil.MockUserRepo) *SubscriptionService {
	return NewSubscriptionService(&config.Config{}, repo)
}

func TestGetSubscription_NilSubscription_CreatesFreeRow(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = nil
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	sub, err := svc.GetSubscription(user.ID)
	if err != nil {
		t.Fatalf("GetSubscription error: %v", err)
	}
	if sub.Tier != models.TierFree {
		t.Errorf("Tier = %q, want %q", sub.Tier, models.TierFree)
	}
	if !sub.MonthlyResetAt.After(time.Now()) {
		t.Error("MonthlyResetAt should be set in the future for a new row")
	}
	if repo.Users[user.ID].Subscription == nil {
		t.Error("subscription row should have been persisted")
	}
}

func TestCheckLimit_TriggersMonthlyReset(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:                gorm.Model{ID: 1},
		UserID:               user.ID,
		Tier:                 models.TierFree,
		AllergenAnalysesUsed: 5,
		WebSearchesUsed:      20,
		AIGenerationsUsed:    50,
		MonthlyResetAt:       time.Now().Add(-time.Hour), // overdue
	}
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	for _, usageType := range []string{"allergen", "search", "ai_generation"} {
		allowed, err := svc.CheckLimit(user.ID, usageType)
		if err != nil {
			t.Fatalf("CheckLimit(%q) error: %v", usageType, err)
		}
		if !allowed {
			t.Errorf("CheckLimit(%q) = false, want true after overdue monthly reset", usageType)
		}
	}

	sub := repo.Users[user.ID].Subscription
	if sub.AllergenAnalysesUsed != 0 || sub.WebSearchesUsed != 0 || sub.AIGenerationsUsed != 0 {
		t.Errorf("counters not reset: %d/%d/%d", sub.AllergenAnalysesUsed, sub.WebSearchesUsed, sub.AIGenerationsUsed)
	}
	if !sub.MonthlyResetAt.After(time.Now()) {
		t.Error("MonthlyResetAt should be advanced into the future")
	}
}

func TestCheckLimit_FreeUserAtLimit(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:             gorm.Model{ID: 1},
		UserID:            user.ID,
		Tier:              models.TierFree,
		AIGenerationsUsed: 50,
		MonthlyResetAt:    time.Now().Add(time.Hour),
	}
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	allowed, err := svc.CheckLimit(user.ID, "ai_generation")
	if err != nil {
		t.Fatalf("CheckLimit error: %v", err)
	}
	if allowed {
		t.Error("CheckLimit = true, want false for free user at limit")
	}
}

func TestCheckLimit_UnknownUsageType(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	if _, err := svc.CheckLimit(user.ID, "bogus"); err == nil {
		t.Error("CheckLimit with unknown usage type should error")
	}
}

func TestIncrementUsage_Valid(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:          gorm.Model{ID: 1},
		UserID:         user.ID,
		Tier:           models.TierFree,
		MonthlyResetAt: time.Now().Add(time.Hour),
	}
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	if err := svc.IncrementUsage(user.ID, "ai_generation"); err != nil {
		t.Fatalf("IncrementUsage error: %v", err)
	}
	if got := repo.Users[user.ID].Subscription.AIGenerationsUsed; got != 1 {
		t.Errorf("AIGenerationsUsed = %d, want 1", got)
	}
}

func TestIncrementUsage_UnknownType(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestSubscriptionService(repo)

	if err := svc.IncrementUsage(1, "bogus"); err == nil {
		t.Error("IncrementUsage with unknown usage type should error")
	}
}

func TestUpgradeSubscription_NotAvailable(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestSubscriptionService(repo)

	if _, err := svc.UpgradeSubscription(1); err == nil {
		t.Error("UpgradeSubscription should error until paid plans are wired up")
	}
}
