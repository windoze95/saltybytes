package handlers

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// IAPHandler handles store-billing verification and store server webhooks.
type IAPHandler struct {
	Service *service.IAPService
	Cfg     *config.Config
}

// NewIAPHandler creates a new IAPHandler.
func NewIAPHandler(svc *service.IAPService, cfg *config.Config) *IAPHandler {
	return &IAPHandler{Service: svc, Cfg: cfg}
}

// verifyPurchaseRequest is the app's purchase-verification payload.
// VerificationData is the StoreKit 2 signed transaction JWS (apple) or the
// Play purchase token (google).
type verifyPurchaseRequest struct {
	Platform         string `json:"platform" binding:"required"`
	ProductID        string `json:"product_id"`
	VerificationData string `json:"verification_data" binding:"required"`
}

// VerifyPurchase handles POST /v1/iap/verify.
func (h *IAPHandler) VerifyPurchase(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req verifyPurchaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "error_code": "invalid_payload"})
		return
	}
	if req.Platform != models.StorePlatformApple && req.Platform != models.StorePlatformGoogle {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown platform", "error_code": "invalid_payload"})
		return
	}
	if len(req.VerificationData) > 64<<10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Verification data too large", "error_code": "invalid_payload"})
		return
	}

	sub, err := h.Service.VerifyPurchase(user.ID, req.Platform, req.ProductID, req.VerificationData)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrIAPUnavailable):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Purchases are temporarily unavailable", "error_code": "iap_unavailable"})
		case errors.Is(err, service.ErrIAPLinkedElsewhere):
			c.JSON(http.StatusConflict, gin.H{"error": "This subscription is linked to another SaltyBytes account", "error_code": "subscription_linked_to_other_account"})
		case errors.Is(err, service.ErrIAPUnknownProduct):
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown product", "error_code": "unknown_product"})
		case errors.Is(err, service.ErrIAPVerification):
			c.JSON(http.StatusBadRequest, gin.H{"error": "Purchase verification failed", "error_code": "verification_failed"})
		default:
			logger.Get().Error("purchase verification errored", zap.Uint("user_id", user.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify purchase"})
		}
		return
	}

	logger.Get().Info("purchase verified",
		zap.Uint("user_id", user.ID), zap.String("platform", req.Platform), zap.String("tier", string(sub.Tier)))
	c.JSON(http.StatusOK, gin.H{
		"subscription": sub,
		"limits":       sub.Limits(),
		"store":        h.Service.StoreStateForUser(user.ID),
	})
}

// AppleWebhook handles POST /webhooks/apple — App Store Server Notifications
// V2. Bad signatures get a 401 (Apple retries; sustained failures alert via
// ntfy); processing errors get a 500 so Apple redelivers.
func (h *IAPHandler) AppleWebhook(c *gin.Context) {
	var payload struct {
		SignedPayload string `json:"signedPayload"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil || payload.SignedPayload == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing signedPayload"})
		return
	}

	if err := h.Service.HandleAppleNotification(payload.SignedPayload); err != nil {
		if errors.Is(err, service.ErrIAPVerification) || errors.Is(err, service.ErrIAPUnavailable) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "signature verification failed"})
			return
		}
		logger.Get().Error("apple webhook processing failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

// GoogleWebhook handles POST /webhooks/google — Play Real-time Developer
// Notifications delivered as Pub/Sub push. The push endpoint is
// authenticated by the shared secret in the URL (?token=...). Non-retryable
// problems are acked with a 200 so Pub/Sub stops redelivering them.
func (h *IAPHandler) GoogleWebhook(c *gin.Context) {
	if secret := h.Cfg.EnvVars.RTDNPushSecret; secret != "" {
		if subtle.ConstantTimeCompare([]byte(c.Query("token")), []byte(secret)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "bad token"})
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unreadable body"})
		return
	}

	retryable, err := h.Service.HandleGoogleRTDN(body)
	if err != nil {
		if retryable {
			logger.Get().Error("google RTDN processing failed (will retry)", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}
		logger.Get().Warn("google RTDN dropped", zap.Error(err))
	}
	c.JSON(http.StatusOK, gin.H{})
}
