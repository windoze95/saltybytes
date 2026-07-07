package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/iap"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// --- fakes ---

type fakeAppleVerifier struct {
	txn     *iap.AppleTransaction
	txnErr  error
	notif   *iap.AppleNotification
	nTxn    *iap.AppleTransaction
	nRenew  *iap.AppleRenewalInfo
	nErr    error
	renewal *iap.AppleRenewalInfo
}

func (f *fakeAppleVerifier) VerifyTransaction(string) (*iap.AppleTransaction, error) {
	return f.txn, f.txnErr
}
func (f *fakeAppleVerifier) VerifyRenewalInfo(string) (*iap.AppleRenewalInfo, error) {
	if f.renewal == nil {
		return nil, errors.New("no renewal")
	}
	return f.renewal, nil
}
func (f *fakeAppleVerifier) VerifyNotification(string) (*iap.AppleNotification, *iap.AppleTransaction, *iap.AppleRenewalInfo, error) {
	return f.notif, f.nTxn, f.nRenew, f.nErr
}

type fakeGoogleVerifier struct {
	states map[string]*iap.GoogleSubscriptionState
	err    error
	acked  []string
}

func (f *fakeGoogleVerifier) GetSubscription(_ context.Context, token string) (*iap.GoogleSubscriptionState, error) {
	if f.err != nil {
		return nil, f.err
	}
	state, ok := f.states[token]
	if !ok {
		return nil, errors.New("unknown token")
	}
	return state, nil
}
func (f *fakeGoogleVerifier) Acknowledge(_ context.Context, productID, token string) error {
	f.acked = append(f.acked, productID+":"+token)
	return nil
}

// --- helpers ---

func newTestIAPService(t *testing.T) (*IAPService, *testutil.MockUserRepo, *testutil.MockStoreSubscriptionRepo) {
	t.Helper()
	users := testutil.NewMockUserRepo()
	store := testutil.NewMockStoreSubscriptionRepo()
	cfg := &config.Config{}
	cfg.EnvVars.PlayPackageName = "codes.julian.saltybytes"
	subs := NewSubscriptionService(cfg, users)
	svc := NewIAPService(cfg, users, store, subs)
	return svc, users, store
}

func seedUser(users *testutil.MockUserRepo, id uint, tier models.SubscriptionTier) *models.User {
	user := testutil.TestUser()
	user.ID = id
	user.Subscription = &models.Subscription{
		UserID:         id,
		Tier:           tier,
		MonthlyResetAt: time.Now().Add(24 * time.Hour),
	}
	users.Users[id] = user
	return user
}

func appleTxn(userID uint, productID string, expires time.Time) *iap.AppleTransaction {
	return &iap.AppleTransaction{
		OriginalTransactionID: "orig-1",
		TransactionID:         "txn-1",
		ProductID:             productID,
		BundleID:              "codes.julian.saltybytes",
		AppAccountToken:       iap.AccountTokenForUser(userID),
		ExpiresDateMS:         expires.UnixMilli(),
		SignedDateMS:          time.Now().UnixMilli(),
		Environment:           "Production",
		Type:                  "Auto-Renewable Subscription",
	}
}

