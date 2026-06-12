package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// setupRateLimitRouter builds a router with RateLimitByIP applied. The
// limiter uses the real clock (golang.org/x/time/rate), so tests pick rates
// where refill is either effectively zero or comfortably faster than the
// sleeps used.
func setupRateLimitRouter(perMinute, burst int) *gin.Engine {
	r := gin.New()
	r.Use(RateLimitByIP(perMinute, burst, time.Minute, time.Minute))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func doRequestFromIP(r *gin.Engine, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = ip + ":12345"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRateLimitByIP_BurstExhaustion_429(t *testing.T) {
	// 1 request/minute refill is effectively zero within the test, so only
	// the burst of 3 is available.
	r := setupRateLimitRouter(1, 3)

	for i := 1; i <= 3; i++ {
		w := doRequestFromIP(r, "10.0.0.1")
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d (within burst)", i, w.Code, http.StatusOK)
		}
	}

	w := doRequestFromIP(r, "10.0.0.1")
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("request 4: status = %d, want %d after burst exhausted", w.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimitByIP_429Body(t *testing.T) {
	r := setupRateLimitRouter(1, 1)

	doRequestFromIP(r, "10.0.0.2")
	w := doRequestFromIP(r, "10.0.0.2")

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if body := w.Body.String(); body == "" || body == "{}" {
		t.Errorf("429 body = %q, want an error payload", body)
	}
}

func TestRateLimitByIP_DistinctIPsIndependent(t *testing.T) {
	r := setupRateLimitRouter(1, 1)

	// Exhaust IP A's bucket.
	if w := doRequestFromIP(r, "10.0.0.3"); w.Code != http.StatusOK {
		t.Fatalf("first request from A: status = %d, want %d", w.Code, http.StatusOK)
	}
	if w := doRequestFromIP(r, "10.0.0.3"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from A: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// A different IP gets its own bucket.
	if w := doRequestFromIP(r, "10.0.0.4"); w.Code != http.StatusOK {
		t.Errorf("request from B: status = %d, want %d (buckets must be per-IP)", w.Code, http.StatusOK)
	}
}

func TestRateLimitByIP_RefillsOverTime(t *testing.T) {
	// 60/minute = 1 token per second, burst 1: after draining the bucket, a
	// ~1.1s wait makes exactly one more request admissible.
	r := setupRateLimitRouter(60, 1)

	if w := doRequestFromIP(r, "10.0.0.5"); w.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want %d", w.Code, http.StatusOK)
	}
	if w := doRequestFromIP(r, "10.0.0.5"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("immediate second request: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	time.Sleep(1100 * time.Millisecond)

	if w := doRequestFromIP(r, "10.0.0.5"); w.Code != http.StatusOK {
		t.Errorf("request after refill window: status = %d, want %d", w.Code, http.StatusOK)
	}
}
