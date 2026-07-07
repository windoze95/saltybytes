package service

import (
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"go.uber.org/zap"
)

// SubscriptionService handles subscription management and usage limits.
type SubscriptionService struct {
	Cfg  *config.Config
	Repo repository.UserRepo
	// StaleRefresher, when set (wired to IAPService.RefreshUserIfStale),
	// re-verifies a paid tier whose store-side expiry has passed before the
	// tier is used for gating. It must not call back into GetSubscription.
	StaleRefresher func(userID uint)
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(cfg *config.Config, repo repository.UserRepo) *SubscriptionService {
	return &SubscriptionService{
		Cfg:  cfg,
		Repo: repo,
	}
}

// GetSubscription retrieves the subscription for a user. Users without a
// subscription row (created before rows were stamped at signup) get a
// free-tier row created on the fly so usage counters can be tracked.
func (s *SubscriptionService) GetSubscription(userID uint) (*models.Subscription, error) {
	user, err := s.Repo.GetUserByID(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if user.Subscription == nil {
		sub := &models.Subscription{
			UserID:         userID,
			Tier:           models.TierFree,
			MonthlyResetAt: time.Now().AddDate(0, 1, 0),
		}
		if err := s.Repo.CreateSubscription(sub); err != nil {
			// Fall back to in-memory free-tier defaults so the request can
			// still be gated correctly even if persisting the row failed.
			logger.Get().Warn("failed to create missing subscription row; using in-memory free-tier defaults",
				zap.Uint("user_id", userID), zap.Error(err))
		}
		return sub, nil
	}

	// A paid tier past its store-side expiry gets one lazy re-verification
	// before it is trusted: renewals normally arrive via store webhooks, but
	// those can lag or be missed, and expiry must eventually downgrade even
	// if they never come. The refresher rate-limits itself, so this is cheap
	// on the hot path.
	if (user.Subscription.Tier == models.TierPlus || user.Subscription.Tier == models.TierPremium) &&
		user.Subscription.ExpiresAt != nil && time.Now().After(*user.Subscription.ExpiresAt) &&
		s.StaleRefresher != nil {
		s.StaleRefresher(userID)
		user, err = s.Repo.GetUserByID(userID)
		if err != nil {
			return nil, fmt.Errorf("failed to reload user after entitlement refresh: %w", err)
		}
		if user.Subscription == nil {
			return &models.Subscription{UserID: userID, Tier: models.TierFree, MonthlyResetAt: time.Now().AddDate(0, 1, 0)}, nil
		}
	}

	// Reset monthly usage if needed and persist to DB
	if time.Now().After(user.Subscription.MonthlyResetAt) {
		nextReset := time.Now().AddDate(0, 1, 0)
		if err := s.Repo.ResetSubscriptionUsage(userID, nextReset); err != nil {
			return nil, fmt.Errorf("failed to reset subscription usage: %w", err)
		}
		user.Subscription.AllergenAnalysesUsed = 0
		user.Subscription.WebSearchesUsed = 0
		user.Subscription.AIGenerationsUsed = 0
		user.Subscription.VideoImportsUsed = 0
		user.Subscription.AIImportsUsed = 0
		user.Subscription.MonthlyResetAt = nextReset
	}

	return user.Subscription, nil
}

// UpgradeSubscription is the legacy pre-IAP upgrade endpoint. Paid plans are
// now purchased through the App Store / Google Play and verified via
// POST /v1/iap/verify; this stays only so old app builds get an honest error
// instead of a 404.
func (s *SubscriptionService) UpgradeSubscription(userID uint) (*models.Subscription, error) {
	return nil, fmt.Errorf("subscriptions are now handled through the App Store and Google Play — update the app to subscribe")
}

// usageColumn maps a usage type to its subscription counter column.
func usageColumn(usageType string) (string, error) {
	switch usageType {
	case "allergen":
		return "allergen_analyses_used", nil
	case "search":
		return "web_searches_used", nil
	case "ai_generation":
		return "ai_generations_used", nil
	case "video_import":
		return "video_imports_used", nil
	case "ai_import":
		return "ai_imports_used", nil
	default:
		return "", fmt.Errorf("unknown usage type: %s", usageType)
	}
}

// IncrementUsage atomically increments a usage counter in the database.
// Valid usageType values: "allergen", "search", "ai_generation", "video_import", "ai_import".
func (s *SubscriptionService) IncrementUsage(userID uint, usageType string) error {
	column, err := usageColumn(usageType)
	if err != nil {
		return err
	}
	return s.Repo.IncrementSubscriptionUsage(userID, column)
}

// DecrementUsage refunds one unit of a usage counter (floored at zero) — used
// when an action that was counted on acceptance later fails on our side.
func (s *SubscriptionService) DecrementUsage(userID uint, usageType string) error {
	column, err := usageColumn(usageType)
	if err != nil {
		return err
	}
	return s.Repo.DecrementSubscriptionUsage(userID, column)
}

// CheckLimit returns true if the user is within their usage limits for the given type.
func (s *SubscriptionService) CheckLimit(userID uint, usageType string) (bool, error) {
	sub, err := s.GetSubscription(userID)
	if err != nil {
		return false, err
	}

	switch usageType {
	case "allergen":
		return sub.CanUseAllergenAnalysis(), nil
	case "search":
		return sub.CanUseWebSearch(), nil
	case "ai_generation":
		return sub.CanUseAIGeneration(), nil
	case "video_import":
		return sub.CanUseVideoImport(), nil
	case "ai_import":
		return sub.CanUseAIImport(), nil
	default:
		return false, fmt.Errorf("unknown usage type: %s", usageType)
	}
}
