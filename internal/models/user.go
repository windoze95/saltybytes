package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User is the model for a user.
type User struct {
	gorm.Model
	Username  string `gorm:"unique;index"`
	FirstName string `gorm:"default:null"`
	Email     string `gorm:"unique;default:null"`
	// EmailVerifiedAt is set when the user proves ownership of Email (or
	// immediately at signup while email verification is disabled). Nil means
	// unverified: AI-cost endpoints are gated and a stale signup releases its
	// email address for reuse.
	EmailVerifiedAt  *time.Time
	Auth             *UserAuth        `gorm:"foreignKey:UserID"`
	Subscription     *Subscription    `gorm:"foreignKey:UserID"`
	Settings         *UserSettings    `gorm:"foreignKey:UserID"`
	Personalization  *Personalization `gorm:"foreignKey:UserID"`
	CollectedRecipes []*Recipe        `gorm:"many2many:user_collected_recipes;"`
}

// EmailVerified reports whether the user's email address is verified.
func (u *User) EmailVerified() bool {
	return u.EmailVerifiedAt != nil
}

// EmailVerification holds the pending 6-digit signup verification code for a
// user. One row per user; resends overwrite it. The code itself is stored
// bcrypt-hashed so a database leak can't be replayed.
type EmailVerification struct {
	gorm.Model
	UserID    uint   `gorm:"uniqueIndex;not null"`
	CodeHash  string `gorm:"not null"`
	ExpiresAt time.Time
	// Attempts counts wrong codes entered for the current code; capped so a
	// 6-digit space can't be brute-forced.
	Attempts int `gorm:"default:0"`
	// SendCount counts emails sent during the UTC day of LastSentAt,
	// bounding daily sends per user.
	SendCount  int `gorm:"default:0"`
	LastSentAt time.Time
}

// UserAuth is the model for a user's authentication information.
type UserAuth struct {
	gorm.Model
	UserID         uint `gorm:"unique;index"`
	HashedPassword string
	AuthType       UserAuthType `gorm:"type:text"`
	// TokenVersion is embedded in refresh tokens as the "token_version" claim.
	// Incrementing it (e.g. on logout) revokes all outstanding refresh tokens.
	// Defaults to 0 so refresh tokens issued before this field existed (which
	// carry no claim) remain valid.
	TokenVersion int `gorm:"default:0"`
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
	TierPlus    SubscriptionTier = "plus"
	TierPremium SubscriptionTier = "premium"
	// TierUnlimited is the hidden operator tier: no caps at all. It is never
	// offered as an upgrade option anywhere (not in the app, not via the
	// upgrade endpoint) — it is assigned manually, directly in the database,
	// for select accounts. The app-wide daily AI budget is its only bound.
	TierUnlimited SubscriptionTier = "unlimited"
)

// TierLimits is a tier's monthly allowance per metered feature. -1 means
// unlimited.
type TierLimits struct {
	AIGenerations    int `json:"ai_generations"`
	WebSearches      int `json:"web_searches"`
	AllergenAnalyses int `json:"allergen_analyses"`
	VideoImports     int `json:"video_imports"`
	AIImports        int `json:"ai_imports"`
}

// limitsByTier sets each tier's caps so a subscriber maxing every counter
// still costs less than the tier's net revenue (after the app-store cut),
// using worst-case per-op model costs: generation/fork/regen ≈ $0.05
// (Sonnet), agent search ≈ $0.012 (Flash + CSE), allergen ≈ $0.02 (Sonnet),
// video ≈ $0.02 blended (native Gemini; frame fallback is rare and bounded
// by the video daily budget), AI import ≈ $0.012 (Flash vision / Whisper).
// Free maxes out around $0.83/mo (acquisition cost), plus ≈ $1.43 against
// ~$1.40 net of $1.99, premium ≈ $3.46 against ~$3.49 net of $4.99.
var limitsByTier = map[SubscriptionTier]TierLimits{
	TierFree:      {AIGenerations: 10, WebSearches: 10, AllergenAnalyses: 3, VideoImports: 1, AIImports: 10},
	TierPlus:      {AIGenerations: 15, WebSearches: 20, AllergenAnalyses: 5, VideoImports: 2, AIImports: 25},
	TierPremium:   {AIGenerations: 30, WebSearches: 50, AllergenAnalyses: 12, VideoImports: 20, AIImports: 60},
	TierUnlimited: {AIGenerations: -1, WebSearches: -1, AllergenAnalyses: -1, VideoImports: -1, AIImports: -1},
}

// LimitsForTier returns the caps for a tier, defaulting unknown tiers to
// free-tier limits (fail-closed).
func LimitsForTier(tier SubscriptionTier) TierLimits {
	if l, ok := limitsByTier[tier]; ok {
		return l
	}
	return limitsByTier[TierFree]
}

