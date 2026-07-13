package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// UserHandler is the handler for user-related requests.
type UserHandler struct {
	Service *service.UserService
	// EmailVerification, when set and enabled, sends the signup verification
	// code after account creation. Nil in tests that don't exercise it.
	EmailVerification *service.EmailVerificationService
}

// NewUserHandler is the constructor function for initializing a new UserHandler.
func NewUserHandler(userService *service.UserService) *UserHandler {
	return &UserHandler{Service: userService}
}

// CreateUser creates a new user.
func (h *UserHandler) CreateUser(c *gin.Context) {
	var newUser struct {
		Username  string `json:"username" binding:"required"`
		FirstName string `json:"first_name"`
		Email     string `json:"email" binding:"required"`
		Password  string `json:"password" binding:"required"`
	}

	// Returns error if a required field is not included
	if err := c.ShouldBindJSON(&newUser); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username, email, and password fields are required"})
		return
	}

	// Mobile keyboards autocapitalize and append stray spaces; normalize the
	// identifier fields before validating. The password is never trimmed —
	// spaces are legitimate password characters.
	newUser.Username = strings.TrimSpace(newUser.Username)
	newUser.Email = strings.TrimSpace(newUser.Email)
	newUser.FirstName = strings.TrimSpace(newUser.FirstName)

	// Validate username
	if err := h.Service.ValidateUsername(newUser.Username); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate the optional display name
	if err := h.Service.ValidateFirstName(newUser.FirstName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate email
	if err := h.Service.ValidateEmail(newUser.Email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Reject duplicate emails up front with a clear message; the DB unique
	// constraint stays as the backstop for races. Stale unverified signups
	// release the address instead of squatting it forever.
	emailTaken, err := h.Service.EmailTakenForSignup(newUser.Email)
	if err != nil {
		logger.Get().Error("failed to check email availability", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}
	if emailTaken {
		c.JSON(http.StatusConflict, gin.H{"error": "email already in use"})
		return
	}

	// Validate password
	if err := h.Service.ValidatePassword(newUser.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create user
	user, err := h.Service.CreateUser(newUser.Username, newUser.FirstName, newUser.Email, newUser.Password)
	if err != nil {
		logger.Get().Error("failed to create user", zap.String("username", newUser.Username), zap.Error(err))
		switch {
		case errors.Is(err, repository.ErrUsernameTaken):
			c.JSON(http.StatusConflict, gin.H{"error": "username already in use"})
		case errors.Is(err, repository.ErrEmailTaken):
			c.JSON(http.StatusConflict, gin.H{"error": "email already in use"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		}
		return
	}

	// Send the verification code (best-effort — a mail hiccup must never
	// fail the signup; the app offers resend from the verify screen).
	if h.EmailVerification != nil && h.EmailVerification.Enabled() {
		if err := h.EmailVerification.StartVerification(c.Request.Context(), user); err != nil {
			logger.Get().Warn("failed to send signup verification email", zap.Uint("user_id", user.ID), zap.Error(err))
		}
	}

	// Log the user in
	accessToken, err := generateAccessToken(user.ID, h.Service.Cfg.EnvVars.JwtSecretKey)
	if err != nil {
		logger.Get().Error("failed to generate access token on signup", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	refreshToken, err := generateRefreshToken(user.ID, userTokenVersion(user), h.Service.Cfg.EnvVars.JwtSecretKey)
	if err != nil {
		logger.Get().Error("failed to generate refresh token on signup", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"access_token": accessToken, "refresh_token": refreshToken, "message": "User signed up successfully", "user": service.ToUserResponse(user)})
}

// LoginUser logs a user in.
func (h *UserHandler) LoginUser(c *gin.Context) {
	var userCredentials struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&userCredentials); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "All fields are required"})
		return
	}

	user, err := h.Service.LoginUser(strings.TrimSpace(userCredentials.Username), userCredentials.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			// Identical generic response for unknown-user and bad-password so
			// the endpoint cannot be used to enumerate usernames.
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
			return
		}
		// Infrastructure failure (e.g. DB outage) — telling the user their
		// credentials are wrong would be a lie they can't fix.
		logger.Get().Error("login failed on internal error", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Something went wrong, please try again"})
		return
	}

	// Log the user in
	accessToken, err := generateAccessToken(user.ID, h.Service.Cfg.EnvVars.JwtSecretKey)
	if err != nil {
		logger.Get().Error("failed to generate access token on login", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	refreshToken, err := generateRefreshToken(user.ID, userTokenVersion(user), h.Service.Cfg.EnvVars.JwtSecretKey)
	if err != nil {
		logger.Get().Error("failed to generate refresh token on login", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"access_token": accessToken, "refresh_token": refreshToken, "message": "User logged in successfully", "user": service.ToUserResponse(user)})
}

// generateAccessToken generates a short-lived JWT access token for a user.
func generateAccessToken(userID uint, secretKey string) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(15 * time.Minute).Unix(),
		"iat":     time.Now().Unix(),
		"type":    "access",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		return "", fmt.Errorf("generateAccessToken: %v", err)
	}
	return tokenString, nil
}

// generateRefreshToken generates a long-lived JWT refresh token for a user.
// tokenVersion is embedded as the "token_version" claim; tokens whose version
// no longer matches UserAuth.TokenVersion are rejected on refresh.
func generateRefreshToken(userID uint, tokenVersion int, secretKey string) (string, error) {
	claims := jwt.MapClaims{
		"user_id":       userID,
		"exp":           time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat":           time.Now().Unix(),
		"type":          "refresh",
		"token_version": tokenVersion,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		return "", fmt.Errorf("generateRefreshToken: %v", err)
	}
	return tokenString, nil
}

// userTokenVersion returns the user's current refresh-token version,
// defaulting to 0 when no auth record is loaded.
func userTokenVersion(user *models.User) int {
	if user != nil && user.Auth != nil {
		return user.Auth.TokenVersion
	}
	return 0
}

// RefreshToken validates a refresh token and issues a new access token.
func (h *UserHandler) RefreshToken(c *gin.Context) {
	var request struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token is required"})
		return
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(request.RefreshToken, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(h.Service.Cfg.EnvVars.JwtSecretKey), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}

	tokenType, ok := claims["type"].(string)
	if !ok || tokenType != "refresh" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token type"})
		return
	}

	idFloat, ok := claims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid user_id in token"})
		return
	}
	userID := uint(idFloat)

	// Load the user and verify the token version so revoked refresh tokens
	// (logout increments UserAuth.TokenVersion) stop working.
	user, err := h.Service.GetUserWithAuthByID(userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}

	// Tokens issued before versioning carry no claim; treat missing as 0,
	// which matches the column default so existing tokens keep working.
	claimVersion := 0
	if v, ok := claims["token_version"].(float64); ok {
		claimVersion = int(v)
	}
	currentVersion := userTokenVersion(user)
	if claimVersion != currentVersion {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}

	accessToken, err := generateAccessToken(userID, h.Service.Cfg.EnvVars.JwtSecretKey)
	if err != nil {
		logger.Get().Error("failed to generate access token on refresh", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	newRefreshToken, err := generateRefreshToken(userID, currentVersion, h.Service.Cfg.EnvVars.JwtSecretKey)
	if err != nil {
		logger.Get().Error("failed to generate refresh token on refresh", zap.Uint("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"access_token": accessToken, "refresh_token": newRefreshToken})
}

// Logout revokes all of the user's outstanding refresh tokens by
// incrementing their token version.
// POST /v1/auth/logout
func (h *UserHandler) Logout(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := h.Service.LogoutUser(user.ID); err != nil {
		logger.Get().Error("failed to revoke refresh tokens on logout", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to log out"})
		return
	}

	c.Status(http.StatusNoContent)
}

// VerifyToken verifies a user's JWT token.
func (h *UserHandler) VerifyToken(c *gin.Context) {
	// Retrieve the user from the context
	user, _ := util.GetUserFromContext(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"isAuthenticated": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"isAuthenticated": true, "user": service.ToUserResponse(user)})
}

// GetUserByID fetches a user by ID.
func (h *UserHandler) GetUserByID(c *gin.Context) {
	// Retrieve the user from the context
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user": service.ToUserResponse(user)})
}

// GetUserSettings fetches a user with settings.
func (h *UserHandler) GetUserSettings(c *gin.Context) {
	// Retrieve the user from the context
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"settings": user.Settings})
}

// UpdateUser updates user profile fields.
func (h *UserHandler) UpdateUser(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		FirstName string `json:"first_name"`
		Email     string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	req.FirstName = strings.TrimSpace(req.FirstName)
	req.Email = strings.TrimSpace(req.Email)

	if err := h.Service.ValidateFirstName(req.FirstName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Note the address change before the update rewrites the in-memory user.
	emailChanged := req.Email != "" && !strings.EqualFold(req.Email, user.Email)
	if emailChanged {
		if err := h.Service.ValidateEmail(req.Email); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	if err := h.Service.UpdateUser(user, req.FirstName, req.Email); err != nil {
		if errors.Is(err, repository.ErrEmailTaken) {
			c.JSON(http.StatusConflict, gin.H{"error": "email already in use"})
			return
		}
		logger.Get().Error("failed to update user", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user"})
		return
	}

	// The address changed, so UpdateUser un-verified the account. Send a code to
	// the new address — best-effort, exactly like signup: a mail hiccup must not
	// fail the update, and the verify screen offers a resend.
	verificationRequired := false
	if emailChanged && h.EmailVerification != nil && h.EmailVerification.Enabled() {
		verificationRequired = true
		if err := h.EmailVerification.StartVerification(c.Request.Context(), user); err != nil {
			logger.Get().Warn("failed to send verification email after address change",
				zap.Uint("user_id", user.ID), zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "User updated successfully",
		// Tells a client it has to collect a code before AI features work again.
		"email_verification_required": verificationRequired,
	})
}

// UpdateSettings updates a user's settings.
func (h *UserHandler) UpdateSettings(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		KeepScreenAwake bool `json:"keep_screen_awake"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if err := h.Service.UpdateSettings(user, req.KeepScreenAwake); err != nil {
		logger.Get().Error("failed to update settings", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Settings updated successfully"})
}

// UpdatePersonalization partially updates a user's personalization settings.
// Only fields present in the request body are written; omitted fields keep
// their current values.
func (h *UserHandler) UpdatePersonalization(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		UnitSystem     *string `json:"unit_system"`
		Requirements   *string `json:"requirements"`
		CookingContext *string `json:"cooking_context"`
		UID            *string `json:"uid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.UnitSystem != nil && *req.UnitSystem != "us_customary" && *req.UnitSystem != "metric" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unit_system must be 'us_customary' or 'metric'"})
		return
	}

	update := &models.PersonalizationUpdate{
		UnitSystem:     req.UnitSystem,
		Requirements:   req.Requirements,
		CookingContext: req.CookingContext,
	}
	if req.UID != nil {
		uid, parseErr := uuid.Parse(*req.UID)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid must be a valid UUID"})
			return
		}
		update.UID = &uid
	}

	if err := h.Service.UpdatePersonalization(user, update); err != nil {
		logger.Get().Error("failed to update personalization", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update personalization"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Personalization updated successfully"})
}
