package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/windoze95/saltybytes-api/internal/config"
)

const testSecret = "test-secret-key-for-jwt-signing"

func init() {
	gin.SetMode(gin.TestMode)
}

func makeTestToken(userID uint, tokenType string, expiry time.Time, secret string) string {
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     expiry.Unix(),
		"iat":     time.Now().Unix(),
		"type":    tokenType,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := token.SignedString([]byte(secret))
	return s
}

func setupTokenRouter() (*gin.Engine, *config.Config) {
	cfg := &config.Config{
		EnvVars: config.EnvVars{
			JwtSecretKey: testSecret,
		},
	}

	r := gin.New()
	r.Use(VerifyTokenMiddleware(cfg))
	r.GET("/test", func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		c.JSON(http.StatusOK, gin.H{"user_id": userID})
	})
	return r, cfg
}

func TestVerifyToken_ValidAccessToken(t *testing.T) {
	r, _ := setupTokenRouter()

	token := makeTestToken(42, "access", time.Now().Add(15*time.Minute), testSecret)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestVerifyToken_MissingAuthorizationHeader(t *testing.T) {
	r, _ := setupTokenRouter()

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestVerifyToken_ExpiredToken(t *testing.T) {
	r, _ := setupTokenRouter()

	token := makeTestToken(42, "access", time.Now().Add(-1*time.Hour), testSecret)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestVerifyToken_InvalidToken(t *testing.T) {
	r, _ := setupTokenRouter()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestVerifyToken_WrongSecret(t *testing.T) {
	r, _ := setupTokenRouter()

	token := makeTestToken(42, "access", time.Now().Add(15*time.Minute), "wrong-secret")

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestVerifyToken_RefreshTokenRejected(t *testing.T) {
	r, _ := setupTokenRouter()

	token := makeTestToken(42, "refresh", time.Now().Add(30*24*time.Hour), testSecret)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (refresh token should be rejected for access routes)", w.Code, http.StatusUnauthorized)
	}
}

func TestVerifyToken_SetsUserIDInContext(t *testing.T) {
	cfg := &config.Config{
		EnvVars: config.EnvVars{
			JwtSecretKey: testSecret,
		},
	}

	var capturedUserID uint
	r := gin.New()
	r.Use(VerifyTokenMiddleware(cfg))
	r.GET("/test", func(c *gin.Context) {
		val, exists := c.Get("user_id")
		if !exists {
			t.Error("user_id not set in context")
			return
		}
		capturedUserID = val.(uint)
		c.JSON(http.StatusOK, gin.H{})
	})

	token := makeTestToken(99, "access", time.Now().Add(15*time.Minute), testSecret)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if capturedUserID != 99 {
		t.Errorf("user_id in context = %d, want 99", capturedUserID)
	}
}
