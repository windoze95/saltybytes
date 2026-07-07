package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// AttachUserToContext attaches a user to the context. It runs after
// VerifyTokenMiddleware on protected routes, so a missing user_id or a
// failed user lookup (e.g. deleted account with a still-valid token) aborts
// with 401 instead of letting handlers dereference a nil user. Public routes
// do not use this middleware and are unaffected.
func AttachUserToContext(userService *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := util.GetUserIDFromContext(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		user, err := userService.GetUserByID(userID)
		if err != nil {
			logger.Get().Warn("failed to load user for valid token", zap.Uint("user_id", userID), zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		// Locked accounts (runaway guard / suspected compromise) are dead in
		// the water everywhere, with a distinct code so the app can explain.
		if user.Locked() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":      "This account has been locked. Contact support.",
				"error_code": "account_locked",
			})
			return
		}

		c.Set("user", user)
		// Tag the request context so every AI call this request makes is
		// attributed to the user in ai_usage_logs.
		c.Request = c.Request.WithContext(ai.WithUserID(c.Request.Context(), user.ID))
		c.Next()
	}
}
