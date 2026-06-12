package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
