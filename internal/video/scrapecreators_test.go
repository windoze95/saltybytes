package video

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDetectPlatform(t *testing.T) {
	cases := []struct {
		url  string
		want Platform
		ok   bool
	}{
		{"https://www.tiktok.com/@chef/video/7499229683859426602", PlatformTikTok, true},
		{"https://vm.tiktok.com/ABC123/", PlatformTikTok, true},
		{"https://www.instagram.com/reel/Cabc123/", PlatformInstagram, true},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", PlatformYouTube, true},
		{"https://youtu.be/dQw4w9WgXcQ", PlatformYouTube, true},
		{"https://www.youtube.com/shorts/abc123", PlatformYouTube, true},
		{"https://www.facebook.com/reel/123", PlatformFacebook, true},
		{"https://fb.watch/xyz/", PlatformFacebook, true},
		{"https://www.pinterest.com/pin/12345/", PlatformPinterest, true},
		{"https://pin.it/abc", PlatformPinterest, true},
		{"https://example.com/recipe", "", false},
		{"not a url at all", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got, ok := DetectPlatform(tc.url)
			if ok != tc.ok || got != tc.want {
				t.Errorf("DetectPlatform(%q) = (%q, %v), want (%q, %v)", tc.url, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestFetchVideo_TikTok(t *testing.T) {
	const fixture = `{
		"success": true,
		"credits_remaining": 999,
		"transcript": "First mix the flour and eggs, then cook on a griddle.",
		"aweme_detail": {
			"aweme_id": "7499229683859426602",
			"desc": "easy fluffy pancakes #recipe #breakfast",
			"video": {
				"duration": 45000,
				"has_watermark": false,
				"download_no_watermark_addr": {"url_list": ["https://cdn.example/nowm.mp4"]},
				"play_addr": {"url_list": ["https://cdn.example/play.mp4"]}
			}
		}
	}`

	c := NewScrapeCreatorsClient("test-key")
	var gotKey, gotURL string
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		gotKey = req.Header.Get("x-api-key")
		gotURL = req.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}

	m, err := c.FetchVideo(context.Background(), "https://www.tiktok.com/@chef/video/7499229683859426602")
	if err != nil {
		t.Fatalf("FetchVideo error: %v", err)
	}
	if gotKey != "test-key" {
		t.Errorf("x-api-key header = %q, want test-key", gotKey)
	}
	if !strings.Contains(gotURL, "/v2/tiktok/video") || !strings.Contains(gotURL, "get_transcript=true") {
		t.Errorf("request URL = %q, want /v2/tiktok/video with get_transcript=true", gotURL)
	}
	if m.Platform != PlatformTikTok {
		t.Errorf("platform = %q, want tiktok", m.Platform)
	}
	if m.VideoID != "7499229683859426602" {
		t.Errorf("video id = %q", m.VideoID)
	}
	if m.Caption != "easy fluffy pancakes #recipe #breakfast" {
		t.Errorf("caption = %q", m.Caption)
	}
	if m.Transcript == "" {
		t.Error("expected a transcript")
	}
	if m.MediaURL != "https://cdn.example/nowm.mp4" {
		t.Errorf("media url = %q, want the no-watermark url", m.MediaURL)
	}
	if m.DurationMS != 45000 {
		t.Errorf("duration = %d, want 45000", m.DurationMS)
	}
}

func TestFetchVideo_TikTok_FallsBackToPlayAddr(t *testing.T) {
	const fixture = `{
		"aweme_detail": {
			"aweme_id": "1",
			"desc": "x",
			"video": {"has_watermark": false, "play_addr": {"url_list": ["https://cdn.example/play.mp4"]}}
		}
	}`
	c := NewScrapeCreatorsClient("k")
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}
	m, err := c.FetchVideo(context.Background(), "https://www.tiktok.com/@x/video/1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m.MediaURL != "https://cdn.example/play.mp4" {
		t.Errorf("media url = %q, want play_addr fallback", m.MediaURL)
	}
}

func TestFetchVideo_UnsupportedPlatform(t *testing.T) {
	c := NewScrapeCreatorsClient("k")
	if _, err := c.FetchVideo(context.Background(), "https://example.com/v/1"); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}

func TestFetchVideo_HTTPError(t *testing.T) {
	c := NewScrapeCreatorsClient("k")
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 402, Body: io.NopCloser(strings.NewReader("payment required"))}, nil
	}
	if _, err := c.FetchVideo(context.Background(), "https://www.tiktok.com/@x/video/1"); err == nil {
		t.Fatal("expected error on non-200 status")
	}
}
