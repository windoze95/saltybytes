package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// AllergenAnalysis is the model for allergen analysis results of a recipe.
type AllergenAnalysis struct {
	gorm.Model
	RecipeID             uint                   `gorm:"index;not null"`
	Recipe               *Recipe                `gorm:"foreignKey:RecipeID"`
	NodeID               *uint                  `gorm:"index"`
	IngredientAnalyses   IngredientAnalysisList `gorm:"type:jsonb"`
	ContainsNuts         bool
	ContainsDairy        bool
	ContainsGluten       bool
	ContainsSoy          bool
	ContainsSeedOils     bool
	ContainsShellfish    bool
	ContainsEggs         bool
	SafeForProfiles      UintList               `gorm:"type:jsonb"`
	UnsafeForProfiles    UintList               `gorm:"type:jsonb"`
	Confidence           float64                `gorm:"default:0"`
	RequiresReview       bool                   `gorm:"default:true"`
	IsPremium            bool                   `gorm:"default:false"`
	PromptVersion        string
	Disclaimer           string `gorm:"-"`
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
