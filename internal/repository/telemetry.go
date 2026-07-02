package repository

import (
	"gorm.io/gorm"

	"github.com/windoze95/saltybytes-api/internal/models"
)

// FinderRunRepository persists per-run agent workflow telemetry (append-only).
type FinderRunRepository struct {
	DB *gorm.DB
}

// NewFinderRunRepository creates a new FinderRunRepository.
func NewFinderRunRepository(db *gorm.DB) *FinderRunRepository {
	return &FinderRunRepository{DB: db}
}

// Create inserts one finished finder run's telemetry row.
func (r *FinderRunRepository) Create(run *models.FinderRun) error {
	return r.DB.Create(run).Error
}

// ExtractionEventRepository persists terminal recipe-extraction outcomes
// (append-only).
type ExtractionEventRepository struct {
	DB *gorm.DB
}

// NewExtractionEventRepository creates a new ExtractionEventRepository.
func NewExtractionEventRepository(db *gorm.DB) *ExtractionEventRepository {
	return &ExtractionEventRepository{DB: db}
}

// Create inserts one extraction event.
func (r *ExtractionEventRepository) Create(event *models.ExtractionEvent) error {
	return r.DB.Create(event).Error
}
