package models

import "time"

// AIModelOption is a user-managed entry in the swappable "light tier" model
// registry. The dashboard lets an operator add OpenAI/Gemini/DeepSeek/Anthropic
// models here (just provider + model id + display/price metadata; API keys stay
// in SSM, never in the DB) and switch the active one live. Persisted in RDS so
// the registry survives container restarts and image updates.
//
// Validated/ValidationError/LastValidatedAt record the outcome of the live
// validation probe (a real minimal extraction) run when the option is added or
// activated — a model can only go live after a green probe (fail-closed).
//
// The fields are additive and the table is read by the dashboard directly, so
// keep changes additive (see the dashboard-schema-coupling note).
type AIModelOption struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// (provider, model_id) is unique so concurrent first-boot seeding across
	// multiple instances can't create duplicate registry rows.
	Provider string `gorm:"size:32;uniqueIndex:idx_ai_model_options_provider_model,priority:1" json:"provider"` // anthropic|openai|gemini|deepseek
	ModelID  string `gorm:"size:96;uniqueIndex:idx_ai_model_options_provider_model,priority:2" json:"model_id"` // e.g. "gpt-4o-mini"
	Label    string `gorm:"size:96" json:"label"`                                                               // human-friendly name
	BaseURL  string `gorm:"size:255" json:"base_url"`                                                           // optional endpoint override

	// List prices (USD per 1M tokens) for dashboard counterfactual comparison.
	InputPricePerMTok  float64 `json:"input_price_per_mtok"`
	OutputPricePerMTok float64 `json:"output_price_per_mtok"`

	Enabled bool `gorm:"default:true" json:"enabled"`

	// Validation-probe outcome.
	Validated       bool       `json:"validated"`
	ValidationError string     `gorm:"size:512" json:"validation_error"`
	LastValidatedAt *time.Time `json:"last_validated_at"`
}

// AIConfig is the single-row table holding the currently-active light-tier
// selection. It stores the resolved spec (provider/model/base URL) rather than a
// foreign key to AIModelOption, so the running provider keeps working even if
// the option row is later edited or deleted. Seeded from the env default on
// first boot; updated by the dashboard live-switch after a green probe.
type AIConfig struct {
	ID        uint      `gorm:"primarykey" json:"id"` // always 1 (single row)
	UpdatedAt time.Time `json:"updated_at"`

	ActiveProvider string `gorm:"size:32" json:"active_provider"`
	ActiveModel    string `gorm:"size:96" json:"active_model"`
	ActiveBaseURL  string `gorm:"size:255" json:"active_base_url"`
}