func rtdnBody(t *testing.T, notif map[string]any) []byte {
	t.Helper()
	inner, err := json.Marshal(notif)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"message": map[string]any{"data": base64.StdEncoding.EncodeToString(inner), "messageId": "m1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// --- apple client verify ---

func TestVerifyPurchase_Apple_NewSubscriber(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	svc.Apple = &fakeAppleVerifier{txn: appleTxn(7, "sb_premium_monthly", time.Now().Add(30*24*time.Hour))}

	sub, err := svc.VerifyPurchase(7, "apple", "sb_premium_monthly", "jws")
	if err != nil {
		t.Fatalf("VerifyPurchase: %v", err)
	}
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium", sub.Tier)
	}
	if sub.ExpiresAt == nil {
		t.Error("ExpiresAt should be set")
	}
	row, _ := store.GetByExternalKey("orig-1")
	if row == nil || row.UserID != 7 || row.Status != models.StoreSubActive {
		t.Errorf("store row wrong: %+v", row)
	}
}

func TestVerifyPurchase_Apple_VerificationFails(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	svc.Apple = &fakeAppleVerifier{txnErr: errors.New("bad signature")}

	if _, err := svc.VerifyPurchase(7, "apple", "sb_premium_monthly", "jws"); !errors.Is(err, ErrIAPVerification) {
		t.Fatalf("err = %v, want ErrIAPVerification", err)
	}
}

func TestVerifyPurchase_Apple_UnknownProduct(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	svc.Apple = &fakeAppleVerifier{txn: appleTxn(7, "sb_mystery", time.Now().Add(time.Hour))}

	if _, err := svc.VerifyPurchase(7, "apple", "sb_mystery", "jws"); !errors.Is(err, ErrIAPUnknownProduct) {
		t.Fatalf("err = %v, want ErrIAPUnknownProduct", err)
	}
}

func TestVerifyPurchase_LinkedToOtherAccount(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	seedUser(users, 8, models.TierFree)
	svc.Apple = &fakeAppleVerifier{txn: appleTxn(7, "sb_premium_monthly", time.Now().Add(30*24*time.Hour))}

	if _, err := svc.VerifyPurchase(7, "apple", "sb_premium_monthly", "jws"); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := svc.VerifyPurchase(8, "apple", "sb_premium_monthly", "jws"); !errors.Is(err, ErrIAPLinkedElsewhere) {
		t.Fatalf("err = %v, want ErrIAPLinkedElsewhere", err)
	}
	// The original owner keeps the entitlement.
	sub, _ := svc.Subs.GetSubscription(7)
	if sub.Tier != models.TierPremium {
		t.Errorf("original owner tier = %s, want premium", sub.Tier)
	}
}

func TestVerifyPurchase_UnavailableWhenNotConfigured(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)

	if _, err := svc.VerifyPurchase(7, "google", "sb_plus_monthly", "token"); !errors.Is(err, ErrIAPUnavailable) {
		t.Fatalf("err = %v, want ErrIAPUnavailable", err)
	}
	svc.Apple = nil
	if _, err := svc.VerifyPurchase(7, "apple", "sb_plus_monthly", "jws"); !errors.Is(err, ErrIAPUnavailable) {
		t.Fatalf("err = %v, want ErrIAPUnavailable", err)
	}
}

// --- google client verify ---

func TestVerifyPurchase_Google_ActiveAcknowledges(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 9, models.TierFree)
	google := &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-1": {
			ProductID: "sb_plus_monthly",
			State:     iap.GoogleStateActive,
			ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
			AutoRenew: true,
			NeedsAck:  true,
		},
	}}
	svc.Google = google

	sub, err := svc.VerifyPurchase(9, "google", "sb_plus_monthly", "tok-1")
	if err != nil {
		t.Fatalf("VerifyPurchase: %v", err)
	}
	if sub.Tier != models.TierPlus {
		t.Errorf("tier = %s, want plus", sub.Tier)
	}
	if len(google.acked) != 1 {
		t.Errorf("acked = %v, want one acknowledge", google.acked)
	}
	row, _ := store.GetByExternalKey("tok-1")
	if row == nil || !row.AutoRenew || row.Status != models.StoreSubActive {
		t.Errorf("store row wrong: %+v", row)
	}
}

func TestVerifyPurchase_Google_CanceledStillEntitledUntilExpiry(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 9, models.TierFree)
	svc.Google = &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-1": {
			ProductID: "sb_premium_monthly",
			State:     iap.GoogleStateCanceled,
			ExpiresAt: time.Now().Add(10 * 24 * time.Hour),
			AutoRenew: false,
		},
	}}

	sub, err := svc.VerifyPurchase(9, "google", "sb_premium_monthly", "tok-1")
	if err != nil {
		t.Fatalf("VerifyPurchase: %v", err)
	}
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium (paid-up canceled sub)", sub.Tier)
	}
}

