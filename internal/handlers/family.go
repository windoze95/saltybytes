package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FamilyHandler is the handler for family-related requests.
type FamilyHandler struct {
	Service *service.FamilyService
}

// NewFamilyHandler is the constructor function for initializing a new FamilyHandler.
func NewFamilyHandler(familyService *service.FamilyService) *FamilyHandler {
	return &FamilyHandler{Service: familyService}
}

// CreateFamily creates a new family for the authenticated user.
func (h *FamilyHandler) CreateFamily(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	family, err := h.Service.CreateFamily(user.ID, req.Name)
	if err != nil {
		logger.Get().Error("failed to create family", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create family"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"family": family})
}

// GetFamily retrieves the authenticated user's family.
func (h *FamilyHandler) GetFamily(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	family, err := h.Service.GetFamily(user.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusOK, gin.H{"family": nil})
			return
		}
		logger.Get().Error("failed to get family", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get family"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"family": family})
}

// AddMember adds a new member to the authenticated user's family.
func (h *FamilyHandler) AddMember(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Name         string `json:"name" binding:"required"`
		Relationship string `json:"relationship"`
		UserID       *uint  `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	family, err := h.Service.GetFamily(user.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "family not found"})
		return
	}

	member, err := h.Service.AddMember(family.ID, req.Name, req.Relationship, req.UserID)
	if err != nil {
		logger.Get().Error("failed to add family member", zap.Uint("family_id", family.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add family member"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"member": member})
}

// UpdateMember updates a family member's details.
func (h *FamilyHandler) UpdateMember(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	memberID, err := strconv.ParseUint(c.Param("member_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid member ID"})
		return
	}

	if err := h.Service.VerifyMemberOwnership(uint(memberID), user.ID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not own this family member"})
		return
	}

	var req struct {
		Name         string `json:"name" binding:"required"`
		Relationship string `json:"relationship"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	member, err := h.Service.UpdateMember(uint(memberID), req.Name, req.Relationship)
	if err != nil {
		logger.Get().Error("failed to update family member", zap.Uint("member_id", uint(memberID)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update family member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"member": member})
}

// DeleteMember deletes a family member.
func (h *FamilyHandler) DeleteMember(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	memberID, err := strconv.ParseUint(c.Param("member_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid member ID"})
		return
	}

	if err := h.Service.VerifyMemberOwnership(uint(memberID), user.ID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not own this family member"})
		return
	}

	if err := h.Service.DeleteMember(uint(memberID)); err != nil {
		logger.Get().Error("failed to delete family member", zap.Uint("member_id", uint(memberID)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete family member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "member deleted"})
}

// UpdateDietaryProfile updates the dietary profile for a family member.
func (h *FamilyHandler) UpdateDietaryProfile(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	memberID, err := strconv.ParseUint(c.Param("member_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid member ID"})
		return
	}

	if err := h.Service.VerifyMemberOwnership(uint(memberID), user.ID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not own this family member"})
		return
	}

	var profile models.DietaryProfile
	if err := c.ShouldBindJSON(&profile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dietary profile data"})
		return
	}

	if err := h.Service.UpdateDietaryProfile(uint(memberID), &profile); err != nil {
		logger.Get().Error("failed to update dietary profile", zap.Uint("member_id", uint(memberID)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update dietary profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "dietary profile updated"})
}

// DietaryInterview conducts a multi-turn dietary interview for a family member.
func (h *FamilyHandler) DietaryInterview(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	memberID, err := strconv.ParseUint(c.Param("member_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid member ID"})
		return
	}

	if err := h.Service.VerifyMemberOwnership(uint(memberID), user.ID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not own this family member"})
		return
	}

	var req struct {
		Messages []ai.Message `json:"messages" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "messages are required"})
		return
	}

	response, err := h.Service.DietaryInterview(c.Request.Context(), uint(memberID), req.Messages)
	if err != nil {
		logger.Get().Error("dietary interview failed", zap.Uint("member_id", uint(memberID)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dietary interview failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"response": response})
}
