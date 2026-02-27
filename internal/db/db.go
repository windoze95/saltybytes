package db

import (
	"fmt"
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
	logger.Get().Info("connecting to database", zap.String("url", databaseURL))
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
	database.Exec("CREATE EXTENSION IF NOT EXISTS vector")

	// AutoMigrate all models. RecipeTree.Nodes and RecipeNode.Children use gorm:"-"
	// to avoid circular FK issues during migration.
	database.AutoMigrate(
		&models.User{},
		&models.UserAuth{},
		&models.Subscription{},
		&models.UserSettings{},
		&models.Personalization{},
		&models.Tag{},
		&models.RecipeHistory{},
		&models.RecipeHistoryEntry{},
		&models.Family{},
		&models.FamilyMember{},
		&models.DietaryProfile{},
		&models.RecipeTree{},
		&models.RecipeNode{},
		&models.Recipe{},
		&models.AllergenAnalysis{},
	)

	// Add the FK from recipe_nodes.tree_id → recipe_trees.id that gorm:"-" skipped.
	database.Exec(`ALTER TABLE recipe_nodes ADD CONSTRAINT IF NOT EXISTS fk_recipe_nodes_tree
		FOREIGN KEY (tree_id) REFERENCES recipe_trees(id)
		ON UPDATE CASCADE ON DELETE CASCADE`)

	return database, err
}