// withinLimit reports whether used is under the cap; -1 means no cap.
func withinLimit(used, limit int) bool {
	return limit < 0 || used < limit
}

// Subscription is the model for a user's subscription.
type Subscription struct {
	gorm.Model
	UserID               uint             `gorm:"uniqueIndex;not null"`
	Tier                 SubscriptionTier `gorm:"type:text;default:'free'"`
	ExpiresAt            *time.Time
	AllergenAnalysesUsed int `gorm:"default:0"`
	WebSearchesUsed      int `gorm:"default:0"`
	AIGenerationsUsed    int `gorm:"default:0"`
	VideoImportsUsed     int `gorm:"default:0"`
	// AIImportsUsed meters the AI-powered import paths (photo, files, voice,
	// text). URL/manual imports stay unmetered — they're cache-heavy and
	// cheap.
	AIImportsUsed  int `gorm:"default:0"`
	MonthlyResetAt time.Time
}

// Limits returns this subscription's tier caps.
func (s *Subscription) Limits() TierLimits {
	return LimitsForTier(s.Tier)
}

// CanUseAllergenAnalysis checks if the user can use allergen analysis.
func (s *Subscription) CanUseAllergenAnalysis() bool {
	return withinLimit(s.AllergenAnalysesUsed, s.Limits().AllergenAnalyses)
}

// CanUseWebSearch checks if the user can use web/agent search.
func (s *Subscription) CanUseWebSearch() bool {
	return withinLimit(s.WebSearchesUsed, s.Limits().WebSearches)
}

// CanUseAIGeneration checks if the user can use AI generation (generate,
// regenerate, fork — the flagship-model calls).
func (s *Subscription) CanUseAIGeneration() bool {
	return withinLimit(s.AIGenerationsUsed, s.Limits().AIGenerations)
}

// CanUseVideoImport checks if the user can import a recipe from a video link.
func (s *Subscription) CanUseVideoImport() bool {
	return withinLimit(s.VideoImportsUsed, s.Limits().VideoImports)
}

// CanUseAIImport checks if the user can run an AI-powered import
// (photo/files/voice/text).
func (s *Subscription) CanUseAIImport() bool {
	return withinLimit(s.AIImportsUsed, s.Limits().AIImports)
}

// IsPremiumGrade reports whether the tier gets premium-quality treatment
// (e.g. the deeper allergen analysis): premium and the hidden unlimited
// tier. Plus is a budget tier and stays on standard quality.
func (s *Subscription) IsPremiumGrade() bool {
	return s.Tier == TierPremium || s.Tier == TierUnlimited
}

// IsValidSubscriptionTier checks if the SubscriptionTier is valid.
func (s *Subscription) IsValidSubscriptionTier() bool {
	switch s.Tier {
	case TierFree, TierPlus, TierPremium, TierUnlimited:
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
	UserID         uint   `gorm:"unique;index"`
	UnitSystem     string `gorm:"type:text;default:'us_customary'"`
	Requirements   string // Additional instructions or guidelines
	CookingContext string `json:"cooking_context" gorm:"type:text"` // free-form cooking preferences injected into AI prompts
	UID            uuid.UUID
}

// PersonalizationUpdate carries a partial update to a user's Personalization.
// Nil fields are left unchanged.
type PersonalizationUpdate struct {
	UnitSystem     *string
	Requirements   *string
	CookingContext *string
	UID            *uuid.UUID
}

// UnitSystemText returns the display text for the current UnitSystem.
func (p *Personalization) UnitSystemText() string {
	switch p.UnitSystem {
	case "metric":
		return "Metric"
	default:
		return "US Customary"
	}
}

// CookingContextPrompt returns the cooking context formatted for AI prompt injection.
// Returns empty string if no context is set.
func (p *Personalization) CookingContextPrompt() string {
	if p.CookingContext == "" {
		return ""
	}
	return fmt.Sprintf("Additional context about the user's kitchen and preferences: %s", p.CookingContext)
}

// BeforeCreate is a GORM hook that runs before creating a new user Personalization.
func (p *Personalization) BeforeCreate(tx *gorm.DB) (err error) {
	if p.UnitSystem != "us_customary" && p.UnitSystem != "metric" {
		p.UnitSystem = "us_customary"
	}
	return nil
}

// BeforeUpdate is a GORM hook that runs before updating a user Personalization.
func (p *Personalization) BeforeUpdate(tx *gorm.DB) (err error) {
	if p.UnitSystem != "us_customary" && p.UnitSystem != "metric" {
		p.UnitSystem = "us_customary"
	}
	return nil
}
