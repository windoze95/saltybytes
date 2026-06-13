package middleware

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// limiterInfo is a struct that holds a rate limiter and the last time it was
// seen. lastSeenNano is accessed atomically (UnixNano) because request
// handlers write it while the cleanup goroutine reads it concurrently;
// sync.Map only synchronizes the map slots, not the stored struct's fields.
type limiterInfo struct {
	limiter      *rate.Limiter
	lastSeenNano atomic.Int64
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
				lastSeen := time.Unix(0, value.(*limiterInfo).lastSeenNano.Load())
				if time.Since(lastSeen) > expiration {
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
		fresh := &limiterInfo{limiter: rate.NewLimiter(limit, burst)}
		actual, _ := limiters.LoadOrStore(ip, fresh)

		info := actual.(*limiterInfo)
		info.lastSeenNano.Store(time.Now().UnixNano())

		if !info.limiter.Allow() {
			// Too many requests
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
			c.Abort()
			return
		}

		c.Next()
	}
}
