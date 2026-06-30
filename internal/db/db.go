package db

import (
	"fmt"
	"net/url"
	"time"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// New creates a new database connection.
func New(cfg *config.Config) (*gorm.DB, error) {
	return connectToDatabaseWithRetry(cfg.EnvVars.DatabaseUrl)
}

// connectToDatabaseWithRetry connects to the database and retries if necessary.
func connectToDatabaseWithRetry(databaseURL string) (*gorm.DB, error) {
	logger.Get().Info("connecting to database", zap.String("url", redactDSN(databaseURL)))
	var database *gorm.DB
	var err error

	start := time.Now()
	for {
		database, err = gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
		if err == nil {
			break
		}
		if time.Since(start) > 1*time.Minute {
			return nil, fmt.Errorf("could not connect to database after 1 minute: %w", err)
		}
		logger.Get().Warn("could not connect to database, retrying...", zap.Error(err))
		time.Sleep(5 * time.Second)
	}

	// Enable pgvector extension for embedding similarity search
	if execErr := database.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; execErr != nil {
		logger.Get().Warn("failed to create pgvector extension", zap.Error(execErr))
	}

	// AutoMigrate all models. RecipeTree.Nodes and RecipeNode.Children use gorm:"-"
	// to avoid circular FK issues during migration.
	if migrateErr := database.AutoMigrate(
		&models.User{},
		&models.UserAuth{},
		&models.Subscription{},
		&models.UserSettings{},
		&models.Personalization{},
		&models.Tag{},
		&models.Family{},
		&models.FamilyMember{},
		&models.DietaryProfile{},
		&models.RecipeTree{},
		&models.RecipeNode{},
		&models.Recipe{},
		&models.AllergenAnalysis{},
		&models.SearchCache{},
		&models.CanonicalRecipe{},
		&models.VideoExtractionCache{},
		&models.VideoImport{},
		&models.AIUsageLog{},
	); migrateErr != nil {
		return nil, fmt.Errorf("database auto-migration failed: %w", migrateErr)
	}

	// HNSW index for vector similarity on search cache embeddings
	if execErr := database.Exec(`CREATE INDEX IF NOT EXISTS idx_search_caches_embedding ON search_caches USING hnsw (embedding vector_cosine_ops)`).Error; execErr != nil {
		logger.Get().Warn("failed to create idx_search_caches_embedding index", zap.Error(execErr))
	}

	// HNSW index for vector similarity on canonical recipe embeddings
	if execErr := database.Exec(`CREATE INDEX IF NOT EXISTS idx_canonical_recipes_embedding ON canonical_recipes USING hnsw (embedding vector_cosine_ops)`).Error; execErr != nil {
		logger.Get().Warn("failed to create idx_canonical_recipes_embedding index", zap.Error(execErr))
	}

	// HNSW index for vector similarity on user recipe embeddings
	if execErr := database.Exec(`CREATE INDEX IF NOT EXISTS idx_recipes_embedding ON recipes USING hnsw (embedding vector_cosine_ops)`).Error; execErr != nil {
		logger.Get().Warn("failed to create idx_recipes_embedding index", zap.Error(execErr))
	}

	// Composite index for GetUserRecipes query (created_by_id, created_at DESC)
	if execErr := database.Exec(`CREATE INDEX IF NOT EXISTS idx_recipes_user_created ON recipes (created_by_id, created_at DESC)`).Error; execErr != nil {
		logger.Get().Warn("failed to create idx_recipes_user_created index", zap.Error(execErr))
	}

	// Add the FK from recipe_nodes.tree_id → recipe_trees.id that gorm:"-" skipped.
	// Postgres does not support ADD CONSTRAINT IF NOT EXISTS, so guard with a
	// pg_constraint existence check.
	if execErr := database.Exec(`DO $$
	BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'fk_recipe_nodes_tree'
		) THEN
			ALTER TABLE recipe_nodes ADD CONSTRAINT fk_recipe_nodes_tree
				FOREIGN KEY (tree_id) REFERENCES recipe_trees(id)
				ON UPDATE CASCADE ON DELETE CASCADE;
		END IF;
	END $$`).Error; execErr != nil {
		logger.Get().Warn("failed to add fk_recipe_nodes_tree constraint", zap.Error(execErr))
	}

	return database, nil
}

// redactDSN parses a database connection string and masks the password.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "[unparseable DSN]"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}
