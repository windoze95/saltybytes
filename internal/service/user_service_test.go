package service

import (
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

func newTestUserService(repo *testutil.MockUserRepo) *UserService {
	return &UserService{
		Cfg:  &config.Config{},
		Repo: repo,
	}
}

func TestCreateUser_Success(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)

	user, err := svc.CreateUser("testuser", "Test", "test@example.com", "Password1!")
	if err != nil {
		t.Fatalf("CreateUser error: %v", err)
	}
	if user == nil {
		t.Fatal("CreateUser returned nil user")
	}
	if user.Username != "testuser" {
		t.Errorf("Username = %q, want 'testuser'", user.Username)
	}
	if user.Email != "test@example.com" {
		t.Errorf("Email = %q", user.Email)
	}
	if user.Auth == nil {
		t.Fatal("Auth should not be nil")
	}
	if user.Auth.AuthType != models.Standard {
		t.Errorf("AuthType = %q, want 'standard'", user.Auth.AuthType)
	}
	// Verify password was hashed
	err = bcrypt.CompareHashAndPassword([]byte(user.Auth.HashedPassword), []byte("Password1!"))
	if err != nil {
		t.Error("Password was not correctly hashed")
	}
	// Verify default settings
	if user.Settings == nil || !user.Settings.KeepScreenAwake {
		t.Error("Default KeepScreenAwake should be true")
	}
	if user.Personalization == nil || user.Personalization.UnitSystem != models.USCustomary {
		t.Error("Default UnitSystem should be USCustomary")
	}
}

func TestCreateUser_RepoError(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	repo.CreateUserErr = errTest
	svc := newTestUserService(repo)

	_, err := svc.CreateUser("testuser", "Test", "test@example.com", "Password1!")
	if err == nil {
		t.Fatal("CreateUser should return error when repo fails")
	}
}

func TestLoginUser_Success(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)

	// Create a user first
	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte("Password1!"), 10)
	user := &models.User{
		Username: "testuser",
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{KeepScreenAwake: true},
		Personalization: &models.Personalization{UnitSystem: models.USCustomary},
	}
	repo.CreateUser(user)

	// Login
	loggedIn, err := svc.LoginUser("testuser", "Password1!")
	if err != nil {
		t.Fatalf("LoginUser error: %v", err)
	}
	if loggedIn == nil {
		t.Fatal("LoginUser returned nil user")
	}
	if loggedIn.Username != "testuser" {
		t.Errorf("LoginUser username = %q", loggedIn.Username)
	}
}

func TestLoginUser_WrongPassword(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)

	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte("Correct1!"), 10)
	user := &models.User{
		Username: "testuser",
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{},
		Personalization: &models.Personalization{},
	}
	repo.CreateUser(user)

	_, err := svc.LoginUser("testuser", "Wrong1!")
	if err == nil {
		t.Fatal("LoginUser with wrong password should return error")
	}
}

func TestLoginUser_UserNotFound(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	svc := newTestUserService(repo)

	_, err := svc.LoginUser("nonexistent", "Password1!")
	if err == nil {
		t.Fatal("LoginUser with nonexistent user should return error")
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidatePassword("Ab1!")
	if err == nil {
		t.Error("ValidatePassword: too short should fail")
	}
}

func TestValidatePassword_NoUppercase(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidatePassword("password1!")
	if err == nil {
		t.Error("ValidatePassword: no uppercase should fail")
	}
}

func TestValidatePassword_NoLowercase(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidatePassword("PASSWORD1!")
	if err == nil {
		t.Error("ValidatePassword: no lowercase should fail")
	}
}

func TestValidatePassword_NoDigit(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidatePassword("Password!")
	if err == nil {
		t.Error("ValidatePassword: no digit should fail")
	}
}

func TestValidatePassword_NoSpecialChar(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidatePassword("Password1")
	if err == nil {
		t.Error("ValidatePassword: no special char should fail")
	}
}

func TestValidatePassword_Valid(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidatePassword("Password1!")
	if err != nil {
		t.Errorf("ValidatePassword: valid password should pass, got %v", err)
	}
}

func TestValidateUsername_TooShort(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidateUsername("ab")
	if err == nil {
		t.Error("ValidateUsername: too short should fail")
	}
}

func TestValidateUsername_NonAlphanumeric(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidateUsername("user@name")
	if err == nil {
		t.Error("ValidateUsername: non-alphanumeric should fail")
	}
}

func TestValidateUsername_Forbidden(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidateUsername("admin")
	if err == nil {
		t.Error("ValidateUsername: 'admin' should be forbidden")
	}
}

func TestValidateUsername_AlreadyTaken(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := &models.User{Username: "existinguser"}
	repo.CreateUser(user)

	svc := newTestUserService(repo)
	err := svc.ValidateUsername("existinguser")
	if err == nil {
		t.Error("ValidateUsername: already taken should fail")
	}
}

func TestValidateUsername_Valid(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidateUsername("validuser123")
	if err != nil {
		t.Errorf("ValidateUsername: valid username should pass, got %v", err)
	}
}

func TestValidateEmail_Invalid(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidateEmail("not-an-email")
	if err == nil {
		t.Error("ValidateEmail: invalid email should fail")
	}
}

func TestValidateEmail_Valid(t *testing.T) {
	svc := newTestUserService(testutil.NewMockUserRepo())
	err := svc.ValidateEmail("test@example.com")
	if err != nil {
		t.Errorf("ValidateEmail: valid email should pass, got %v", err)
	}
}

func TestToUserResponse(t *testing.T) {
	user := testutil.TestUser()
	resp := ToUserResponse(user)

	if resp.ID != "1" {
		t.Errorf("ID = %q, want '1'", resp.ID)
	}
	if resp.Username != "testuser" {
		t.Errorf("Username = %q, want 'testuser'", resp.Username)
	}
	if resp.FirstName != "Test" {
		t.Errorf("FirstName = %q", resp.FirstName)
	}
	if resp.Email != "test@example.com" {
		t.Errorf("Email = %q", resp.Email)
	}
	if !resp.Settings.KeepScreenAwake {
		t.Error("KeepScreenAwake should be true")
	}
	if resp.Personalization.UnitSystem != 0 {
		t.Errorf("UnitSystem = %d, want 0", resp.Personalization.UnitSystem)
	}
	if resp.Personalization.Requirements != "No peanuts" {
		t.Errorf("Requirements = %q", resp.Personalization.Requirements)
	}
}

// errTest is a shared test error for convenience.
var errTest = errTestType{}

type errTestType struct{}

func (e errTestType) Error() string { return "test error" }
