package iap

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	appleAPIProductionBase = "https://api.storekit.itunes.apple.com"
	appleAPISandboxBase    = "https://api.storekit-sandbox.itunes.apple.com"
)

// AppleAPIClient polls the App Store Server API for a subscription's latest
// state. Optional: local JWS verification plus server notifications keep
// entitlements correct without it, but with a key configured, expired-looking
// apple rows are re-polled exactly like google ones instead of riding the
// notification-lag slack.
type AppleAPIClient struct {
	keyID    string
	issuerID string
	bundleID string
	key      *ecdsa.PrivateKey
	http     *http.Client
}

// NewAppleAPIClient builds a client from an App Store Connect API key (.p8,
// base64-encoded) with the In-App Purchase (or Admin) role.
func NewAppleAPIClient(keyID, issuerID, privateKeyB64, bundleID string) (*AppleAPIClient, error) {
	raw, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return nil, fmt.Errorf("apple IAP key does not base64-decode: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("apple IAP key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apple IAP key does not parse: %w", err)
	}
	ecKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apple IAP key is not ECDSA")
	}
	return &AppleAPIClient{
		keyID:    keyID,
		issuerID: issuerID,
		bundleID: bundleID,
		key:      ecKey,
		http:     &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// LatestTransaction fetches the subscription's most recent signed transaction
// and renewal info JWS for an original transaction ID. The caller re-verifies
// both through the AppleVerifier, so trust still anchors at the pinned root.
func (c *AppleAPIClient) LatestTransaction(ctx context.Context, originalTransactionID, environment string) (txnJWS, renewalJWS string, err error) {
	base := appleAPIProductionBase
	if environment == "Sandbox" {
		base = appleAPISandboxBase
	}
	token, err := c.mintJWT()
	if err != nil {
		return "", "", err
	}
	url := fmt.Sprintf("%s/inApps/v1/subscriptions/%s", base, originalTransactionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("app store server API returned %d: %.200s", resp.StatusCode, string(body))
	}

	var statuses struct {
		Data []struct {
			LastTransactions []struct {
				OriginalTransactionID string `json:"originalTransactionId"`
				SignedTransactionInfo string `json:"signedTransactionInfo"`
				SignedRenewalInfo     string `json:"signedRenewalInfo"`
			} `json:"lastTransactions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &statuses); err != nil {
		return "", "", fmt.Errorf("app store server API response does not decode: %w", err)
	}
	for _, group := range statuses.Data {
		for _, last := range group.LastTransactions {
			if last.OriginalTransactionID == originalTransactionID {
				return last.SignedTransactionInfo, last.SignedRenewalInfo, nil
			}
		}
	}
	return "", "", fmt.Errorf("no status for original transaction %s", originalTransactionID)
}

// mintJWT signs the short-lived ES256 bearer token the App Store Server API
// requires.
func (c *AppleAPIClient) mintJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": c.issuerID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"aud": "appstoreconnect-v1",
		"bid": c.bundleID,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = c.keyID
	return tok.SignedString(c.key)
}
