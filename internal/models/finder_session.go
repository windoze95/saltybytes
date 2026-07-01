package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// FinderSession is a saved Recipe Finder run: what the user asked for (Intent),
// the recipes it surfaced (Results) and a short human narration of the run.
// It is auto-saved server-side after each completed first-page find, so a user
// can browse and resume past finds. gorm.Model fields are declared explicitly so
// JSON serializes snake_case (mirrors Family).
type FinderSession struct {
	ID        uint             `gorm:"primarykey" json:"id"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	DeletedAt gorm.DeletedAt   `gorm:"index" json:"-"`
	UserID    uint             `gorm:"index;not null" json:"user_id"`
	Title     string           `gorm:"type:text" json:"title"`
	Intent    FinderIntent     `gorm:"type:jsonb" json:"intent"`
	Results   SearchResultList `gorm:"type:jsonb" json:"results"`
	Narration StringList       `gorm:"type:jsonb" json:"narration"`
}

// FinderIntent captures what a user asked for in a finder run, stored as JSONB.
// It mirrors the finder's facet + free-text wire shape so the app can re-run it.
type FinderIntent struct {
	Occasion     string   `json:"occasion,omitempty"`
	TimeBudget   string   `json:"time_budget,omitempty"`
	Protein      string   `json:"protein,omitempty"`
	Cuisine      string   `json:"cuisine,omitempty"`
	UseWhatIHave []string `json:"use_what_i_have,omitempty"`
	SurpriseMe   bool     `json:"surprise_me,omitempty"`
	FreeText     string   `json:"free_text,omitempty"`
}

// Scan is a GORM hook that scans jsonb into FinderIntent.
func (j *FinderIntent) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}
	return json.Unmarshal(bytes, j)
}

// Value is a GORM hook that returns the json value of FinderIntent.
func (j FinderIntent) Value() (driver.Value, error) {
	return json.Marshal(j)
}
