package iap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testChain builds a root → intermediate → leaf ECDSA chain shaped like
// Apple's (marker OIDs included when withOIDs), valid over [notBefore,
// notAfter].
type testChain struct {
	rootPool *x509.CertPool
	leafKey  *ecdsa.PrivateKey
	x5c      []string
}

func newTestChain(t *testing.T, withOIDs bool, notBefore, notAfter time.Time) *testChain {
	t.Helper()

	mkKey := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		return k
	}
	rootKey, intKey, leafKey := mkKey(), mkKey(), mkKey()

	oidExt := func(oid []int) []pkix.Extension {
		if !withOIDs {
			return nil
		}
		return []pkix.Extension{{Id: asn1.ObjectIdentifier(oid), Value: []byte{0x05, 0x00}}}
	}

	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Apple Root CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	rootCert, _ := x509.ParseCertificate(rootDER)

	intTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Test WWDR Intermediate"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		ExtraExtensions:       oidExt([]int{1, 2, 840, 113635, 100, 6, 2, 1}),
	}
	intDER, err := x509.CreateCertificate(rand.Reader, intTmpl, rootCert, &intKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create intermediate: %v", err)
	}
	intCert, _ := x509.ParseCertificate(intDER)

	leafTmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(3),
		Subject:         pkix.Name{CommonName: "Test App Store Signing"},
		NotBefore:       notBefore,
		NotAfter:        notAfter,
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtraExtensions: oidExt([]int{1, 2, 840, 113635, 100, 6, 11, 1}),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, intCert, &leafKey.PublicKey, intKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(rootCert)
	return &testChain{
		rootPool: pool,
		leafKey:  leafKey,
		x5c: []string{
			base64.StdEncoding.EncodeToString(leafDER),
			base64.StdEncoding.EncodeToString(intDER),
			base64.StdEncoding.EncodeToString(rootDER),
		},
	}
}

// sign produces a JWS over the claims with the chain in x5c.
func (c *testChain) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims(claims))
	tok.Header["x5c"] = c.x5c
	signed, err := tok.SignedString(c.leafKey)
	if err != nil {
		t.Fatalf("sign JWS: %v", err)
	}
	return signed
}

func testTransactionClaims(now time.Time) map[string]any {
	return map[string]any{
		"originalTransactionId": "100000123",
		"transactionId":         "100000124",
		"productId":             "sb_premium_monthly",
		"bundleId":              "codes.julian.saltybytes",
		"appAccountToken":       AccountTokenForUser(42),
		"purchaseDate":          now.Add(-time.Hour).UnixMilli(),
		"expiresDate":           now.Add(30 * 24 * time.Hour).UnixMilli(),
		"signedDate":            now.UnixMilli(),
		"environment":           "Sandbox",
		"type":                  "Auto-Renewable Subscription",
	}
}

func TestVerifyTransaction_Valid(t *testing.T) {
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", chain.rootPool)

	txn, err := v.VerifyTransaction(chain.sign(t, testTransactionClaims(now)))
	if err != nil {
		t.Fatalf("VerifyTransaction: %v", err)
	}
	if txn.ProductID != "sb_premium_monthly" || txn.OriginalTransactionID != "100000123" {
		t.Errorf("decoded fields wrong: %+v", txn)
	}
	if txn.ExpiresAt().Before(now) {
		t.Errorf("expiry decoded wrong: %v", txn.ExpiresAt())
	}
	if txn.Revoked() {
		t.Error("transaction should not be revoked")
	}
	if got, ok := UserForAccountToken(txn.AppAccountToken); !ok || got != 42 {
		t.Errorf("account token roundtrip = (%d, %v), want (42, true)", got, ok)
	}
}

func TestVerifyTransaction_WrongBundleID(t *testing.T) {
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", chain.rootPool)

	claims := testTransactionClaims(now)
	claims["bundleId"] = "com.evil.app"
	if _, err := v.VerifyTransaction(chain.sign(t, claims)); err == nil {
		t.Fatal("expected bundle ID mismatch error")
	}
}

func TestVerifyTransaction_TamperedPayload(t *testing.T) {
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", chain.rootPool)

	token := chain.sign(t, testTransactionClaims(now))
	parts := strings.Split(token, ".")
	var payload map[string]any
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(raw, &payload)
	payload["productId"] = "sb_premium_yearly" // upgrade yourself for free
	forged, _ := json.Marshal(payload)
	parts[1] = base64.RawURLEncoding.EncodeToString(forged)

	if _, err := v.VerifyTransaction(strings.Join(parts, ".")); err == nil {
		t.Fatal("expected signature failure on tampered payload")
	}
}

func TestVerifyTransaction_UntrustedRoot(t *testing.T) {
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	otherChain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	// Verifier trusts a different root than the one that signed the chain.
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", otherChain.rootPool)

	if _, err := v.VerifyTransaction(chain.sign(t, testTransactionClaims(now))); err == nil {
		t.Fatal("expected chain verification failure for untrusted root")
	}
}

