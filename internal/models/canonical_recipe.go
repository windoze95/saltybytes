package models

import (
	"time"

	"gorm.io/gorm"
)

// ExtractionMethod indicates how a canonical recipe was extracted.
type ExtractionMethod string

const (
	ExtractionJSONLD          ExtractionMethod = "json_ld"
	ExtractionHaiku           ExtractionMethod = "haiku"
	ExtractionFirecrawlJSONLD ExtractionMethod = "firecrawl_json_ld"
	ExtractionFirecrawlHaiku  ExtractionMethod = "firecrawl_haiku"
)

// CanonicalRecipe is the URL-keyed master copy of an extracted recipe.
type CanonicalRecipe struct {
	gorm.Model
	NormalizedURL    string           `gorm:"uniqueIndex;size:2048;not null"`
	OriginalURL      string           `gorm:"size:2048;not null"`
	RecipeData       RecipeDef        `gorm:"type:jsonb;not null"`
	ExtractionMethod ExtractionMethod `gorm:"type:text;not null"`
	HitCount         int              `gorm:"default:0"`
	LastAccessedAt   time.Time        `gorm:"index;not null"`
	FetchedAt        time.Time        `gorm:"index;not null"`
	Embedding        *string          `gorm:"type:vector(1536)" json:"-"`
	PromptVersion    string           `gorm:"size:16"`
	// IsMultiPage marks this URL as a collection/listicle (an index of links to
	// separate recipes) rather than a single recipe. Such rows are markers with
	// empty RecipeData and must never be served as a recipe — they keep
	// collection URLs out of the single-recipe cache fast path so they still
	// expand into their individual recipes.
	IsMultiPage bool `gorm:"default:false"`
}
