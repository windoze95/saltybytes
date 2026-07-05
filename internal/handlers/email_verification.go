package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// EmailVerificationHandler serves the signup email-verification endpoints.
type EmailVerificationHandler struct {
	Service *service.EmailVerificationService
}

// NewEmailVerificationHandler constructs the handler.
func NewEmailVerificationHandler(svc *service.EmailVerificationService) *EmailVerificationHandler {
	return &EmailVerificationHandler{Service: svc}
}

// RequestVerification (re)sends the 6-digit code to the user's email.
// POST /v1/users/me/email/verification
func (h *EmailVerificationHandler) RequestVerification(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := h.Service.StartVerification(c.Request.Context(), user); err != nil {
		switch {
		case errors.Is(err, service.ErrVerificationDisabled):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "error_code": "verification_disabled"})
		case errors.Is(err, service.ErrAlreadyVerified):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "error_code": "already_verified"})
		case errors.Is(err, service.ErrNoEmailOnAccount):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "error_code": "no_email"})
		case errors.Is(err, service.ErrResendCooldown):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error(), "error_code": "resend_cooldown"})
		case errors.Is(err, service.ErrDailySendLimit):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error(), "error_code": "daily_limit"})
		default:
			logger.Get().Error("failed to send verification email", zap.Uint("user_id", user.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Couldn't send the verification email — please try again"})
		}
		return
	}

	c.Status(http.StatusNoContent)
}

// ConfirmVerification checks the entered code and marks the email verified.
// POST /v1/users/me/email/verification/confirm
func (h *EmailVerificationHandler) ConfirmVerification(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}

	if err := h.Service.ConfirmCode(user, req.Code); err != nil {
		switch {
		case errors.Is(err, service.ErrVerificationDisabled):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "error_code": "verification_disabled"})
		case errors.Is(err, service.ErrCodeInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"error": "That code didn't match. Try again.", "error_code": "code_invalid"})
		case errors.Is(err, service.ErrCodeExpired):
			c.JSON(http.StatusBadRequest, gin.H{"error": "That code expired — request a new one.", "error_code": "code_expired"})
		case errors.Is(err, service.ErrTooManyAttempts):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many wrong codes — request a new one.", "error_code": "too_many_attempts"})
		default:
			logger.Get().Error("failed to confirm verification code", zap.Uint("user_id", user.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Something went wrong, please try again"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Email verified", "user": service.ToUserResponse(user)})
}
