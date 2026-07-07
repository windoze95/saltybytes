// Package iap implements store-side purchase verification for the App Store
// (StoreKit 2 signed JWS, verified locally against Apple's pinned root CA)
// and Google Play (Android Publisher API), plus the account-token scheme that
// ties store purchases back to SaltyBytes users.
package iap

import (
	"fmt"
	"strconv"
	"strings"
)

// accountTokenPrefix makes our synthetic UUIDs recognizable: Apple requires
// appAccountToken to be a UUID string, so the uint user ID is encoded into
// the final segment of a fixed-prefix UUID. The same value is passed as the
// obfuscated account ID on Android.
const accountTokenPrefix = "00000000-0000-4000-8000-"

// AccountTokenForUser encodes a user ID as the UUID the app attaches to
// purchases (appAccountToken on iOS, obfuscated account ID on Android).
func AccountTokenForUser(userID uint) string {
	return fmt.Sprintf("%s%012x", accountTokenPrefix, uint64(userID))
}

// UserForAccountToken decodes an account token back to a user ID. Returns
// (0, false) for anything that isn't one of ours — store payloads may carry
// tokens set by other apps' conventions or none at all.
func UserForAccountToken(token string) (uint, bool) {
	t := strings.ToLower(strings.TrimSpace(token))
	if !strings.HasPrefix(t, accountTokenPrefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(t, accountTokenPrefix)
	if len(suffix) != 12 {
		return 0, false
	}
	id, err := strconv.ParseUint(suffix, 16, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}