func TestVerifyPurchase_Google_OnHoldNotEntitled(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 9, models.TierFree)
	svc.Google = &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-1": {
			ProductID: "sb_premium_monthly",
			State:     iap.GoogleStateOnHold,
			ExpiresAt: time.Now().Add(10 * 24 * time.Hour),
		},
	}}

	sub, err := svc.VerifyPurchase(9, "google", "sb_premium_monthly", "tok-1")
	if err != nil {
		t.Fatalf("VerifyPurchase: %v", err)
	}
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free (on hold)", sub.Tier)
	}
}

func TestVerifyPurchase_Google_LinkedTokenRetiresOldRow(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 9, models.TierFree)
	google := &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-old": {
			ProductID: "sb_plus_monthly",
			State:     iap.GoogleStateActive,
			ExpiresAt: time.Now().Add(20 * 24 * time.Hour),
			AutoRenew: true,
		},
	}}
	svc.Google = google
	if _, err := svc.VerifyPurchase(9, "google", "sb_plus_monthly", "tok-old"); err != nil {
		t.Fatal(err)
	}

	// Upgrade issues a new token linked to the old one.
	google.states["tok-new"] = &iap.GoogleSubscriptionState{
		ProductID:           "sb_premium_monthly",
		State:               iap.GoogleStateActive,
		ExpiresAt:           time.Now().Add(30 * 24 * time.Hour),
		AutoRenew:           true,
		LinkedPurchaseToken: "tok-old",
	}
	sub, err := svc.VerifyPurchase(9, "google", "sb_premium_monthly", "tok-new")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium", sub.Tier)
	}
	old, _ := store.GetByExternalKey("tok-old")
	if old.Status != models.StoreSubReplaced {
		t.Errorf("old token status = %s, want replaced", old.Status)
	}
}

// --- entitlement recompute ---

func TestRecomputeEntitlement_HighestTierWins(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 5, models.TierFree)
	now := time.Now()
	_ = store.Save(&models.StoreSubscription{
		UserID: 5, Platform: "google", ProductID: "sb_plus_monthly", Tier: models.TierPlus,
		ExternalKey: "k1", Status: models.StoreSubActive, ExpiresAt: now.Add(48 * time.Hour),
	})
	_ = store.Save(&models.StoreSubscription{
		UserID: 5, Platform: "apple", ProductID: "sb_premium_yearly", Tier: models.TierPremium,
		ExternalKey: "k2", Status: models.StoreSubActive, ExpiresAt: now.Add(24 * time.Hour),
	})

	if err := svc.RecomputeEntitlement(5); err != nil {
		t.Fatal(err)
	}
	sub, _ := svc.Subs.GetSubscription(5)
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium", sub.Tier)
	}
}

func TestRecomputeEntitlement_AllExpiredDowngradesToFree(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	user := seedUser(users, 5, models.TierPremium)
	exp := time.Now().Add(-100 * time.Hour)
	user.Subscription.ExpiresAt = &exp
	_ = store.Save(&models.StoreSubscription{
		UserID: 5, Platform: "google", ProductID: "sb_premium_monthly", Tier: models.TierPremium,
		ExternalKey: "k1", Status: models.StoreSubExpired, ExpiresAt: exp,
	})

	if err := svc.RecomputeEntitlement(5); err != nil {
		t.Fatal(err)
	}
	sub, _ := svc.Subs.GetSubscription(5)
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free", sub.Tier)
	}
	if sub.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil for free", sub.ExpiresAt)
	}
}

func TestRecomputeEntitlement_NeverTouchesUnlimited(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 5, models.TierUnlimited)

	if err := svc.RecomputeEntitlement(5); err != nil {
		t.Fatal(err)
	}
	sub, _ := svc.Subs.GetSubscription(5)
	if sub.Tier != models.TierUnlimited {
		t.Errorf("tier = %s, operator tier must never be recomputed", sub.Tier)
	}
}

