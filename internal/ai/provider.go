package ai

import "context"

// UnitSystemPreserveSource is a sentinel unit-system value that instructs
// extraction to keep the source's original measurements instead of converting,
// reporting the detected system via the unit_system tool field.
const UnitSystemPreserveSource = "preserve source"

// TextProvider handles all text/reasoning tasks (Claude).
type TextProvider interface {
	GenerateRecipe(ctx context.Context, req RecipeRequest) (*RecipeResult, error)
	RegenerateRecipe(ctx context.Context, req RegenerateRequest) (*RecipeResult, error)
	ForkRecipe(ctx context.Context, req ForkRequest) (*RecipeResult, error)
	AnalyzeAllergens(ctx context.Context, req AllergenRequest) (*AllergenResult, error)
	ClassifyVoiceIntent(ctx context.Context, transcript string) (*VoiceIntent, error)
	EstimatePortions(ctx context.Context, recipeDef interface{}) (*PortionEstimate, error)
	ExtractRecipeFromText(ctx context.Context, text string, unitSystem string) (*RecipeResult, error)
	CookingQA(ctx context.Context, question string, recipeContext string) (string, error)
	DietaryInterview(ctx context.Context, messages []Message, memberName string) (*DietaryInterviewResult, error)
	// ExpandAndRankRecipes is the recipe finder's single model call: given the
	// user's request and a list of REAL search-result candidates, it returns the
	// candidates (referenced only by their index in the request) in match-ranked
	// order with one-line rationales and best-effort per-family-member dietary
	// safety, plus a few broadened query suggestions. Because the output is only
	// indices into the caller's candidate list, it structurally cannot invent a
	// recipe.
	ExpandAndRankRecipes(ctx context.Context, req FinderRankRequest) (*FinderRankResult, error)
}

// MediaKind identifies whether a MediaInput is a raster image or a PDF document.
type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaPDF   MediaKind = "pdf"
)

// MediaInput is a single image or PDF document supplied for recipe extraction.
type MediaInput struct {
	Data []byte
	Kind MediaKind
}

// VisionProvider handles image- and document-based recipe extraction (Claude).
type VisionProvider interface {
	ExtractRecipeFromImage(ctx context.Context, imageData []byte, unitSystem string, requirements string) (*RecipeResult, error)
	// ExtractRecipesFromMedia extracts every distinct recipe found across the
	// given images and/or PDF documents in a single request, returning one
	// RecipeResult per recipe. contextText, when non-empty, supplies additional
	// source text (e.g. a video's transcript and caption) that the model should
	// treat as a primary source of steps and quantities alongside the media.
	ExtractRecipesFromMedia(ctx context.Context, media []MediaInput, contextText string, unitSystem string, requirements string) ([]*RecipeResult, error)
}

// VideoProvider extracts recipes from a whole video ingested natively (video +
// audio in one pass), rather than from sampled frames. Satisfied by
// *GeminiVideoProvider. contextText carries the caption/transcript. Returns
// ErrVideoTooLarge when the video exceeds the inline size limit so the caller
// can fall back to frame sampling.
type VideoProvider interface {
	ExtractRecipesFromVideo(ctx context.Context, videoData []byte, mimeType, contextText, unitSystem, requirements string) ([]*RecipeResult, error)
}

// ImageProvider handles image generation (DALL-E 3).
type ImageProvider interface {
	GenerateImage(ctx context.Context, prompt string) ([]byte, error)
}

// SpeechProvider handles speech-to-text (Whisper). format is the audio
// container format (e.g. "webm", "m4a"); empty defaults to webm.
type SpeechProvider interface {
	TranscribeAudio(ctx context.Context, audioData []byte, format string) (string, error)
}

// EmbeddingProvider handles vector embeddings.
type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
}

// SearchProvider handles web recipe search (Google + Brave fallback).
type SearchProvider interface {
	SearchRecipes(ctx context.Context, query string, count int, offset int) ([]SearchResult, error)
}

// RecipeRequest holds parameters for generating a new recipe.
type RecipeRequest struct {
	UserPrompt     string
	UnitSystem     string
	Requirements   string
	CookingContext string    // free-form cooking preferences injected into AI prompts
	Messages       []Message // for conversation history
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
	UnitSystem        string
	PromptVersion     string // hash of prompt templates used to generate this recipe
}

// IngredientResult is a single ingredient in the recipe output.
type IngredientResult struct {
	Name         string
	Unit         string
	Amount       float64
	AmountHigh   float64
	MetricUnit   string
	MetricAmount float64
	OriginalText string
}

// IngredientInput is an ingredient supplied by the caller.
type IngredientInput struct {
	Name   string
	Unit   string
	Amount float64
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

// DietaryInterviewResult is the structured output of one dietary interview
// turn. When the model has gathered enough information it calls the
// save_dietary_profile tool: Complete is true and Profile is non-nil, with
// Response carrying a short wrap-up message. Otherwise Complete is false,
// Profile is nil and Response carries the next interview question.
type DietaryInterviewResult struct {
	Response string
	Complete bool
	Profile  *DietaryProfileResult
}

// DietaryProfileResult is the structured dietary profile produced by a
// completed dietary interview.
type DietaryProfileResult struct {
	Allergies    []DietaryAllergyResult
	Intolerances []string
	Restrictions []string
	Preferences  []string
	MedicalNotes string
}

// DietaryAllergyResult is a single allergy entry in a dietary profile.
type DietaryAllergyResult struct {
	Name     string
	Severity string
	SubForms []string
	Notes    string
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

// FinderCandidate is one real recipe search result offered to the finder's
// ranker, identified by its Index in the candidate list. The ranker may only
// refer back to candidates by Index — it is never given a way to emit a recipe
// that is not in this list.
type FinderCandidate struct {
	Index       int
	Title       string
	URL         string
	Source      string
	Description string
}

// FinderRankRequest is the input to ExpandAndRankRecipes. All of the steering
// fields are composed server-side (never client-trusted); Candidates are the
// real search results to rank.
type FinderRankRequest struct {
	Facets         string // deterministic summary of the tapped facet chips
	FreeText       string // optional typed/spoken free text
	UnitSystem     string
	CookingContext string
	Requirements   string
	DietSummary    string // compacted family dietary needs (allergies, restrictions, preferences)
	Candidates     []FinderCandidate
}

// MemberSafety is the model's best-effort dietary assessment of a candidate for
// a single family member, inferred only from the candidate's title and
// description. It is advisory (the authoritative per-ingredient allergen check
// stays on the post-import path), and lights up the existing result-card safety
// badges.
type MemberSafety struct {
	MemberName string `json:"member_name"`
	Status     string `json:"status"` // "safe" | "caution" | "avoid"
	Note       string `json:"note"`
}

// FinderRanking is one ranked candidate: Index into the FinderRankRequest's
// candidate list, a one-line rationale and per-member safety badges.
type FinderRanking struct {
	Index  int
	Reason string
	Safety []MemberSafety
	// Expand marks a candidate that is a collection/listicle/roundup page (many
	// recipes) rather than a single recipe, worth digging into for its
	// individual recipes. ExpandPriority (higher = more promising) orders which
	// collections to dig first when several are flagged.
	Expand         bool
	ExpandPriority int
}

// FinderRankResult is the output of ExpandAndRankRecipes: candidates in
// match-ranked order (by Index into the request's candidate list) plus a few
// broadened query suggestions the user can fall back to.
type FinderRankResult struct {
	Ranked         []FinderRanking
	BroadenQueries []string
}
