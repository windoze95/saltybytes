package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/iap"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/notify"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"go.uber.org/zap"
)

// Typed IAP errors the handler maps to HTTP statuses/error codes.
var (
	ErrIAPUnavailable     = errors.New("store verification is not configured")
	ErrIAPVerification    = errors.New("purchase verification failed")
	ErrIAPUnknownProduct  = errors.New("unknown product")
	ErrIAPLinkedElsewhere = errors.New("subscription is linked to another account")
)

// AppleIAPVerifier verifies Apple-signed JWS data. Satisfied by
// *iap.AppleVerifier; faked in tests.
type AppleIAPVerifier interface {
	VerifyTransaction(jws string) (*iap.AppleTransaction, error)
	VerifyRenewalInfo(jws string) (*iap.AppleRenewalInfo, error)
	VerifyNotification(signedPayload string) (*iap.AppleNotification, *iap.AppleTransaction, *iap.AppleRenewalInfo, error)
}

// GooglePlayVerifier verifies Play purchase tokens. Satisfied by
// *iap.GoogleVerifier; faked in tests.
type GooglePlayVerifier interface {
	GetSubscription(ctx context.Context, purchaseToken string) (*iap.GoogleSubscriptionState, error)
	Acknowledge(ctx context.Context, productID, purchaseToken string) error
}

// AppleStatusPoller re-polls apple subscription state (App Store Server API).
// Optional; nil means apple renewals rely on server notifications alone.
type AppleStatusPoller interface {
	LatestTransaction(ctx context.Context, originalTransactionID, environment string) (txnJWS, renewalJWS string, err error)
}

// StoreView is the client-facing summary of the store subscription backing
// the user's current tier.
type StoreView struct {
	Platform    string    `json:"platform"`
	ProductID   string    `json:"product_id"`
	Status      string    `json:"status"`
	AutoRenew   bool      `json:"auto_renew"`
	ExpiresAt   time.Time `json:"expires_at"`
	Environment string    `json:"environment,omitempty"`
}

// IAPService verifies store purchases (client receipts and store server
// notifications) and keeps each user's Subscription.Tier in sync with their
// store-side entitlements.
type IAPService struct {
	Cfg   *config.Config
	Users repository.UserRepo
	Store repository.StoreSubscriptionRepo
	Subs  *SubscriptionService

	Apple       AppleIAPVerifier
	Google      GooglePlayVerifier
	ApplePoller AppleStatusPoller

	// now is a test seam.
	now func() time.Time
}

// NewIAPService creates a new IAPService.
func NewIAPService(cfg *config.Config, users repository.UserRepo, store repository.StoreSubscriptionRepo, subs *SubscriptionService) *IAPService {
	return &IAPService{
		Cfg:   cfg,
		Users: users,
		Store: store,
		Subs:  subs,
		now:   time.Now,
	}
}

// appleSlack is how long an auto-renewing apple subscription stays entitled
// past its expiry. Without the App Store Server API we cannot re-poll apple —
// renewals arrive via server notifications, which can lag or be missed — so
// the slack bridges the gap. With a poller configured we only cover delivery
// jitter.
func (s *IAPService) appleSlack() time.Duration {
	if s.ApplePoller != nil {
		return 15 * time.Minute
	}
	return 72 * time.Hour
}

// VerifyPurchase verifies a purchase the app just made (or restored) and
// binds it to the user. verificationData is the StoreKit 2 signed transaction
// JWS (apple) or the Play purchase token (google). Returns the user's updated
// subscription.
func (s *IAPService) VerifyPurchase(userID uint, platform, productID, verificationData string) (*models.Subscription, error) {
	switch platform {
	case models.StorePlatformApple:
		if err := s.verifyApplePurchase(userID, verificationData); err != nil {
			return nil, err
		}
	case models.StorePlatformGoogle:
		if err := s.verifyGooglePurchase(userID, verificationData); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: platform %q", ErrIAPVerification, platform)
	}

	if err := s.RecomputeEntitlement(userID); err != nil {
		return nil, err
	}
	return s.Subs.GetSubscription(userID)
}

