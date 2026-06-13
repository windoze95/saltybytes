package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

const testIDHeaderValue = "test-identifier-value"

func setupIDHeaderRouter() *gin.Engine {
	r := gin.New()
	r.Use(CheckIDHeader(testIDHeaderValue))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestCheckIDHeader_CorrectValue(t *testing.T) {
	r := setupIDHeaderRouter()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-SaltyBytes-Identifier", testIDHeaderValue)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestCheckIDHeader_MissingHeader(t *testing.T) {
	r := setupIDHeaderRouter()

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckIDHeader_WrongValue(t *testing.T) {
	r := setupIDHeaderRouter()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-SaltyBytes-Identifier", "wrong-value")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
