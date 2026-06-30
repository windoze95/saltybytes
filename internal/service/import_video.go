package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/s3"
	"github.com/windoze95/saltybytes-api/internal/video"
	"go.uber.org/zap"
)

// VideoFetcher resolves a supported social/video URL to its caption, transcript,
// and a downloadable media URL. Satisfied by *video.ScrapeCreatorsClient.
type VideoFetcher interface {
	FetchVideo(ctx context.Context, rawURL string) (*video.VideoMeta, error)
}

// VideoFrameSampler downloads a video and returns representative JPEG frames.
// Satisfied by *video.FrameSampler.
type VideoFrameSampler interface {
	Sample(ctx context.Context, mediaURL string, durationMS int) ([][]byte, error)
}

const (
	// sonnetCostPerFrame approximates one standard-resolution frame on Sonnet
	// (~1600 input tokens × $3/MTok). Frames dominate the cost of a fresh video
	// import, so this is the master figure behind the daily budget.
	sonnetCostPerFrame = 0.0048
	// videoTextOverheadUSD is a conservative flat allowance for the transcript/
	// caption input tokens plus the extraction's output tokens.
	videoTextOverheadUSD = 0.02
	// videoProcessTimeout bounds the whole async pipeline for one fresh import.
	videoProcessTimeout = 5 * time.Minute
)

// estimateVideoCost approximates the metered spend of a fresh video extraction.
// It is intentionally an over-estimate so the daily-budget kill switch trips
// early rather than late. Cache hits cost nothing and are recorded as 0.
func estimateVideoCost(numFrames int) float64 {
	return float64(numFrames)*sonnetCostPerFrame + videoTextOverheadUSD
}

// videoImportConfigured reports whether the video-import dependencies are wired.
// Video import stays dark until a ScrapeCreators key is configured at startup.
func (s *ImportService) videoImportConfigured() bool {
	return s.VideoRepo != nil && s.VideoFetcher != nil && s.VideoFrameSampler != nil
}

// StartVideoImport validates a social/video URL, creates an async import job,
// and kicks off background processing. It returns the queued job immediately;
// callers poll GetVideoImport for completion. The heavy work (fetch, frame
// sampling, multimodal extraction) and all cost controls run in the goroutine.
func (s *ImportService) StartVideoImport(ctx context.Context, rawURL string, user *models.User) (*models.VideoImport, error) {
	if !s.videoImportConfigured() {
		return nil, &ExtractionError{Code: "video_unavailable", Message: "video import is not available"}
	}

	platform, ok := video.DetectPlatform(rawURL)
	if !ok {
		return nil, &ExtractionError{Code: "unsupported_platform", Message: "unsupported video link; supported platforms are TikTok, Instagram, YouTube, Facebook, and Pinterest"}
	}

	job := &models.VideoImport{
		UserID:    user.ID,
		SourceURL: rawURL,
		Platform:  string(platform),
		Status:    models.VideoImportQueued,
	}
	if err := s.VideoRepo.CreateImport(job); err != nil {
		return nil, fmt.Errorf("failed to create video import job: %w", err)
	}

	go s.processVideoImport(job.ID, rawURL, user)

	return job, nil
}

// GetVideoImport returns the current state of an async video-import job.
func (s *ImportService) GetVideoImport(id uint) (*models.VideoImport, error) {
	if s.VideoRepo == nil {
		return nil, fmt.Errorf("video import is not available")
	}
	return s.VideoRepo.GetImportByID(id)
}