func TestAppleSlack_KeepsAutoRenewingAppleEntitledPastExpiry(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 5, models.TierFree)
	// Expired 1h ago, auto-renewing, no poller: inside the 72h slack.
	_ = store.Save(&models.StoreSubscription{
		UserID: 5, Platform: "apple", ProductID: "sb_premium_monthly", Tier: models.TierPremium,
		ExternalKey: "k1", Status: models.StoreSubActive, AutoRenew: true,
		ExpiresAt: time.Now().Add(-time.Hour),
	})
	if err := svc.RecomputeEntitlement(5); err != nil {
		t.Fatal(err)
	}
	sub, _ := svc.Subs.GetSubscription(5)
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium (within apple notification slack)", sub.Tier)
	}

	// A canceled (auto-renew off) apple sub gets no slack.
	_ = store.Save(&models.StoreSubscription{
		UserID: 6, Platform: "apple", ProductID: "sb_premium_monthly", Tier: models.TierPremium,
		ExternalKey: "k2", Status: models.StoreSubActive, AutoRenew: false,
		ExpiresAt: time.Now().Add(-time.Hour),
	})
	seedUser(users, 6, models.TierPremium)
	if err := svc.RecomputeEntitlement(6); err != nil {
		t.Fatal(err)
	}
	sub, _ = svc.Subs.GetSubscription(6)
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free (canceled apple sub past expiry)", sub.Tier)
	}
}

// --- apple notifications ---

func TestHandleAppleNotification_RenewalExtendsEntitlement(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	first := appleTxn(7, "sb_premium_monthly", time.Now().Add(24*time.Hour))
	svc.Apple = &fakeAppleVerifier{txn: first}
	if _, err := svc.VerifyPurchase(7, "apple", "sb_premium_monthly", "jws"); err != nil {
		t.Fatal(err)
	}

	renewed := appleTxn(7, "sb_premium_monthly", time.Now().Add(31*24*time.Hour))
	svc.Apple = &fakeAppleVerifier{
		notif:  &iap.AppleNotification{NotificationType: "DID_RENEW"},
		nTxn:   renewed,
		nRenew: &iap.AppleRenewalInfo{AutoRenewStatus: 1},
	}
	if err := svc.HandleAppleNotification("signed"); err != nil {
		t.Fatal(err)
	}
	row, _ := store.GetByExternalKey("orig-1")
	if !row.ExpiresAt.After(time.Now().Add(30 * 24 * time.Hour)) {
		t.Errorf("expiry not extended: %v", row.ExpiresAt)
	}
	if row.LastEventType != "DID_RENEW" {
		t.Errorf("LastEventType = %s", row.LastEventType)
	}
}

func TestHandleAppleNotification_ExpiredDowngrades(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	svc.Apple = &fakeAppleVerifier{txn: appleTxn(7, "sb_premium_monthly", time.Now().Add(time.Hour))}
	if _, err := svc.VerifyPurchase(7, "apple", "sb_premium_monthly", "jws"); err != nil {
		t.Fatal(err)
	}

	expired := appleTxn(7, "sb_premium_monthly", time.Now().Add(-time.Minute))
	svc.Apple = &fakeAppleVerifier{
		notif: &iap.AppleNotification{NotificationType: "EXPIRED"},
		nTxn:  expired,
	}
	if err := svc.HandleAppleNotification("signed"); err != nil {
		t.Fatal(err)
	}
	sub, _ := svc.Subs.GetSubscription(7)
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free after EXPIRED", sub.Tier)
	}
}

func TestHandleAppleNotification_RefundRevokes(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	svc.Apple = &fakeAppleVerifier{txn: appleTxn(7, "sb_premium_monthly", time.Now().Add(30*24*time.Hour))}
	if _, err := svc.VerifyPurchase(7, "apple", "sb_premium_monthly", "jws"); err != nil {
		t.Fatal(err)
	}

	refunded := appleTxn(7, "sb_premium_monthly", time.Now().Add(30*24*time.Hour))
	refunded.RevocationDateMS = time.Now().UnixMilli()
	svc.Apple = &fakeAppleVerifier{
		notif: &iap.AppleNotification{NotificationType: "REFUND"},
		nTxn:  refunded,
	}
	if err := svc.HandleAppleNotification("signed"); err != nil {
		t.Fatal(err)
	}
	row, _ := store.GetByExternalKey("orig-1")
	if row.Status != models.StoreSubRevoked {
		t.Errorf("status = %s, want revoked", row.Status)
	}
	sub, _ := svc.Subs.GetSubscription(7)
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free after refund", sub.Tier)
	}
}