func (s *IAPService) verifyApplePurchase(userID uint, jws string) error {
	if s.Apple == nil {
		return ErrIAPUnavailable
	}
	txn, err := s.Apple.VerifyTransaction(jws)
	if err != nil {
		logger.Get().Warn("apple transaction verification failed", zap.Uint("user_id", userID), zap.Error(err))
		return fmt.Errorf("%w: %v", ErrIAPVerification, err)
	}
	tier, ok := models.TierByProductID[txn.ProductID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrIAPUnknownProduct, txn.ProductID)
	}

	status := models.StoreSubActive
	switch {
	case txn.Revoked():
		status = models.StoreSubRevoked
	case txn.ExpiresAt().IsZero() || !txn.ExpiresAt().After(s.now()):
		status = models.StoreSubExpired
	}

	// A lone SK2 transaction doesn't carry auto-renew state (that lives in
	// renewal info); assume renewing and let notifications correct it.
	_, err = s.upsertStoreSub(userID, &models.StoreSubscription{
		Platform:        models.StorePlatformApple,
		ProductID:       txn.ProductID,
		Tier:            tier,
		ExternalKey:     txn.OriginalTransactionID,
		Status:          status,
		AutoRenew:       status == models.StoreSubActive,
		ExpiresAt:       txn.ExpiresAt(),
		Environment:     txn.Environment,
		AppAccountToken: txn.AppAccountToken,
		LastEventType:   "client_verify",
	})
	return err
}

func (s *IAPService) verifyGooglePurchase(userID uint, purchaseToken string) error {
	if s.Google == nil {
		return ErrIAPUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.refreshGoogleToken(ctx, purchaseToken, userID, "client_verify")
}

// refreshGoogleToken re-reads a purchase token from Google and applies the
// result. hintUserID binds the row when it is new or an orphan (0 = no hint).
func (s *IAPService) refreshGoogleToken(ctx context.Context, purchaseToken string, hintUserID uint, eventType string) error {
	state, err := s.Google.GetSubscription(ctx, purchaseToken)
	if err != nil {
		logger.Get().Warn("google purchase verification failed", zap.Uint("user_id", hintUserID), zap.Error(err))
		return fmt.Errorf("%w: %v", ErrIAPVerification, err)
	}
	tier, ok := models.TierByProductID[state.ProductID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrIAPUnknownProduct, state.ProductID)
	}

	var status string
	switch state.State {
	case iap.GoogleStateActive, iap.GoogleStateCanceled:
		// Canceled means auto-renew is off but the paid period still runs;
		// entitlement is decided by ExpiresAt.
		status = models.StoreSubActive
	case iap.GoogleStateGrace:
		status = models.StoreSubGrace
	case iap.GoogleStateOnHold:
		status = models.StoreSubHold
	case iap.GoogleStatePaused:
		status = models.StoreSubPaused
	case iap.GoogleStatePending:
		status = models.StoreSubPending
	case iap.GoogleStateExpired:
		status = models.StoreSubExpired
	default:
		status = models.StoreSubExpired
	}

	row, err := s.upsertStoreSub(hintUserID, &models.StoreSubscription{
		Platform:        models.StorePlatformGoogle,
		ProductID:       state.ProductID,
		Tier:            tier,
		ExternalKey:     purchaseToken,
		Status:          status,
		AutoRenew:       state.AutoRenew,
		ExpiresAt:       state.ExpiresAt,
		AppAccountToken: state.ObfuscatedAccountID,
		LastEventType:   eventType,
	})
	if err != nil {
		return err
	}

	// Plan changes issue a new token that references the one it replaced;
	// retire the old row so it can't grant anything.
	if state.LinkedPurchaseToken != "" {
		if old, err := s.Store.GetByExternalKey(state.LinkedPurchaseToken); err == nil && old != nil && old.Status != models.StoreSubReplaced {
			old.Status = models.StoreSubReplaced
			old.LastEventType = "replaced_by_new_token"
			if err := s.Store.Save(old); err != nil {
				logger.Get().Warn("failed to retire replaced purchase token", zap.Error(err))
			}
			if old.UserID != 0 && old.UserID != row.UserID {
				// Extremely unlikely, but keep the old owner's tier honest.
				if err := s.RecomputeEntitlement(old.UserID); err != nil {
					logger.Get().Warn("failed to recompute entitlement for replaced-token owner", zap.Error(err))
				}
			}
		}
	}

	// Server-side acknowledge so an unacknowledged purchase is never
	// auto-refunded by Google (the app acknowledges too; both are idempotent).
	if state.NeedsAck && status == models.StoreSubActive {
		if err := s.Google.Acknowledge(ctx, state.ProductID, purchaseToken); err != nil {
			logger.Get().Warn("google acknowledge failed (app-side ack should cover it)", zap.Error(err))
		}
	}
	return nil
}

