package router

import (
	"context"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/handlers"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/middleware"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/video"
	"github.com/windoze95/saltybytes-api/internal/ws"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// lightKeysFromConfig collects the light-tier API keys from config. Keys come
// from env/SSM only — never the DB.
func lightKeysFromConfig(cfg *config.Config) ai.LightKeys {
	return ai.LightKeys{
		AnthropicAPIKey:     cfg.EnvVars.AnthropicAPIKey,
		AnthropicLightModel: cfg.EnvVars.AnthropicLightModel,
		OpenAIAPIKey:        cfg.EnvVars.OpenAIAPIKey,
		GeminiAPIKey:        cfg.EnvVars.GeminiAPIKey,
		DeepSeekAPIKey:      cfg.EnvVars.DeepSeekAPIKey,
	}
}

// buildMainTextProvider selects the flagship reasoning provider. Defaults to the
// Anthropic Sonnet provider; MAIN_PROVIDER=gemini/openai/deepseek swaps in a
// cheaper frontier model (an OpenAI-compatible provider that implements the full
// TextProvider). gemini defaults to gemini-2.5-pro. Falls back to Sonnet if the
// alternative can't be built (e.g. a missing key), so the app always boots.
func buildMainTextProvider(cfg *config.Config, sonnet ai.TextProvider, mw ai.AIMiddleware) ai.TextProvider {
	switch cfg.EnvVars.MainProvider {
	case "", "anthropic":
		return sonnet
	default:
		model := cfg.EnvVars.MainModel
		if model == "" && cfg.EnvVars.MainProvider == "gemini" {
			model = "gemini-2.5-pro"
		}
		spec := ai.LightProviderSpec{Provider: cfg.EnvVars.MainProvider, Model: model, BaseURL: cfg.EnvVars.MainBaseURL}
		p, err := ai.BuildLightProvider(spec, lightKeysFromConfig(cfg), cfg.Prompts, mw)
		if err != nil {
			logger.Get().Warn("main provider unbuildable, using anthropic sonnet",
				zap.String("provider", cfg.EnvVars.MainProvider), zap.Error(err))
			return sonnet
		}
		logger.Get().Info("main tier provider active",
			zap.String("provider", cfg.EnvVars.MainProvider), zap.String("model", model))
		return p
	}
}

// SetupRouter sets up the Gin router.
func SetupRouter(cfg *config.Config, database *gorm.DB) *gin.Engine {
	// Create default Gin router
	r := gin.Default()

	// Trust only the load balancer in front of the app (private VPC ranges).
	// Gin's default trusts every hop, which lets clients spoof
	// X-Forwarded-For and defeat the per-IP rate limiting below. With this
	// set, ClientIP() resolves to the rightmost non-private address in the
	// chain (the real client as seen by the ALB).
	if err := r.SetTrustedProxies([]string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.1", "::1"}); err != nil {
		logger.Get().Error("failed to set trusted proxies", zap.Error(err))
	}

	config := cors.DefaultConfig()
	config.AllowCredentials = true
	config.AllowOrigins = []string{
		"https://api.saltybytes.ai",
		"https://www.api.saltybytes.ai",
		"https://saltybytes.ai",
		"https://www.saltybytes.ai",
	}
	config.AddAllowHeaders("Authorization", "X-SaltyBytes-Identifier")
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

	// Subscription service (shared by AI-generation, allergen, search and
	// subscription routes for usage gating)
	subService := service.NewSubscriptionService(cfg, userRepo)

	// AI provider setup
	textProvider := ai.NewAnthropicProvider(cfg.EnvVars.AnthropicAPIKey, cfg.EnvVars.AnthropicModel, cfg.Prompts)
	imageProvider := ai.NewDALLEProvider(cfg.EnvVars.OpenAIAPIKey)

	// AI observability + cost metering. The cost middleware records every AI
	// call's tokens + metered cost to ai_usage_logs (off the request path) —
	// the data behind the dashboard's model cost comparison + counterfactuals.
	aiUsageRepo := repository.NewAIUsageRepository(database)
	costMW := &ai.CostMiddleware{
		Pricing: ai.DefaultPricing,
		Sink: func(rec ai.UsageRecord) {
			go func() {
				if err := aiUsageRepo.Insert(&models.AIUsageLog{
					Operation:        rec.Operation,
					Provider:         rec.Provider,
					Model:            rec.Model,
					InputTokens:      rec.InputTokens,
					OutputTokens:     rec.OutputTokens,
					CacheInputTokens: rec.CacheInputTokens,
					CostUSD:          rec.CostUSD,
					DurationMS:       rec.DurationMS,
					Success:          rec.Success,
				}); err != nil {
					logger.Get().Warn("failed to record AI usage", zap.Error(err))
				}
			}()
		},
	}
	aiMW := ai.NewMiddlewareChain(&ai.LoggingMiddleware{}, costMW)
	textProvider.WithMiddleware(aiMW)

	// Main (flagship reasoning) tier: Sonnet by default; a cheaper frontier model
	// (e.g. gemini-2.5-pro) when MAIN_PROVIDER is set, driving recipe
	// generation/regen/fork, allergens and dietary. Streaming generation falls
	// back to non-streaming for non-Anthropic providers (no StreamingTextProvider).
	mainTextProvider := buildMainTextProvider(cfg, textProvider, aiMW)

	// Recipe-related routes setup
	recipeRepo := repository.NewRecipeRepository(database)
	vectorRepo := repository.NewVectorRepository(database)
	embedProvider := ai.NewEmbeddingProvider(cfg.EnvVars.OpenAIAPIKey)
	recipeService := service.NewRecipeService(cfg, recipeRepo, mainTextProvider, imageProvider)
	recipeService.EmbedProvider = embedProvider
	recipeService.VectorRepo = vectorRepo
	recipeHandler := handlers.NewRecipeHandler(recipeService)
	recipeHandler.SubService = subService

	// Light-tier model manager: owns the swappable cheap provider behind a
	// single SwitchableTextProvider. It seeds the registry + active selection
	// from the env default, applies whatever the DB marks active, and polls so a
	// dashboard live-switch on one instance propagates to the others.
	aiModelOptionRepo := repository.NewAIModelOptionRepository(database)
	envLightSpec := ai.LightProviderSpec{
		Provider: cfg.EnvVars.LightProvider,
		Model:    cfg.EnvVars.LightModel,
		BaseURL:  cfg.EnvVars.LightBaseURL,
	}
	modelManager := service.NewAIModelManager(aiModelOptionRepo, lightKeysFromConfig(cfg), cfg.Prompts, aiMW, envLightSpec)
	modelManager.Load(context.Background())
	modelManager.StartRefresh(context.Background(), 30*time.Second)
	previewProvider := modelManager.Provider()

	// Vision provider: Sonnet by default; native Gemini when VISION_NATIVE_GEMINI
	// is set — routes image/PDF import (photo + files) and the video frame
	// fallback through Gemini instead of Sonnet.
	var visionProvider ai.VisionProvider = textProvider
	if cfg.EnvVars.VisionNativeGemini && cfg.EnvVars.GeminiAPIKey != "" {
		gv := ai.NewGeminiVisionProvider(cfg.EnvVars.GeminiAPIKey, cfg.EnvVars.GeminiVisionModel, cfg.Prompts)
		gv.WithMiddleware(aiMW)
		visionProvider = gv
		logger.Get().Info("native gemini vision enabled", zap.String("model", cfg.EnvVars.GeminiVisionModel))
	}

	// Import-related routes setup
	canonicalRepo := repository.NewCanonicalRecipeRepository(database)
	importService := service.NewImportService(cfg, recipeRepo, recipeService, mainTextProvider, visionProvider, previewProvider)
	importService.CanonicalRepo = canonicalRepo
	// Portion estimation for imports that lack a serving count (cheap Haiku task)
	importService.Normalize = service.NewNormalizeService(cfg, previewProvider)
	importHandler := handlers.NewImportHandler(importService)
	importHandler.SubService = subService
	importService.SubService = subService
	// MultiResolver is wired later after search setup; set via field

	// Video-link import (premium). Stays dark until a ScrapeCreators API key is
	// configured, so the endpoint returns 503 until the feature is switched on.
	if cfg.EnvVars.ScrapeCreatorsAPIKey != "" {
		importService.VideoRepo = repository.NewVideoImportRepository(database)
		importService.VideoFetcher = video.NewScrapeCreatorsClient(cfg.EnvVars.ScrapeCreatorsAPIKey)
		importService.VideoFrameSampler = video.NewFrameSampler()
		logger.Get().Info("video-link import enabled")
	} else {
		logger.Get().Info("video-link import disabled (no ScrapeCreators API key configured)")
	}

	// Native Gemini video extraction (opt-in): when enabled, video import ingests
	// the whole clip (video + audio) through Gemini instead of sampling frames
	// onto Sonnet — far cheaper, and it reads the narration natively. Falls back
	// to frame sampling per-video. Requires a Gemini key.
	if cfg.EnvVars.VideoNativeGemini && cfg.EnvVars.GeminiAPIKey != "" {
		gvp := ai.NewGeminiVideoProvider(cfg.EnvVars.GeminiAPIKey, cfg.EnvVars.GeminiVideoModel, cfg.Prompts)
		gvp.WithMiddleware(aiMW)
		importService.VideoProvider = gvp
		logger.Get().Info("native gemini video extraction enabled", zap.String("model", cfg.EnvVars.GeminiVideoModel))
	}

	// Admin API: light-tier model registry + live switch, used by the operator
	// dashboard. Guarded by the shared ID header AND a dedicated admin token; the
	// whole group is disabled (503) when ADMIN_TOKEN is unset, so it is never
	// exposed by accident.
	adminAIHandler := handlers.NewAdminAIHandler(modelManager)
	apiAdmin := r.Group("/v1/admin")
	apiAdmin.Use(middleware.CheckIDHeader(cfg.EnvVars.IDHeader))
	apiAdmin.Use(middleware.RequireAdminToken(cfg.EnvVars.AdminToken))
	{
		apiAdmin.GET("/ai/models", adminAIHandler.ListModels)
		apiAdmin.POST("/ai/models", adminAIHandler.CreateModel)
		apiAdmin.PUT("/ai/models/:id", adminAIHandler.UpdateModel)
		apiAdmin.DELETE("/ai/models/:id", adminAIHandler.DeleteModel)
		apiAdmin.GET("/ai/active", adminAIHandler.GetActive)
		apiAdmin.PUT("/ai/active", adminAIHandler.SetActive)
	}

	// Group for API routes that don't require token verification
	apiPublic := r.Group("/v1")
	apiPublic.Use(middleware.CheckIDHeader(cfg.EnvVars.IDHeader))
	// Rate-limit the public auth endpoints per IP (5 req/min, burst 10) to
	// slow down credential stuffing and signup abuse.
	apiPublic.Use(middleware.RateLimitByIP(5, 10, 5*time.Minute, 15*time.Minute))
	{
		// User-related routes

		// Create a new user
		apiPublic.POST("/users", userHandler.CreateUser)
		// Login a user
		apiPublic.POST("/auth/login", userHandler.LoginUser)
		// Refresh an access token
		apiPublic.POST("/auth/refresh", userHandler.RefreshToken)
	}

	// Group for API routes that require token verification
	apiProtected := r.Group("/v1")
	{
		apiProtected.Use(middleware.CheckIDHeader(cfg.EnvVars.IDHeader))
		apiProtected.Use(middleware.VerifyTokenMiddleware(cfg))

		// User-related routes

		// Log out: revoke all outstanding refresh tokens
		apiProtected.POST("/auth/logout", middleware.AttachUserToContext(userService), userHandler.Logout)
		// Verify a user's token
		apiProtected.GET("/users/verify", middleware.AttachUserToContext(userService), userHandler.VerifyToken)
		// Get a user by their ID
		apiProtected.GET("/users/me", middleware.AttachUserToContext(userService), userHandler.GetUserByID)
		// Get a user's settings
		apiProtected.GET("/users/me/settings", middleware.AttachUserToContext(userService), userHandler.GetUserSettings)

		// Recipe-related routes

		// Get a single recipe by its ID (any authenticated user may view any
		// recipe; kept out of the public group to prevent anonymous
		// sequential-ID enumeration)
		apiProtected.GET("/recipes/:recipe_id", middleware.AttachUserToContext(userService), recipeHandler.GetRecipe)
		// List the authenticated user's recipes
		apiProtected.GET("/recipes", middleware.AttachUserToContext(userService), recipeHandler.ListRecipes)
		// Regenerate a recipe in place based on a previous recipe and the user's chat
		apiProtected.PUT("/recipes/:recipe_id/chat", middleware.AttachUserToContext(userService), recipeHandler.RegenerateRecipe)

		apiProtected.POST("/recipes/:recipe_id/fork", middleware.AttachUserToContext(userService), recipeHandler.GenerateRecipeWithFork)

		// Recipe import routes
		apiProtected.POST("/recipes/import/url", middleware.AttachUserToContext(userService), importHandler.ImportFromURL)
		apiProtected.POST("/recipes/import/photo", middleware.AttachUserToContext(userService), importHandler.ImportFromPhoto)
		apiProtected.POST("/recipes/import/files", middleware.AttachUserToContext(userService), importHandler.ImportFromFiles)
		apiProtected.POST("/recipes/import/voice", middleware.AttachUserToContext(userService), importHandler.ImportFromVoice)
		apiProtected.POST("/recipes/import/video", middleware.AttachUserToContext(userService), importHandler.ImportFromVideo)
		apiProtected.GET("/recipes/import/video/:id", middleware.AttachUserToContext(userService), importHandler.GetVideoImportStatus)
		apiProtected.POST("/recipes/import/text", middleware.AttachUserToContext(userService), importHandler.ImportFromText)
		apiProtected.POST("/recipes/import/manual", middleware.AttachUserToContext(userService), importHandler.ImportManual)
		apiProtected.POST("/recipes/import/canonical", middleware.AttachUserToContext(userService), importHandler.ImportFromCanonical)

		// Recipe preview route (cheap extraction for pre-import preview)
		apiProtected.POST("/recipes/preview/url", middleware.AttachUserToContext(userService), importHandler.PreviewFromURL)

		// Recipe tree/branching routes
		treeService := service.NewRecipeTreeService(cfg, recipeRepo)
		treeService.EmbedProvider = embedProvider
		treeService.VectorRepo = vectorRepo
		treeHandler := handlers.NewRecipeTreeHandler(treeService)

		apiProtected.GET("/recipes/:recipe_id/tree", middleware.AttachUserToContext(userService), treeHandler.GetTree)
		apiProtected.POST("/recipes/:recipe_id/branch", middleware.AttachUserToContext(userService), treeHandler.CreateBranch)
		apiProtected.PUT("/recipes/:recipe_id/tree/active/:node_id", middleware.AttachUserToContext(userService), treeHandler.SetActiveNode)
	}

	// Family-related routes setup
	familyRepo := repository.NewFamilyRepository(database)
	familyService := service.NewFamilyService(cfg, familyRepo, mainTextProvider)
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
	allergenService := service.NewAllergenService(cfg, allergenRepo, familyRepo, recipeRepo, mainTextProvider, subService)
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
	searchCacheRepo := repository.NewSearchCacheRepository(database)
	searchService := service.NewSearchService(cfg, searchProvider, subService, searchCacheRepo)
	searchService.EmbedProvider = embedProvider
	searchService.StartBackgroundTasks()

	// Backfill missing recipe/canonical embeddings in the background
	service.StartEmbeddingBackfill(vectorRepo, embedProvider)

	// Multi-recipe resolution (detection happens on click via preview, not search)
	multiRegistry := service.NewMultiRecipeRegistry()
	multiResolver := service.NewMultiRecipeResolver(multiRegistry, importService)
	importHandler.MultiResolver = multiResolver

	// Proactive cache warming — extract+cache search results before the user
	// taps, so taps are instant and the (user-agnostic) cache pays off for the
	// next searcher.
	warmService := service.NewWarmService(importService, multiResolver, cfg.EnvVars.RecipeWarmingConcurrency, cfg.EnvVars.RecipeWarmingDailyLimit)

	searchHandler := &handlers.SearchHandler{Service: searchService, MultiResolver: multiResolver, WarmService: warmService}
	apiProtected.GET("/recipes/search", middleware.AttachUserToContext(userService), searchHandler.SearchRecipes)
	apiProtected.GET("/recipes/search/resolve/:multi_id", middleware.AttachUserToContext(userService), searchHandler.ResolveMultiRecipe)
	apiProtected.POST("/recipes/search/check-multi", middleware.AttachUserToContext(userService), searchHandler.CheckMultiRecipe)
	apiProtected.POST("/recipes/search/warm", middleware.AttachUserToContext(userService), searchHandler.WarmRecipes)

	// Recipe finder — a guided agent that finds REAL recipes by driving search +
	// a single cheap ranking call (light tier), streamed over SSE. Gated by the
	// same "search" limit as the search endpoint. Ships dark (no caller yet).
	// Saved finder-run history (ungated) — also the finder's server-side auto-save sink.
	finderSessionRepo := repository.NewFinderSessionRepository(database)
	finderSessionService := service.NewFinderSessionService(finderSessionRepo)
	finderSessionHandler := handlers.NewFinderSessionHandler(finderSessionService)

	finderService := service.NewRecipeFinderService(cfg, searchService, familyRepo, warmService, previewProvider)
	// Agentic digging expands ranker-flagged collections via the multi-recipe
	// resolver; auto-save records each completed first-page run for history.
	finderService.MultiResolver = multiResolver
	finderService.Sessions = finderSessionService
	finderHandler := &handlers.FinderHandler{Service: finderService, SubService: subService}
	apiProtected.POST("/recipes/find", middleware.AttachUserToContext(userService), finderHandler.FindRecipes)
	apiProtected.GET("/recipes/finder/sessions", middleware.AttachUserToContext(userService), finderSessionHandler.ListSessions)
	apiProtected.GET("/recipes/finder/sessions/:session_id", middleware.AttachUserToContext(userService), finderSessionHandler.GetSession)
	apiProtected.DELETE("/recipes/finder/sessions/:session_id", middleware.AttachUserToContext(userService), finderSessionHandler.DeleteSession)

	// Vector similarity routes
	similarityHandler := handlers.NewSimilarityHandler(vectorRepo, embedProvider, recipeService)
	apiProtected.GET("/recipes/similar/:recipe_id", middleware.AttachUserToContext(userService), similarityHandler.FindSimilar)

	// Subscription routes
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
	importService.SpeechProvider = speechProvider
	// Voice cooking assistant (intent classification + cooking Q&A) runs on the
	// cheap/fast light tier, not the flagship model.
	voiceService := service.NewVoiceService(cfg, previewProvider, speechProvider)
	cookingHandler := ws.NewCookingHandler(hub, cfg.EnvVars.JwtSecretKey, voiceService, recipeRepo)
	r.GET("/v1/ws/cook/:recipe_id", cookingHandler.HandleCookingSession)

	return r
}
