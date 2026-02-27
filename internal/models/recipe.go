package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Recipe is the model for a recipe.
type Recipe struct {
	gorm.Model
	RecipeDef
	// Title        string
	// Ingredients  Ingredients    `gorm:"type:jsonb"` // Embedded slice of Ingredient
	// Instructions pq.StringArray `gorm:"type:text[]"`
	// CookTime      int
	UnitSystem    UnitSystem `gorm:"type:int"`
	LinkedRecipes []*Recipe  `gorm:"many2many:recipe_linked_recipes;association_jointable_foreignkey:link_recipe_id"`
	// LinkedSuggestions  pq.StringArray `gorm:"type:text[]"`
	Hashtags []*Tag `gorm:"many2many:recipe_tags;"`
	// UserHashtags []*Tag `gorm:"many2many:recipe_tags;"`
	// ImagePrompt        string
	ImageURL           string
	CreatedByID        uint
	CreatedBy          *User `gorm:"foreignKey:CreatedByID"`
	PersonalizationUID uuid.UUID
	UserEdited         bool           `gorm:"default:false"`
	HistoryID          uint           `gorm:"unique;index"`
	History            *RecipeHistory `gorm:"foreignKey:HistoryID"`
	ForkedFromID       *uint          `gorm:"index"`
	ForkedFrom         *Recipe        `gorm:"-"` // loaded manually in repository to avoid self-referential GORM issues
	TreeID             *uint          `gorm:"index"`
	Tree               *RecipeTree    `gorm:"foreignKey:TreeID"`
	OriginalImageURL   string         `json:"original_image_url,omitempty"`
	Embedding          *string        `gorm:"type:vector(1536)" json:"-"`
}

// RecipeHistory is the model for a recipe history and the current entry that is being used to represent the recipe.
type RecipeHistory struct {
	gorm.Model
	Entries       []RecipeHistoryEntry `gorm:"foreignKey:HistoryID"`
	ActiveEntryID *uint                // Foreign key (belongs to RecipeHistoryEntry)
}

// RecipeHistoryEntry is the model for a recipe history entry.
type RecipeHistoryEntry struct {
	gorm.Model
	HistoryID uint // Foreign key (belongs to RecipeHistory)
	Prompt    string
	Response  *RecipeDef `gorm:"type:jsonb"` // Embedded struct
	Summary   string
	Type      RecipeType `gorm:"type:text"`
	Order     int        // To track the order of the entries
}

// Tag is the model for a recipe hashtag.
type Tag struct {
	gorm.Model
	Hashtag string `gorm:"index:idx_hashtag;unique"`
}

// RecipeType is the type for the RecipeType enum.
type RecipeType string

// RecipeType enum values.
const (
	RecipeTypeChat            RecipeType = "chat"
	RecipeTypeRegenChat       RecipeType = "regen_chat"
	RecipeTypeFork            RecipeType = "fork"
	RecipeTypeCopycat         RecipeType = "copycat"
	RecipeTypeImportVision    RecipeType = "import_vision"
	RecipeTypeImportLink      RecipeType = "import_link"
	RecipeTypeImportCopypasta RecipeType = "import_text"
	RecipeTypeManualEntry     RecipeType = "user_input"
	RecipeTypeRemix           RecipeType = "remix"
)

// RecipeTree is the model for a recipe's branching tree structure.
type RecipeTree struct {
	gorm.Model
	RecipeID   uint         `gorm:"uniqueIndex;not null"`
	RootNodeID *uint        `gorm:"index"`
	RootNode   *RecipeNode  `gorm:"foreignKey:RootNodeID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`
	Nodes      []RecipeNode `gorm:"-"` // loaded manually to avoid circular migration
}

// RecipeNode is the model for a single node in a recipe tree.
type RecipeNode struct {
	gorm.Model
	TreeID      uint         `gorm:"index"`
	ParentID    *uint        `gorm:"index"`
	Parent      *RecipeNode  `gorm:"foreignKey:ParentID"`
	Children    []RecipeNode `gorm:"-"` // loaded manually to avoid circular migration
	Prompt      string
	Response    *RecipeDef  `gorm:"type:jsonb"`
	Summary     string
	Type        RecipeType  `gorm:"type:text"`
	BranchName  string      `gorm:"default:'original'"`
	IsEphemeral bool        `gorm:"default:false"`
	CreatedByID uint        `gorm:"index"`
	CreatedBy   *User       `gorm:"foreignKey:CreatedByID"`
	IsActive    bool        `gorm:"default:false"`
}
