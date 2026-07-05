package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/notify"
	"go.uber.org/zap"
)

// AIBudgetGuard refuses AI-cost requests once the day's total metered AI
// spend reaches budgetUSD — the app-wide kill switch against runaway AI
// cost. Per-user quotas bound what one account can spend; this bounds the
// whole fleet (bugs, abuse waves, provider price surprises).
//
// sumSince reports the metered spend recorded at or after a given instant
// (backed by ai_usage_logs, so it is restart-safe and shared across tasks).
// The total is cached for a minute, so the worst-case overshoot is one
// minute of burn. Query failures fail OPEN with a warning: a DB blip must
// not take down every AI feature, and the request will hit the same DB
// anyway. budgetUSD <= 0 disables the guard entirely. dashboardURL, when
// set, is the tap-through target on the operator alert fired at the trip.
func AIBudgetGuard(budgetUSD float64, dashboardURL string, sumSince func(time.Time) (float64, error)) gin.HandlerFunc {
	if budgetUSD <= 0 {
		return func(c *gin.Context) { c.Next() }
	}

	var mu sync.Mutex
	var cachedSpent float64
	var cachedAt time.Time
	var cachedDay time.Time

	return func(c *gin.Context) {
		now := time.Now().UTC()
		dayStart := now.Truncate(24 * time.Hour)

		mu.Lock()
		if now.Sub(cachedAt) > time.Minute || !cachedDay.Equal(dayStart) {
			spent, err := sumSince(dayStart)
			if err != nil {
				mu.Unlock()
				logger.Get().Warn("ai budget check failed, allowing request", zap.Error(err))
				c.Next()
				return
			}
			cachedSpent = spent
			cachedAt = now
			cachedDay = dayStart
		}
		spent := cachedSpent
		mu.Unlock()

		if spent >= budgetUSD {
			logger.Get().Warn("daily AI budget reached, refusing AI request",
				zap.Float64("budget_usd", budgetUSD),
				zap.Float64("spent_usd", spent),
				zap.String("path", c.FullPath()),
			)
			notify.Alert(
				"ai-daily-budget",
				"SaltyBytes: daily AI budget reached",
				fmt.Sprintf("Metered AI spend hit $%.2f (limit $%.2f). AI endpoints are paused until midnight UTC. If this is real growth rather than a runaway, raise AI_DAILY_BUDGET_USD in SSM and redeploy.", spent, budgetUSD),
				dashboardURL,
				6*time.Hour,
			)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":      "AI features are temporarily at capacity — please try again later",
				"error_code": "ai_budget_exhausted",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
