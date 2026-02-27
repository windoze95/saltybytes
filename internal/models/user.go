package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User is the model for a user.
type User struct {
	gorm.Model
	Username  string    `gorm:"unique;index"`
	FirstName string    `gorm:"default:null"`
	Email     string    `gorm:"unique;default:null"`
	Auth         *UserAuth     `gorm:"foreignKey:UserID"`
	Subscription *Subscription `gorm:"foreignKey:UserID"`
	Settings         *UserSettings    `gorm:"foreignKey:UserID"`
	Personalization  *Personalization `gorm:"foreignKey:UserID"`
	CollectedRecipes []*Recipe        `gorm:"many2many:user_collected_recipes;"`
}

// UserAuth is the model for a user's authentication information.
type UserAuth struct {
	gorm.Model
	UserID         uint `gorm:"unique;index"`
	HashedPassword string
	AuthType       UserAuthType `gorm:"type:text"`
}

// UserAuthType is the type for the UserAuthType enum.
type UserAuthType string

// UserAuthType enum values.
const (
	Standard UserAuthType = "standard"
)

// IsValidAuthType checks if the AuthType is valid.
func (ua *UserAuth) IsValidAuthType() bool {
	switch ua.AuthType {
	case Standard:
		return true
	default:
		return false
	}
}

// BeforeCreate is a GORM hook that runs before creating a new UserAuth.
func (ua *UserAuth) BeforeCreate(tx *gorm.DB) (err error) {
	if !ua.IsValidAuthType() {
		// Cancel transaction
		return errors.New("invalid AuthType provided")
	}

	return nil
}

// BeforeUpdate is a GORM hook that runs before updating a UserAuth.
func (ua *UserAuth) BeforeUpdate(tx *gorm.DB) (err error) {
	if !ua.IsValidAuthType() {
		// Cancel transaction
		return errors.New("invalid AuthType provided")
	}

	return nil
}

// SubscriptionTier is the type for the SubscriptionTier enum.
type SubscriptionTier string

// SubscriptionTier enum values.
const (
	TierFree    SubscriptionTier = "free"
	TierPremium SubscriptionTier = "premium"
)

// Subscription is the model for a user's subscription.
type Subscription struct {
	gorm.Model
	UserID               uint             `gorm:"uniqueIndex;not null"`
	Tier                 SubscriptionTier `gorm:"type:text;default:'free'"`
	ExpiresAt            *time.Time
	AllergenAnalysesUsed int              `gorm:"default:0"`
	WebSearchesUsed      int              `gorm:"default:0"`
	AIGenerationsUsed    int              `gorm:"default:0"`
	MonthlyResetAt       time.Time
}

// CanUseAllergenAnalysis checks if the user can use allergen analysis.
func (s *Subscription) CanUseAllergenAnalysis() bool {
	if s.Tier == TierPremium {
		return true
	}
	return s.AllergenAnalysesUsed < 5
}

// CanUseWebSearch checks if the user can use web search.
func (s *Subscription) CanUseWebSearch() bool {
	if s.Tier == TierPremium {
		return true
	}
	return s.WebSearchesUsed < 20
}

// CanUseAIGeneration checks if the user can use AI generation.
func (s *Subscription) CanUseAIGeneration() bool {
	if s.Tier == TierPremium {
		return true
	}
	return s.AIGenerationsUsed < 50
}

// IsValidSubscriptionTier checks if the SubscriptionTier is valid.
func (s *Subscription) IsValidSubscriptionTier() bool {
	switch s.Tier {
	case TierFree, TierPremium:
		return true
	default:
		return false
	}
}

// BeforeCreate is a GORM hook that runs before creating a new user Subscription.
func (s *Subscription) BeforeCreate(tx *gorm.DB) (err error) {
	if !s.IsValidSubscriptionTier() {
		s.Tier = TierFree
	}

	return nil
}

// BeforeUpdate is a GORM hook that runs before updating a user Subscription.
func (s *Subscription) BeforeUpdate(tx *gorm.DB) (err error) {
	if !s.IsValidSubscriptionTier() {
		return errors.New("invalid SubscriptionTier provided")
	}

	return nil
}

// UserSettings is the model for a user's settings.
type UserSettings struct {
	gorm.Model
	UserID          uint `gorm:"unique;index"`
	KeepScreenAwake bool `gorm:"default:true"`
}

// Personalization is the model for a user's personalization settings.
type Personalization struct {
	gorm.Model
	UserID       uint       `gorm:"unique;index"`
	UnitSystem   UnitSystem `gorm:"type:int"`
	Requirements string     // Additional instructions or guidelines
	UID          uuid.UUID
}

// UnitSystem is the type for the UnitSystem enum.
type UnitSystem int

// UnitSystem enum values.
const (
	USCustomary     UnitSystem       = iota // 0 - US Customary
	Metric                                  // 1 - Metric
	USCustomaryText = "US Customary"        // 0 - US Customary
	MetricText      = "Metric"              // 1 - Metric
)

// IsValidUnitSystem checks if the UnitSystem is valid.
func (p *Personalization) IsValidUnitSystem() bool {
	switch p.UnitSystem {
	case USCustomary, Metric:
		return true
	default:
		return false
	}
}

// GetUnitSystemText returns the text representation of the UnitSystem.
func (p *Personalization) GetUnitSystemText() string {
	switch p.UnitSystem {
	case USCustomary:
		return USCustomaryText
	case Metric:
		return MetricText
	default:
		return USCustomaryText
	}
}

// BeforeCreate is a GORM hook that runs before creating a new user Personalization.
func (p *Personalization) BeforeCreate(tx *gorm.DB) (err error) {
	if !p.IsValidUnitSystem() {
		// Set default
		p.UnitSystem = USCustomary
	}

	return nil
}

// BeforeUpdate is a GORM hook that runs before updating a user Personalization.
func (p *Personalization) BeforeUpdate(tx *gorm.DB) (err error) {
	if !p.IsValidUnitSystem() {
		// Set default
		p.UnitSystem = USCustomary
	}

	return nil
}
