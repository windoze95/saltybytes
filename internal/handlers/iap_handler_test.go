package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/iap"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

type fakeAppleVerifierH struct {
	txn  *iap.AppleTransaction
	err  error
	nErr error
}

func (f *fakeAppleVerifierH) VerifyTransaction(string) (*iap.AppleTransaction, error) {
	return f.txn, f.err
}
func (f *fakeAppleVerifierH) VerifyRenewalInfo(string) (*iap.AppleRenewalInfo, error) {
	return nil, errors.New("unused")
}
func (f *fakeAppleVerifierH) VerifyNotification(string) (*iap.AppleNotification, *iap.AppleTransaction, *iap.AppleRenewalInfo, error) {
	if f.nErr != nil {
		return nil, nil, nil, f.nErr
	}
	return &iap.AppleNotification{NotificationType: "TEST"}, nil, nil, nil
}

type fakeGoogleVerifierH struct {
	state *iap.GoogleSubscriptionState
	err   error
}

func (f *fakeGoogleVerifierH) GetSubscription(context.Context, string) (*iap.GoogleSubscriptionState, error) {
	return f.state, f.err
}
func (f *fakeGoogleVerifierH) Acknowledge(context.Context, string, string) error { return nil }

func newIAPRouter(t *testing.T, user *models.User, apple service.AppleIAPVerifier, google service.GooglePlayVerifier, rtdnSecret string) (*gin.Engine, *service.IAPService) {
	t.Helper()
	users := testutil.NewMockUserRepo()
	if user != nil {
		users.Users[user.ID] = user
	}
	store := testutil.NewMockStoreSubscriptionRepo()
	cfg := &config.Config{}
	cfg.EnvVars.PlayPackageName = "codes.julian.saltybytes"
	cfg.EnvVars.RTDNPushSecret = rtdnSecret
	subs := service.NewSubscriptionService(cfg, users)
	svc := service.NewIAPService(cfg, users, store, subs)
	svc.Apple = apple
	svc.Google = google
	handler := NewIAPHandler(svc, cfg)

	r := gin.New()
	r.POST("/iap/verify", setUser(user), handler.VerifyPurchase)
	r.POST("/webhooks/apple", handler.AppleWebhook)
	r.POST("/webhooks/google", handler.GoogleWebhook)
	return r, svc
}

func iapUser() *models.User {
	u := testutil.TestUser()
	u.Subscription = &models.Subscription{
		UserID:         u.ID,
		Tier:           models.TierFree,
		MonthlyResetAt: time.Now().Add(24 * time.Hour),
	}
	return u
}

func postIAPJSON(r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestVerifyPurchase_Handler_Success(t *testing.T) {
	user := iapUser()
	apple := &fakeAppleVerifierH{txn: &iap.AppleTransaction{
		OriginalTransactionID: "orig-1",
		ProductID:             "sb_premium_monthly",
		BundleID:              "codes.julian.saltybytes",
		ExpiresDateMS:         time.Now().Add(30 * 24 * time.Hour).UnixMilli(),
		Environment:           "Production",
	}}
	r, _ := newIAPRouter(t, user, apple, nil, "")

	w := postIAPJSON(r, "/iap/verify", map[string]string{
		"platform": "apple", "product_id": "sb_premium_monthly", "verification_data": "jws",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	sub, ok := resp["subscription"].(map[string]any)
	if !ok || sub["Tier"] != "premium" {
		t.Errorf("subscription envelope wrong: %v", resp)
	}
	if resp["store"] == nil {
		t.Error("store view missing")
	}
	if _, ok := resp["limits"]; !ok {
		t.Error("limits missing")
	}
}

func TestVerifyPurchase_Handler_ErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		apple    service.AppleIAPVerifier
		body     map[string]string
		wantCode int
		wantErr  string
	}{
		{
			name:     "bad signature",
			apple:    &fakeAppleVerifierH{err: errors.New("nope")},
			body:     map[string]string{"platform": "apple", "verification_data": "jws"},
			wantCode: http.StatusBadRequest,
			wantErr:  "verification_failed",
		},
		{
			name: "unknown product",
			apple: &fakeAppleVerifierH{txn: &iap.AppleTransaction{
				OriginalTransactionID: "o", ProductID: "sb_wat", BundleID: "codes.julian.saltybytes",
				ExpiresDateMS: time.Now().Add(time.Hour).UnixMilli(),
			}},
			body:     map[string]string{"platform": "apple", "verification_data": "jws"},
			wantCode: http.StatusBadRequest,
			wantErr:  "unknown_product",
		},
		{
			name:     "google unconfigured",
			apple:    &fakeAppleVerifierH{},
			body:     map[string]string{"platform": "google", "verification_data": "tok"},
			wantCode: http.StatusServiceUnavailable,
			wantErr:  "iap_unavailable",
		},
		{
			name:     "bad platform",
			apple:    &fakeAppleVerifierH{},
			body:     map[string]string{"platform": "amazon", "verification_data": "x"},
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_payload",
		},
		{
			name:     "missing data",
			apple:    &fakeAppleVerifierH{},
			body:     map[string]string{"platform": "apple"},
			wantCode: http.StatusBadRequest,
			wantErr:  "invalid_payload",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newIAPRouter(t, iapUser(), tc.apple, nil, "")
			w := postIAPJSON(r, "/iap/verify", tc.body)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d. body: %s", w.Code, tc.wantCode, w.Body.String())
			}
			var resp map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["error_code"] != tc.wantErr {
				t.Errorf("error_code = %v, want %s", resp["error_code"], tc.wantErr)
			}
		})
	}
}

