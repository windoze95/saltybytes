package main

import (
	"os"
	"runtime"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/db"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/router"
	"go.uber.org/zap"
)

// init is called before the main function.
func init() {
	// Initialize structured logger (dev mode if GIN_MODE != release)
	isDev := os.Getenv("GIN_MODE") != "release"
	logger.Init(isDev)

	// Configure the runtime
	ConfigureRuntime()
}

// Entry point for the API.
func main() {
	defer logger.Sync()

	// Load the config
	var cfg *config.Config
	if c, err := config.LoadConfig(); err != nil {
		logger.Get().Fatal("failed to load config", zap.Error(err))
	} else {
		cfg = c
	}

	// Check that all ENV variables are set
	if err := cfg.CheckConfigEnvFields(); err != nil {
		logger.Get().Fatal("missing required config fields", zap.Error(err))
	}

	// Load prompts from YAML
	prompts, err := config.LoadPrompts("configs/prompts.yaml")
	if err != nil {
		logger.Get().Fatal("failed to load prompts", zap.Error(err))
	}
	cfg.Prompts = prompts

	// Connect to the database
	database, err := db.New(cfg)
	if err != nil {
		logger.Get().Fatal("failed to connect to database", zap.Error(err))
	}
	sqlDB, err := database.DB()
	if err != nil {
		logger.Get().Fatal("failed to get underlying sql.DB", zap.Error(err))
	}
	defer sqlDB.Close()

	// Create a new gin router
	gin.SetMode(gin.ReleaseMode)
	r := router.SetupRouter(cfg, database)

	// Run the server
	logger.Get().Info("starting server", zap.String("port", cfg.EnvVars.Port))
	r.Run(":" + cfg.EnvVars.Port)
}

// ConfigureRuntime sets the number of operating system threads.
func ConfigureRuntime() {
	nuCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(nuCPU)
	logger.Get().Info("runtime configured", zap.Int("cpus", nuCPU))
}
