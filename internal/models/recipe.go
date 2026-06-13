package models

import (
	"time"

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
	LinkedRecipes []*Recipe `gorm:"many2many:recipe_linked_recipes;association_jointable_foreignkey:link_recipe_id"`
	// LinkedSuggestions  pq.StringArray `gorm:"type:text[]"`
	Hashtags []*Tag `gorm:"many2many:recipe_tags;"`
	// UserHashtags []*Tag `gorm:"many2many:recipe_tags;"`
	// ImagePrompt        string
	Status             string `gorm:"type:text;default:'ready'"`
	ImageURL           string
	CreatedByID        uint
	CreatedBy          *User `gorm:"foreignKey:CreatedByID"`
	PersonalizationUID uuid.UUID
	UserEdited         bool             `gorm:"default:false"`
	ForkedFromID       *uint            `gorm:"index"`
	ForkedFrom         *Recipe          `gorm:"-"` // loaded manually in repository to avoid self-referential GORM issues
	TreeID             *uint            `gorm:"index"`
	Tree               *RecipeTree      `gorm:"foreignKey:TreeID"`
	OriginalImageURL   string           `json:"original_image_url,omitempty"`
	Embedding          *string          `gorm:"type:vector(1536)" json:"-"`
	CanonicalID        *uint            `gorm:"index"`
	Canonical          *CanonicalRecipe `gorm:"foreignKey:CanonicalID"`
	HasDiverged        bool             `gorm:"default:false"`
	PromptVersion      string           `json:"prompt_version,omitempty" gorm:"size:16"` // hash of prompt templates used
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
// gorm.Model fields are declared explicitly so JSON serializes snake_case.
type RecipeTree struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
	RecipeID   uint           `gorm:"uniqueIndex;not null" json:"recipe_id"`
	RootNodeID *uint          `gorm:"index" json:"root_node_id"`
	RootNode   *RecipeNode    `gorm:"foreignKey:RootNodeID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL" json:"-"`
	Nodes      []RecipeNode   `gorm:"-" json:"nodes,omitempty"` // loaded manually to avoid circular migration
}

// RecipeNode is the model for a single node in a recipe tree.
// gorm.Model fields are declared explicitly so JSON serializes snake_case.
type RecipeNode struct {
	ID          uint           `gorm:"primarykey" json:"id"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
	TreeID      uint           `gorm:"index" json:"tree_id"`
	ParentID    *uint          `gorm:"index" json:"parent_id"`
	Parent      *RecipeNode    `gorm:"foreignKey:ParentID" json:"-"`
	Children    []RecipeNode   `gorm:"-" json:"-"` // loaded manually to avoid circular migration
	Prompt      string         `json:"user_prompt"`
	Response    *RecipeDef     `gorm:"type:jsonb" json:"response"`
	Summary     string         `json:"summary"`
	Type        RecipeType     `gorm:"type:text" json:"type"`
	BranchName  string         `gorm:"default:'original'" json:"branch_name"`
	IsEphemeral bool           `gorm:"default:false" json:"is_ephemeral"`
	CreatedByID uint           `gorm:"index" json:"created_by_id"`
	CreatedBy   *User          `gorm:"foreignKey:CreatedByID" json:"-"`
	IsActive    bool           `gorm:"default:false" json:"is_active"`
}