func TestVerifyPurchase_Handler_Conflict(t *testing.T) {
	user := iapUser()
	other := iapUser()
	other.ID = user.ID + 1
	other.Subscription.UserID = other.ID

	apple := &fakeAppleVerifierH{txn: &iap.AppleTransaction{
		OriginalTransactionID: "orig-1",
		ProductID:             "sb_premium_monthly",
		BundleID:              "codes.julian.saltybytes",
		ExpiresDateMS:         time.Now().Add(30 * 24 * time.Hour).UnixMilli(),
	}}

	users := testutil.NewMockUserRepo()
	users.Users[user.ID] = user
	users.Users[other.ID] = other
	store := testutil.NewMockStoreSubscriptionRepo()
	cfg := &config.Config{}
	subs := service.NewSubscriptionService(cfg, users)
	svc := service.NewIAPService(cfg, users, store, subs)
	svc.Apple = apple
	handler := NewIAPHandler(svc, cfg)

	// First user binds the purchase.
	if _, err := svc.VerifyPurchase(user.ID, "apple", "sb_premium_monthly", "jws"); err != nil {
		t.Fatal(err)
	}

	// Second user tries the same original transaction.
	r := gin.New()
	r.POST("/iap/verify", setUser(other), handler.VerifyPurchase)
	w := postIAPJSON(r, "/iap/verify", map[string]string{"platform": "apple", "verification_data": "jws"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409. body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "subscription_linked_to_other_account" {
		t.Errorf("error_code = %v", resp["error_code"])
	}
}

func TestAppleWebhook_Handler(t *testing.T) {
	r, _ := newIAPRouter(t, nil, &fakeAppleVerifierH{}, nil, "")
	// TEST notification → 200.
	w := postIAPJSON(r, "/webhooks/apple", map[string]string{"signedPayload": "signed"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	// Missing payload → 400.
	w = postIAPJSON(r, "/webhooks/apple", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	// Bad signature → 401.
	r, _ = newIAPRouter(t, nil, &fakeAppleVerifierH{nErr: errors.New("bad chain")}, nil, "")
	w = postIAPJSON(r, "/webhooks/apple", map[string]string{"signedPayload": "forged"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401. body: %s", w.Code, w.Body.String())
	}
}

func TestGoogleWebhook_Handler_SecretEnforced(t *testing.T) {
	google := &fakeGoogleVerifierH{state: &iap.GoogleSubscriptionState{
		ProductID: "sb_plus_monthly",
		State:     iap.GoogleStateActive,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}}
	r, _ := newIAPRouter(t, nil, &fakeAppleVerifierH{}, google, "s3cret")

	inner, _ := json.Marshal(map[string]any{
		"version": "1.0", "packageName": "codes.julian.saltybytes",
		"testNotification": map[string]any{"version": "1.0"},
	})
	envelope := map[string]any{"message": map[string]any{"data": base64.StdEncoding.EncodeToString(inner)}}

	w := postIAPJSON(r, "/webhooks/google", envelope)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: status = %d, want 401", w.Code)
	}
	w = postIAPJSON(r, "/webhooks/google?token=wrong", envelope)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", w.Code)
	}
	w = postIAPJSON(r, "/webhooks/google?token=s3cret", envelope)
	if w.Code != http.StatusOK {
		t.Fatalf("right token: status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestGoogleWebhook_Handler_MalformedAcked(t *testing.T) {
	r, _ := newIAPRouter(t, nil, &fakeAppleVerifierH{}, nil, "")
	req := httptest.NewRequest("POST", "/webhooks/google", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Malformed pushes are acked (200) so Pub/Sub stops redelivering them.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for malformed push", w.Code)
	}
}
