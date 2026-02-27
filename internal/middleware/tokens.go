package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// VerifyTokenMiddleware verifies the JWT token provided in the Authorization header.
func VerifyTokenMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		tokenString = strings.TrimSpace(tokenString)

		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(cfg.EnvVars.JwtSecretKey), nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "Invalid or expired token"})
			c.Abort()
			return
		}

		// Ensure this is an access token, not a refresh token
		tokenType, ok := claims["type"].(string)
		if !ok || tokenType != "access" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "Invalid token type"})
			c.Abort()
			return
		}

		// Type assert to float64 (default for JSON numbers)
		if idFloat, ok := claims["user_id"].(float64); ok {
			// Convert to uint
			userID := uint(idFloat)
			// Set the userID in the context
			c.Set("user_id", userID)
			c.Next()
		} else {
			// Handle error: claim is not a float64
			c.JSON(http.StatusBadRequest, gin.H{"message": "Invalid user_id in token"})
			c.Abort()
			return
		}
	}
}
