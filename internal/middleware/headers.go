package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CheckIDHeader checks the X-SaltyBytes-Identifier header for a specific value.
func CheckIDHeader(id string) gin.HandlerFunc {
	return func(c *gin.Context) {
		idHeaderValue := c.GetHeader("X-SaltyBytes-Identifier")
		if idHeaderValue != id {
			// If the header is absent or the value is incorrect, reject the request
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}
		// Otherwise, proceed with the request
		c.Next()
	}
}
