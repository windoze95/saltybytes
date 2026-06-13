package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"golang.org/x/crypto/bcrypt"
)

func newTestUserHandler() (*UserHandler, *testutil.MockUserRepo) {
	repo := testutil.NewMockUserRepo()
	cfg := &config.Config{
		EnvVars: config.EnvVars{
			JwtSecretKey: "test-jwt-secret-key",
		},
	}
	svc := service.NewUserService(cfg, repo)
	handler := NewUserHandler(svc)
	return handler, repo
}

func TestCreateUser_Handler_Success(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.POST("/users", handler.CreateUser)

	body := `{
		"username": "chefbob42",
		"first_name": "New",
		"email": "new@example.com",
		"password": "Password1!"
	}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["access_token"] == nil {
		t.Error("response should contain 'access_token'")
	}
	if resp["refresh_token"] == nil {
		t.Error("response should contain 'refresh_token'")
	}
	if resp["user"] == nil {
		t.Error("response should contain 'user'")
	}
}

func TestCreateUser_Handler_MissingFields(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.POST("/users", handler.CreateUser)

	body := `{"username": "test"}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateUser_Handler_InvalidPassword(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.POST("/users", handler.CreateUser)

	body := `{
		"username": "chefbob42",
		"email": "new@example.com",
		"password": "weak"
	}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestLoginUser_Handler_Success(t *testing.T) {
	handler, repo := newTestUserHandler()

	// Create a user in the mock repo
	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte("Password1!"), 10)
	repo.CreateUser(&models.User{
		Username: "testuser",
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{KeepScreenAwake: true},
		Personalization: &models.Personalization{UnitSystem: "us_customary"},
	})

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)

	body := `{"username": "testuser", "password": "Password1!"}`
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["access_token"] == nil {
		t.Error("response should contain 'access_token'")
	}
	if resp["refresh_token"] == nil {
		t.Error("response should contain 'refresh_token'")
	}
}

func TestLoginUser_Handler_InvalidCredentials(t *testing.T) {
	handler, repo := newTestUserHandler()

	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte("Correct1!"), 10)
	repo.CreateUser(&models.User{
		Username: "testuser",
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{},
		Personalization: &models.Personalization{},
	})

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)

	body := `{"username": "testuser", "password": "Wrong1!"}`
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestLoginUser_Handler_MissingFields(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)

	body := `{"username": "testuser"}`
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- UpdatePersonalization (partial update) ---

func newPersonalizationTestRouter(repo *testutil.MockUserRepo, user *models.User) (*gin.Engine, *UserHandler) {
	cfg := &config.Config{EnvVars: config.EnvVars{JwtSecretKey: "test-jwt-secret-key"}}
	svc := service.NewUserService(cfg, repo)
	handler := NewUserHandler(svc)

	r := gin.New()
	r.PUT("/users/me/personalization", setUser(user), handler.UpdatePersonalization)
	return r, handler
}

func TestUpdatePersonalization_PartialUpdate_PreservesCookingContext(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Personalization.CookingContext = "induction stove, small kitchen"
	user.Personalization.Requirements = "No peanuts"
	repo.Users[user.ID] = user

	r, _ := newPersonalizationTestRouter(repo, user)

	// Only unit_system is sent — a unit toggle must not wipe other fields.
	body := `{"unit_system": "metric"}`
	req := httptest.NewRequest("PUT", "/users/me/personalization", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	p := repo.Users[user.ID].Personalization
	if p.UnitSystem != "metric" {
		t.Errorf("UnitSystem = %q, want 'metric'", p.UnitSystem)
	}
	if p.CookingContext != "induction stove, small kitchen" {
		t.Errorf("CookingContext = %q, want preserved value", p.CookingContext)
	}
	if p.Requirements != "No peanuts" {
		t.Errorf("Requirements = %q, want preserved value", p.Requirements)
	}
}

func TestUpdatePersonalization_UpdatesOnlyProvidedFields(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Personalization.CookingContext = "gas stove"
	repo.Users[user.ID] = user

	r, _ := newPersonalizationTestRouter(repo, user)

	body := `{"cooking_context": "electric oven", "requirements": "vegetarian"}`
	req := httptest.NewRequest("PUT", "/users/me/personalization", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	p := repo.Users[user.ID].Personalization
	if p.CookingContext != "electric oven" {
		t.Errorf("CookingContext = %q, want 'electric oven'", p.CookingContext)
	}
	if p.Requirements != "vegetarian" {
		t.Errorf("Requirements = %q, want 'vegetarian'", p.Requirements)
	}
	if p.UnitSystem != "us_customary" {
		t.Errorf("UnitSystem = %q, want unchanged 'us_customary'", p.UnitSystem)
	}
}

func TestUpdatePersonalization_InvalidUnitSystem(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r, _ := newPersonalizationTestRouter(repo, user)

	body := `{"unit_system": "imperial"}`
	req := httptest.NewRequest("PUT", "/users/me/personalization", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if repo.Users[user.ID].Personalization.UnitSystem != "us_customary" {
		t.Errorf("UnitSystem should be unchanged after invalid request")
	}
}

// --- Login enumeration hardening ---

func TestLoginUser_Handler_UnknownUserAndBadPassword_IdenticalResponse(t *testing.T) {
	handler, repo := newTestUserHandler()

	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte("Correct1!"), 10)
	repo.CreateUser(&models.User{
		Username: "realuser",
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{},
		Personalization: &models.Personalization{},
	})

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)

	doLogin := func(body string) (int, string) {
		req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	unknownCode, unknownBody := doLogin(`{"username": "nosuchuser", "password": "Whatever1!"}`)
	badPwdCode, badPwdBody := doLogin(`{"username": "realuser", "password": "Wrong1!"}`)

	if unknownCode != http.StatusUnauthorized {
		t.Errorf("unknown-user status = %d, want %d", unknownCode, http.StatusUnauthorized)
	}
	if badPwdCode != http.StatusUnauthorized {
		t.Errorf("bad-password status = %d, want %d", badPwdCode, http.StatusUnauthorized)
	}
	if unknownBody != badPwdBody {
		t.Errorf("login failure bodies differ (username enumeration):\n unknown user: %s\n bad password: %s", unknownBody, badPwdBody)
	}

	var resp map[string]interface{}
	json.Unmarshal([]byte(unknownBody), &resp)
	if resp["error"] != "invalid username or password" {
		t.Errorf("error = %v, want 'invalid username or password'", resp["error"])
	}
}

// --- Refresh token versioning / revocation ---

func loginAndGetRefreshToken(t *testing.T, r *gin.Engine, username, password string) string {
	t.Helper()
	body := `{"username": "` + username + `", "password": "` + password + `"}`
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	token, _ := resp["refresh_token"].(string)
	if token == "" {
		t.Fatal("login response missing refresh_token")
	}
	return token
}

func doRefresh(r *gin.Engine, refreshToken string) *httptest.ResponseRecorder {
	body := `{"refresh_token": "` + refreshToken + `"}`
	req := httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func newAuthTestRouter(t *testing.T) (*gin.Engine, *testutil.MockUserRepo, *models.User) {
	t.Helper()
	handler, repo := newTestUserHandler()

	hashedPwd, _ := bcrypt.GenerateFromPassword([]byte("Password1!"), 10)
	user := &models.User{
		Username: "refreshuser",
		Auth: &models.UserAuth{
			HashedPassword: string(hashedPwd),
			AuthType:       models.Standard,
		},
		Settings:        &models.UserSettings{},
		Personalization: &models.Personalization{},
	}
	repo.CreateUser(user)

	r := gin.New()
	r.POST("/auth/login", handler.LoginUser)
	r.POST("/auth/refresh", handler.RefreshToken)
	r.POST("/auth/logout", setUser(user), handler.Logout)
	return r, repo, user
}

func TestRefreshToken_Success_WithCurrentVersion(t *testing.T) {
	r, _, _ := newAuthTestRouter(t)

	refreshToken := loginAndGetRefreshToken(t, r, "refreshuser", "Password1!")
	w := doRefresh(r, refreshToken)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["access_token"] == nil || resp["refresh_token"] == nil {
		t.Error("refresh response should contain access_token and refresh_token")
	}
}

func TestRefreshToken_MissingVersionClaim_TreatedAsZero(t *testing.T) {
	// Tokens minted before versioning carry no token_version claim; they must
	// keep working while UserAuth.TokenVersion is still 0.
	r, _, user := newAuthTestRouter(t)

	legacyToken := makeLegacyRefreshToken(t, user.ID)
	w := doRefresh(r, legacyToken)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (legacy token with version 0). body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestRefreshToken_VersionMismatch_401(t *testing.T) {
	r, repo, user := newAuthTestRouter(t)

	refreshToken := loginAndGetRefreshToken(t, r, "refreshuser", "Password1!")

	// Bump the version out from under the token (e.g. logout on another device)
	repo.Users[user.ID].Auth.TokenVersion = 5

	w := doRefresh(r, refreshToken)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestRefreshToken_UnknownUser_401(t *testing.T) {
	handler, _ := newTestUserHandler()
	r := gin.New()
	r.POST("/auth/refresh", handler.RefreshToken)

	// Valid signature, but user 999 doesn't exist in the repo
	token := makeLegacyRefreshToken(t, 999)
	w := doRefresh(r, token)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestLogout_RevokesRefreshTokens(t *testing.T) {
	r, repo, user := newAuthTestRouter(t)

	refreshToken := loginAndGetRefreshToken(t, r, "refreshuser", "Password1!")

	// Logout
	req := httptest.NewRequest("POST", "/auth/logout", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d. body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
	if got := repo.Users[user.ID].Auth.TokenVersion; got != 1 {
		t.Errorf("TokenVersion after logout = %d, want 1", got)
	}

	// The pre-logout refresh token must now be rejected
	w2 := doRefresh(r, refreshToken)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("refresh with revoked token status = %d, want %d", w2.Code, http.StatusUnauthorized)
	}

	// A fresh login issues a token carrying the new version, which works
	newToken := loginAndGetRefreshToken(t, r, "refreshuser", "Password1!")
	w3 := doRefresh(r, newToken)
	if w3.Code != http.StatusOK {
		t.Errorf("refresh with post-logout token status = %d, want %d. body: %s", w3.Code, http.StatusOK, w3.Body.String())
	}
}

// makeLegacyRefreshToken mints a refresh token without a token_version claim,
// mimicking tokens issued before revocation support.
func makeLegacyRefreshToken(t *testing.T, userID uint) string {
	t.Helper()
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
		"type":    "refresh",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte("test-jwt-secret-key"))
	if err != nil {
		t.Fatalf("failed to sign legacy token: %v", err)
	}
	return s
}
