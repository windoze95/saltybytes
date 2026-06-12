package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// AllergenAnalysis is the model for allergen analysis results of a recipe.
// gorm.Model fields are declared explicitly so JSON serializes snake_case.
type AllergenAnalysis struct {
	ID                 uint                   `gorm:"primarykey" json:"id"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
	DeletedAt          gorm.DeletedAt         `gorm:"index" json:"-"`
	RecipeID           uint                   `gorm:"index;not null" json:"recipe_id"`
	Recipe             *Recipe                `gorm:"foreignKey:RecipeID" json:"-"`
	NodeID             *uint                  `gorm:"index" json:"node_id"`
	IngredientAnalyses IngredientAnalysisList `gorm:"type:jsonb" json:"ingredient_analyses"`
	ContainsNuts       bool                   `json:"contains_nuts"`
	ContainsDairy      bool                   `json:"contains_dairy"`
	ContainsGluten     bool                   `json:"contains_gluten"`
	ContainsSoy        bool                   `json:"contains_soy"`
	ContainsSeedOils   bool                   `json:"contains_seed_oils"`
	ContainsShellfish  bool                   `json:"contains_shellfish"`
	ContainsEggs       bool                   `json:"contains_eggs"`
	SafeForProfiles    UintList               `gorm:"type:jsonb" json:"safe_for_profiles"`
	UnsafeForProfiles  UintList               `gorm:"type:jsonb" json:"unsafe_for_profiles"`
	Confidence         float64                `gorm:"default:0" json:"confidence"`
	RequiresReview     bool                   `gorm:"default:true" json:"requires_review"`
	IsPremium          bool                   `gorm:"default:false" json:"is_premium"`
	PromptVersion      string                 `json:"prompt_version"`
	Disclaimer         string                 `gorm:"-" json:"disclaimer"`
}

// IngredientAnalysis represents the allergen analysis for a single ingredient.
type IngredientAnalysis struct {
	IngredientName    string   `json:"ingredient_name"`
	CommonAllergens   []string `json:"common_allergens"`
	PossibleAllergens []string `json:"possible_allergens"`
	SubIngredients    []string `json:"sub_ingredients"`
	SeedOilRisk       bool     `json:"seed_oil_risk"`
	Confidence        float64  `json:"confidence"`
}

// IngredientAnalysisList is a slice of IngredientAnalysis for JSONB storage.
type IngredientAnalysisList []IngredientAnalysis

// Scan is a GORM hook that scans jsonb into IngredientAnalysisList.
func (j *IngredientAnalysisList) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}

	result := IngredientAnalysisList{}
	err := json.Unmarshal(bytes, &result)
	*j = IngredientAnalysisList(result)

	return err
}

// Value is a GORM hook that returns json value of IngredientAnalysisList.
func (j IngredientAnalysisList) Value() (driver.Value, error) {
	return json.Marshal(j)
}

// UintList is a slice of uint for JSONB storage.
type UintList []uint

// Scan is a GORM hook that scans jsonb into UintList.
func (j *UintList) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}

	result := UintList{}
	err := json.Unmarshal(bytes, &result)
	*j = UintList(result)

	return err
}

// Value is a GORM hook that returns json value of UintList.
func (j UintList) Value() (driver.Value, error) {
	return json.Marshal(j)
}
