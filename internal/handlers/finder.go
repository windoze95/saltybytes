package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// FinderHandler streams a guided recipe-finder run over SSE.
type FinderHandler struct {
	Service *service.RecipeFinderService
	// SubService gates the finder by the SAME "search" usage limit that
	// GET /recipes/search uses (nil skips gating, e.g. in isolated tests).
	SubService *service.SubscriptionService
}

// FindRecipes handles POST /v1/recipes/find. It runs the bounded finder
// trajectory and streams each step (searching → found → filtering → shortlist →
// warming → refine_ready → done, or a terminal empty/error) as an SSE event.
func (h *FinderHandler) FindRecipes(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req service.FinderRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Subscription gate. The finder drives search, so it is gated by the same
	// "search" limit as the search endpoint. SearchService.SearchRecipes does
	// NOT check or increment usage itself — all gating lives in the search
	// handler — so we gate here exactly once and increment once for a
	// non-cached search below, avoiding any double counting.
	if h.SubService != nil {
		allowed, err := h.SubService.CheckLimit(user.ID, "search")
		if err != nil {
			logger.Get().Error("failed to check search limit", zap.Uint("user_id", user.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check subscription limits"})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "search limit reached; upgrade to premium for unlimited searches"})
			return
		}
	}

	// SSE headers.
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	events := make(chan service.FinderEvent, 32)

	go func() {
		defer close(events)
		defer util.RecoverPanic("recipe finder")
		h.Service.FindRecipes(ctx, user, req, events)
	}()

	c.Stream(func(w io.Writer) bool {
		select {
		case event, ok := <-events:
			if !ok {
				return false
			}
			// Count one search against the user's monthly quota only when the
			// finder actually hit the search provider — mirroring the search
			// handler, which increments for every non-cached search. The
			// "found" event carries whether results came from cache, and it is
			// emitted exactly once per run, so this counts at most once.
			if event.Type == service.FinderEventFound && !event.FromCache {
				h.incrementSearchUsage(user.ID)
			}
			data, _ := json.Marshal(event)
			c.SSEvent(string(event.Type), string(data))
			c.Writer.Flush()
			// Terminal events end the stream.
			return event.Type != service.FinderEventDone &&
				event.Type != service.FinderEventEmpty &&
				event.Type != service.FinderEventError
		case <-ctx.Done():
			return false
		}
	})
}

// incrementSearchUsage records one search against the user's monthly quota.
// Failures are logged but never block the response.
func (h *FinderHandler) incrementSearchUsage(userID uint) {
	if h.SubService == nil {
		return
	}
	if err := h.SubService.IncrementUsage(userID, "search"); err != nil {
		logger.Get().Error("failed to increment search usage", zap.Uint("user_id", userID), zap.Error(err))
	}
}
