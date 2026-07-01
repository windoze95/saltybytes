package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FinderSessionHandler serves the (ungated) saved recipe-finder session history.
type FinderSessionHandler struct {
	Service *service.FinderSessionService
}

// NewFinderSessionHandler creates a new FinderSessionHandler.
func NewFinderSessionHandler(svc *service.FinderSessionService) *FinderSessionHandler {
	return &FinderSessionHandler{Service: svc}
}

// ListSessions handles GET /v1/recipes/finder/sessions?page=&page_size=.
func (h *FinderSessionHandler) ListSessions(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	page := 1
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	pageSize := 20
	if ps, err := strconv.Atoi(c.Query("page_size")); err == nil && ps > 0 && ps <= 100 {
		pageSize = ps
	}

	sessions, total, err := h.Service.List(c.Request.Context(), user.ID, pageSize, (page-1)*pageSize)
	if err != nil {
		logger.Get().Error("failed to list finder sessions", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "total": total, "page": page, "page_size": pageSize})
}

// GetSession handles GET /v1/recipes/finder/sessions/:session_id.
func (h *FinderSessionHandler) GetSession(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id, err := strconv.ParseUint(c.Param("session_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session ID"})
		return
	}

	session, err := h.Service.Get(c.Request.Context(), user.ID, uint(id))
	if err != nil {
		// A not-owned session is reported as not-found so a user can't probe for
		// the existence of other users' sessions.
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, service.ErrFinderSessionNotOwned) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		logger.Get().Error("failed to get finder session", zap.Uint("session_id", uint(id)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"session": session})
}

// DeleteSession handles DELETE /v1/recipes/finder/sessions/:session_id.
func (h *FinderSessionHandler) DeleteSession(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id, err := strconv.ParseUint(c.Param("session_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session ID"})
		return
	}

	if err := h.Service.Delete(c.Request.Context(), user.ID, uint(id)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, service.ErrFinderSessionNotOwned) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		logger.Get().Error("failed to delete finder session", zap.Uint("session_id", uint(id)), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session deleted"})
}