// upsertStoreSub creates or updates the row identified by next.ExternalKey,
// enforcing the one-purchase-one-account rule and claiming orphans. Returns
// the persisted row.
func (s *IAPService) upsertStoreSub(userID uint, next *models.StoreSubscription) (*models.StoreSubscription, error) {
	existing, err := s.Store.GetByExternalKey(next.ExternalKey)
	if err != nil {
		return nil, fmt.Errorf("failed to look up store subscription: %w", err)
	}

	if existing == nil {
		// Brand-new purchase. If the caller has no user (webhook-first
		// arrival), try the account token the app attached at purchase time.
		if userID == 0 {
			if id, ok := iap.UserForAccountToken(next.AppAccountToken); ok {
				userID = id
			}
		}
		next.UserID = userID
		next.LastVerifiedAt = s.now()
		if err := s.Store.Save(next); err != nil {
			return nil, fmt.Errorf("failed to save store subscription: %w", err)
		}
		if next.UserID != 0 && next.Status == models.StoreSubActive {
			notify.Alert("iap-new-"+next.ExternalKey,
				fmt.Sprintf("💰 New %s subscriber", next.Tier),
				fmt.Sprintf("%s via %s (%s)", next.ProductID, next.Platform, next.Environment),
				s.Cfg.EnvVars.DashboardURL, 24*time.Hour)
		}
		return next, nil
	}

	// The store identity is already known: it must not move between accounts.
	switch {
	case existing.UserID == 0 && userID != 0:
		existing.UserID = userID // claim the orphan
	case userID != 0 && existing.UserID != userID:
		logger.Get().Warn("store subscription bound to another account",
			zap.Uint("bound_user", existing.UserID), zap.Uint("claimant", userID),
			zap.String("platform", next.Platform))
		return nil, ErrIAPLinkedElsewhere
	}

	prevStatus := existing.Status
	existing.ProductID = next.ProductID
	existing.Tier = next.Tier
	existing.Status = next.Status
	existing.AutoRenew = next.AutoRenew
	existing.ExpiresAt = next.ExpiresAt
	if next.Environment != "" {
		existing.Environment = next.Environment
	}
	if next.AppAccountToken != "" {
		existing.AppAccountToken = next.AppAccountToken
	}
	existing.LastEventType = next.LastEventType
	existing.LastVerifiedAt = s.now()
	if err := s.Store.Save(existing); err != nil {
		return nil, fmt.Errorf("failed to update store subscription: %w", err)
	}

	if prevStatus != models.StoreSubRevoked && existing.Status == models.StoreSubRevoked {
		notify.Alert("iap-revoked-"+existing.ExternalKey,
			"⚠️ Subscription refunded/revoked",
			fmt.Sprintf("user %d, %s via %s", existing.UserID, existing.ProductID, existing.Platform),
			s.Cfg.EnvVars.DashboardURL, 24*time.Hour)
	}
	return existing, nil
}

// RecomputeEntitlement re-derives Subscription.Tier/ExpiresAt from the user's
// store subscriptions: highest entitled tier wins, free when none. The hidden
// operator tier (unlimited) is never touched.
func (s *IAPService) RecomputeEntitlement(userID uint) error {
	user, err := s.Users.GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("failed to load user for entitlement recompute: %w", err)
	}
	if user.Subscription != nil && user.Subscription.Tier == models.TierUnlimited {
		return nil
	}
	if user.Subscription == nil {
		// Ensure the row exists so the tier update below has a target.
		if _, err := s.Subs.GetSubscription(userID); err != nil {
			return err
		}
	}

	rows, err := s.Store.ListByUser(userID)
	if err != nil {
		return fmt.Errorf("failed to list store subscriptions: %w", err)
	}

	now := s.now()
	best := models.TierFree
	var bestExpiry *time.Time
	for i := range rows {
		row := &rows[i]
		if !row.Entitled(now, s.appleSlack()) {
			continue
		}
		switch {
		case models.TierRank(row.Tier) > models.TierRank(best):
			best = row.Tier
			exp := row.ExpiresAt
			bestExpiry = &exp
		case models.TierRank(row.Tier) == models.TierRank(best) && bestExpiry != nil && row.ExpiresAt.After(*bestExpiry):
			exp := row.ExpiresAt
			bestExpiry = &exp
		}
	}

	if err := s.Users.UpdateSubscriptionTier(userID, best, bestExpiry); err != nil {
		return fmt.Errorf("failed to update subscription tier: %w", err)
	}
	return nil
}

