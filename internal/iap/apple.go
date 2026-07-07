package iap

import (
	"crypto/ecdsa"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Apple Root CA - G3, downloaded from apple.com/certificateauthority. All App
// Store server data (StoreKit 2 transaction JWS, App Store Server
// Notifications V2) chains to this root in both Production and Sandbox.
//
//go:embed certs/AppleRootCA-G3.cer
var appleRootCAG3 []byte

// Apple's chain-marker OIDs, per the official app-store-server libraries:
// the signing (leaf) certificate and the WWDR intermediate must each carry
// their marker extension, so a valid-but-unrelated Apple chain can't sign
// store data.
var (
	appleLeafOID         = "1.2.840.113635.100.6.11.1"
	appleIntermediateOID = "1.2.840.113635.100.6.2.1"
)

// AppleTransaction is the subset of a decoded JWSTransaction payload we act
// on. Dates are millisecond epochs, matching Apple's wire format.
type AppleTransaction struct {
	OriginalTransactionID string `json:"originalTransactionId"`
	TransactionID         string `json:"transactionId"`
	ProductID             string `json:"productId"`
	BundleID              string `json:"bundleId"`
	AppAccountToken       string `json:"appAccountToken"`
	PurchaseDateMS        int64  `json:"purchaseDate"`
	ExpiresDateMS         int64  `json:"expiresDate"`
	SignedDateMS          int64  `json:"signedDate"`
	RevocationDateMS      int64  `json:"revocationDate"`
	Environment           string `json:"environment"` // "Production" | "Sandbox"
	Type                  string `json:"type"`        // "Auto-Renewable Subscription", ...
}

// ExpiresAt converts the ms-epoch expiry; zero time when absent.
func (t *AppleTransaction) ExpiresAt() time.Time { return msToTime(t.ExpiresDateMS) }

// Revoked reports whether Apple revoked the transaction (refund/family
// sharing revocation).
func (t *AppleTransaction) Revoked() bool { return t.RevocationDateMS > 0 }

// AppleRenewalInfo is the subset of a decoded JWSRenewalInfo payload we use.
type AppleRenewalInfo struct {
	OriginalTransactionID    string `json:"originalTransactionId"`
	AutoRenewProductID       string `json:"autoRenewProductId"`
	AutoRenewStatus          int    `json:"autoRenewStatus"` // 1 = will renew
	GracePeriodExpiresDateMS int64  `json:"gracePeriodExpiresDate"`
	SignedDateMS             int64  `json:"signedDate"`
}

// AppleNotification is a decoded App Store Server Notification V2 envelope.
// SignedTransactionInfo/SignedRenewalInfo are themselves JWS strings and are
// verified separately.
type AppleNotification struct {
	NotificationType string `json:"notificationType"`
	Subtype          string `json:"subtype"`
	NotificationUUID string `json:"notificationUUID"`
	Data             struct {
		BundleID              string `json:"bundleId"`
		Environment           string `json:"environment"`
		SignedTransactionInfo string `json:"signedTransactionInfo"`
		SignedRenewalInfo     string `json:"signedRenewalInfo"`
	} `json:"data"`
}

// AppleVerifier verifies Apple-signed JWS data (StoreKit 2 transactions and
// server notifications) locally: the x5c chain must verify to the pinned
// Apple root, carry Apple's marker OIDs, and the ES256 signature must check
// out against the leaf key. No network calls and no API key required.
type AppleVerifier struct {
	BundleID string
	roots    *x509.CertPool
	// requireOIDs is disabled only by tests, whose generated chains stand in
	// for Apple's; production chains must always carry the marker OIDs.
	requireOIDs bool
}

// NewAppleVerifier builds a verifier pinned to the embedded Apple Root CA.
func NewAppleVerifier(bundleID string) *AppleVerifier {
	root, err := x509.ParseCertificate(appleRootCAG3)
	if err != nil {
		// The cert is embedded at build time; failing to parse it is a build
		// defect, not a runtime condition.
		panic(fmt.Sprintf("iap: embedded Apple root CA is invalid: %v", err))
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return &AppleVerifier{BundleID: bundleID, roots: pool, requireOIDs: true}
}

// NewAppleVerifierWithRoots builds a verifier trusting the given roots and
// skipping Apple's marker-OID checks — for tests with generated chains.
func NewAppleVerifierWithRoots(bundleID string, roots *x509.CertPool) *AppleVerifier {
	return &AppleVerifier{BundleID: bundleID, roots: roots, requireOIDs: false}
}

// VerifyTransaction verifies a StoreKit 2 signed transaction JWS and decodes
// its payload. The bundle ID must match ours.
func (v *AppleVerifier) VerifyTransaction(jws string) (*AppleTransaction, error) {
	var txn AppleTransaction
	if err := v.verifyJWS(jws, &txn); err != nil {
		return nil, err
	}
	if txn.BundleID != v.BundleID {
		return nil, fmt.Errorf("transaction bundle ID %q is not %q", txn.BundleID, v.BundleID)
	}
	return &txn, nil
}

// VerifyRenewalInfo verifies a standalone JWSRenewalInfo (as returned by the
// App Store Server API's subscription-statuses endpoint). Renewal info
// carries no bundle ID, so only the chain and signature are checked.
func (v *AppleVerifier) VerifyRenewalInfo(jws string) (*AppleRenewalInfo, error) {
	var renewal AppleRenewalInfo
	if err := v.verifyJWS(jws, &renewal); err != nil {
		return nil, err
	}
	return &renewal, nil
}

// VerifyNotification verifies an App Store Server Notification V2
// signedPayload and its nested transaction/renewal JWS. Transaction and
// renewal info may be nil for notification types that omit them (e.g. TEST).
func (v *AppleVerifier) VerifyNotification(signedPayload string) (*AppleNotification, *AppleTransaction, *AppleRenewalInfo, error) {
	var notif AppleNotification
	if err := v.verifyJWS(signedPayload, &notif); err != nil {
		return nil, nil, nil, err
	}
	if notif.Data.BundleID != "" && notif.Data.BundleID != v.BundleID {
		return nil, nil, nil, fmt.Errorf("notification bundle ID %q is not %q", notif.Data.BundleID, v.BundleID)
	}

	var txn *AppleTransaction
	if notif.Data.SignedTransactionInfo != "" {
		var t AppleTransaction
		if err := v.verifyJWS(notif.Data.SignedTransactionInfo, &t); err != nil {
			return nil, nil, nil, fmt.Errorf("nested transaction: %w", err)
		}
		txn = &t
	}
	var renewal *AppleRenewalInfo
	if notif.Data.SignedRenewalInfo != "" {
		var r AppleRenewalInfo
		if err := v.verifyJWS(notif.Data.SignedRenewalInfo, &r); err != nil {
			return nil, nil, nil, fmt.Errorf("nested renewal info: %w", err)
		}
		renewal = &r
	}
	return &notif, txn, renewal, nil
}

// verifyJWS checks an Apple JWS end to end — x5c chain to the pinned root
// (validity evaluated at the payload's signedDate, so notifications signed
// under a since-rotated cert still verify), marker OIDs, ES256 signature —
// then unmarshals the payload into out.
func (v *AppleVerifier) verifyJWS(token string, out any) error {
	chain, err := certChainFromJWS(token)
	if err != nil {
		return err
	}

	// Apple evaluates signed data at its signing time. Read signedDate from
	// the (as yet unverified) payload purely to anchor certificate validity;
	// nothing else is trusted before the signature check below.
	verifyAt := time.Now()
	if sd := unverifiedSignedDate(token); !sd.IsZero() {
		verifyAt = sd
	}

	leaf := chain[0]
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: intermediates,
		CurrentTime:   verifyAt,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("certificate chain does not verify to the Apple root: %w", err)
	}

	if v.requireOIDs {
		if len(chain) < 2 {
			return errors.New("certificate chain is missing the intermediate")
		}
		if !hasOID(leaf, appleLeafOID) {
			return errors.New("leaf certificate is missing Apple's signing marker OID")
		}
		if !hasOID(chain[1], appleIntermediateOID) {
			return errors.New("intermediate certificate is missing Apple's WWDR marker OID")
		}
	}

	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("leaf certificate key is not ECDSA")
	}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"ES256"}))
	if _, err := parser.Parse(token, func(*jwt.Token) (any, error) { return pub, nil }); err != nil {
		return fmt.Errorf("JWS signature invalid: %w", err)
	}

	payload, err := jwsPayload(token)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("JWS payload does not decode: %w", err)
	}
	return nil
}

