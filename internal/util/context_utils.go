package util

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/models"
)

// GetUserFromContext gets the user from the context.
func GetUserFromContext(c *gin.Context) (*models.User, error) {
	val, ok := c.Get("user")
	if !ok {
		return nil, errors.New("no user information")
	}

	user, ok := val.(*models.User)
	if !ok {
		return nil, errors.New("user information is of the wrong type")
	}

	return user, nil
}

// GetUserIDFromContext gets the user ID from the context.
func GetUserIDFromContext(c *gin.Context) (uint, error) {
	val, ok := c.Get("user_id")
	if !ok {
		return 0, errors.New("no user ID information")
	}

	userID, ok := val.(uint)
	if !ok {
		return 0, errors.New("user ID information is of the wrong type")
	}

	return userID, nil
}
