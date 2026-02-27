package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"go.uber.org/zap"
)

// AllergenDisclaimer is the standard disclaimer attached to all allergen analysis responses.
const AllergenDisclaimer = "AI-generated analysis — does not replace medical advice. Always check ingredient labels and consult your doctor for severe allergies."

// promptVersion is the current version of the allergen analysis prompt.
// Bump this when the AI prompt changes to invalidate cached results.
const promptVersion = "v1"

// AllergenService is the business logic layer for allergen analysis operations.
type AllergenService struct {
	Cfg          *config.Config
	AllergenRepo *repository.AllergenRepository
	FamilyRepo   *repository.FamilyRepository
	RecipeRepo   *repository.RecipeRepository
	AIProvider   ai.TextProvider
	SubService   *SubscriptionService
}

// NewAllergenService is the constructor function for initializing a new AllergenService.
func NewAllergenService(cfg *config.Config, allergenRepo *repository.AllergenRepository, familyRepo *repository.FamilyRepository, recipeRepo *repository.RecipeRepository, aiProvider ai.TextProvider, subService *SubscriptionService) *AllergenService {
	return &AllergenService{
		Cfg:          cfg,
		AllergenRepo: allergenRepo,
		FamilyRepo:   familyRepo,
		RecipeRepo:   recipeRepo,
		AIProvider:   aiProvider,
		SubService:   subService,
	}
}

// AllergenAnalysisResponse wraps the analysis model with a disclaimer.
type AllergenAnalysisResponse struct {
	models.AllergenAnalysis
	Disclaimer string `json:"disclaimer"`
}

// FamilyCheckResponse contains the per-member allergen check results.
type FamilyCheckResponse struct {
	RecipeID      uint                   `json:"recipe_id"`
	MemberResults []MemberAllergenResult `json:"member_results"`
	Disclaimer    string                 `json:"disclaimer"`
}

// MemberAllergenResult is the allergen safety result for a single family member.
type MemberAllergenResult struct {
	MemberID   uint     `json:"member_id"`
	MemberName string   `json:"member_name"`
	Status     string   `json:"status"`   // "safe", "caution", "unsafe"
	Warnings   []string `json:"warnings"` // specific allergen warnings
}

// AnalyzeRecipe performs allergen analysis on a recipe's ingredients.
func (s *AllergenService) AnalyzeRecipe(ctx context.Context, recipeID uint, isPremium bool) (*AllergenAnalysisResponse, error) {
	if s.AIProvider == nil {
		return nil, errors.New("AI provider is not configured")
	}

	// 1. Get recipe and its ingredients
	recipe, err := s.RecipeRepo.GetRecipeByID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipe: %w", err)
	}

	// 2. Check for cached analysis with the same prompt version and premium tier
	existing, err := s.AllergenRepo.GetAnalysisByRecipeID(recipeID)
	if err == nil && existing.PromptVersion == promptVersion && existing.IsPremium == isPremium {
		existing.Disclaimer = AllergenDisclaimer
		return &AllergenAnalysisResponse{
			AllergenAnalysis: *existing,
			Disclaimer:       AllergenDisclaimer,
		}, nil
	}

	// 3. Build ingredient list for AI analysis
	ingredients := make([]ai.IngredientInput, len(recipe.Ingredients))
	for i, ing := range recipe.Ingredients {
		ingredients[i] = ai.IngredientInput{
			Name:   ing.Name,
			Unit:   ing.Unit,
			Amount: ing.Amount,
		}
	}

	// 4. Call AI provider for allergen analysis
	result, err := s.AIProvider.AnalyzeAllergens(ctx, ai.AllergenRequest{
		Ingredients: ingredients,
		IsPremium:   isPremium,
	})
	if err != nil {
		return nil, fmt.Errorf("AI allergen analysis failed: %w", err)
	}

	// 5. Map AI results to model ingredient analyses
	ingredientAnalyses := make(models.IngredientAnalysisList, len(result.IngredientAnalyses))
	for i, ia := range result.IngredientAnalyses {
		ingredientAnalyses[i] = models.IngredientAnalysis{
			IngredientName:    ia.IngredientName,
			CommonAllergens:   ia.CommonAllergens,
			PossibleAllergens: ia.PossibleAllergens,
			SubIngredients:    ia.SubIngredients,
			SeedOilRisk:       ia.SeedOilRisk,
			Confidence:        ia.Confidence,
		}
	}

	// 6. Set aggregate flags — conservative approach: uncertain = flagged
	analysis := &models.AllergenAnalysis{
		RecipeID:           recipeID,
		IngredientAnalyses: ingredientAnalyses,
		Confidence:         result.Confidence,
		RequiresReview:     result.Confidence < 0.8 || result.RequiresReview,
		IsPremium:          isPremium,
		PromptVersion:      promptVersion,
	}

	for _, ia := range ingredientAnalyses {
		allAllergens := append(ia.CommonAllergens, ia.PossibleAllergens...)
		for _, allergen := range allAllergens {
			lower := strings.ToLower(allergen)
			switch {
			case containsAny(lower, "nut", "peanut", "almond", "cashew", "walnut", "pecan", "pistachio", "hazelnut", "macadamia"):
				analysis.ContainsNuts = true
			case containsAny(lower, "dairy", "milk", "cheese", "butter", "cream", "lactose", "whey", "casein"):
				analysis.ContainsDairy = true
			case containsAny(lower, "gluten", "wheat", "barley", "rye"):
				analysis.ContainsGluten = true
			case containsAny(lower, "soy", "soya", "soybean"):
				analysis.ContainsSoy = true
			case containsAny(lower, "shellfish", "shrimp", "crab", "lobster", "clam", "mussel", "oyster"):
				analysis.ContainsShellfish = true
			case containsAny(lower, "egg"):
				analysis.ContainsEggs = true
			}
		}
		if ia.SeedOilRisk {
			analysis.ContainsSeedOils = true
		}
	}

	// 7. Save to DB (update if existing, create if not)
	if existing != nil {
		analysis.Model = existing.Model
		if err := s.AllergenRepo.UpdateAnalysis(analysis); err != nil {
			logger.Get().Error("failed to update allergen analysis", zap.Uint("recipe_id", recipeID), zap.Error(err))
			return nil, fmt.Errorf("failed to save allergen analysis: %w", err)
		}
	} else {
		if err := s.AllergenRepo.CreateAnalysis(analysis); err != nil {
			return nil, fmt.Errorf("failed to save allergen analysis: %w", err)
		}
	}

	// 8. Return with disclaimer
	return &AllergenAnalysisResponse{
		AllergenAnalysis: *analysis,
		Disclaimer:       AllergenDisclaimer,
	}, nil
}

