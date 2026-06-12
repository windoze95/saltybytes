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
)

// newUserEndpointsRouter wires the authenticated user endpoints with the
// given user attached to the context.
func newUserEndpointsRouter(repo *testutil.MockUserRepo, user *models.User) *gin.Engine {
	cfg := &config.Config{EnvVars: config.EnvVars{JwtSecretKey: "test-jwt-secret-key"}}
	svc := service.NewUserService(cfg, repo)
	handler := NewUserHandler(svc)

	r := gin.New()
	r.GET("/auth/verify", setUser(user), handler.VerifyToken)
	r.GET("/users/me", setUser(user), handler.GetUserByID)
	r.GET("/users/me/settings", setUser(user), handler.GetUserSettings)
	r.PUT("/users/me", setUser(user), handler.UpdateUser)
	r.PUT("/users/me/settings", setUser(user), handler.UpdateSettings)
	return r
}

// --- VerifyToken ---

func TestVerifyToken_Handler_Authenticated(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	req := httptest.NewRequest("GET", "/auth/verify", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["isAuthenticated"] != true {
		t.Errorf("isAuthenticated = %v, want true", resp["isAuthenticated"])
	}
	userResp, ok := resp["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("user is not an object: %v", resp["user"])
	}
	if userResp["username"] != "testuser" {
		t.Errorf("user.username = %v, want 'testuser'", userResp["username"])
	}
}

func TestVerifyToken_Handler_NoUser_401(t *testing.T) {
	handler, _ := newTestUserHandler()

	r := gin.New()
	r.GET("/auth/verify", handler.VerifyToken) // no setUser middleware

	req := httptest.NewRequest("GET", "/auth/verify", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["isAuthenticated"] != false {
		t.Errorf("isAuthenticated = %v, want false", resp["isAuthenticated"])
	}
}

// --- GetUserByID ---

func TestGetUserByID_Handler_Envelope(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	req := httptest.NewRequest("GET", "/users/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	u, ok := resp["user"]
	if !ok {
		t.Fatalf("response missing 'user' envelope key. body: %s", w.Body.String())
	}
	if u["username"] != "testuser" || u["first_name"] != "Test" || u["email"] != "test@example.com" {
		t.Errorf("user = %v, want testuser/Test/test@example.com", u)
	}
	// UserResponse serializes the ID as a string.
	if u["id"] != "1" {
		t.Errorf("user.id = %v, want \"1\"", u["id"])
	}
	personalization, ok := u["personalization"].(map[string]interface{})
	if !ok {
		t.Fatalf("user.personalization missing: %v", u)
	}
	if personalization["unit_system"] != "us_customary" {
		t.Errorf("personalization.unit_system = %v, want 'us_customary'", personalization["unit_system"])
	}
}

// --- GetUserSettings ---

func TestGetUserSettings_Handler_Envelope(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Settings.KeepScreenAwake = true
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	req := httptest.NewRequest("GET", "/users/me/settings", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	settings, ok := resp["settings"]
	if !ok {
		t.Fatalf("response missing 'settings' envelope key. body: %s", w.Body.String())
	}
	// models.UserSettings has no json tags, so fields serialize PascalCase.
	if settings["KeepScreenAwake"] != true {
		t.Errorf("settings.KeepScreenAwake = %v, want true", settings["KeepScreenAwake"])
	}
}

// --- UpdateUser ---

func TestUpdateUser_Handler_UpdatesNameAndEmail(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	body := `{"first_name": "Updated", "email": "updated@example.com"}`
	req := httptest.NewRequest("PUT", "/users/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := repo.Users[user.ID].FirstName; got != "Updated" {
		t.Errorf("FirstName = %q, want 'Updated'", got)
	}
	if got := repo.Users[user.ID].Email; got != "updated@example.com" {
		t.Errorf("Email = %q, want 'updated@example.com'", got)
	}
}

func TestUpdateUser_Handler_EmptyFields_NoChanges(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	req := httptest.NewRequest("PUT", "/users/me", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if repo.Users[user.ID].FirstName != "Test" || repo.Users[user.ID].Email != "test@example.com" {
		t.Error("empty update must not change existing fields")
	}
}

func TestUpdateUser_Handler_InvalidEmail(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	body := `{"email": "not-an-email"}`
	req := httptest.NewRequest("PUT", "/users/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Errorf("status = %d, want an error status for an invalid email", w.Code)
	}
	if repo.Users[user.ID].Email != "test@example.com" {
		t.Errorf("Email = %q, want unchanged after invalid update", repo.Users[user.ID].Email)
	}
}

func TestUpdateUser_Handler_InvalidJSON_400(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	req := httptest.NewRequest("PUT", "/users/me", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- UpdateSettings ---

func TestUpdateSettings_Handler_Persists(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	user.Settings.KeepScreenAwake = true
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	body := `{"keep_screen_awake": false}`
	req := httptest.NewRequest("PUT", "/users/me/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if repo.Users[user.ID].Settings.KeepScreenAwake {
		t.Error("KeepScreenAwake = true, want false after update")
	}
}

func TestUpdateSettings_Handler_InvalidJSON_400(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := newUserEndpointsRouter(repo, user)

	req := httptest.NewRequest("PUT", "/users/me/settings", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- RefreshToken edge cases ---

// makeRefreshTokenWithClaims signs a token with the given claims using the
// test handler secret.
func makeRefreshTokenWithClaims(t *testing.T, claims jwt.MapClaims, secret string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return s
}

func TestRefreshToken_Expired_401(t *testing.T) {
	r, _, user := newAuthTestRouter(t)

	expired := makeRefreshTokenWithClaims(t, jwt.MapClaims{
		"user_id":       user.ID,
		"exp":           time.Now().Add(-time.Hour).Unix(),
		"iat":           time.Now().Add(-2 * time.Hour).Unix(),
		"type":          "refresh",
		"token_version": 0,
	}, "test-jwt-secret-key")

	w := doRefresh(r, expired)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d for expired refresh token. body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestRefreshToken_AccessTokenRejected_401(t *testing.T) {
	r, _, user := newAuthTestRouter(t)

	// Valid signature and expiry, but type=access: must not be usable as a
	// refresh token.
	accessToken := makeRefreshTokenWithClaims(t, jwt.MapClaims{
		"user_id":       user.ID,
		"exp":           time.Now().Add(15 * time.Minute).Unix(),
		"iat":           time.Now().Unix(),
		"type":          "access",
		"token_version": 0,
	}, "test-jwt-secret-key")

	w := doRefresh(r, accessToken)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d for access token used as refresh. body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestRefreshToken_TamperedSignature_401(t *testing.T) {
	r, _, user := newAuthTestRouter(t)

	// Signed with the wrong secret: signature does not verify.
	forged := makeRefreshTokenWithClaims(t, jwt.MapClaims{
		"user_id":       user.ID,
		"exp":           time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat":           time.Now().Unix(),
		"type":          "refresh",
		"token_version": 0,
	}, "attacker-secret")

	w := doRefresh(r, forged)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d for forged signature. body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestRefreshToken_AlgNoneRejected_401(t *testing.T) {
	r, _, user := newAuthTestRouter(t)

	// alg=none token: must be rejected by the HS256 allowlist.
	claims := jwt.MapClaims{
		"user_id":       user.ID,
		"exp":           time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat":           time.Now().Unix(),
		"type":          "refresh",
		"token_version": 0,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	unsigned, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to build alg=none token: %v", err)
	}

	w := doRefresh(r, unsigned)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d for alg=none token. body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestRefreshToken_MissingBody_400(t *testing.T) {
	r, _, _ := newAuthTestRouter(t)

	req := httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d when refresh_token is missing", w.Code, http.StatusBadRequest)
	}
}
