package service

import (
	"context"
	"strings"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// embeddingBackfillBatchSize is how many rows are scanned per batch.
const embeddingBackfillBatchSize = 25

// embeddingBackfillDelay is the pause between embedding API calls to avoid
// hammering the provider. Package-level var so tests can shorten it.
var embeddingBackfillDelay = 200 * time.Millisecond

// embeddingCallTimeout bounds a single embedding API call made with a
// background context. Without it a stalled provider connection would wedge
// background loops (backfill, canonical refresh) indefinitely.
const embeddingCallTimeout = 30 * time.Second

// EmbeddingBackfillRepo is the subset of VectorRepository needed by the
// embedding backfill task.
type EmbeddingBackfillRepo interface {
	ListRecipesMissingEmbedding(afterID uint, limit int) ([]models.Recipe, error)
	UpdateEmbedding(recipeID uint, embedding []float32) error
	ListCanonicalsMissingEmbedding(afterID uint, limit int) ([]models.CanonicalRecipe, error)
	UpdateCanonicalEmbedding(canonicalID uint, embedding []float32) error
}

// StartEmbeddingBackfill launches a background goroutine that scans recipes
// and canonical recipes missing embeddings and populates them. Per-row
// failures are logged and skipped; the scan always makes forward progress.
func StartEmbeddingBackfill(repo EmbeddingBackfillRepo, embedProvider ai.EmbeddingProvider) {
	if repo == nil || embedProvider == nil {
		return
	}

	go func() {
		defer util.RecoverPanic("embedding backfill")

		backfillRecipeEmbeddings(repo, embedProvider)
		backfillCanonicalEmbeddings(repo, embedProvider)
	}()
}

// backfillRecipeEmbeddings populates recipes.embedding for rows where it is NULL.
func backfillRecipeEmbeddings(repo EmbeddingBackfillRepo, embedProvider ai.EmbeddingProvider) {
	log := logger.Get()
	var lastID uint
	filled, skipped := 0, 0

	for {
		batch, err := repo.ListRecipesMissingEmbedding(lastID, embeddingBackfillBatchSize)
		if err != nil {
			log.Warn("embedding backfill: failed to list recipes", zap.Error(err))
			return
		}
		if len(batch) == 0 {
			break
		}

		for i := range batch {
			recipe := &batch[i]
			lastID = recipe.ID

			def := effectiveRecipeDef(recipe)
			text := strings.TrimSpace(embeddingText(&def))
			if text == "" {
				skipped++
				continue
			}

			embedCtx, cancel := context.WithTimeout(context.Background(), embeddingCallTimeout)
			embedding, err := embedProvider.GenerateEmbedding(embedCtx, text)
			cancel()
			if err != nil {
				log.Warn("embedding backfill: failed to embed recipe", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
				skipped++
				time.Sleep(embeddingBackfillDelay)
				continue
			}

			if err := repo.UpdateEmbedding(recipe.ID, embedding); err != nil {
				log.Warn("embedding backfill: failed to store recipe embedding", zap.Uint("recipe_id", recipe.ID), zap.Error(err))
				skipped++
			} else {
				filled++
			}

			time.Sleep(embeddingBackfillDelay)
		}

		log.Info("embedding backfill: recipes progress",
			zap.Uint("last_id", lastID), zap.Int("filled", filled), zap.Int("skipped", skipped))
	}

	if filled > 0 || skipped > 0 {
		log.Info("embedding backfill: recipes complete", zap.Int("filled", filled), zap.Int("skipped", skipped))
	}
}

// backfillCanonicalEmbeddings populates canonical_recipes.embedding for rows
// where it is NULL.
func backfillCanonicalEmbeddings(repo EmbeddingBackfillRepo, embedProvider ai.EmbeddingProvider) {
	log := logger.Get()
	var lastID uint
	filled, skipped := 0, 0

	for {
		batch, err := repo.ListCanonicalsMissingEmbedding(lastID, embeddingBackfillBatchSize)
		if err != nil {
			log.Warn("embedding backfill: failed to list canonicals", zap.Error(err))
			return
		}
		if len(batch) == 0 {
			break
		}

		for i := range batch {
			entry := &batch[i]
			lastID = entry.ID

			text := strings.TrimSpace(embeddingText(&entry.RecipeData))
			if text == "" {
				skipped++
				continue
			}

			embedCtx, cancel := context.WithTimeout(context.Background(), embeddingCallTimeout)
			embedding, err := embedProvider.GenerateEmbedding(embedCtx, text)
			cancel()
			if err != nil {
				log.Warn("embedding backfill: failed to embed canonical", zap.Uint("canonical_id", entry.ID), zap.Error(err))
				skipped++
				time.Sleep(embeddingBackfillDelay)
				continue
			}

			if err := repo.UpdateCanonicalEmbedding(entry.ID, embedding); err != nil {
				log.Warn("embedding backfill: failed to store canonical embedding", zap.Uint("canonical_id", entry.ID), zap.Error(err))
				skipped++
			} else {
				filled++
			}

			time.Sleep(embeddingBackfillDelay)
		}

		log.Info("embedding backfill: canonicals progress",
			zap.Uint("last_id", lastID), zap.Int("filled", filled), zap.Int("skipped", skipped))
	}

	if filled > 0 || skipped > 0 {
		log.Info("embedding backfill: canonicals complete", zap.Int("filled", filled), zap.Int("skipped", skipped))
	}
}