// CheckFamily cross-references allergen analysis with family member dietary profiles.
func (s *AllergenService) CheckFamily(ctx context.Context, recipeID uint, ownerID uint) (*FamilyCheckResponse, error) {
	// 1. Get allergen analysis for recipe
	analysis, err := s.AllergenRepo.GetAnalysisByRecipeID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("no allergen analysis found for recipe; run analysis first: %w", err)
	}

	// 2. Get family and all member dietary profiles
	family, err := s.FamilyRepo.GetFamilyByOwnerID(ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get family: %w", err)
	}

	// 3. For each member, check their allergies/intolerances against ingredient analyses
	var memberResults []MemberAllergenResult
	var safeProfiles models.UintList
	var unsafeProfiles models.UintList

	for _, member := range family.Members {
		result := MemberAllergenResult{
			MemberID:   member.ID,
			MemberName: member.Name,
			Status:     "safe",
		}

		if member.DietaryProfile == nil {
			// No dietary profile means we cannot check — mark as safe by default
			memberResults = append(memberResults, result)
			safeProfiles = append(safeProfiles, member.ID)
			continue
		}

		profile := member.DietaryProfile

		// Check allergies
		for _, allergy := range profile.Allergies {
			allergyLower := strings.ToLower(allergy.Name)
			for _, ia := range analysis.IngredientAnalyses {
				// Check common allergens — definite match = unsafe
				for _, common := range ia.CommonAllergens {
					if strings.Contains(strings.ToLower(common), allergyLower) || strings.Contains(allergyLower, strings.ToLower(common)) {
						result.Status = "unsafe"
						result.Warnings = append(result.Warnings, fmt.Sprintf("%s contains %s (allergy: %s)", ia.IngredientName, common, allergy.Name))
					}
				}
				// Check possible allergens — possible match = caution
				for _, possible := range ia.PossibleAllergens {
					if strings.Contains(strings.ToLower(possible), allergyLower) || strings.Contains(allergyLower, strings.ToLower(possible)) {
						if result.Status != "unsafe" {
							result.Status = "caution"
						}
						result.Warnings = append(result.Warnings, fmt.Sprintf("%s may contain %s (allergy: %s)", ia.IngredientName, possible, allergy.Name))
					}
				}
				// Check sub-forms of the allergy
				for _, subForm := range allergy.SubForms {
					subFormLower := strings.ToLower(subForm)
					for _, common := range ia.CommonAllergens {
						if strings.Contains(strings.ToLower(common), subFormLower) || strings.Contains(subFormLower, strings.ToLower(common)) {
							result.Status = "unsafe"
							result.Warnings = append(result.Warnings, fmt.Sprintf("%s contains %s (sub-form of %s)", ia.IngredientName, common, allergy.Name))
						}
					}
				}
			}
		}

		// Check intolerances — treated as caution unless already unsafe
		for _, intolerance := range profile.Intolerances {
			intoleranceLower := strings.ToLower(intolerance)
			for _, ia := range analysis.IngredientAnalyses {
				for _, common := range ia.CommonAllergens {
					if strings.Contains(strings.ToLower(common), intoleranceLower) || strings.Contains(intoleranceLower, strings.ToLower(common)) {
						if result.Status != "unsafe" {
							result.Status = "caution"
						}
						result.Warnings = append(result.Warnings, fmt.Sprintf("%s contains %s (intolerance: %s)", ia.IngredientName, common, intolerance))
					}
				}
			}
		}

		memberResults = append(memberResults, result)
		if result.Status == "unsafe" {
			unsafeProfiles = append(unsafeProfiles, member.ID)
		} else {
			safeProfiles = append(safeProfiles, member.ID)
		}
	}

	// 4. Update the analysis with safe/unsafe profile lists
	analysis.SafeForProfiles = safeProfiles
	analysis.UnsafeForProfiles = unsafeProfiles
	if err := s.AllergenRepo.UpdateAnalysis(analysis); err != nil {
		logger.Get().Error("failed to update analysis with profile results", zap.Uint("recipe_id", recipeID), zap.Error(err))
	}

	return &FamilyCheckResponse{
		RecipeID:      recipeID,
		MemberResults: memberResults,
		Disclaimer:    AllergenDisclaimer,
	}, nil
}

// GetAnalysis returns cached allergen analysis for a recipe.
func (s *AllergenService) GetAnalysis(ctx context.Context, recipeID uint) (*AllergenAnalysisResponse, error) {
	analysis, err := s.AllergenRepo.GetAnalysisByRecipeID(recipeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get allergen analysis: %w", err)
	}

	return &AllergenAnalysisResponse{
		AllergenAnalysis: *analysis,
		Disclaimer:       AllergenDisclaimer,
	}, nil
}

// containsAny checks if s contains any of the substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