func TestHandleAppleNotification_GracePeriodKeepsEntitlement(t *testing.T) {
	svc, users, _ := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)
	graceEnd := time.Now().Add(16 * 24 * time.Hour)
	txn := appleTxn(7, "sb_premium_monthly", time.Now().Add(-time.Hour))
	svc.Apple = &fakeAppleVerifier{
		notif:  &iap.AppleNotification{NotificationType: "DID_FAIL_TO_RENEW", Subtype: "GRACE_PERIOD"},
		nTxn:   txn,
		nRenew: &iap.AppleRenewalInfo{AutoRenewStatus: 1, GracePeriodExpiresDateMS: graceEnd.UnixMilli()},
	}
	if err := svc.HandleAppleNotification("signed"); err != nil {
		t.Fatal(err)
	}
	sub, _ := svc.Subs.GetSubscription(7)
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium during billing grace", sub.Tier)
	}
}

func TestHandleAppleNotification_OrphanThenClaim(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 7, models.TierFree)

	// Webhook arrives first, with no usable account token.
	txn := appleTxn(7, "sb_premium_monthly", time.Now().Add(30*24*time.Hour))
	txn.AppAccountToken = ""
	svc.Apple = &fakeAppleVerifier{
		notif: &iap.AppleNotification{NotificationType: "SUBSCRIBED"},
		nTxn:  txn,
	}
	if err := svc.HandleAppleNotification("signed"); err != nil {
		t.Fatal(err)
	}
	row, _ := store.GetByExternalKey("orig-1")
	if row.UserID != 0 {
		t.Fatalf("row should be an orphan, got user %d", row.UserID)
	}
	sub, _ := svc.Subs.GetSubscription(7)
	if sub.Tier != models.TierFree {
		t.Errorf("orphan must not grant anything, tier = %s", sub.Tier)
	}

	// Client verify claims it.
	svc.Apple = &fakeAppleVerifier{txn: txn}
	if _, err := svc.VerifyPurchase(7, "apple", "sb_premium_monthly", "jws"); err != nil {
		t.Fatal(err)
	}
	row, _ = store.GetByExternalKey("orig-1")
	if row.UserID != 7 {
		t.Errorf("orphan not claimed: user %d", row.UserID)
	}
	sub, _ = svc.Subs.GetSubscription(7)
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium after claim", sub.Tier)
	}
}

func TestHandleAppleNotification_AccountTokenBindsWebhookFirstPurchase(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 42, models.TierFree)

	txn := appleTxn(42, "sb_plus_monthly", time.Now().Add(30*24*time.Hour))
	svc.Apple = &fakeAppleVerifier{
		notif: &iap.AppleNotification{NotificationType: "SUBSCRIBED"},
		nTxn:  txn,
	}
	if err := svc.HandleAppleNotification("signed"); err != nil {
		t.Fatal(err)
	}
	row, _ := store.GetByExternalKey("orig-1")
	if row.UserID != 42 {
		t.Fatalf("account token should bind the row, got user %d", row.UserID)
	}
	sub, _ := svc.Subs.GetSubscription(42)
	if sub.Tier != models.TierPlus {
		t.Errorf("tier = %s, want plus", sub.Tier)
	}
}

func TestHandleAppleNotification_BadSignature(t *testing.T) {
	svc, _, _ := newTestIAPService(t)
	svc.Apple = &fakeAppleVerifier{nErr: errors.New("chain does not verify")}
	if err := svc.HandleAppleNotification("garbage"); !errors.Is(err, ErrIAPVerification) {
		t.Fatalf("err = %v, want ErrIAPVerification", err)
	}
}

// --- google RTDN ---

func TestHandleGoogleRTDN_TestNotification(t *testing.T) {
	svc, _, _ := newTestIAPService(t)
	body := rtdnBody(t, map[string]any{
		"version": "1.0", "packageName": "codes.julian.saltybytes",
		"testNotification": map[string]any{"version": "1.0"},
	})
	retryable, err := svc.HandleGoogleRTDN(body)
	if err != nil || retryable {
		t.Fatalf("test notification: retryable=%v err=%v", retryable, err)
	}
}

