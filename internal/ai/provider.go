package ai

import "context"

// TextProvider handles all text/reasoning tasks (Claude).
type TextProvider interface {
	GenerateRecipe(ctx context.Context, req RecipeRequest) (*RecipeResult, error)
	RegenerateRecipe(ctx context.Context, req RegenerateRequest) (*RecipeResult, error)
	ForkRecipe(ctx context.Context, req ForkRequest) (*RecipeResult, error)
	AnalyzeAllergens(ctx context.Context, req AllergenRequest) (*AllergenResult, error)
	ClassifyVoiceIntent(ctx context.Context, transcript string) (*VoiceIntent, error)
	NormalizeMeasurements(ctx context.Context, ingredients []IngredientInput) ([]NormalizedIngredient, error)
	EstimatePortions(ctx context.Context, recipeDef interface{}) (*PortionEstimate, error)
	ExtractRecipeFromText(ctx context.Context, text string, unitSystem string) (*RecipeResult, error)
	CookingQA(ctx context.Context, question string, recipeContext string) (string, error)
	DietaryInterview(ctx context.Context, messages []Message, memberName string) (string, error)
}

// VisionProvider handles image-based recipe extraction (Claude).
type VisionProvider interface {
	ExtractRecipeFromImage(ctx context.Context, imageData []byte, unitSystem string, requirements string) (*RecipeResult, error)
}

// ImageProvider handles image generation (DALL-E 3).
type ImageProvider interface {
	GenerateImage(ctx context.Context, prompt string) ([]byte, error)
}

// SpeechProvider handles speech-to-text (Whisper).
type SpeechProvider interface {
	TranscribeAudio(ctx context.Context, audioData []byte) (string, error)
}

// EmbeddingProvider handles vector embeddings.
type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
}

// SearchProvider handles web recipe search (Google + Brave fallback).
type SearchProvider interface {
	SearchRecipes(ctx context.Context, query string, count int) ([]SearchResult, error)
}

// RecipeRequest holds parameters for generating a new recipe.
type RecipeRequest struct {
	UserPrompt   string
	UnitSystem   string
	Requirements string
	Messages     []Message // for conversation history
}

// RegenerateRequest extends RecipeRequest with prior conversation history.
type RegenerateRequest struct {
	RecipeRequest
	ExistingHistory []Message
}

// ForkRequest extends RecipeRequest with prior conversation history.
type ForkRequest struct {
	RecipeRequest
	ExistingHistory []Message
}

// RecipeResult is the structured output from any recipe-generating call.
type RecipeResult struct {
	Title             string
	Ingredients       []IngredientResult
	Instructions      []string
	CookTime          int
	ImagePrompt       string
	Hashtags          []string
	LinkedSuggestions []string
	Summary           string
	Portions          int
	PortionSize       string
	SourceURL         string
}

// IngredientResult is a single ingredient in the recipe output.
type IngredientResult struct {
	Name             string
	Unit             string
	Amount           float64
	OriginalText     string
	NormalizedAmount float64
	NormalizedUnit   string
	IsEstimated      bool
}

// IngredientInput is an ingredient supplied by the caller.
type IngredientInput struct {
	Name   string
	Unit   string
	Amount float64
}

// NormalizedIngredient is the result of measurement normalisation.
type NormalizedIngredient struct {
	OriginalText     string
	NormalizedAmount float64
	NormalizedUnit   string
	IsEstimated      bool
}

// AllergenRequest holds parameters for allergen analysis.
type AllergenRequest struct {
	Ingredients []IngredientInput
	IsPremium   bool
}

// AllergenResult is the structured output of allergen analysis.
type AllergenResult struct {
	IngredientAnalyses []IngredientAnalysisResult
	Confidence         float64
	RequiresReview     bool
}

// IngredientAnalysisResult is the allergen analysis for a single ingredient.
type IngredientAnalysisResult struct {
	IngredientName    string
	CommonAllergens   []string
	PossibleAllergens []string
	SubIngredients    []string
	SeedOilRisk       bool
	Confidence        float64
}

// VoiceIntent is the classified intent from a voice command.
type VoiceIntent struct {
	Type   string // "scroll_up", "scroll_down", "navigate", "question", "ignore"
	Amount string // "small", "large"
	Target string // "ingredients", "instructions"
	Text   string // for questions
}

// PortionEstimate is the estimated portion information for a recipe.
type PortionEstimate struct {
	Portions    int
	PortionSize string
	Confidence  float64
}

// SearchResult is a single web search result.
type SearchResult struct {
	Title       string  `json:"title"`
	URL         string  `json:"source_url"`
	Source      string  `json:"source_domain"`
	Rating      float64 `json:"rating"`
	ImageURL    string  `json:"image_url"`
	Description string  `json:"description"`
}

// Message represents a single message in a conversation.
type Message struct {
	Role    string // "user", "assistant", "system"
	Content string
}
