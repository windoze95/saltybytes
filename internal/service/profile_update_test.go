package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

// stubVerification stands in for EmailVerificationService.
type stubVerification struct{ enabled bool }

func (s stubVerification) Enabled() bool { return s.enabled }

// newProfileHarness builds a user service with a verified account already in it.
// The returned user is the object the repo holds — tests should pass a
// sessionCopy of it to UpdateUser, not this pointer.
func newProfileHarness(t *testing.T, verificationLive bool) (*UserService, *testutil.MockUserRepo, *models.User) {
	t.Helper()

	repo := testutil.NewMockUserRepo()
	svc := &UserService{
		Cfg:          verificationEnabledConfig(),
		Repo:         repo,
		Verification: stubVerification{enabled: verificationLive},
	}

	verifiedAt := time.Now()
	user := &models.User{
		Username:        "cook",
		Email:           "cook@example.com",
		EmailVerifiedAt: &verifiedAt,
		Auth:            &models.UserAuth{AuthType: models.Standard},
	}
	repo.CreateUser(user)
	return svc, repo, user
}

// sessionCopy mimics what the handler actually passes in: a user loaded from the
// database, which is a *different* object from the row the repo will write.
//
// This matters. MockUserRepo hands back the very pointer it stores, so calling
// UpdateUser with the harness's user would make every "did it persist?"
// assertion vacuous — they'd pass on the service's in-memory mutation alone,
// even if it never touched the repo.
func sessionCopy(stored *models.User) *models.User {
	c := *stored
	return &c
}

func TestUpdateUser_EmailChangeUnverifiesAccount(t *testing.T) {
	svc, repo, stored := newProfileHarness(t, true)
	user := sessionCopy(stored)

	if err := svc.UpdateUser(user, "", "new@example.com"); err != nil {
		t.Fatalf("UpdateUser error: %v", err)
	}

	// The in-memory user has to move too: the handler mails user.Email next, and
	// StartVerification refuses to send to an account that still looks verified.
	if user.Email != "new@example.com" {
		t.Errorf("in-memory email = %q, want new@example.com", user.Email)
	}
	if user.EmailVerified() {
		t.Error("in-memory user still looks verified after an address change")
	}

	// And it has to be persisted, not just mutated in memory.
	written, _ := repo.GetUserByID(stored.ID)
	if written.Email != "new@example.com" {
		t.Errorf("stored email = %q, want new@example.com", written.Email)
	}
	if written.EmailVerified() {
		t.Error("stored account still verified on an address nobody proved")
	}
}

func TestUpdateUser_EmailChangeAutoVerifiesWhileVerificationOff(t *testing.T) {
	svc, repo, stored := newProfileHarness(t, false)

	if err := svc.UpdateUser(sessionCopy(stored), "", "new@example.com"); err != nil {
		t.Fatalf("UpdateUser error: %v", err)
	}

	written, _ := repo.GetUserByID(stored.ID)
	if !written.EmailVerified() {
		t.Error("with verification off, a changed address must be auto-verified — the gate is a no-op and nothing would ever send a code")
	}
}

func TestUpdateUser_SameAddressDifferentCaseKeepsVerification(t *testing.T) {
	svc, repo, stored := newProfileHarness(t, true)

	if err := svc.UpdateUser(sessionCopy(stored), "", "Cook@Example.com"); err != nil {
		t.Fatalf("UpdateUser error: %v", err)
	}

	written, _ := repo.GetUserByID(stored.ID)
	if !written.EmailVerified() {
		t.Error("retyping the same address in different case must not cost the user their verified status")
	}
}

func TestUpdateUser_TakenEmailIsRejected(t *testing.T) {
	svc, repo, stored := newProfileHarness(t, true)

	otherVerified := time.Now()
	repo.CreateUser(&models.User{
		Username:        "taken",
		Email:           "taken@example.com",
		EmailVerifiedAt: &otherVerified,
		Auth:            &models.UserAuth{AuthType: models.Standard},
	})

	err := svc.UpdateUser(sessionCopy(stored), "", "taken@example.com")
	if !errors.Is(err, repository.ErrEmailTaken) {
		t.Errorf("UpdateUser with a taken email = %v, want ErrEmailTaken", err)
	}

	written, _ := repo.GetUserByID(stored.ID)
	if written.Email != "cook@example.com" || !written.EmailVerified() {
		t.Error("a rejected email change must leave the account untouched")
	}
}

func TestUpdateUser_FirstNameOnlyKeepsVerification(t *testing.T) {
	svc, repo, stored := newProfileHarness(t, true)

	if err := svc.UpdateUser(sessionCopy(stored), "Julia", ""); err != nil {
		t.Fatalf("UpdateUser error: %v", err)
	}

	written, _ := repo.GetUserByID(stored.ID)
	if written.FirstName != "Julia" {
		t.Errorf("stored first name = %q, want Julia", written.FirstName)
	}
	if !written.EmailVerified() {
		t.Error("changing only the display name must not touch verification")
	}
}

func TestValidateFirstName(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())

	tests := []struct {
		name      string
		firstName string
		wantErr   bool
	}{
		{"empty is allowed (optional)", "", false},
		{"plain", "Julian", false},
		{"accented", "José", false},
		{"umlaut", "Zoë", false},
		{"non-latin script", "李", false},
		{"hyphenated", "Mary-Jane", false},
		{"apostrophe", "O'Brien", false},
		{"curly apostrophe", "O’Brien", false},
		{"initials", "J. R.", false},
		{"two words", "Ann Marie", false},
		{"at the length limit", strings.Repeat("a", maxFirstNameLength), false},

		{"over the length limit", strings.Repeat("a", maxFirstNameLength+1), true},
		{"absurdly long", strings.Repeat("a", 5000), true},
		{"digits", "Chef2000", true},
		{"emoji", "Julian🍳", true},
		{"newline", "Julian\nAdmin", true},
		{"tab", "Julian\tAdmin", true},
		{"control character", "Julian\x00", true},
		{"punctuation only", "---", true},
		{"symbols", "<script>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.ValidateFirstName(tt.firstName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFirstName(%q) error = %v, wantErr %v", tt.firstName, err, tt.wantErr)
			}
		})
	}
}

func TestValidateEmail_LengthCap(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())

	long := strings.Repeat("a", 250) + "@example.com" // valid shape, 262 chars
	if err := svc.ValidateEmail(long); err == nil {
		t.Error("ValidateEmail: an over-long address should fail")
	}
	if err := svc.ValidateEmail("fine@example.com"); err != nil {
		t.Errorf("ValidateEmail: a normal address should pass, got %v", err)
	}
}
