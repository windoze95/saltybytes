package service

import (
	"fmt"
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

func TestGetSubscription_MonthlyReset_ZeroesCounters(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:                gorm.Model{ID: 1},
		UserID:               user.ID,
		Tier:                 models.TierFree,
		AllergenAnalysesUsed: 5,
		WebSearchesUsed:      20,
		AIGenerationsUsed:    50,
		MonthlyResetAt:       time.Now().Add(-24 * time.Hour), // overdue
	}
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	sub, err := svc.GetSubscription(user.ID)
	if err != nil {
		t.Fatalf("GetSubscription error: %v", err)
	}
	if sub.AllergenAnalysesUsed != 0 || sub.WebSearchesUsed != 0 || sub.AIGenerationsUsed != 0 {
		t.Errorf("returned counters not zeroed: %d/%d/%d", sub.AllergenAnalysesUsed, sub.WebSearchesUsed, sub.AIGenerationsUsed)
	}
	if !sub.MonthlyResetAt.After(time.Now()) {
		t.Error("MonthlyResetAt should be advanced into the future")
	}
	// The reset must also be persisted, not just reflected in the return value.
	persisted := repo.Users[user.ID].Subscription
	if persisted.AllergenAnalysesUsed != 0 || persisted.WebSearchesUsed != 0 || persisted.AIGenerationsUsed != 0 {
		t.Errorf("persisted counters not zeroed: %d/%d/%d", persisted.AllergenAnalysesUsed, persisted.WebSearchesUsed, persisted.AIGenerationsUsed)
	}
}

func TestGetSubscription_NoReset_BeforeWindow(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:                gorm.Model{ID: 1},
		UserID:               user.ID,
		Tier:                 models.TierFree,
		AllergenAnalysesUsed: 3,
		MonthlyResetAt:       time.Now().Add(time.Hour), // not yet due
	}
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	sub, err := svc.GetSubscription(user.ID)
	if err != nil {
		t.Fatalf("GetSubscription error: %v", err)
	}
	if sub.AllergenAnalysesUsed != 3 {
		t.Errorf("AllergenAnalysesUsed = %d, want unchanged 3", sub.AllergenAnalysesUsed)
	}
}

func TestGetSubscription_CreateRowFails_InMemoryFallback(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = nil
	repo.Users[user.ID] = user
	repo.CreateSubscriptionErr = fmt.Errorf("db down")

	svc := newTestSubscriptionService(repo)

	// Persisting the row fails, but the caller still gets free-tier defaults
	// so gating works for this request.
	sub, err := svc.GetSubscription(user.ID)
	if err != nil {
		t.Fatalf("GetSubscription error: %v (should fall back to in-memory defaults)", err)
	}
	if sub == nil || sub.Tier != models.TierFree {
		t.Fatalf("sub = %+v, want in-memory free-tier subscription", sub)
	}
	if repo.Users[user.ID].Subscription != nil {
		t.Error("subscription row should not have been persisted when create fails")
	}
}

func TestGetSubscription_UserNotFound(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestSubscriptionService(repo)

	if _, err := svc.GetSubscription(999); err == nil {
		t.Error("GetSubscription for unknown user should error")
	}
}

func TestCheckLimit_PremiumUnlimited(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Subscription = &models.Subscription{
		Model:                gorm.Model{ID: 1},
		UserID:               user.ID,
		Tier:                 models.TierPremium,
		AllergenAnalysesUsed: 1000,
		WebSearchesUsed:      1000,
		AIGenerationsUsed:    1000,
		MonthlyResetAt:       time.Now().Add(time.Hour),
	}
	repo.Users[user.ID] = user

	svc := newTestSubscriptionService(repo)

	for _, usageType := range []string{"allergen", "search", "ai_generation"} {
		allowed, err := svc.CheckLimit(user.ID, usageType)
		if err != nil {
			t.Fatalf("CheckLimit(%q) error: %v", usageType, err)
		}
		if !allowed {
			t.Errorf("CheckLimit(%q) = false, want true for premium regardless of usage", usageType)
		}
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
	tests := []struct {
		usageType string
		counter   func(s *models.Subscription) int
	}{
		{"allergen", func(s *models.Subscription) int { return s.AllergenAnalysesUsed }},
		{"search", func(s *models.Subscription) int { return s.WebSearchesUsed }},
		{"ai_generation", func(s *models.Subscription) int { return s.AIGenerationsUsed }},
	}

	for _, tt := range tests {
		t.Run(tt.usageType, func(t *testing.T) {
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

			if err := svc.IncrementUsage(user.ID, tt.usageType); err != nil {
				t.Fatalf("IncrementUsage(%q) error: %v", tt.usageType, err)
			}
			sub := repo.Users[user.ID].Subscription
			if got := tt.counter(sub); got != 1 {
				t.Errorf("%s counter = %d, want 1", tt.usageType, got)
			}
			// Only the targeted counter moves.
			total := sub.AllergenAnalysesUsed + sub.WebSearchesUsed + sub.AIGenerationsUsed
			if total != 1 {
				t.Errorf("sum of counters = %d, want exactly 1 incremented", total)
			}
		})
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
