package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
)

// AttachUserToContext attaches a user to the context.
func AttachUserToContext(userService *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := util.GetUserIDFromContext(c)
		if err != nil {
			c.Set("user", nil)
			c.Next()
			return
		}

		user, err := userService.GetUserByID(userID)
		if err != nil {
			c.Set("user", nil)
		} else {
			c.Set("user", user)
		}
		c.Next()
	}
}