func TestHandleGoogleRTDN_SubscriptionEventRefreshesToken(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 9, models.TierFree)
	google := &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-1": {
			ProductID:           "sb_premium_monthly",
			State:               iap.GoogleStateActive,
			ExpiresAt:           time.Now().Add(30 * 24 * time.Hour),
			AutoRenew:           true,
			ObfuscatedAccountID: iap.AccountTokenForUser(9),
		},
	}}
	svc.Google = google

	body := rtdnBody(t, map[string]any{
		"version": "1.0", "packageName": "codes.julian.saltybytes",
		"subscriptionNotification": map[string]any{
			"version": "1.0", "notificationType": 4, "purchaseToken": "tok-1", "subscriptionId": "sb_premium_monthly",
		},
	})
	retryable, err := svc.HandleGoogleRTDN(body)
	if err != nil {
		t.Fatalf("retryable=%v err=%v", retryable, err)
	}
	row, _ := store.GetByExternalKey("tok-1")
	if row == nil || row.UserID != 9 {
		t.Fatalf("row should be bound via obfuscated account id: %+v", row)
	}
	sub, _ := svc.Subs.GetSubscription(9)
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium", sub.Tier)
	}
}

func TestHandleGoogleRTDN_VoidedPurchaseRevokes(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 9, models.TierFree)
	svc.Google = &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-1": {
			ProductID: "sb_premium_monthly",
			State:     iap.GoogleStateActive,
			ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		},
	}}
	if _, err := svc.VerifyPurchase(9, "google", "sb_premium_monthly", "tok-1"); err != nil {
		t.Fatal(err)
	}

	body := rtdnBody(t, map[string]any{
		"version": "1.0", "packageName": "codes.julian.saltybytes",
		"voidedPurchaseNotification": map[string]any{"purchaseToken": "tok-1", "orderId": "o1", "productType": 1},
	})
	retryable, err := svc.HandleGoogleRTDN(body)
	if err != nil {
		t.Fatalf("retryable=%v err=%v", retryable, err)
	}
	row, _ := store.GetByExternalKey("tok-1")
	if row.Status != models.StoreSubRevoked {
		t.Errorf("status = %s, want revoked", row.Status)
	}
	sub, _ := svc.Subs.GetSubscription(9)
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free after voided purchase", sub.Tier)
	}
}

func TestHandleGoogleRTDN_MalformedDropped(t *testing.T) {
	svc, _, _ := newTestIAPService(t)
	retryable, err := svc.HandleGoogleRTDN([]byte("not json"))
	if err == nil || retryable {
		t.Fatalf("malformed body must be a non-retryable error, retryable=%v err=%v", retryable, err)
	}

	retryable, err = svc.HandleGoogleRTDN(rtdnBody(t, map[string]any{
		"version": "1.0", "packageName": "com.other.app",
		"subscriptionNotification": map[string]any{"purchaseToken": "t", "notificationType": 4},
	}))
	if err == nil || retryable {
		t.Fatalf("wrong package must be a non-retryable error, retryable=%v err=%v", retryable, err)
	}
}

// --- lazy refresh ---

func TestRefreshUserIfStale_GoogleRenewalPickedUp(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	user := seedUser(users, 9, models.TierPremium)
	oldExp := time.Now().Add(-time.Hour)
	user.Subscription.ExpiresAt = &oldExp
	_ = store.Save(&models.StoreSubscription{
		UserID: 9, Platform: "google", ProductID: "sb_premium_monthly", Tier: models.TierPremium,
		ExternalKey: "tok-1", Status: models.StoreSubActive, AutoRenew: true,
		ExpiresAt:      oldExp,
		LastVerifiedAt: time.Now().Add(-time.Hour),
	})
	// Google says it renewed.
	svc.Google = &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-1": {
			ProductID: "sb_premium_monthly",
			State:     iap.GoogleStateActive,
			ExpiresAt: time.Now().Add(29 * 24 * time.Hour),
			AutoRenew: true,
		},
	}}

	svc.RefreshUserIfStale(9)
	sub, _ := svc.Subs.GetSubscription(9)
	if sub.Tier != models.TierPremium {
		t.Errorf("tier = %s, want premium after refresh", sub.Tier)
	}
	if sub.ExpiresAt == nil || !sub.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt not advanced: %v", sub.ExpiresAt)
	}
}