// StoreStateForUser returns the store subscription backing the user's tier
// (highest-ranked entitled row), or nil when the user has none.
func (s *IAPService) StoreStateForUser(userID uint) *StoreView {
	rows, err := s.Store.ListByUser(userID)
	if err != nil || len(rows) == 0 {
		return nil
	}
	now := s.now()
	var best *models.StoreSubscription
	for i := range rows {
		row := &rows[i]
		if !row.Entitled(now, s.appleSlack()) {
			continue
		}
		if best == nil || models.TierRank(row.Tier) > models.TierRank(best.Tier) ||
			(models.TierRank(row.Tier) == models.TierRank(best.Tier) && row.ExpiresAt.After(best.ExpiresAt)) {
			best = row
		}
	}
	if best == nil {
		return nil
	}
	return &StoreView{
		Platform:    best.Platform,
		ProductID:   best.ProductID,
		Status:      best.Status,
		AutoRenew:   best.AutoRenew,
		ExpiresAt:   best.ExpiresAt,
		Environment: best.Environment,
	}
}

// HandleAppleNotification processes an App Store Server Notification V2
// signedPayload. Signature failures return an error (the handler responds
// non-2xx so Apple retries and we notice); unknown types are logged and
// acknowledged.
func (s *IAPService) HandleAppleNotification(signedPayload string) error {
	if s.Apple == nil {
		return ErrIAPUnavailable
	}
	notif, txn, renewal, err := s.Apple.VerifyNotification(signedPayload)
	if err != nil {
		notify.Alert("iap-apple-bad-signature", "⚠️ Apple webhook signature failure",
			err.Error(), s.Cfg.EnvVars.DashboardURL, time.Hour)
		return fmt.Errorf("%w: %v", ErrIAPVerification, err)
	}

	logger.Get().Info("apple server notification",
		zap.String("type", notif.NotificationType), zap.String("subtype", notif.Subtype))

	if notif.NotificationType == "TEST" || txn == nil {
		return nil
	}
	tier, ok := models.TierByProductID[txn.ProductID]
	if !ok {
		logger.Get().Warn("apple notification for unknown product", zap.String("product_id", txn.ProductID))
		return nil
	}

	status := models.StoreSubActive
	expiresAt := txn.ExpiresAt()
	autoRenew := renewal != nil && renewal.AutoRenewStatus == 1

	switch notif.NotificationType {
	case "EXPIRED", "GRACE_PERIOD_EXPIRED":
		status = models.StoreSubExpired
	case "REFUND", "REVOKE":
		status = models.StoreSubRevoked
	case "DID_FAIL_TO_RENEW":
		if notif.Subtype == "GRACE_PERIOD" {
			status = models.StoreSubGrace
			if renewal != nil {
				if grace := msToTimeSafe(renewal.GracePeriodExpiresDateMS); !grace.IsZero() {
					expiresAt = grace
				}
			}
		} else {
			// Billing retry without grace: access lapses at expiry.
			status = models.StoreSubActive
		}
	default:
		// SUBSCRIBED, DID_RENEW, DID_CHANGE_RENEWAL_STATUS,
		// DID_CHANGE_RENEWAL_PREF, OFFER_REDEEMED, RENEWAL_EXTENDED, ... —
		// trust the transaction's own dates/revocation.
		if txn.Revoked() {
			status = models.StoreSubRevoked
		} else if !expiresAt.IsZero() && !expiresAt.After(s.now()) {
			status = models.StoreSubExpired
		}
	}

	row, err := s.upsertStoreSub(0, &models.StoreSubscription{
		Platform:        models.StorePlatformApple,
		ProductID:       txn.ProductID,
		Tier:            tier,
		ExternalKey:     txn.OriginalTransactionID,
		Status:          status,
		AutoRenew:       autoRenew,
		ExpiresAt:       expiresAt,
		Environment:     txn.Environment,
		AppAccountToken: txn.AppAccountToken,
		LastEventType:   notif.NotificationType,
	})
	if err != nil {
		return err
	}
	if row.UserID != 0 {
		return s.RecomputeEntitlement(row.UserID)
	}
	return nil
}