// processVideoImport runs the full pipeline for one job. It never uses the
// request context (which is cancelled once the HTTP handler returns); it owns
// its own timeout. Failures are recorded on the job, not returned.
func (s *ImportService) processVideoImport(jobID uint, rawURL string, user *models.User) {
	ctx, cancel := context.WithTimeout(context.Background(), videoProcessTimeout)
	defer cancel()

	log := logger.Get().With(zap.Uint("video_import_id", jobID), zap.Uint("user_id", user.ID))

	job, err := s.VideoRepo.GetImportByID(jobID)
	if err != nil {
		log.Error("video import job vanished before processing", zap.Error(err))
		return
	}

	fail := func(code, msg string, cause error) {
		log.Warn("video import failed", zap.String("code", code), zap.Error(cause))
		job.Status = models.VideoImportFailed
		job.Error = msg
		if uErr := s.VideoRepo.UpdateImport(job); uErr != nil {
			log.Error("failed to persist video import failure", zap.Error(uErr))
		}
		// The quota was consumed on acceptance; refund it since the user got no
		// recipe and the failure is on our side.
		if s.SubService != nil {
			if rErr := s.SubService.DecrementUsage(user.ID, "video_import"); rErr != nil {
				log.Warn("failed to refund video import quota", zap.Error(rErr))
			}
		}
	}

	job.Status = models.VideoImportProcessing
	if err := s.VideoRepo.UpdateImport(job); err != nil {
		log.Error("failed to mark video import processing", zap.Error(err))
	}

	// 1. Resolve caption, transcript, and a downloadable media URL.
	meta, err := s.VideoFetcher.FetchVideo(ctx, rawURL)
	if err != nil {
		fail("fetch_failed", "could not retrieve the video", err)
		return
	}
	videoKey := videoCacheKey(meta)
	job.VideoKey = videoKey

	// 2. Cache: a viral video is extracted once and served to everyone after,
	//    at no metered cost — the primary cost control.
	if entry, cacheErr := s.VideoRepo.GetCacheByVideoKey(videoKey); cacheErr == nil && entry != nil {
		log.Info("video extraction cache hit", zap.String("video_key", videoKey))
		go s.VideoRepo.IncrementCacheHit(entry.ID)
		def := entry.RecipeData
		if def.SourceURL == "" {
			def.SourceURL = rawURL
		}
		_, recipeID, createErr := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportVideo, rawURL, entry.ThumbnailURL, nil, nil, entry.PromptVersion)
		if createErr != nil {
			fail("save_failed", "could not save the recipe", createErr)
			return
		}
		job.Status = models.VideoImportDone
		job.RecipeID = &recipeID
		job.CacheHit = true
		job.CostUSD = 0
		if err := s.VideoRepo.UpdateImport(job); err != nil {
			log.Error("failed to persist completed cache-hit import", zap.Error(err))
		}
		return
	}

	// 3. Fresh extraction is expensive — enforce the global daily budget first.
	if budget := s.Cfg.EnvVars.VideoImportDailyBudgetUSD; budget > 0 {
		since := time.Now().UTC().Truncate(24 * time.Hour)
		if spent, sErr := s.VideoRepo.SumImportCostSince(since); sErr == nil && spent >= budget {
			fail("at_capacity", "video import is temporarily at capacity; please try again later", fmt.Errorf("daily budget $%.2f reached (spent $%.2f)", budget, spent))
			return
		}
	}

	// 4. Extract the recipe. Video platforms give a downloadable media URL and
	//    go through frame sampling + multimodal extraction; text platforms
	//    (YouTube, Pinterest) have no video to sample, so they extract from the
	//    title/description/transcript instead.
	contextText := buildVideoContext(meta)
	unitSystem := user.Personalization.UnitSystemText()

	var chosen *ai.RecipeResult
	var frames [][]byte

	if meta.MediaURL != "" {
		// The media URL comes from a third-party scraper; treat it as untrusted
		// and block private/internal addresses before the sampler fetches it.
		if err := ValidateExternalURL(meta.MediaURL); err != nil {
			fail("no_media", "the video could not be downloaded", err)
			return
		}
		// Scene-change frames catch on-screen text and flashy cuts; even
		// sampling backs it up.
		sampled, sErr := s.VideoFrameSampler.Sample(ctx, meta.MediaURL, meta.DurationMS)
		if sErr != nil {
			fail("sampling_failed", "could not process the video", sErr)
			return
		}
		frames = sampled

		media := make([]ai.MediaInput, 0, len(frames))
		for _, f := range frames {
			media = append(media, ai.MediaInput{Data: f, Kind: ai.MediaImage})
		}
		results, eErr := s.VisionProvider.ExtractRecipesFromMedia(ctx, media, contextText, unitSystem, user.Personalization.Requirements)
		if eErr != nil || len(results) == 0 {
			fail("extraction_failed", "could not find a recipe in this video", eErr)
			return
		}
		chosen = results[0] // a single video yields a single recipe
	} else {
		if strings.TrimSpace(contextText) == "" {
			fail("no_content", "this link did not contain a recipe", fmt.Errorf("no media or text for %s", videoKey))
			return
		}
		if s.TextProvider == nil {
			fail("extraction_failed", "could not find a recipe in this video", fmt.Errorf("no text provider configured"))
			return
		}
		r, eErr := s.TextProvider.ExtractRecipeFromText(ctx, contextText, unitSystem)
		if eErr != nil || r == nil {
			fail("extraction_failed", "could not find a recipe in this video", eErr)
			return
		}
		chosen = r
	}

	def := recipeResultToRecipeDef(chosen)
	def.SourceURL = rawURL
	ensureUnitSystem(&def)

	// Use a representative frame as the recipe thumbnail (best-effort; the text
	// path has no frames, so this is a no-op there).
	thumbnailURL := s.uploadVideoThumbnail(ctx, frames, videoKey)

	_, recipeID, createErr := s.createImportedRecipe(ctx, &def, user, models.RecipeTypeImportVideo, rawURL, thumbnailURL, nil, chosen.Hashtags, chosen.PromptVersion)
	if createErr != nil {
		fail("save_failed", "could not save the recipe", createErr)
		return
	}

	// 6. Cache the extraction (and its thumbnail) so the next importer of this
	//    video pays nothing and reuses the same hero image.
	now := time.Now()
	cacheEntry := &models.VideoExtractionCache{
		VideoKey:       videoKey,
		Platform:       string(meta.Platform),
		OriginalURL:    rawURL,
		RecipeData:     def,
		ThumbnailURL:   thumbnailURL,
		FetchedAt:      now,
		LastAccessedAt: now,
		PromptVersion:  chosen.PromptVersion,
	}
	if err := s.VideoRepo.UpsertCache(cacheEntry); err != nil {
		log.Warn("failed to cache video extraction", zap.Error(err))
	}

	job.Status = models.VideoImportDone
	job.RecipeID = &recipeID
	job.CostUSD = estimateVideoCost(len(frames))
	job.CacheHit = false
	if err := s.VideoRepo.UpdateImport(job); err != nil {
		log.Error("failed to persist completed video import", zap.Error(err))
	}
	log.Info("video import complete", zap.Int("frames", len(frames)), zap.Float64("cost_usd", job.CostUSD))
}

