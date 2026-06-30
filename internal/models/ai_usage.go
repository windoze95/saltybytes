package models

import "time"

// AIUsageLog records one AI provider call: which model/operation, the token
// usage it consumed, the metered cost, latency and outcome. It's the data
// behind the dashboard's model cost comparison and counterfactual ("what would
// model X have cost") pricing — those are computed from the stored token counts.
type AIUsageLog struct {
	ID        uint      `gorm:"primarykey"`
	CreatedAt time.Time `gorm:"index"`

	Operation string `gorm:"size:64;index"` // e.g. "ExtractRecipeFromText"
	Provider  string `gorm:"size:32;index"` // e.g. "anthropic"
	Model     string `gorm:"size:96;index"` // e.g. "claude-haiku-4-5-20251001"

	InputTokens      int
	OutputTokens     int
	CacheInputTokens int

	CostUSD    float64 // metered cost for the actual model (from the price table)
	DurationMS int64   // call latency
	Success    bool    `gorm:"index"`
}
