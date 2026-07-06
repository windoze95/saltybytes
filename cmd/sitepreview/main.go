// Command sitepreview serves the public website (internal/web) with canned
// fixture data on localhost — no database, no network. It exists so the
// marketing and recipe pages can be designed/reviewed without prod data:
//
//	go run ./cmd/sitepreview
//	open http://localhost:8099/         (home)
//	open http://localhost:8099/r/1     (rich recipe)
//	open http://localhost:8099/r/2     (sparse recipe)
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/web"
)

type fixtureRepo struct{}

var fixtures = map[uint]*models.CanonicalRecipe{
	1: {
		OriginalURL: "https://pinchofyum.com/bang-bang-salmon-with-avocado-cucumber-salsa",
		RecipeData: models.RecipeDef{
			Title: "Bang Bang Salmon with Avocado Cucumber Salsa",
			Ingredients: models.Ingredients{
				{Name: "salmon fillets, skin removed", Amount: 4, Unit: "6-oz"},
				{OriginalText: "1/2 cup mayonnaise (Kewpie if you have it)"},
				{Name: "sweet chili sauce", Amount: 0.25, Unit: "cup"},
				{Name: "sriracha", Amount: 1, AmountHigh: 2, Unit: "tbsp"},
				{Name: "honey", Amount: 1, Unit: "tbsp"},
				{OriginalText: "1 avocado, diced"},
				{OriginalText: "1/2 English cucumber, diced small"},
				{Name: "lime, juiced", Amount: 1, Unit: ""},
				{Name: "cilantro, chopped", Amount: 0.25, Unit: "cup"},
				{Name: "kosher salt", Amount: 0, Unit: ""},
			},
			Instructions: []string{
				"Pat the salmon dry and season generously with salt. Whisk the mayonnaise, sweet chili sauce, sriracha, and honey into a bang bang sauce; reserve half for serving.",
				"Brush the salmon with the remaining sauce and roast at 425°F for 10–12 minutes, until the thickest part flakes easily.",
				"While it roasts, toss the avocado, cucumber, lime juice, cilantro, and a pinch of salt into a quick salsa.",
				"Spoon the reserved sauce over the salmon, pile the salsa on top, and serve over rice.",
			},
			CookTime:    25,
			Portions:    4,
			PortionSize: "1 fillet",
			SourceURL:   "https://pinchofyum.com/bang-bang-salmon-with-avocado-cucumber-salsa",
			UnitSystem:  "us_customary",
		},
	},
	2: {
		OriginalURL: "https://alexandracooks.com/2012/11/07/my-mothers-peasant-bread",
		RecipeData: models.RecipeDef{
			Title: "My Mother's Peasant Bread",
			Ingredients: models.Ingredients{
				{Name: "all-purpose flour", Amount: 4, Unit: "cups"},
				{Name: "kosher salt", Amount: 2, Unit: "tsp"},
				{Name: "instant yeast", Amount: 2.25, Unit: "tsp"},
				{Name: "lukewarm water", Amount: 2, Unit: "cups"},
			},
			Instructions: []string{
				"Whisk the flour, salt, and yeast together, then stir in the water until a sticky dough forms.",
				"Cover and let rise until doubled, about an hour.",
				"Butter two oven-safe bowls, divide the dough, and let rise again while the oven heats to 425°F.",
				"Bake 15 minutes, reduce to 375°F, and bake 15–17 minutes more until golden.",
			},
			CookTime:  95,
			Portions:  8,
			SourceURL: "https://alexandracooks.com/2012/11/07/my-mothers-peasant-bread",
		},
	},
}

func (fixtureRepo) GetByID(id uint) (*models.CanonicalRecipe, error) {
	if entry, ok := fixtures[id]; ok {
		out := *entry
		out.ID = id
		return &out, nil
	}
	return nil, fmt.Errorf("not found")
}

func (fixtureRepo) GetByNormalizedURL(normalizedURL string) (*models.CanonicalRecipe, error) {
	for id, entry := range fixtures {
		if entry.OriginalURL == normalizedURL {
			out := *entry
			out.ID = id
			return &out, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (fixtureRepo) Upsert(*models.CanonicalRecipe) error { return nil }
func (fixtureRepo) IncrementHitCount(uint) error         { return nil }

func (fixtureRepo) ListServableSummaries(limit int) ([]repository.CanonicalSummary, error) {
	now := time.Now()
	return []repository.CanonicalSummary{
		{ID: 1, Title: "Bang Bang Salmon with Avocado Cucumber Salsa", OriginalURL: "https://pinchofyum.com/bang-bang-salmon", UpdatedAt: now},
		{ID: 2, Title: "My Mother's Peasant Bread", OriginalURL: "https://alexandracooks.com/peasant-bread", UpdatedAt: now},
		{ID: 3, Title: "Crockpot Beef Ramen", OriginalURL: "https://therealfooddietitians.com/crockpot-beef-ramen", UpdatedAt: now},
		{ID: 4, Title: "Chicken Wontons in Spicy Chili Sauce", OriginalURL: "https://pinchofyum.com/chicken-wontons", UpdatedAt: now},
		{ID: 5, Title: "Radish, Celery & Cucumber Salad", OriginalURL: "https://www.eatingwell.com/recipe/7902268", UpdatedAt: now},
		{ID: 6, Title: "Miso Peanut Ramen Bowls", OriginalURL: "https://pinchofyum.com/miso-peanut-ramen-bowls", UpdatedAt: now},
	}, nil
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	cfg := &config.Config{}
	cfg.EnvVars.SiteBaseURL = "http://localhost:8099"
	cfg.EnvVars.PublicBaseURL = "https://api.saltybytes.ai"

	r := gin.New()
	web.NewHandler(cfg, fixtureRepo{}).Register(r)
	log.Println("sitepreview on http://localhost:8099 (/, /r/1, /r/2, /privacy, /nope)")
	log.Fatal(r.Run(":8099"))
}