// certChainFromJWS parses the x5c header of a JWS into certificates
// (leaf first, per RFC 7515).
func certChainFromJWS(token string) ([]*x509.Certificate, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWS")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("JWS header does not decode: %w", err)
	}
	var header struct {
		Alg string   `json:"alg"`
		X5c []string `json:"x5c"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("JWS header is not JSON: %w", err)
	}
	if len(header.X5c) == 0 {
		return nil, errors.New("JWS header has no x5c certificate chain")
	}
	chain := make([]*x509.Certificate, 0, len(header.X5c))
	for i, c := range header.X5c {
		der, err := base64.StdEncoding.DecodeString(c)
		if err != nil {
			return nil, fmt.Errorf("x5c[%d] does not base64-decode: %w", i, err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("x5c[%d] is not a certificate: %w", i, err)
		}
		chain = append(chain, cert)
	}
	return chain, nil
}

// jwsPayload returns the decoded payload segment of a JWS.
func jwsPayload(token string) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWS")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("JWS payload does not decode: %w", err)
	}
	return payload, nil
}

// unverifiedSignedDate extracts signedDate from a JWS payload without
// verifying it — used only to anchor certificate-validity time.
func unverifiedSignedDate(token string) time.Time {
	payload, err := jwsPayload(token)
	if err != nil {
		return time.Time{}
	}
	var probe struct {
		SignedDateMS int64 `json:"signedDate"`
	}
	if json.Unmarshal(payload, &probe) != nil {
		return time.Time{}
	}
	return msToTime(probe.SignedDateMS)
}

func hasOID(cert *x509.Certificate, oid string) bool {
	for _, ext := range cert.Extensions {
		if ext.Id.String() == oid {
			return true
		}
	}
	return false
}

// msToTime converts a millisecond epoch to time.Time; zero input → zero time.
func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
