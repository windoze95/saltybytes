package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/iap"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// SubscriptionHandler handles subscription-related requests.
type SubscriptionHandler struct {
	Service *service.SubscriptionService
	// IAP, when wired, enriches GET /v1/subscription with the store-side
	// subscription state and the account token the app attaches to purchases.
	IAP *service.IAPService
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

	// Include the tier's caps so the app can render allowances without
	// hardcoding them, plus (additive, older clients ignore them) the store
	// subscription backing the tier and the account token the app passes as
	// appAccountToken / obfuscated account ID on store purchases.
	resp := gin.H{"subscription": sub, "limits": sub.Limits(), "account_token": iap.AccountTokenForUser(user.ID)}
	if h.IAP != nil {
		resp["store"] = h.IAP.StoreStateForUser(user.ID)
	}
	c.JSON(http.StatusOK, resp)
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
		// Paid plans are not wired up yet; surface that honestly rather than
		// masking it as a server error.
		logger.Get().Warn("subscription upgrade requested but unavailable", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"subscription": sub, "message": "Subscription upgraded successfully"})
}
