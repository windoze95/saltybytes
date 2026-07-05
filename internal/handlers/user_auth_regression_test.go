package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

// Regression tests for the tester-reported signup/login failures: signups
// that appeared to succeed but couldn't log in (case-sensitive lookup, the
// "Username or Email" field only matching usernames) and opaque signup
// failures (duplicate email surfacing as a 500).

func postJSON(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedHandlerUser(repo *testutil.MockUserRepo, username, email, password string) {
	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte(password), 10)
	repo.CreateUser(&models.User{
		Username: username,
		Email:    email,
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{KeepScreenAwake: true},
		Personalization: &models.Personalization{UnitSystem: "us_customary"},
	})
}

func TestSignupThenLogin_DifferentCase_Roundtrip(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.POST("/users", handler.CreateUser)
	r.POST("/auth/login", handler.LoginUser)

	// Sign up the way an iOS keyboard types it: capitalized.
	w := postJSON(r, "/users", `{"username": "Lincoln42", "email": "lincoln@example.com", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("signup status = %d, body: %s", w.Code, w.Body.String())
	}

	// Log in lowercase — this is the exact flow that used to fail.
	w = postJSON(r, "/auth/login", `{"username": "lincoln42", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestLoginUser_Handler_EmailIdentifier(t *testing.T) {
	handler, repo := newTestUserHandler()
	seedHandlerUser(repo, "testuser", "Test@Example.com", "Password1!")

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)

	// The login screen is labeled "Username or Email" and always sends the
	// value under the "username" key.
	w := postJSON(r, "/auth/login", `{"username": "test@example.com", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login-by-email status = %d, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["access_token"] == nil {
		t.Error("response should contain 'access_token'")
	}
}

func TestLoginUser_Handler_TrimsIdentifier(t *testing.T) {
	handler, repo := newTestUserHandler()
	seedHandlerUser(repo, "testuser", "test@example.com", "Password1!")

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)

	w := postJSON(r, "/auth/login", `{"username": " testuser ", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login with padded identifier status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestCreateUser_Handler_DuplicateEmail_409(t *testing.T) {
	handler, repo := newTestUserHandler()
	seedHandlerUser(repo, "existing", "taken@example.com", "Password1!")

	r := gin.New()
	r.POST("/users", handler.CreateUser)

	// Same email, different case — used to slip past validation and die on
	// the DB constraint as an opaque 500.
	w := postJSON(r, "/users", `{"username": "newperson", "email": "Taken@Example.com", "password": "Password1!"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusConflict, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if msg, _ := resp["error"].(string); !strings.Contains(msg, "email") {
		t.Errorf("error message %q should mention the email", msg)
	}
}

func TestCreateUser_Handler_TrimsFields(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.POST("/users", handler.CreateUser)
	r.POST("/auth/login", handler.LoginUser)

	// Keyboard autocomplete loves trailing spaces. Untrimmed, the username
	// would fail alphanumeric validation and the email would fail format
	// validation.
	w := postJSON(r, "/users", `{"username": "chefbob42 ", "email": " new@example.com", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("signup status = %d, body: %s", w.Code, w.Body.String())
	}

	w = postJSON(r, "/auth/login", `{"username": "chefbob42", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestCreateUser_Handler_PasswordWithCommonSpecialChar(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.POST("/users", handler.CreateUser)

	// '.' was not in the old special-character allowlist, so this signup
	// used to 400 — and the app swallowed the message.
	w := postJSON(r, "/users", `{"username": "chefbob42", "email": "new@example.com", "password": "Password1."}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
}

func TestLoginUser_Handler_InfraError_500(t *testing.T) {
	handler, repo := newTestUserHandler()
	repo.GetUserAuthErr = errors.New("connection refused")

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)

	w := postJSON(r, "/auth/login", `{"username": "testuser", "password": "Password1!"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (DB outages must not read as bad credentials). body: %s", w.Code, w.Body.String())
	}
}
