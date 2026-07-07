package iap

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/androidpublisher/v3"
	"google.golang.org/api/option"
)

// GoogleSubscriptionState is the normalized view of a Play subscription
// purchase (purchases.subscriptionsv2.get) that the service layer acts on.
type GoogleSubscriptionState struct {
	ProductID           string
	BasePlanID          string
	State               string // raw subscriptionState enum value
	ExpiresAt           time.Time
	AutoRenew           bool
	LinkedPurchaseToken string // set when this token replaced an older one (plan changes)
	ObfuscatedAccountID string
	NeedsAck            bool
}

// GoogleVerifier verifies Play purchase tokens against the Android Publisher
// API using the Play service account.
type GoogleVerifier struct {
	pkg string
	svc *androidpublisher.Service
}

// NewGoogleVerifier builds a verifier from the service-account JSON key.
func NewGoogleVerifier(ctx context.Context, serviceAccountJSON []byte, packageName string) (*GoogleVerifier, error) {
	svc, err := androidpublisher.NewService(ctx, option.WithCredentialsJSON(serviceAccountJSON))
	if err != nil {
		return nil, fmt.Errorf("android publisher client: %w", err)
	}
	return &GoogleVerifier{pkg: packageName, svc: svc}, nil
}

// GetSubscription fetches the current state of a purchase token. The token is
// the single source of truth — RTDN events and client verifies both funnel
// through this same lookup.
func (g *GoogleVerifier) GetSubscription(ctx context.Context, purchaseToken string) (*GoogleSubscriptionState, error) {
	sub, err := g.svc.Purchases.Subscriptionsv2.Get(g.pkg, purchaseToken).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("subscriptionsv2.get: %w", err)
	}

	state := &GoogleSubscriptionState{
		State:               sub.SubscriptionState,
		LinkedPurchaseToken: sub.LinkedPurchaseToken,
		NeedsAck:            sub.AcknowledgementState == "ACKNOWLEDGEMENT_STATE_PENDING",
	}
	if sub.ExternalAccountIdentifiers != nil {
		state.ObfuscatedAccountID = sub.ExternalAccountIdentifiers.ObfuscatedExternalAccountId
	}
	// A subscription purchase carries one line item per product; plan changes
	// issue a fresh token. Take the line item with the latest expiry to be
	// safe with any multi-item edge case.
	for _, item := range sub.LineItems {
		expires, _ := time.Parse(time.RFC3339, item.ExpiryTime)
		if expires.After(state.ExpiresAt) {
			state.ExpiresAt = expires
			state.ProductID = item.ProductId
			if item.OfferDetails != nil {
				state.BasePlanID = item.OfferDetails.BasePlanId
			}
			state.AutoRenew = item.AutoRenewingPlan != nil && item.AutoRenewingPlan.AutoRenewEnabled
		}
	}
	if state.ProductID == "" {
		return nil, fmt.Errorf("purchase has no line items (state %s)", sub.SubscriptionState)
	}
	return state, nil
}

// Acknowledge acknowledges a subscription purchase server-side. The app also
// acknowledges via completePurchase; this is the belt-and-braces path so a
// purchase verified with us never gets auto-refunded for lack of an ack.
func (g *GoogleVerifier) Acknowledge(ctx context.Context, productID, purchaseToken string) error {
	return g.svc.Purchases.Subscriptions.Acknowledge(g.pkg, productID, purchaseToken,
		&androidpublisher.SubscriptionPurchasesAcknowledgeRequest{}).Context(ctx).Do()
}

// Google subscriptionState values we branch on (the rest map to "not
// entitled" conservatively).
const (
	GoogleStateActive   = "SUBSCRIPTION_STATE_ACTIVE"
	GoogleStateCanceled = "SUBSCRIPTION_STATE_CANCELED"
	GoogleStateGrace    = "SUBSCRIPTION_STATE_IN_GRACE_PERIOD"
	GoogleStateOnHold   = "SUBSCRIPTION_STATE_ON_HOLD"
	GoogleStatePaused   = "SUBSCRIPTION_STATE_PAUSED"
	GoogleStateExpired  = "SUBSCRIPTION_STATE_EXPIRED"
	GoogleStatePending  = "SUBSCRIPTION_STATE_PENDING"
)
