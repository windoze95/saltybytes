package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// limiterInfo is a struct that holds a rate limiter and the last time it was seen.
type limiterInfo struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimitByIP applies rate limiting to requests per IP address. Each IP
// gets a token bucket that refills at perMinute requests per minute and
// allows bursts of up to burst requests.
func RateLimitByIP(perMinute int, burst int, cleanupInterval time.Duration, expiration time.Duration) gin.HandlerFunc {
	var limiters sync.Map

	// Cleanup goroutine
	go func() {
		for range time.Tick(cleanupInterval) {
			limiters.Range(func(key, value interface{}) bool {
				if time.Since(value.(*limiterInfo).lastSeen) > expiration {
					limiters.Delete(key)
				}
				return true
			})
		}
	}()

	limit := rate.Limit(float64(perMinute) / 60.0)

	return func(c *gin.Context) {
		ip := c.ClientIP()

		// Use LoadOrStore to ensure thread safety
		actual, _ := limiters.LoadOrStore(ip, &limiterInfo{
			limiter:  rate.NewLimiter(limit, burst),
			lastSeen: time.Now(),
		})

		info := actual.(*limiterInfo)
		info.lastSeen = time.Now()

		if !info.limiter.Allow() {
			// Too many requests
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
			c.Abort()
			return
		}

		c.Next()
	}
}
