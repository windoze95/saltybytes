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

// VideoFrameSampler downloads a video and returns representative JPEG frames,
// and (for the last-resort Whisper escalation) its audio track. Satisfied by
// *video.FrameSampler.
type VideoFrameSampler interface {
	Sample(ctx context.Context, mediaURL string, durationMS int) ([][]byte, error)
	ExtractAudio(ctx context.Context, mediaURL string) ([]byte, error)
	// DownloadVideo fetches the media into memory (native path), rejecting clips
	// larger than maxBytes. ThumbnailFromVideo cuts a single hero frame from those
	// bytes since the native path samples no frames.
	DownloadVideo(ctx context.Context, mediaURL string, maxBytes int64) ([]byte, error)
	ThumbnailFromVideo(ctx context.Context, videoData []byte) ([]byte, error)
}

const (
	// sonnetCostPerFrame approximates one standard-resolution frame on Sonnet
	// (~1600 input tokens × $3/MTok). Frames dominate the cost of a fresh video
	// import, so this is the master figure behind the daily budget.
	sonnetCostPerFrame = 0.0048
	// videoTextOverheadUSD is a conservative flat allowance for the transcript/
	// caption input tokens plus the extraction's output tokens.
	videoTextOverheadUSD = 0.02
	// whisperCostPerMin is OpenAI Whisper's per-minute transcription price.
	whisperCostPerMin = 0.006
	// videoProcessTimeout bounds the whole async pipeline for one fresh import.
	videoProcessTimeout = 5 * time.Minute
	// maxNativeVideoBytes caps clips sent to the native video provider (inlined
	// into the request); larger clips fall back to frame sampling.
	maxNativeVideoBytes = 18 << 20 // 18 MiB
)

// estimateVideoCost approximates the metered spend of a fresh video extraction.
// It is intentionally an over-estimate so the daily-budget kill switch trips
// early rather than late. When the Whisper fallback fired, the frames are
// re-sent on a second extraction and audio is transcribed, so both are added.
// Cache hits cost nothing and are recorded as 0.
func estimateVideoCost(numFrames int, usedWhisper bool, durationMS int) float64 {
	cost := float64(numFrames)*sonnetCostPerFrame + videoTextOverheadUSD
	if usedWhisper {
		cost += float64(numFrames)*sonnetCostPerFrame + videoTextOverheadUSD // re-extraction
		cost += (float64(durationMS) / 60000.0) * whisperCostPerMin
	}
	return cost
}

// recipeNeedsMoreContext reports whether a frames+caption extraction produced a
// plausible recipe (a titled dish with at least one ingredient — so a recipe
// clearly exists) that is nonetheless too thin to trust (few ingredients or no
// steps). It gates the Whisper fallback: true means "a recipe is there but we
// need more detail"; false means either a solid recipe or no recipe at all
// (don't waste Whisper on a video that likely has no recipe to fetch).
func recipeNeedsMoreContext(r *ai.RecipeResult) bool {
	if r == nil || r.Title == "" || len(r.Ingredients) == 0 {
		return false
	}
	return len(r.Ingredients) < 4 || len(r.Instructions) < 2
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
	var nativeVideoBytes []byte
	usedWhisper := false

	if meta.MediaURL != "" {
		// The media URL comes from a third-party scraper; treat it as untrusted
		// and block private/internal addresses before the sampler fetches it.
		if err := ValidateExternalURL(meta.MediaURL); err != nil {
			fail("no_media", "the video could not be downloaded", err)
			return
		}

		// Native path: ingest the whole clip (video + audio) in one pass — far
		// cheaper than frames on the vision model, and it reads the narration so
		// the Whisper fallback isn't needed. Any failure or an oversized clip
		// falls through to frame sampling below.
		if s.VideoProvider != nil {
			if videoData, dlErr := s.VideoFrameSampler.DownloadVideo(ctx, meta.MediaURL, maxNativeVideoBytes); dlErr != nil {
				log.Info("native video unavailable for this clip, using frames", zap.Error(dlErr))
			} else if results, nErr := s.VideoProvider.ExtractRecipesFromVideo(ctx, videoData, "video/mp4", contextText, unitSystem, user.Personalization.Requirements); nErr != nil || len(results) == 0 {
				log.Info("native video extraction failed, using frames", zap.Error(nErr))
			} else {
				chosen = results[0]
				nativeVideoBytes = videoData
				log.Info("video import used native gemini video extraction", zap.String("video_key", videoKey))
			}
		}

		// Frame sampling path — the default, and the fallback when native is off
		// or unavailable for this clip. Scene-change frames catch on-screen text
		// and flashy cuts; even sampling backs it up.
		if chosen == nil {
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

			// Last-resort Whisper: only when the frames+caption pass yielded a
			// plausible-but-thin recipe AND the scraper gave no transcript to work
			// with. The spoken audio is then the only untapped source of detail.
			// Gated this tightly so Whisper never runs on the common (good) path
			// nor on videos that likely have no recipe at all. (The native path
			// already reads the audio, so it skips this.)
			if s.SpeechProvider != nil && meta.Transcript == "" && recipeNeedsMoreContext(chosen) {
				if audio, aErr := s.VideoFrameSampler.ExtractAudio(ctx, meta.MediaURL); aErr == nil && len(audio) > 0 {
					if transcript, tErr := s.SpeechProvider.TranscribeAudio(ctx, audio, "m4a"); tErr == nil && strings.TrimSpace(transcript) != "" {
						meta.Transcript = transcript
						if results2, e2 := s.VisionProvider.ExtractRecipesFromMedia(ctx, media, buildVideoContext(meta), unitSystem, user.Personalization.Requirements); e2 == nil && len(results2) > 0 {
							chosen = results2[0]
							usedWhisper = true
							log.Info("video import used the whisper fallback for extra context", zap.String("video_key", videoKey))
						}
					}
				}
			}
		}
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

	// Hero image (best-effort): the middle sampled frame, or — on the native
	// path, which samples no frames — a single frame cut from the video bytes.
	// The text path has neither, so this is a no-op there.
	var thumbFrame []byte
	if len(frames) > 0 {
		thumbFrame = frames[len(frames)/2]
	} else if len(nativeVideoBytes) > 0 {
		if tf, tErr := s.VideoFrameSampler.ThumbnailFromVideo(ctx, nativeVideoBytes); tErr == nil {
			thumbFrame = tf
		}
	}
	thumbnailURL := s.uploadVideoThumbnail(ctx, thumbFrame, videoKey)

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
	job.CostUSD = estimateVideoCost(len(frames), usedWhisper, meta.DurationMS)
	job.CacheHit = false
	if err := s.VideoRepo.UpdateImport(job); err != nil {
		log.Error("failed to persist completed video import", zap.Error(err))
	}
	log.Info("video import complete", zap.Int("frames", len(frames)), zap.Float64("cost_usd", job.CostUSD))
}

// uploadVideoThumbnail stores the given representative frame as the recipe's
// hero image and returns its URL. Best-effort: returns "" on empty input or any
// failure so the import still succeeds.
func (s *ImportService) uploadVideoThumbnail(ctx context.Context, frame []byte, videoKey string) string {
	if len(frame) == 0 {
		return ""
	}
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
