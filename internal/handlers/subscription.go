package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// SubscriptionHandler handles subscription-related requests.
type SubscriptionHandler struct {
	Service *service.SubscriptionService
}

// NewSubscriptionHandler creates a new SubscriptionHandler.
func NewSubscriptionHandler(subService *service.SubscriptionService) *SubscriptionHandler {
	return &SubscriptionHandler{Service: subService}
}

// GetSubscription handles GET /v1/subscription
func (h *SubscriptionHandler) GetSubscription(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	sub, err := h.Service.GetSubscription(user.ID)
	if err != nil {
		logger.Get().Error("failed to get subscription", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get subscription"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"subscription": sub})
}

// UpgradeSubscription handles POST /v1/subscription/upgrade
func (h *SubscriptionHandler) UpgradeSubscription(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	sub, err := h.Service.UpgradeSubscription(user.ID)
	if err != nil {
		logger.Get().Error("failed to upgrade subscription", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upgrade subscription"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"subscription": sub, "message": "Subscription upgraded successfully"})
}
