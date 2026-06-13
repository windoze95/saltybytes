package util

import (
	"runtime/debug"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// RecoverPanic logs and swallows a panic in a detached goroutine. Gin's
// Recovery middleware only protects request-handler goroutines, so background
// goroutines (AI generation, websocket dispatch, embedding refresh) must
// recover themselves or a single panic takes down the whole process.
//
// Usage: defer util.RecoverPanic("scope description")
func RecoverPanic(scope string) {
	if r := recover(); r != nil {
		logger.Get().Error("panic recovered in background goroutine",
			zap.String("scope", scope),
			zap.Any("panic", r),
			zap.ByteString("stack", debug.Stack()),
		)
	}
}
