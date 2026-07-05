package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

// seedLoginUser stores a user the way signup does (username/email kept as
// typed) so login tests exercise the same data shape as production.
func seedLoginUser(repo *testutil.MockUserRepo, username, email, password string) *models.User {
	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte(password), 10)
	user := &models.User{
		Username: username,
		Email:    email,
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{KeepScreenAwake: true},
		Personalization: &models.Personalization{UnitSystem: "us_customary"},
	}
	repo.CreateUser(user)
	return user
}

func TestLoginUser_CaseInsensitiveUsername(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)
	// Mobile keyboards autocapitalize at signup: the stored username is
	// "Lincoln", but the user types it lowercase at login.
	seedLoginUser(repo, "Lincoln", "lincoln@example.com", "Password1!")

	for _, attempt := range []string{"lincoln", "LINCOLN", "Lincoln"} {
		if _, err := svc.LoginUser(attempt, "Password1!"); err != nil {
			t.Errorf("LoginUser(%q) error: %v", attempt, err)
		}
	}
}

func TestLoginUser_TrimsIdentifier(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)
	seedLoginUser(repo, "testuser", "test@example.com", "Password1!")

	if _, err := svc.LoginUser("  testuser ", "Password1!"); err != nil {
		t.Errorf("LoginUser with padded identifier error: %v", err)
	}
}

func TestLoginUser_ByEmail(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)
	seedLoginUser(repo, "testuser", "Test@Example.com", "Password1!")

	loggedIn, err := svc.LoginUser("test@example.com", "Password1!")
	if err != nil {
		t.Fatalf("LoginUser by email error: %v", err)
	}
	if loggedIn.Username != "testuser" {
		t.Errorf("Username = %q, want 'testuser'", loggedIn.Username)
	}
}

func TestLoginUser_UnknownEmail(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)

	_, err := svc.LoginUser("nobody@example.com", "Password1!")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUser_WrongPasswordByEmail(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)
	seedLoginUser(repo, "testuser", "test@example.com", "Correct1!")

	_, err := svc.LoginUser("test@example.com", "Wrong1!")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUser_InfraErrorIsNotInvalidCredentials(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	repo.GetUserAuthErr = errors.New("connection refused")
	svc := newTestUserService(repo)

	_, err := svc.LoginUser("testuser", "Password1!")
	if err == nil {
		t.Fatal("LoginUser should fail when the repo errors")
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Error("infrastructure errors must not masquerade as bad credentials")
	}
}

func TestCreateUser_TrimsIdentifierFields(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)

	user, err := svc.CreateUser("  chefbob42 ", " Bob ", "  bob@example.com ", "Password1!")
	if err != nil {
		t.Fatalf("CreateUser error: %v", err)
	}
	if user.Username != "chefbob42" {
		t.Errorf("Username = %q, want trimmed 'chefbob42'", user.Username)
	}
	if user.FirstName != "Bob" {
		t.Errorf("FirstName = %q, want trimmed 'Bob'", user.FirstName)
	}
	if user.Email != "bob@example.com" {
		t.Errorf("Email = %q, want trimmed 'bob@example.com'", user.Email)
	}
}

func TestEmailInUse(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)
	seedLoginUser(repo, "testuser", "Bob@Example.com", "Password1!")

	inUse, err := svc.EmailInUse("bob@example.com")
	if err != nil {
		t.Fatalf("EmailInUse error: %v", err)
	}
	if !inUse {
		t.Error("EmailInUse should match case-insensitively")
	}

	inUse, err = svc.EmailInUse("free@example.com")
	if err != nil {
		t.Fatalf("EmailInUse error: %v", err)
	}
	if inUse {
		t.Error("EmailInUse should be false for an unknown email")
	}
}

func TestValidatePassword_AcceptsAnySpecialCharacter(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())

	// The old allowlist [!@#$%^&*] rejected all of these.
	for _, pw := range []string{"Password1.", "Password1?", "Password1-", "Password1_", "Pass word1", "Password1("} {
		if err := svc.ValidatePassword(pw); err != nil {
			t.Errorf("ValidatePassword(%q) = %v, want nil", pw, err)
		}
	}
}

func TestValidatePassword_StillRequiresSpecialCharacter(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())

	if err := svc.ValidatePassword("Password1"); err == nil {
		t.Error("ValidatePassword should reject passwords without a special character")
	}
}

func TestValidatePassword_RejectsOverBcryptLimit(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())

	long := "Aa1!" + strings.Repeat("x", 72)
	if err := svc.ValidatePassword(long); err == nil {
		t.Error("ValidatePassword should reject passwords longer than 72 bytes")
	}
}
