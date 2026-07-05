package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SearchCache stores cached web search results to avoid repeated API calls.
type SearchCache struct {
	gorm.Model
	NormalizedQuery string           `gorm:"uniqueIndex;size:512;not null"`
	Results         SearchResultList `gorm:"type:jsonb;not null"`
	ResultCount     int              `gorm:"not null"`
	HitCount        int              `gorm:"default:0"`
	LastAccessedAt  time.Time        `gorm:"index;not null"`
	FetchedAt       time.Time        `gorm:"index;not null"`
	Embedding       *string          `gorm:"type:vector(1536)" json:"-"`
}

// SearchResultItem mirrors ai.SearchResult for JSONB storage.
type SearchResultItem struct {
	Title       string  `json:"title"`
	URL         string  `json:"source_url"`
	Source      string  `json:"source_domain"`
	Rating      float64 `json:"rating"`
	ImageURL    string  `json:"image_url"`
	Description string  `json:"description"`
	// Finder-session extras (additive; absent for plain cached searches): the
	// agent's one-line rationale, per-member dietary safety and — for recipes
	// mined out of a roundup — the collection title they came from.
	Reason string             `json:"reason,omitempty"`
	Safety []ResultSafetyItem `json:"safety,omitempty"`
	Via    string             `json:"via,omitempty"`
}

// ResultSafetyItem mirrors the finder's per-member safety badge for JSONB
// storage (models must not import the ai package).
type ResultSafetyItem struct {
	MemberName string `json:"member_name"`
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
}

// SearchResultList is a slice of SearchResultItem for JSONB storage.
type SearchResultList []SearchResultItem

// Scan is a GORM hook that scans jsonb into SearchResultList.
func (j *SearchResultList) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}

	result := SearchResultList{}
	err := json.Unmarshal(bytes, &result)
	*j = SearchResultList(result)

	return err
}

// Value is a GORM hook that returns json value of SearchResultList.
func (j SearchResultList) Value() (driver.Value, error) {
	return json.Marshal(j)
}
