package service

import (
	"strings"
	"testing"
)

func TestCheckUsernamePolicy_Length(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"too short", "ab", true},
		{"at minimum", "abc", false},
		{"at maximum", strings.Repeat("a", maxUsernameLength), false},
		{"one over maximum", strings.Repeat("a", maxUsernameLength+1), true},
		{"far over maximum", strings.Repeat("a", 5000), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkUsernamePolicy(tt.username)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkUsernamePolicy(%d chars) error = %v, wantErr %v", len(tt.username), err, tt.wantErr)
			}
		})
	}
}

func TestCheckUsernamePolicy_Reserved(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		// Exact reservations: blocked on their own…
		{"exact reserved word", "root", true},
		{"exact reserved word, mixed case", "RooT", true},
		{"exact reserved word: user", "user", true},
		{"exact reserved word: test", "test", true},
		{"exact reserved tier", "unlimited", true},
		// …but not as a fragment of a real name. These are the names a
		// substring match on "test"/"user" would wrongly eat.
		{"reserved word as fragment: sweetest", "sweetestchef", false},
		{"reserved word as fragment: greatest", "thegreatest", false},
		{"reserved word as fragment: user", "validuser123", false},
		{"reserved word as fragment: home", "homecook", false},

		// Substring reservations: blocked anywhere in the name.
		{"brand alone", "saltybytes", true},
		{"brand embedded", "thesaltybytesguy", true},
		{"brand suffixed", "saltybytesadmin", true},
		{"admin alone", "admin", true},
		{"admin embedded", "xxadminxx", true},
		{"administrator", "administrator", true},
		{"moderator embedded", "recipemoderator", true},
		{"owner handle", "windoze95", true},
		{"reserved personal handle", "russianminx", true},
		{"reserved personal handle, suffixed", "russianminxx", true},
		{"owner real-name handle", "juliandice", true},

		// Deliberately still available.
		{"julian stays available", "julian", false},
		{"yana stays available", "yana", false},

		// Leetspeak evasion is folded before matching.
		{"leetspeak admin", "4dm1n", true},
		{"leetspeak brand", "s4l7ybyte5", true},
		{"leetspeak root", "r00t", true},

		// Ordinary names still pass.
		{"ordinary name", "chefjulia", false},
		{"ordinary name with digits", "baker2026", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkUsernamePolicy(tt.username)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkUsernamePolicy(%q) error = %v, wantErr %v", tt.username, err, tt.wantErr)
			}
		})
	}
}

func TestCheckUsernamePolicy_CharacterSet(t *testing.T) {
	for _, username := range []string{"user@name", "with space", "under_score", "hyphen-ated", "emoji🍳"} {
		if err := checkUsernamePolicy(username); err == nil {
			t.Errorf("checkUsernamePolicy(%q): non-alphanumeric should fail", username)
		}
	}
}

// The reserved lists are only reachable for input that already passed the
// format rules, so an entry that can never match is dead weight.
func TestForbiddenListsAreReachable(t *testing.T) {
	for word := range forbiddenExact {
		if err := checkFormatOnly(word); err != nil {
			t.Errorf("forbiddenExact entry %q can never match: %v", word, err)
		}
	}
	for _, word := range forbiddenSubstrings {
		if word != strings.ToLower(word) {
			t.Errorf("forbiddenSubstrings entry %q must be lowercase — matching is done on a lowercased name", word)
		}
	}
}

// checkFormatOnly mirrors the format rules checkUsernamePolicy applies before
// consulting the reserved lists.
func checkFormatOnly(username string) error {
	length := len([]rune(username))
	if length < minUsernameLength {
		return errTooShort
	}
	if length > maxUsernameLength {
		return errTooLong
	}
	for _, r := range username {
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'z'
		isUpper := r >= 'A' && r <= 'Z'
		if !isDigit && !isLower && !isUpper {
			return errNotAlphanumeric
		}
	}
	return nil
}

var (
	errTooShort        = errStr("shorter than the minimum length")
	errTooLong         = errStr("longer than the maximum length")
	errNotAlphanumeric = errStr("not alphanumeric")
)

type errStr string

func (e errStr) Error() string { return string(e) }
