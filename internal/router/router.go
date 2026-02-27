package router

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/handlers"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/middleware"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/ws"
	"gorm.io/gorm"
)

// SetupRouter sets up the Gin router.
func SetupRouter(cfg *config.Config, database *gorm.DB) *gin.Engine {
	// Create default Gin router
	r := gin.Default()

	config := cors.DefaultConfig()
	config.AllowCredentials = true
	config.AllowOrigins = []string{
		"https://api.saltybytes.ai",
		"https://www.api.saltybytes.ai",
		"https://saltybytes.ai",
		"https://www.saltybytes.ai",
	}
	r.Use(cors.New(config))

	// Add request ID middleware for request correlation
	r.Use(logger.RequestIDMiddleware())

	// Ping route for testing
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	// User-related routes setup
	userRepo := repository.NewUserRepository(database)
	userService := service.NewUserService(cfg, userRepo)
	userHandler := handlers.NewUserHandler(userService)

	// AI provider setup
	textProvider := ai.NewAnthropicProvider(cfg.EnvVars.AnthropicAPIKey, cfg.Prompts)
	imageProvider := ai.NewDALLEProvider(cfg.EnvVars.OpenAIAPIKey)

	// Recipe-related routes setup
	recipeRepo := repository.NewRecipeRepository(database)
	recipeService := service.NewRecipeService(cfg, recipeRepo, textProvider, imageProvider)
	recipeHandler := handlers.NewRecipeHandler(recipeService)

	// Import-related routes setup
	previewProvider := ai.NewAnthropicLightProvider(cfg.EnvVars.AnthropicAPIKey, cfg.Prompts)
	importService := service.NewImportService(cfg, recipeRepo, recipeService, textProvider, textProvider, previewProvider)
	importHandler := handlers.NewImportHandler(importService)

	// Group for API routes that don't require token verification
	apiPublic := r.Group("/v1")
	{
		// User-related routes

		// Create a new user
		apiPublic.POST("/users", userHandler.CreateUser)
		// Login a user
		apiPublic.POST("/auth/login", userHandler.LoginUser)
		// Refresh an access token
		apiPublic.POST("/auth/refresh", userHandler.RefreshToken)

		// Recipe-related routes

		// Get a single recipe by it's ID
		apiPublic.GET("/recipes/:recipe_id", recipeHandler.GetRecipe)
	}

	// Group for API routes that require token verification
	apiProtected := r.Group("/v1")
	{
		apiProtected.Use(middleware.VerifyTokenMiddleware(cfg))

		// User-related routes

		// Verify a user's token
		apiProtected.GET("/users/verify", middleware.AttachUserToContext(userService), userHandler.VerifyToken)
		// Get a user by their ID
		apiProtected.GET("/users/me", middleware.AttachUserToContext(userService), userHandler.GetUserByID)
		// Get a user's settings
		apiProtected.GET("/users/me/settings", middleware.AttachUserToContext(userService), userHandler.GetUserSettings)

		// Recipe-related routes

		// List the authenticated user's recipes
		apiProtected.GET("/recipes", middleware.AttachUserToContext(userService), recipeHandler.ListRecipes)
		// Get a single recipe history by the recipe history's ID
		apiProtected.GET("/recipes/chat-history/:history_id", middleware.AttachUserToContext(userService), recipeHandler.GetRecipeHistory)
		// Generate a new recipe with chat
		apiProtected.POST("/recipes/chat", middleware.AttachUserToContext(userService), recipeHandler.GenerateRecipe)
		// Generate a new recipe based on a previous recipe and the user's chat
		apiProtected.PUT("/recipes/:recipe_id/chat", middleware.AttachUserToContext(userService), recipeHandler.RegenerateRecipe)

		apiProtected.POST("/recipes/:recipe_id/fork", middleware.AttachUserToContext(userService), recipeHandler.GenerateRecipeWithFork)

		// Recipe import routes
		apiProtected.POST("/recipes/import/url", middleware.AttachUserToContext(userService), importHandler.ImportFromURL)
		apiProtected.POST("/recipes/import/photo", middleware.AttachUserToContext(userService), importHandler.ImportFromPhoto)
		apiProtected.POST("/recipes/import/text", middleware.AttachUserToContext(userService), importHandler.ImportFromText)
		apiProtected.POST("/recipes/import/manual", middleware.AttachUserToContext(userService), importHandler.ImportManual)

		// Recipe preview route (cheap extraction for pre-import preview)
		apiProtected.POST("/recipes/preview/url", middleware.AttachUserToContext(userService), importHandler.PreviewFromURL)

		// Recipe tree/branching routes
		treeService := service.NewRecipeTreeService(cfg, recipeRepo)
		treeHandler := handlers.NewRecipeTreeHandler(treeService)

		apiProtected.GET("/recipes/:recipe_id/tree", middleware.AttachUserToContext(userService), treeHandler.GetTree)
		apiProtected.POST("/recipes/:recipe_id/branch", middleware.AttachUserToContext(userService), treeHandler.CreateBranch)
		apiProtected.PUT("/recipes/:recipe_id/tree/active/:node_id", middleware.AttachUserToContext(userService), treeHandler.SetActiveNode)
	}

	// Family-related routes setup
	familyRepo := repository.NewFamilyRepository(database)
	familyService := service.NewFamilyService(cfg, familyRepo, textProvider)
	familyHandler := handlers.NewFamilyHandler(familyService)

	apiProtected.POST("/family", middleware.AttachUserToContext(userService), familyHandler.CreateFamily)
	apiProtected.GET("/family", middleware.AttachUserToContext(userService), familyHandler.GetFamily)
	apiProtected.POST("/family/members", middleware.AttachUserToContext(userService), familyHandler.AddMember)
	apiProtected.PUT("/family/members/:member_id", middleware.AttachUserToContext(userService), familyHandler.UpdateMember)
	apiProtected.DELETE("/family/members/:member_id", middleware.AttachUserToContext(userService), familyHandler.DeleteMember)
	apiProtected.PUT("/family/members/:member_id/dietary", middleware.AttachUserToContext(userService), familyHandler.UpdateDietaryProfile)
	apiProtected.POST("/family/members/:member_id/dietary/interview", middleware.AttachUserToContext(userService), familyHandler.DietaryInterview)

	// Allergen analysis routes setup
	allergenRepo := repository.NewAllergenRepository(database)
	allergenService := service.NewAllergenService(cfg, allergenRepo, familyRepo, recipeRepo, textProvider)
	allergenHandler := handlers.NewAllergenHandler(allergenService)

	apiProtected.POST("/recipes/:recipe_id/allergens/analyze", middleware.AttachUserToContext(userService), allergenHandler.AnalyzeRecipe)
	apiProtected.GET("/recipes/:recipe_id/allergens", middleware.AttachUserToContext(userService), allergenHandler.GetAnalysis)
	apiProtected.POST("/recipes/:recipe_id/allergens/check-family", middleware.AttachUserToContext(userService), allergenHandler.CheckFamily)

	// User update routes
	apiProtected.PUT("/users/me", middleware.AttachUserToContext(userService), userHandler.UpdateUser)
	apiProtected.PUT("/users/me/settings", middleware.AttachUserToContext(userService), userHandler.UpdateSettings)
	apiProtected.PUT("/users/me/personalization", middleware.AttachUserToContext(userService), userHandler.UpdatePersonalization)

	// Recipe delete
	apiProtected.DELETE("/recipes/:recipe_id", middleware.AttachUserToContext(userService), recipeHandler.DeleteRecipe)

	// Search routes
	searchProvider := ai.NewWebSearchProvider(cfg.EnvVars.GoogleSearchKey, cfg.EnvVars.GoogleSearchCX, cfg.EnvVars.BraveSearchKey)
	searchService := service.NewSearchService(cfg, searchProvider)
	searchHandler := handlers.NewSearchHandler(searchService)
	apiProtected.GET("/recipes/search", middleware.AttachUserToContext(userService), searchHandler.SearchRecipes)

	// Vector similarity routes
	vectorRepo := repository.NewVectorRepository(database)
	embedProvider := ai.NewEmbeddingProvider(cfg.EnvVars.OpenAIAPIKey)
	similarityHandler := handlers.NewSimilarityHandler(vectorRepo, embedProvider, recipeService)
	apiProtected.GET("/recipes/similar/:recipe_id", middleware.AttachUserToContext(userService), similarityHandler.FindSimilar)

	// Subscription routes
	subService := service.NewSubscriptionService(cfg, userRepo)
	subHandler := handlers.NewSubscriptionHandler(subService)
	apiProtected.GET("/subscription", middleware.AttachUserToContext(userService), subHandler.GetSubscription)
	apiProtected.POST("/subscription/upgrade", middleware.AttachUserToContext(userService), subHandler.UpgradeSubscription)

	// Image upload
	imageHandler := handlers.NewImageHandler(cfg)
	apiProtected.POST("/images/upload", middleware.AttachUserToContext(userService), imageHandler.UploadImage)

	// WebSocket routes (authenticated via query param token)
	hub := ws.NewHub()
	go hub.Run()
	speechProvider := ai.NewWhisperProvider(cfg.EnvVars.OpenAIAPIKey)
	voiceService := service.NewVoiceService(cfg, textProvider, speechProvider)
	cookingHandler := ws.NewCookingHandler(hub, cfg.EnvVars.JwtSecretKey, voiceService, recipeRepo)
	r.GET("/v1/ws/cook/:recipe_id", cookingHandler.HandleCookingSession)

	return r
}
