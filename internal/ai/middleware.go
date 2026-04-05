package ai

import (
	"context"
	"time"

	"github.com/windoze95/saltybytes-api/internal/logger"
	"go.uber.org/zap"
)

// AIOperation describes an in-flight AI call for middleware inspection.
type AIOperation struct {
	Name      string    // e.g. "GenerateRecipe", "AnalyzeAllergens"
	Provider  string    // e.g. "anthropic"
	Model     string    // e.g. "claude-3-5-sonnet-20241022"
	StartTime time.Time
}

// AIOperationResult captures the outcome of an AI call.
type AIOperationResult struct {
	Operation AIOperation
	Duration  time.Duration
	Err       error
}

// AIMiddleware intercepts AI calls for observability and control.
type AIMiddleware interface {
	Before(ctx context.Context, op AIOperation) context.Context
	After(ctx context.Context, result AIOperationResult)
}

// LoggingMiddleware logs every AI operation with timing and error info.
type LoggingMiddleware struct{}

func (m *LoggingMiddleware) Before(ctx context.Context, op AIOperation) context.Context {
	logger.Get().Info("ai operation started",
		zap.String("operation", op.Name),
		zap.String("provider", op.Provider),
		zap.String("model", op.Model),
	)
	return ctx
}

func (m *LoggingMiddleware) After(ctx context.Context, result AIOperationResult) {
	fields := []zap.Field{
		zap.String("operation", result.Operation.Name),
		zap.String("provider", result.Operation.Provider),
		zap.String("model", result.Operation.Model),
		zap.Duration("duration", result.Duration),
	}
	if result.Err != nil {
		fields = append(fields, zap.Error(result.Err))
		logger.Get().Warn("ai operation failed", fields...)
	} else {
		logger.Get().Info("ai operation completed", fields...)
	}
}

// MiddlewareChain runs multiple middlewares in order.
type MiddlewareChain struct {
	middlewares []AIMiddleware
}

// NewMiddlewareChain creates a chain from the given middlewares.
func NewMiddlewareChain(mws ...AIMiddleware) *MiddlewareChain {
	return &MiddlewareChain{middlewares: mws}
}

func (c *MiddlewareChain) Before(ctx context.Context, op AIOperation) context.Context {
	for _, mw := range c.middlewares {
		ctx = mw.Before(ctx, op)
	}
	return ctx
}

func (c *MiddlewareChain) After(ctx context.Context, result AIOperationResult) {
	// Run in reverse order (like defer)
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		c.middlewares[i].After(ctx, result)
	}
}

// runWithMiddleware executes an AI operation with middleware hooks.
// If mw is nil, the operation runs without middleware.
func runWithMiddleware[T any](ctx context.Context, mw AIMiddleware, op AIOperation, fn func(context.Context) (T, error)) (T, error) {
	if mw != nil {
		ctx = mw.Before(ctx, op)
	}

	result, err := fn(ctx)

	if mw != nil {
		mw.After(ctx, AIOperationResult{
			Operation: op,
			Duration:  time.Since(op.StartTime),
			Err:       err,
		})
	}

	return result, err
}
