package video

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const (
	// DefaultMaxFrames bounds frames sampled per video — the master cost dial
	// (~$0.0048/frame on Sonnet). Lower it to cut spend, raise it for coverage.
	DefaultMaxFrames = 30
	// minSceneFrames: if scene detection yields fewer than this (e.g. a static
	// talking-head clip with no cuts), fall back to even time sampling.
	minSceneFrames = 6
	// maxVideoBytes caps the downloaded source video.
	maxVideoBytes = 120 * 1024 * 1024
	// frameWidth scales frames down; keeps Claude image input at standard
	// resolution (avoids the ~3x high-res token cost) while staying legible.
	frameWidth = 640
)

// FrameSampler downloads a video and samples representative JPEG frames via
// ffmpeg — preferring scene changes (to catch quick on-screen text and flashy
// cuts), falling back to even time sampling. The downloaded source video is
// always deleted before returning (we do not retain source media).
type FrameSampler struct {
	MaxFrames int
	HTTP      *http.Client
	// runFFmpeg is a test seam; nil uses the real ffmpeg binary.
	runFFmpeg func(ctx context.Context, args []string) error
	// download is a test seam; nil downloads over HTTP.
	download func(ctx context.Context, mediaURL, dest string) error
}

// NewFrameSampler returns a sampler with sensible defaults.
func NewFrameSampler() *FrameSampler {
	return &FrameSampler{MaxFrames: DefaultMaxFrames, HTTP: &http.Client{Timeout: 90 * time.Second}}
}

func (s *FrameSampler) maxFrames() int {
	if s.MaxFrames > 0 {
		return s.MaxFrames
	}
	return DefaultMaxFrames
}

// Sample downloads mediaURL and returns up to MaxFrames JPEG frames.
func (s *FrameSampler) Sample(ctx context.Context, mediaURL string, durationMS int) ([][]byte, error) {
	dir, err := os.MkdirTemp("", "vidframes-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir) // removes the downloaded video and all extracted frames

	videoPath := filepath.Join(dir, "video.mp4")
	dl := s.download
	if dl == nil {
		dl = s.httpDownload
	}
	if err := dl(ctx, mediaURL, videoPath); err != nil {
		return nil, fmt.Errorf("download video: %w", err)
	}

	run := s.runFFmpeg
	if run == nil {
		run = realFFmpeg
	}
	mf := s.maxFrames()

	// Primary: scene-change frames.
	scenePattern := filepath.Join(dir, "scene_%04d.jpg")
	if err := run(ctx, sceneArgs(videoPath, scenePattern, mf)); err == nil {
		if frames := readFrames(dir, "scene_*.jpg", mf); len(frames) >= minSceneFrames {
			return frames, nil
		}
	}

	// Fallback: even time sampling across the whole duration.
	evenPattern := filepath.Join(dir, "even_%04d.jpg")
	if err := run(ctx, evenArgs(videoPath, evenPattern, durationMS, mf)); err != nil {
		return nil, fmt.Errorf("ffmpeg frame extraction: %w", err)
	}
	frames := readFrames(dir, "even_*.jpg", mf)
	if len(frames) == 0 {
		return nil, errors.New("no frames extracted from video")
	}
	return frames, nil
}

// computeFPS returns the sampling rate (frames/sec) for even sampling so that a
// video of the given duration yields about maxFrames frames, clamped to a sane
// range for very short or very long videos.
func computeFPS(durationMS, maxFrames int) float64 {
	fps := 0.5 // ~1 frame / 2s when duration is unknown
	if durSec := float64(durationMS) / 1000.0; durSec > 1 {
		fps = float64(maxFrames) / durSec
	}
	if fps > 2 {
		fps = 2 // never more than 2 fps
	}
	if fps < 0.1 {
		fps = 0.1 // at least 1 frame / 10s
	}
	return fps
}

func sceneArgs(inPath, outPattern string, maxFrames int) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-i", inPath,
		"-vf", fmt.Sprintf("select='gt(scene,0.2)',scale=%d:-1", frameWidth),
		"-vsync", "vfr", "-frames:v", strconv.Itoa(maxFrames), outPattern,
	}
}

func evenArgs(inPath, outPattern string, durationMS, maxFrames int) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-i", inPath,
		"-vf", fmt.Sprintf("fps=%.4f,scale=%d:-1", computeFPS(durationMS, maxFrames), frameWidth),
		"-frames:v", strconv.Itoa(maxFrames), outPattern,
	}
}

func readFrames(dir, glob string, cap int) [][]byte {
	matches, _ := filepath.Glob(filepath.Join(dir, glob))
	sort.Strings(matches)
	if len(matches) > cap {
		matches = matches[:cap]
	}
	frames := make([][]byte, 0, len(matches))
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err == nil && len(b) > 0 {
			frames = append(frames, b)
		}
	}
	return frames
}

func realFFmpeg(ctx context.Context, args []string) error {
	out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w (%s)", err, string(out))
	}
	return nil
}

func (s *FrameSampler) httpDownload(ctx context.Context, mediaURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return err
	}
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxVideoBytes))
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("downloaded an empty video")
	}
	return nil
}
