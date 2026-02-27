package logger

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalLogger *zap.Logger
	once         sync.Once
)

// Init initializes the global logger. In development mode, it uses a
// human-readable console encoder; in production, it uses JSON.
func Init(isDev bool) {
	once.Do(func() {
		var cfg zap.Config
		if isDev {
			cfg = zap.NewDevelopmentConfig()
			cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		} else {
			cfg = zap.NewProductionConfig()
		}

		var err error
		globalLogger, err = cfg.Build()
		if err != nil {
			panic("failed to initialize logger: " + err.Error())
		}
	})
}

// Get returns the global logger singleton. If Init has not been called,
// it falls back to a no-op logger.
func Get() *zap.Logger {
	if globalLogger == nil {
		return zap.NewNop()
	}
	return globalLogger
}

// With returns a child logger with the given fields attached.
func With(fields ...zap.Field) *zap.Logger {
	return Get().With(fields...)
}

// WithRequestID returns a child logger with a request_id field.
func WithRequestID(requestID string) *zap.Logger {
	return Get().With(zap.String("request_id", requestID))
}

// RequestIDMiddleware generates a UUID for each request, stores it in the
// gin context under "request_id", and sets the X-Request-ID response header.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := uuid.New().String()
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// Sync flushes any buffered log entries. Should be called before the
// application exits.
func Sync() {
	if globalLogger != nil {
		_ = globalLogger.Sync()
	}
}
