package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Family is the model for a family group.
// gorm.Model fields are declared explicitly so JSON serializes snake_case.
type Family struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
	Name      string         `gorm:"not null" json:"name"`
	OwnerID   uint           `gorm:"index;not null" json:"owner_id"`
	Owner     *User          `gorm:"foreignKey:OwnerID" json:"-"`
	Members   []FamilyMember `gorm:"foreignKey:FamilyID" json:"members"`
}

// FamilyMember is the model for a member within a family.
// gorm.Model fields are declared explicitly so JSON serializes snake_case.
type FamilyMember struct {
	ID             uint            `gorm:"primarykey" json:"id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	DeletedAt      gorm.DeletedAt  `gorm:"index" json:"-"`
	FamilyID       uint            `gorm:"index;not null" json:"family_id"`
	Name           string          `gorm:"not null" json:"name"`
	Relationship   string          `gorm:"type:text" json:"relationship"`
	UserID         *uint           `gorm:"index" json:"user_id"`
	User           *User           `gorm:"foreignKey:UserID" json:"-"`
	DietaryProfile *DietaryProfile `gorm:"foreignKey:MemberID" json:"dietary_profile"`
}

// DietaryProfile is the model for a family member's dietary information.
// gorm.Model fields are declared explicitly so JSON serializes snake_case.
type DietaryProfile struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
	MemberID     uint           `gorm:"uniqueIndex;not null" json:"member_id"`
	Allergies    AllergyList    `gorm:"type:jsonb" json:"allergies"`
	Intolerances StringList     `gorm:"type:jsonb" json:"intolerances"`
	Restrictions StringList     `gorm:"type:jsonb" json:"restrictions"`
	Preferences  StringList     `gorm:"type:jsonb" json:"preferences"`
	MedicalNotes string         `gorm:"type:text" json:"medical_notes"`
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
