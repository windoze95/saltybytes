package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireAdminToken guards the admin API. The dashboard authenticates by sending
// the shared admin token in the X-Admin-Token header (or Authorization: Bearer).
// When no token is configured the admin API is disabled entirely (503) — it is
// never left open. The compare is constant-time to avoid leaking the token via
// timing.
func RequireAdminToken(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "admin API disabled (ADMIN_TOKEN not configured)"})
			c.Abort()
			return
		}

		got := c.GetHeader("X-Admin-Token")
		if got == "" {
			if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
				got = strings.TrimPrefix(h, "Bearer ")
			}
		}

		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}
		c.Next()
	}
}
