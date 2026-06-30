package video

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeFPS(t *testing.T) {
	cases := []struct {
		durationMS, maxFrames int
		want                  float64
	}{
		{45000, 30, 30.0 / 45.0}, // ~0.667 fps
		{0, 30, 0.5},             // unknown duration → default
		{5000, 30, 2.0},          // short (30/5=6) → clamped to 2 fps max
		{600000, 30, 0.1},        // 10 min → clamped to 0.1 fps min
	}
	for _, tc := range cases {
		got := computeFPS(tc.durationMS, tc.maxFrames)
		if (got-tc.want) > 0.0001 || (tc.want-got) > 0.0001 {
			t.Errorf("computeFPS(%d,%d) = %.4f, want %.4f", tc.durationMS, tc.maxFrames, got, tc.want)
		}
	}
}

// fakeFFmpeg returns a runFFmpeg that writes `count` dummy JPEGs matching the
// output pattern's prefix (scene_ or even_) into the temp dir.
func fakeFFmpeg(t *testing.T, sceneCount, evenCount int) func(ctx context.Context, args []string) error {
	t.Helper()
	return func(ctx context.Context, args []string) error {
		outPattern := args[len(args)-1]
		dir := filepath.Dir(outPattern)
		base := filepath.Base(outPattern) // e.g. "scene_%04d.jpg"
		prefix := strings.SplitN(base, "_", 2)[0]
		n := evenCount
		if prefix == "scene" {
			n = sceneCount
		}
		for i := 1; i <= n; i++ {
			p := filepath.Join(dir, fmt.Sprintf("%s_%04d.jpg", prefix, i))
			if err := os.WriteFile(p, []byte{0xFF, 0xD8, 0xFF, byte(i)}, 0o600); err != nil {
				return err
			}
		}
		return nil
	}
}

func writeFakeVideo(ctx context.Context, _ string, dest string) error {
	return os.WriteFile(dest, []byte("fake video bytes"), 0o600)
}

func TestFrameSampler_UsesSceneFrames(t *testing.T) {
	s := NewFrameSampler()
	s.MaxFrames = 20
	s.download = writeFakeVideo
	s.runFFmpeg = fakeFFmpeg(t, 12, 99) // 12 scene frames (>= minSceneFrames) → used

	frames, err := s.Sample(context.Background(), "https://x/v.mp4", 45000)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 12 {
		t.Errorf("got %d frames, want 12 (scene path)", len(frames))
	}
}

func TestFrameSampler_FallsBackToEven(t *testing.T) {
	s := NewFrameSampler()
	s.MaxFrames = 20
	s.download = writeFakeVideo
	s.runFFmpeg = fakeFFmpeg(t, 2, 15) // only 2 scene frames (< min) → fall back to 15 even

	frames, err := s.Sample(context.Background(), "https://x/v.mp4", 45000)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 15 {
		t.Errorf("got %d frames, want 15 (even fallback)", len(frames))
	}
}

func TestFrameSampler_CapsFrames(t *testing.T) {
	s := NewFrameSampler()
	s.MaxFrames = 10
	s.download = writeFakeVideo
	s.runFFmpeg = fakeFFmpeg(t, 50, 50) // ffmpeg over-produces → capped to MaxFrames

	frames, err := s.Sample(context.Background(), "https://x/v.mp4", 45000)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 10 {
		t.Errorf("got %d frames, want 10 (capped)", len(frames))
	}
}

func TestFrameSampler_DownloadError(t *testing.T) {
	s := NewFrameSampler()
	s.download = func(ctx context.Context, url, dest string) error { return fmt.Errorf("boom") }
	s.runFFmpeg = fakeFFmpeg(t, 10, 10)
	if _, err := s.Sample(context.Background(), "https://x/v.mp4", 1000); err == nil {
		t.Fatal("expected error on download failure")
	}
}

func TestFrameSampler_NoFrames(t *testing.T) {
	s := NewFrameSampler()
	s.download = writeFakeVideo
	s.runFFmpeg = fakeFFmpeg(t, 0, 0) // nothing produced
	if _, err := s.Sample(context.Background(), "https://x/v.mp4", 1000); err == nil {
		t.Fatal("expected error when no frames extracted")
	}
}
