package models

import (
	"time"

	"gorm.io/gorm"
)

// VideoExtractionCache is the per-video master copy of a recipe extracted from a
// social/video link, keyed by platform+video ID so a viral video is processed
// once and served from cache to everyone else (the primary cost control).
type VideoExtractionCache struct {
	gorm.Model
	VideoKey       string    `gorm:"uniqueIndex;size:512;not null"` // "<platform>:<video_id>"
	Platform       string    `gorm:"size:32;not null"`
	OriginalURL    string    `gorm:"size:2048;not null"`
	RecipeData     RecipeDef `gorm:"type:jsonb;not null"`
	ThumbnailURL   string    `gorm:"size:1024"` // S3 URL of a representative frame, shared across importers
	HitCount       int       `gorm:"default:0"`
	LastAccessedAt time.Time `gorm:"index;not null"`
	FetchedAt      time.Time `gorm:"index;not null"`
	PromptVersion  string    `gorm:"size:16"`
}

// VideoImportStatus is the lifecycle state of an async video-import job.
type VideoImportStatus string

// VideoImportStatus values.
const (
	VideoImportQueued     VideoImportStatus = "queued"
	VideoImportProcessing VideoImportStatus = "processing"
	VideoImportDone       VideoImportStatus = "done"
	VideoImportFailed     VideoImportStatus = "failed"
)

// VideoImport is an async job tracking one video-link import. CostUSD meters the
// metered spend (0 for cache hits) and powers the daily-budget kill switch.
type VideoImport struct {
	gorm.Model
	UserID    uint              `gorm:"index;not null"`
	SourceURL string            `gorm:"size:2048;not null"`
	Platform  string            `gorm:"size:32"`
	VideoKey  string            `gorm:"index;size:512"`
	Status    VideoImportStatus `gorm:"type:text;not null;default:'queued'"`
	RecipeID  *uint             `gorm:"index"`
	CostUSD   float64           `gorm:"default:0"`
	CacheHit  bool              `gorm:"default:false"`
	Error     string            `gorm:"size:512"`
}
