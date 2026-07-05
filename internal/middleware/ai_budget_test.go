package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func budgetRouter(budget float64, sumSince func(time.Time) (float64, error)) *gin.Engine {
	r := gin.New()
	r.POST("/ai", AIBudgetGuard(budget, "", sumSince), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func hit(r *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/ai", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAIBudgetGuard_UnderBudgetPasses(t *testing.T) {
	r := budgetRouter(25, func(time.Time) (float64, error) { return 24.99, nil })
	if w := hit(r); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestAIBudgetGuard_OverBudget429(t *testing.T) {
	r := budgetRouter(25, func(time.Time) (float64, error) { return 25.0, nil })
	w := hit(r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429. body: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "ai_budget_exhausted") {
		t.Errorf("body missing error_code: %s", body)
	}
}

func TestAIBudgetGuard_DisabledNeverQueries(t *testing.T) {
	var calls int32
	r := budgetRouter(0, func(time.Time) (float64, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	})
	if w := hit(r); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Error("disabled guard must never query spend")
	}
}

func TestAIBudgetGuard_FailsOpenOnQueryError(t *testing.T) {
	r := budgetRouter(25, func(time.Time) (float64, error) {
		return 0, errors.New("db down")
	})
	if w := hit(r); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fail open)", w.Code)
	}
}

func TestAIBudgetGuard_CachesTheTotal(t *testing.T) {
	var calls int32
	r := budgetRouter(25, func(time.Time) (float64, error) {
		atomic.AddInt32(&calls, 1)
		return 1.0, nil
	})
	hit(r)
	hit(r)
	hit(r)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("sumSince called %d times for 3 quick requests, want 1", got)
	}
}
