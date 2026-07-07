package models

import (
	"time"

	"gorm.io/gorm"
)

// Store platforms for StoreSubscription.Platform.
const (
	StorePlatformApple  = "apple"
	StorePlatformGoogle = "google"
)

// StoreSubscription statuses. "active" covers auto-renew-off-but-paid-up
// (store-side "canceled") — entitlement is decided by status + ExpiresAt.
const (
	StoreSubActive   = "active"
	StoreSubGrace    = "grace"   // billing issue, store grants continued access
	StoreSubHold     = "hold"    // billing issue, access suspended (google)
	StoreSubPaused   = "paused"  // user-initiated pause (google)
	StoreSubPending  = "pending" // purchase not yet complete (google pending txns)
	StoreSubExpired  = "expired"
	StoreSubRevoked  = "revoked"  // refunded / revoked by the store
	StoreSubReplaced = "replaced" // superseded by a newer purchase token (google upgrades)
)

// TierByProductID maps store product identifiers (identical on the App Store
// and Google Play) to the tier they entitle. Changing a product's tier here
// re-prices existing subscribers' entitlements on their next verification.
var TierByProductID = map[string]SubscriptionTier{
	"sb_plus_monthly":    TierPlus,
	"sb_premium_monthly": TierPremium,
	"sb_premium_yearly":  TierPremium,
}

// TierRank orders tiers for entitlement resolution when a user somehow holds
// multiple store subscriptions (e.g. one per platform): highest rank wins.
func TierRank(t SubscriptionTier) int {
	switch t {
	case TierPlus:
		return 1
	case TierPremium:
		return 2
	case TierUnlimited:
		return 3
	default:
		return 0
	}
}

// StoreSubscription records one store-side subscription (an App Store
// original transaction or a Play purchase token) and its last known state.
// It is the source of truth the user's Subscription.Tier is recomputed from;
// rows are updated by client receipt verification, store server notifications
// and lazy re-verification on expiry.
type StoreSubscription struct {
	gorm.Model
	// UserID is 0 for orphan rows: a store notification arrived for a
	// purchase the client never verified with us. Orphans grant nothing and
	// are claimed on the next client verify (or via the account-token hint).
	UserID    uint             `gorm:"index"`
	Platform  string           `gorm:"type:text;not null;index"`
	ProductID string           `gorm:"not null"`
	Tier      SubscriptionTier `gorm:"type:text;not null"`
	// ExternalKey is the store's stable identity for the subscription: the
	// original transaction ID (apple) or the purchase token (google). Unique
	// so one store purchase can never entitle two SaltyBytes accounts.
	ExternalKey string `gorm:"uniqueIndex;not null;size:512"`
	Status      string `gorm:"type:text;not null"`
	AutoRenew   bool
	ExpiresAt   time.Time
	// Environment is "Production" or "Sandbox" for apple rows ("" google).
	// Sandbox purchases (TestFlight, App Review) grant entitlements normally.
	Environment string `gorm:"type:text"`
	// AppAccountToken is the account hint the app attached at purchase time
	// (our uint user ID encoded as a UUID), echoed back in store payloads.
	AppAccountToken string `gorm:"index"`
	LastVerifiedAt  time.Time
	// LastEventType records what last touched the row (a notification type
	// like DID_RENEW, or "client_verify"/"lazy_refresh") for debugging.
	LastEventType string `gorm:"type:text"`
}

// Entitled reports whether this row currently grants its tier. appleSlack
// extends an auto-renewing apple row past its expiry — without an App Store
// Server API key we cannot re-poll apple, so renewals rely on server
// notifications, which can lag or be missed; the slack keeps paying
// subscribers entitled while we wait. Google rows get no slack (we can always
// re-poll) and neither do canceled rows (no renewal is coming).
func (s *StoreSubscription) Entitled(now time.Time, appleSlack time.Duration) bool {
	if s.Status != StoreSubActive && s.Status != StoreSubGrace {
		return false
	}
	deadline := s.ExpiresAt
	if s.Platform == StorePlatformApple && s.AutoRenew {
		deadline = deadline.Add(appleSlack)
	}
	return now.Before(deadline)
}
