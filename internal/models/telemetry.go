package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// FinderRun is one agent-mode search run's workflow telemetry: what each step
// did (search → rank → dig → show) and how long it took. Written best-effort at
// the end of every run so the dashboard can chart the agent's step funnel,
// failure modes and latencies. Rows are facts about a finished run — no soft
// delete, no updates.
type FinderRun struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`

	UserID uint   `gorm:"index" json:"user_id"`
	Query  string `gorm:"size:512" json:"query"`
	Offset int    `json:"offset"`

	// Search step.
	ResultsFound int  `json:"results_found"`
	FromCache    bool `json:"from_cache"`

	// Rank step. RankOK=false means the model call failed and the run degraded
	// to the unranked fallback (no reasons/safety; collection flags only from
	// the canonical-cache override).
	RankOK    bool   `gorm:"index" json:"rank_ok"`
	RankError string `gorm:"type:text" json:"rank_error,omitempty"`

	// Dig step: collections flagged (model + known-multi override), how many
	// were actually mined, and how many individual recipes folded out.
	CollectionsFlagged int `json:"collections_flagged"`
	CollectionsDug     int `json:"collections_dug"`
	CardsMined         int `json:"cards_mined"`

	// What the user ended up seeing.
	ShownDirect int `json:"shown_direct"`
	ShownTotal  int `json:"shown_total"`

	// Terminal event of the run: done | empty | error | cancelled.
	Terminal  string `gorm:"size:16;index" json:"terminal"`
	ErrorText string `gorm:"type:text" json:"error_text,omitempty"`

	// Step latencies.
	SearchMS int64 `json:"search_ms"`
	RankMS   int64 `json:"rank_ms"`
	DigMS    int64 `json:"dig_ms"`
	TotalMS  int64 `json:"total_ms"`
}

// ExtractionEvent records one terminal recipe-extraction attempt — success or
// failure — with enough context that a failure can be diagnosed (and handed to
// a coding agent) without grepping CloudWatch. Append-only.
type ExtractionEvent struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`

	URL    string `gorm:"size:2048" json:"url"`
	Domain string `gorm:"size:255;index" json:"domain"`

	// Origin is which product flow asked for the extraction:
	// import | preview | warm | finder_dig | multi_expand | unknown.
	Origin string `gorm:"size:32;index" json:"origin"`

	// Method is how the recipe was (or was last attempted to be) extracted:
	// json_ld | haiku | firecrawl_json_ld | firecrawl_haiku (ExtractionMethod
	// values), plus multi_marked and card-specific own_page | inline_jsonld |
	// inline_ai; "" when the attempt died before extraction (fetch failures).
	Method string `gorm:"size:32;index" json:"method"`

	Success bool `gorm:"index" json:"success"`

	// ErrorCode is the stable, groupable failure class:
	// site_blocked | not_found | fetch_failed | ai_error | no_provider | ...
	ErrorCode string `gorm:"size:64;index" json:"error_code,omitempty"`
	Error     string `gorm:"type:text" json:"error,omitempty"`

	UsedFirecrawl bool  `json:"used_firecrawl"`
	DurationMS    int64 `json:"duration_ms"`

	// Context carries flow-specific detail for drill-downs: card title +
	// collection URL for multi cards, retry attempts, html length, etc.
	Context ExtractionContext `gorm:"type:jsonb" json:"context"`
}

// ExtractionContext is the free-form JSONB detail bag on an ExtractionEvent.
type ExtractionContext map[string]interface{}

// Scan is a GORM hook that scans jsonb into ExtractionContext.
func (c *ExtractionContext) Scan(value interface{}) error {
	if value == nil {
		*c = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}
	return json.Unmarshal(bytes, c)
}

// Value is a GORM hook that returns the json value of ExtractionContext.
func (c ExtractionContext) Value() (driver.Value, error) {
	if c == nil {
		return json.Marshal(map[string]interface{}{})
	}
	return json.Marshal(map[string]interface{}(c))
}