func TestRefreshUserIfStale_GoogleTrulyExpiredDowngrades(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	user := seedUser(users, 9, models.TierPremium)
	oldExp := time.Now().Add(-time.Hour)
	user.Subscription.ExpiresAt = &oldExp
	_ = store.Save(&models.StoreSubscription{
		UserID: 9, Platform: "google", ProductID: "sb_premium_monthly", Tier: models.TierPremium,
		ExternalKey: "tok-1", Status: models.StoreSubActive, AutoRenew: false,
		ExpiresAt:      oldExp,
		LastVerifiedAt: time.Now().Add(-time.Hour),
	})
	svc.Google = &fakeGoogleVerifier{states: map[string]*iap.GoogleSubscriptionState{
		"tok-1": {
			ProductID: "sb_premium_monthly",
			State:     iap.GoogleStateExpired,
			ExpiresAt: oldExp,
		},
	}}

	svc.RefreshUserIfStale(9)
	sub, _ := svc.Subs.GetSubscription(9)
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free", sub.Tier)
	}
}

func TestRefreshUserIfStale_AppleWithoutPollerExpiresAfterSlack(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	user := seedUser(users, 7, models.TierPremium)
	oldExp := time.Now().Add(-100 * time.Hour) // beyond the 72h slack
	user.Subscription.ExpiresAt = &oldExp
	_ = store.Save(&models.StoreSubscription{
		UserID: 7, Platform: "apple", ProductID: "sb_premium_monthly", Tier: models.TierPremium,
		ExternalKey: "orig-1", Status: models.StoreSubActive, AutoRenew: true,
		ExpiresAt:      oldExp,
		LastVerifiedAt: time.Now().Add(-time.Hour),
	})

	svc.RefreshUserIfStale(7)
	row, _ := store.GetByExternalKey("orig-1")
	if row.Status != models.StoreSubExpired {
		t.Errorf("status = %s, want expired after slack", row.Status)
	}
	sub, _ := svc.Subs.GetSubscription(7)
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free", sub.Tier)
	}
}

func TestGetSubscription_TriggersStaleRefresher(t *testing.T) {
	users := testutil.NewMockUserRepo()
	cfg := &config.Config{}
	subs := NewSubscriptionService(cfg, users)

	user := seedUser(users, 3, models.TierPremium)
	oldExp := time.Now().Add(-time.Minute)
	user.Subscription.ExpiresAt = &oldExp

	called := false
	subs.StaleRefresher = func(userID uint) {
		called = true
		// Simulate the refresher downgrading the user.
		_ = users.UpdateSubscriptionTier(userID, models.TierFree, nil)
	}

	sub, err := subs.GetSubscription(3)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("StaleRefresher was not called for an expired paid tier")
	}
	if sub.Tier != models.TierFree {
		t.Errorf("tier = %s, want free after refresher downgrade", sub.Tier)
	}
}

func TestStoreStateForUser(t *testing.T) {
	svc, users, store := newTestIAPService(t)
	seedUser(users, 5, models.TierFree)
	if view := svc.StoreStateForUser(5); view != nil {
		t.Errorf("expected nil store view, got %+v", view)
	}
	_ = store.Save(&models.StoreSubscription{
		UserID: 5, Platform: "apple", ProductID: "sb_premium_monthly", Tier: models.TierPremium,
		ExternalKey: "k1", Status: models.StoreSubActive, AutoRenew: true,
		ExpiresAt: time.Now().Add(24 * time.Hour), Environment: "Production",
	})
	view := svc.StoreStateForUser(5)
	if view == nil || view.ProductID != "sb_premium_monthly" || view.Platform != "apple" {
		t.Errorf("store view wrong: %+v", view)
	}
}
