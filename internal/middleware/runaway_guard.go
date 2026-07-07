package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/notify"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// UnlimitedRunawayGuard protects the hidden unlimited tier against
// compromise. Unlimited accounts bypass every quota, so a stolen operator
// credential could burn AI spend freely up to the global daily switch —
// while locking every real user out of it. This guard watches each
// unlimited account's own attributed spend over the trailing 24 hours;
// crossing capUSD LOCKS the account outright (every authenticated request
// 403s until locked_at is cleared manually) and pages the operator.
//
// Per-user totals are cached for a minute; capped tiers pass straight
// through (their caps already bound them); capUSD <= 0 disables. Spend
// checks fail open — attribution gaps must not lock the operator out.
func UnlimitedRunawayGuard(
	capUSD float64,
	sumUserSince func(userID uint, since time.Time) (float64, error),
	lockUser func(userID uint, reason string) error,
) gin.HandlerFunc {
	if capUSD <= 0 {
		return func(c *gin.Context) { c.Next() }
	}

	type cached struct {
		spent float64
		at    time.Time
	}
	var mu sync.Mutex
	perUser := make(map[uint]cached)

	return func(c *gin.Context) {
		user, err := util.GetUserFromContext(c)
		if err != nil || user == nil || user.Subscription == nil ||
			user.Subscription.Tier != models.TierUnlimited {
			c.Next()
			return
		}

		now := time.Now()
		mu.Lock()
		entry, ok := perUser[user.ID]
		if !ok || now.Sub(entry.at) > time.Minute {
			spent, sErr := sumUserSince(user.ID, now.Add(-24*time.Hour))
			if sErr != nil {
				mu.Unlock()
				logger.Get().Warn("runaway-guard spend check failed, allowing request",
					zap.Uint("user_id", user.ID), zap.Error(sErr))
				c.Next()
				return
			}
			entry = cached{spent: spent, at: now}
			perUser[user.ID] = entry
		}
		spent := entry.spent
		mu.Unlock()

		if spent < capUSD {
			c.Next()
			return
		}

		reason := fmt.Sprintf("runaway guard: $%.2f attributed AI spend in 24h (cap $%.2f)", spent, capUSD)
		if lErr := lockUser(user.ID, reason); lErr != nil {
			logger.Get().Error("runaway guard failed to lock account",
				zap.Uint("user_id", user.ID), zap.Error(lErr))
		} else {
			logger.Get().Warn("runaway guard LOCKED unlimited account",
				zap.Uint("user_id", user.ID), zap.String("username", user.Username),
				zap.Float64("spent_usd", spent), zap.Float64("cap_usd", capUSD))
		}
		notify.Alert(
			fmt.Sprintf("runaway-lock-%d", user.ID),
			"SaltyBytes SECURITY: unlimited account locked",
			fmt.Sprintf("Account %q spent $%.2f on AI in the last 24h (cap $%.2f) and has been locked — possible compromised credential. If it was you, raise UNLIMITED_DAILY_SPEND_CAP_USD; to unlock: UPDATE users SET locked_at = NULL, lock_reason = '' WHERE id = %d;",
				user.Username, spent, capUSD, user.ID),
			"",
			time.Hour,
		)

		c.JSON(http.StatusForbidden, gin.H{
			"error":      "This account has been locked. Contact support.",
			"error_code": "account_locked",
		})
		c.Abort()
	}
}
