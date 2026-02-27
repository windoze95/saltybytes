package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Family is the model for a family group.
type Family struct {
	gorm.Model
	Name    string         `gorm:"not null"`
	OwnerID uint           `gorm:"index;not null"`
	Owner   *User          `gorm:"foreignKey:OwnerID"`
	Members []FamilyMember `gorm:"foreignKey:FamilyID"`
}

// FamilyMember is the model for a member within a family.
type FamilyMember struct {
	gorm.Model
	FamilyID       uint            `gorm:"index;not null"`
	Name           string          `gorm:"not null"`
	Relationship   string          `gorm:"type:text"`
	UserID         *uint           `gorm:"index"`
	User           *User           `gorm:"foreignKey:UserID"`
	DietaryProfile *DietaryProfile `gorm:"foreignKey:MemberID"`
}

// DietaryProfile is the model for a family member's dietary information.
type DietaryProfile struct {
	gorm.Model
	MemberID     uint        `gorm:"uniqueIndex;not null"`
	Allergies    AllergyList `gorm:"type:jsonb"`
	Intolerances StringList  `gorm:"type:jsonb"`
	Restrictions StringList  `gorm:"type:jsonb"`
	Preferences  StringList  `gorm:"type:jsonb"`
	MedicalNotes string      `gorm:"type:text"`
}

// Allergy represents a single allergy entry.
type Allergy struct {
	Name     string   `json:"name"`
	Severity string   `json:"severity"`
	SubForms []string `json:"sub_forms"`
	Notes    string   `json:"notes"`
}

// AllergyList is a slice of Allergy for JSONB storage.
type AllergyList []Allergy

// Scan is a GORM hook that scans jsonb into AllergyList.
func (j *AllergyList) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}

	result := AllergyList{}
	err := json.Unmarshal(bytes, &result)
	*j = AllergyList(result)

	return err
}

// Value is a GORM hook that returns json value of AllergyList.
func (j AllergyList) Value() (driver.Value, error) {
	return json.Marshal(j)
}

// StringList is a slice of strings for JSONB storage.
type StringList []string

// Scan is a GORM hook that scans jsonb into StringList.
func (j *StringList) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", value))
	}

	result := StringList{}
	err := json.Unmarshal(bytes, &result)
	*j = StringList(result)

	return err
}

// Value is a GORM hook that returns json value of StringList.
func (j StringList) Value() (driver.Value, error) {
	return json.Marshal(j)
}