// uploadVideoThumbnail stores a representative frame (the middle one — past any
// intro, before any outro) as the recipe's hero image and returns its URL.
// Best-effort: returns "" on any failure so the import still succeeds.
func (s *ImportService) uploadVideoThumbnail(ctx context.Context, frames [][]byte, videoKey string) string {
	if len(frames) == 0 {
		return ""
	}
	frame := frames[len(frames)/2]
	if s.ThumbnailUploader != nil {
		url, err := s.ThumbnailUploader(ctx, frame, videoKey)
		if err != nil {
			return ""
		}
		return url
	}
	key := "recipes/video_thumbnails/" + strings.ReplaceAll(videoKey, ":", "_") + ".jpg"
	url, err := s3.UploadRecipeImageToS3(ctx, s.Cfg, frame, key, "image/jpeg")
	if err != nil {
		logger.Get().Warn("failed to upload video thumbnail", zap.String("video_key", videoKey), zap.Error(err))
		return ""
	}
	return url
}

// videoCacheKey is the per-video cache key: "<platform>:<video_id>".
func videoCacheKey(meta *video.VideoMeta) string {
	return string(meta.Platform) + ":" + meta.VideoID
}

// buildVideoContext combines the caption and transcript into a single labeled
// text block for the extractor so the model can weigh each source.
func buildVideoContext(meta *video.VideoMeta) string {
	var b strings.Builder
	if c := strings.TrimSpace(meta.Caption); c != "" {
		b.WriteString("Caption: ")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	if t := strings.TrimSpace(meta.Transcript); t != "" {
		b.WriteString("Transcript: ")
		b.WriteString(t)
	}
	return strings.TrimSpace(b.String())
}
