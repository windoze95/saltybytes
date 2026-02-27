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

// RateLimitByIP applies rate limiting to requests per IP address.
func RateLimitByIP(rps int, cleanupInterval time.Duration, expiration time.Duration) gin.HandlerFunc {
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

	return func(c *gin.Context) {
		ip := c.ClientIP()

		// Use LoadOrStore to ensure thread safety
		actual, _ := limiters.LoadOrStore(ip, &limiterInfo{
			limiter:  rate.NewLimiter(rate.Limit(rps), rps),
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
