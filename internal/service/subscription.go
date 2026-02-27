package service

import (
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// SubscriptionService handles subscription management and usage limits.
type SubscriptionService struct {
	Cfg  *config.Config
	Repo *repository.UserRepository
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(cfg *config.Config, repo *repository.UserRepository) *SubscriptionService {
	return &SubscriptionService{
		Cfg:  cfg,
		Repo: repo,
	}
}

// GetSubscription retrieves the subscription for a user.
func (s *SubscriptionService) GetSubscription(userID uint) (*models.Subscription, error) {
	user, err := s.Repo.GetUserByID(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if user.Subscription == nil {
		return &models.Subscription{
			UserID: userID,
			Tier:   models.TierFree,
		}, nil
	}

	// Reset monthly usage if needed
	if time.Now().After(user.Subscription.MonthlyResetAt) {
		user.Subscription.AllergenAnalysesUsed = 0
		user.Subscription.WebSearchesUsed = 0
		user.Subscription.AIGenerationsUsed = 0
		user.Subscription.MonthlyResetAt = time.Now().AddDate(0, 1, 0)
	}

	return user.Subscription, nil
}

// UpgradeSubscription upgrades a user to premium (placeholder for payment integration).
func (s *SubscriptionService) UpgradeSubscription(userID uint) (*models.Subscription, error) {
	sub, err := s.GetSubscription(userID)
	if err != nil {
		return nil, err
	}

	sub.Tier = models.TierPremium
	expires := time.Now().AddDate(0, 1, 0)
	sub.ExpiresAt = &expires

	return sub, nil
}

// IncrementUsage increments a usage counter for the given type.
// Valid usageType values: "allergen", "search", "ai_generation".
func (s *SubscriptionService) IncrementUsage(userID uint, usageType string) error {
	sub, err := s.GetSubscription(userID)
	if err != nil {
		return err
	}

	switch usageType {
	case "allergen":
		sub.AllergenAnalysesUsed++
	case "search":
		sub.WebSearchesUsed++
	case "ai_generation":
		sub.AIGenerationsUsed++
	default:
		return fmt.Errorf("unknown usage type: %s", usageType)
	}

	return nil
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
	default:
		return false, fmt.Errorf("unknown usage type: %s", usageType)
	}
}
