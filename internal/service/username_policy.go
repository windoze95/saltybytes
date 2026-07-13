package service

import (
	"fmt"
	"strings"
	"unicode/utf8"

	goaway "github.com/TwiN/go-away"
	"github.com/asaskevich/govalidator"
)

// Username length bounds. The 30-character ceiling matches the client rule in
// the app's register_screen.dart — change one and you have to change the other,
// or the app will submit names the API rejects.
const (
	minUsernameLength = 3
	maxUsernameLength = 30
)

// forbiddenSubstrings are terms that may not appear ANYWHERE in a username.
// Every entry is distinctive enough that matching it as a substring has no
// realistic false positive: "admin" also rejects "administrator", "xxadminxx"
// and "saltybytesadmin" without swallowing any ordinary word.
//
// Be strict about what goes here. A short or common word rejects real people:
// "test" would reject "sweetestchef" and "thegreatest", "user" would reject
// "validuser123". Words like those belong in forbiddenExact instead.
var forbiddenSubstrings = []string{
	// Brand and owner handles.
	"saltybyte",   // saltybytes, saltybytesadmin, saltybytez…
	"awfulbits",   //
	"windoze",     // windoze95
	"russianminx", // and russianminxx
	"juliandice",  // the bare handle "julian" stays deliberately available

	// Authority impersonation.
	"admin", // administrator, sysadmin, adm1n, admin1…
	"moderator",
}

// forbiddenExact are reserved words that are only a problem when they ARE the
// whole username — too short or too ordinary to match as substrings. See the
// warning on forbiddenSubstrings.
//
// Entries only need to be alphanumeric and at least minUsernameLength long:
// anything else ("test_user", "saltybytes-admin", "me") is already rejected by
// the format rules before this list is consulted.
var forbiddenExact = newStringSet(
	// Accounts and roles.
	"root", "sys", "system", "systems", "superuser", "user", "users",
	"guest", "anonymous", "anon", "nobody", "null", "none", "undefined",
	"self", "owner", "founder", "staff", "team", "crew", "employee",
	"official", "operator", "security",

	// Auth surface.
	"login", "logout", "signin", "signout", "signup", "register",
	"registration", "auth", "oauth", "token", "session", "password",
	"passwd", "credentials", "verify", "verification", "reset", "recover",

	// Infrastructure and mail.
	"api", "www", "web", "mail", "email", "smtp", "imap", "ftp", "ssh",
	"dns", "cdn", "static", "assets", "media", "img", "image", "images",
	"upload", "uploads", "noreply", "donotreply", "postmaster", "webmaster",
	"hostmaster", "abuse", "spam",

	// Company and support.
	"support", "helpdesk", "help", "faq", "contact", "info", "about",
	"legal", "privacy", "terms", "tos", "policy", "careers", "press",
	"blog", "news", "status",

	// Billing and tiers.
	"billing", "payment", "payments", "checkout", "invoice", "refund",
	"subscribe", "subscription", "subscriptions", "premium", "plus",
	"unlimited", "free", "trial", "upgrade",

	// Product surface.
	"recipe", "recipes", "search", "import", "preview", "explore",
	"discover", "feed", "home", "dashboard", "settings", "profile",
	"account", "accounts", "collection", "collections", "family",
	"families", "connector", "webhook", "webhooks", "mcp",

	// Placeholders and test scaffolding.
	"test", "tester", "testing", "testuser", "newuser", "demo", "example",
	"sample", "dummy", "placeholder", "yourapp", "yourcompany", "yourbrand",
)

// leetReplacer folds digit-for-letter substitutions, so "4dm1n" and "s4l7ybyte5"
// are checked as "admin" and "saltybytes". Usernames are alphanumeric by the
// time this runs, so digits are the only evasion left.
var leetReplacer = strings.NewReplacer(
	"0", "o",
	"1", "i",
	"3", "e",
	"4", "a",
	"5", "s",
	"7", "t",
)

// profanityDetector is read-only once built, so it is shared across requests
// rather than reconstructed on every signup.
var profanityDetector = goaway.NewProfanityDetector().
	WithSanitizeLeetSpeak(true).
	WithSanitizeSpecialCharacters(true).
	WithSanitizeAccents(false)

func newStringSet(items ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

// blocklistCandidates returns the forms of a username to test against the
// reserved lists: as typed (lowercased), and with leetspeak folded back so
// "r00t" is caught alongside "root". The folded form is dropped when it adds
// nothing, which is the common case.
func blocklistCandidates(username string) []string {
	lowered := strings.ToLower(username)
	folded := leetReplacer.Replace(lowered)
	if folded == lowered {
		return []string{lowered}
	}
	return []string{lowered, folded}
}

// checkUsernamePolicy applies every username rule that doesn't need the
// database: length, character set, reserved words and profanity. Uniqueness is
// the caller's job.
func checkUsernamePolicy(username string) error {
	length := utf8.RuneCountInString(username)
	if length < minUsernameLength {
		return fmt.Errorf("username must be at least %d characters", minUsernameLength)
	}
	if length > maxUsernameLength {
		return fmt.Errorf("username must be %d characters or less", maxUsernameLength)
	}

	if !govalidator.IsAlphanumeric(username) {
		return fmt.Errorf("username can only contain alphanumeric characters")
	}

	for _, candidate := range blocklistCandidates(username) {
		if _, reserved := forbiddenExact[candidate]; reserved {
			return fmt.Errorf("username '%s' is not allowed", username)
		}
		for _, term := range forbiddenSubstrings {
			if strings.Contains(candidate, term) {
				return fmt.Errorf("username '%s' is not allowed", username)
			}
		}
	}

	if profanityDetector.IsProfane(username) {
		return fmt.Errorf("username contains inappropriate language")
	}

	return nil
}