func TestVerifyTransaction_MarkerOIDsEnforced(t *testing.T) {
	now := time.Now()
	withOIDs := newTestChain(t, true, now.Add(-time.Hour), now.Add(time.Hour))
	withoutOIDs := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))

	strict := &AppleVerifier{BundleID: "codes.julian.saltybytes", roots: withOIDs.rootPool, requireOIDs: true}
	if _, err := strict.VerifyTransaction(withOIDs.sign(t, testTransactionClaims(now))); err != nil {
		t.Fatalf("OID-carrying chain should verify: %v", err)
	}

	strict = &AppleVerifier{BundleID: "codes.julian.saltybytes", roots: withoutOIDs.rootPool, requireOIDs: true}
	if _, err := strict.VerifyTransaction(withoutOIDs.sign(t, testTransactionClaims(now))); err == nil {
		t.Fatal("chain without Apple marker OIDs must be rejected")
	}
}

func TestVerifyTransaction_SignedDateAnchorsCertValidity(t *testing.T) {
	// Certificates already expired *now*, but valid at the payload's
	// signedDate — Apple evaluates signed data at signing time.
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-3*time.Hour), now.Add(-time.Hour))
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", chain.rootPool)

	claims := testTransactionClaims(now)
	claims["signedDate"] = now.Add(-90 * time.Minute).UnixMilli()
	if _, err := v.VerifyTransaction(chain.sign(t, claims)); err != nil {
		t.Fatalf("expired-now-but-valid-at-signedDate chain should verify: %v", err)
	}
}

func TestVerifyNotification_NestedPayloads(t *testing.T) {
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", chain.rootPool)

	innerTxn := chain.sign(t, testTransactionClaims(now))
	innerRenewal := chain.sign(t, map[string]any{
		"originalTransactionId": "100000123",
		"autoRenewProductId":    "sb_premium_monthly",
		"autoRenewStatus":       1,
		"signedDate":            now.UnixMilli(),
	})
	outer := chain.sign(t, map[string]any{
		"notificationType": "DID_RENEW",
		"notificationUUID": "uuid-1",
		"data": map[string]any{
			"bundleId":              "codes.julian.saltybytes",
			"environment":           "Sandbox",
			"signedTransactionInfo": innerTxn,
			"signedRenewalInfo":     innerRenewal,
		},
		"signedDate": now.UnixMilli(),
	})

	notif, txn, renewal, err := v.VerifyNotification(outer)
	if err != nil {
		t.Fatalf("VerifyNotification: %v", err)
	}
	if notif.NotificationType != "DID_RENEW" {
		t.Errorf("type = %s", notif.NotificationType)
	}
	if txn == nil || txn.ProductID != "sb_premium_monthly" {
		t.Errorf("nested transaction wrong: %+v", txn)
	}
	if renewal == nil || renewal.AutoRenewStatus != 1 {
		t.Errorf("nested renewal wrong: %+v", renewal)
	}
}

func TestVerifyNotification_TamperedInnerTransaction(t *testing.T) {
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", chain.rootPool)

	innerTxn := chain.sign(t, testTransactionClaims(now))
	parts := strings.Split(innerTxn, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"productId":"sb_premium_yearly"}`))

	outer := chain.sign(t, map[string]any{
		"notificationType": "DID_RENEW",
		"data": map[string]any{
			"bundleId":              "codes.julian.saltybytes",
			"signedTransactionInfo": strings.Join(parts, "."),
		},
		"signedDate": now.UnixMilli(),
	})
	if _, _, _, err := v.VerifyNotification(outer); err == nil {
		t.Fatal("expected nested transaction verification failure")
	}
}

func TestVerifyTransaction_RejectsAlgNone(t *testing.T) {
	now := time.Now()
	chain := newTestChain(t, false, now.Add(-time.Hour), now.Add(time.Hour))
	v := NewAppleVerifierWithRoots("codes.julian.saltybytes", chain.rootPool)

	header, _ := json.Marshal(map[string]any{"alg": "none", "x5c": chain.x5c})
	payload, _ := json.Marshal(testTransactionClaims(now))
	forged := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
	if _, err := v.VerifyTransaction(forged); err == nil {
		t.Fatal("alg=none must be rejected")
	}
}

func TestAccountToken_Roundtrip(t *testing.T) {
	for _, id := range []uint{1, 42, 999999} {
		token := AccountTokenForUser(id)
		got, ok := UserForAccountToken(token)
		if !ok || got != id {
			t.Errorf("roundtrip(%d) = (%d, %v)", id, got, ok)
		}
	}
	for _, bad := range []string{"", "garbage", "11111111-2222-3333-4444-555555555555", "00000000-0000-4000-8000-000000000000"} {
		if _, ok := UserForAccountToken(bad); ok {
			t.Errorf("UserForAccountToken(%q) should not decode", bad)
		}
	}
}

func TestEmbeddedAppleRootParses(t *testing.T) {
	v := NewAppleVerifier("codes.julian.saltybytes")
	if v == nil || v.roots == nil {
		t.Fatal("embedded Apple root failed to build a verifier")
	}
	// Real Apple-signed data must fail against a test chain but the call
	// path proves the embedded DER parses (constructor would panic).
	if _, err := v.VerifyTransaction("not.a.jws"); err == nil {
		t.Fatal("garbage JWS must not verify")
	}
}