// rtdnEnvelope is the Pub/Sub push wrapper around a Play developer
// notification.
type rtdnEnvelope struct {
	Message struct {
		Data      string `json:"data"`
		MessageID string `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

// rtdnNotification is the decoded Play developer notification payload.
type rtdnNotification struct {
	Version                  string `json:"version"`
	PackageName              string `json:"packageName"`
	SubscriptionNotification *struct {
		Version          string `json:"version"`
		NotificationType int    `json:"notificationType"`
		PurchaseToken    string `json:"purchaseToken"`
		SubscriptionID   string `json:"subscriptionId"`
	} `json:"subscriptionNotification"`
	VoidedPurchaseNotification *struct {
		PurchaseToken string `json:"purchaseToken"`
		OrderID       string `json:"orderId"`
		ProductType   int    `json:"productType"`
	} `json:"voidedPurchaseNotification"`
	TestNotification *struct {
		Version string `json:"version"`
	} `json:"testNotification"`
}

// HandleGoogleRTDN processes a Play Real-time Developer Notification
// delivered by Pub/Sub push. Returns (retryable, error): non-retryable
// problems (malformed message, wrong package) are dropped with a 200 so
// Pub/Sub doesn't redeliver them forever.
func (s *IAPService) HandleGoogleRTDN(body []byte) (bool, error) {
	var env rtdnEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return false, fmt.Errorf("RTDN envelope does not decode: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		return false, fmt.Errorf("RTDN message data does not base64-decode: %w", err)
	}
	var notif rtdnNotification
	if err := json.Unmarshal(raw, &notif); err != nil {
		return false, fmt.Errorf("RTDN notification does not decode: %w", err)
	}

	if notif.TestNotification != nil {
		logger.Get().Info("google RTDN test notification received")
		return false, nil
	}
	if notif.PackageName != "" && notif.PackageName != s.Cfg.EnvVars.PlayPackageName {
		return false, fmt.Errorf("RTDN for unexpected package %q", notif.PackageName)
	}
	if s.Google == nil {
		// Configured topic but no service account on this deploy; retry
		// later rather than dropping the event.
		return true, ErrIAPUnavailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if vp := notif.VoidedPurchaseNotification; vp != nil {
		row, err := s.Store.GetByExternalKey(vp.PurchaseToken)
		if err != nil {
			return true, err
		}
		if row == nil {
			logger.Get().Info("voided purchase for unknown token — ignoring")
			return false, nil
		}
		row.Status = models.StoreSubRevoked
		row.LastEventType = "VOIDED_PURCHASE"
		row.LastVerifiedAt = s.now()
		if err := s.Store.Save(row); err != nil {
			return true, err
		}
		if row.UserID != 0 {
			return true, s.RecomputeEntitlement(row.UserID)
		}
		return false, nil
	}

	if sn := notif.SubscriptionNotification; sn != nil {
		logger.Get().Info("google RTDN subscription notification",
			zap.Int("type", sn.NotificationType), zap.String("subscription_id", sn.SubscriptionID))
		if err := s.refreshGoogleToken(ctx, sn.PurchaseToken, 0, fmt.Sprintf("RTDN_%d", sn.NotificationType)); err != nil {
			// Verification failures on webhook-supplied tokens are permanent;
			// infrastructure errors are worth a retry.
			if errors.Is(err, ErrIAPVerification) || errors.Is(err, ErrIAPUnknownProduct) {
				return false, err
			}
			return true, err
		}
		row, err := s.Store.GetByExternalKey(sn.PurchaseToken)
		if err == nil && row != nil && row.UserID != 0 {
			return true, s.RecomputeEntitlement(row.UserID)
		}
		return false, nil
	}

	logger.Get().Info("google RTDN with no actionable payload — ignoring")
	return false, nil
}

// RefreshUserIfStale re-verifies a user's expired-looking store subscriptions
// (SubscriptionService calls this lazily when a paid tier's ExpiresAt has
// passed). Google rows re-poll the Play API; apple rows re-poll only with an
// AppleStatusPoller configured, otherwise they expire once the notification
// slack runs out. Best-effort: errors are logged, entitlement is recomputed
// from whatever state we have.
func (s *IAPService) RefreshUserIfStale(userID uint) {
	rows, err := s.Store.ListByUser(userID)
	if err != nil {
		logger.Get().Warn("stale refresh: failed to list store subscriptions", zap.Uint("user_id", userID), zap.Error(err))
		return
	}
	now := s.now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := range rows {
		row := &rows[i]
		if row.Status != models.StoreSubActive && row.Status != models.StoreSubGrace {
			continue
		}
		if now.Before(row.ExpiresAt) {
			continue
		}
		// Rate-limit per row so hot paths (CheckLimit on every AI route)
		// can't hammer the store APIs while a row stays expired.
		if now.Sub(row.LastVerifiedAt) < 10*time.Minute {
			continue
		}

		switch row.Platform {
		case models.StorePlatformGoogle:
			if s.Google == nil {
				continue
			}
			if err := s.refreshGoogleToken(ctx, row.ExternalKey, row.UserID, "lazy_refresh"); err != nil {
				logger.Get().Warn("stale refresh: google re-verify failed", zap.Uint("user_id", userID), zap.Error(err))
				// Still bump LastVerifiedAt via a direct save so we don't
				// retry every request while Google is unreachable.
				row.LastVerifiedAt = now
				_ = s.Store.Save(row)
			}
		case models.StorePlatformApple:
			if s.ApplePoller != nil {
				txnJWS, renewalJWS, err := s.ApplePoller.LatestTransaction(ctx, row.ExternalKey, row.Environment)
				if err != nil {
					logger.Get().Warn("stale refresh: apple poll failed", zap.Uint("user_id", userID), zap.Error(err))
					row.LastVerifiedAt = now
					_ = s.Store.Save(row)
					continue
				}
				s.applyPolledAppleState(row, txnJWS, renewalJWS)
			} else if now.After(row.ExpiresAt.Add(s.appleSlack())) {
				// No poller and the notification slack has run out: the
				// subscription is over until a restore/verify says otherwise.
				row.Status = models.StoreSubExpired
				row.LastEventType = "lazy_expire"
				row.LastVerifiedAt = now
				if err := s.Store.Save(row); err != nil {
					logger.Get().Warn("stale refresh: failed to expire apple row", zap.Error(err))
				}
			}
		}
	}

	if err := s.RecomputeEntitlement(userID); err != nil {
		logger.Get().Warn("stale refresh: recompute failed", zap.Uint("user_id", userID), zap.Error(err))
	}
}

// applyPolledAppleState re-verifies polled JWS through the same pinned-root
// path as client receipts and applies it to the row.
func (s *IAPService) applyPolledAppleState(row *models.StoreSubscription, txnJWS, renewalJWS string) {
	txn, err := s.Apple.VerifyTransaction(txnJWS)
	if err != nil {
		logger.Get().Warn("stale refresh: polled apple transaction failed verification", zap.Error(err))
		return
	}
	var renewal *iap.AppleRenewalInfo
	if renewalJWS != "" {
		if r, err := s.Apple.VerifyRenewalInfo(renewalJWS); err == nil {
			renewal = r
		}
	}

	status := models.StoreSubActive
	switch {
	case txn.Revoked():
		status = models.StoreSubRevoked
	case !txn.ExpiresAt().After(s.now()):
		status = models.StoreSubExpired
	}
	row.ProductID = txn.ProductID
	if tier, ok := models.TierByProductID[txn.ProductID]; ok {
		row.Tier = tier
	}
	row.Status = status
	row.ExpiresAt = txn.ExpiresAt()
	row.AutoRenew = renewal != nil && renewal.AutoRenewStatus == 1
	row.LastEventType = "apple_poll"
	row.LastVerifiedAt = s.now()
	if err := s.Store.Save(row); err != nil {
		logger.Get().Warn("stale refresh: failed to save polled apple state", zap.Error(err))
	}
}

func msToTimeSafe(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
