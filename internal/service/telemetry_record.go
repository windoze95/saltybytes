package service

import (
	"context"
	"errors"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
)

// Extraction origins: which product flow asked for an extraction. Stored on
// ExtractionEvent.Origin so the dashboard can slice completeness per flow.
const (
	ExtractionOriginImport      = "import"
	ExtractionOriginPreview     = "preview"
	ExtractionOriginWarm        = "warm"
	ExtractionOriginFinderDig   = "finder_dig"
	ExtractionOriginMultiExpand = "multi_expand"
	ExtractionOriginUnknown     = "unknown"
)

type extractionOriginKey struct{}

// WithExtractionOrigin tags ctx with the product flow driving any extractions
// beneath it. An origin already present wins (the outermost flow is the one
// the user experienced).
func WithExtractionOrigin(ctx context.Context, origin string) context.Context {
	if _, ok := ctx.Value(extractionOriginKey{}).(string); ok {
		return ctx
	}
	return context.WithValue(ctx, extractionOriginKey{}, origin)
}

// extractionOrigin reads the flow tag off ctx.
func extractionOrigin(ctx context.Context) string {
	if v, ok := ctx.Value(extractionOriginKey{}).(string); ok && v != "" {
		return v
	}
	return ExtractionOriginUnknown
}

// recordExtraction persists one terminal extraction outcome. Best-effort and
// synchronous (a single indexed insert): a telemetry failure only warns, never
// fails the extraction itself. Callers fill URL/Method/Success/Error*; Origin
// falls back to the ctx flow tag and Domain is derived from the URL.
func (s *ImportService) recordExtraction(ctx context.Context, ev models.ExtractionEvent) {
	if s == nil || s.Events == nil {
		return
	}
	if ev.Origin == "" {
		ev.Origin = extractionOrigin(ctx)
	}
	if ev.Domain == "" {
		ev.Domain = domainFromURL(ev.URL)
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	if err := s.Events.Create(&ev); err != nil {
		logger.Get().Warn("failed to record extraction event",
			zap.String("url", ev.URL), zap.Error(err))
	}
}

// extractionErrCode maps an extraction failure to a stable, groupable class
// for the dashboard: ExtractionError codes pass through (site_blocked,
// not_found, fetch_failed, ...), AI-layer failures become ai_error, anything
// else extract_failed.
func extractionErrCode(err error) string {
	if err == nil {
		return ""
	}
	var exErr *ExtractionError
	if errors.As(err, &exErr) && exErr.Code != "" {
		return exErr.Code
	}
	var aiErr *ai.AIError
	if errors.As(err, &aiErr) {
		return "ai_error"
	}
	return "extract_failed"
}

// truncateErr caps an error string for storage (full stacks stay in logs).
func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	const maxLen = 2000
	if len(msg) > maxLen {
		return msg[:maxLen] + "…"
	}
	return msg
}
