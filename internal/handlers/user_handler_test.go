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
		Personalization: &models.Personalization{UnitSystem: models.USCustomary},
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
