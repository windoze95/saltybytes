package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/util"
)

// RequireVerifiedEmail gates AI-cost endpoints behind a verified email
// address, so throwaway signups can't farm free-tier AI quota. Must run
// after AttachUserToContext. A no-op while email verification is disabled
// (enabled=false), so the feature can ship dark.
func RequireVerifiedEmail(enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled {
			c.Next()
			return
		}

		user, err := util.GetUserFromContext(c)
		if err != nil || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}

		if !user.EmailVerified() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "Please verify your email address to use this feature",
				"error_code": "email_unverified",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
