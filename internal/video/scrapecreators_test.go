package video

import (
	"context"
	"encoding/json"
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

func TestFetchVideo_Instagram(t *testing.T) {
	// The caption deliberately contains a RAW newline byte (between "rice" and
	// "with") — invalid per JSON spec and rejected by encoding/json — to prove
	// the client's control-char sanitizer makes real Instagram responses parse.
	const postFixture = `{"success":true,"data":{"xdt_shortcode_media":{"__typename":"XDTGraphVideo","id":"3890383696589050992","shortcode":"DX9a1wjKVRw","is_video":true,"video_url":"https://scontent.cdninstagram.com/v.mp4","video_duration":22.547,"edge_media_to_caption":{"edges":[{"node":{"text":"Thai mango sticky rice
with creamy coconut sauce #recipe"}}]}}}}`
	const transcriptFixture = `{"success":true,"transcripts":["Soak the rice then steam it with coconut milk."]}`

	c := NewScrapeCreatorsClient("k")
	var gotPostURL, gotTranscriptURL string
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		body := postFixture
		if strings.Contains(req.URL.Path, "/v2/instagram/media/transcript") {
			gotTranscriptURL = req.URL.String()
			body = transcriptFixture
		} else {
			gotPostURL = req.URL.String()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}

	m, err := c.FetchVideo(context.Background(), "https://www.instagram.com/reel/DX9a1wjKVRw/")
	if err != nil {
		t.Fatalf("FetchVideo error: %v", err)
	}
	if !strings.Contains(gotPostURL, "/v1/instagram/post") {
		t.Errorf("post URL = %q, want /v1/instagram/post", gotPostURL)
	}
	if !strings.Contains(gotTranscriptURL, "/v2/instagram/media/transcript") {
		t.Errorf("transcript URL = %q, want the transcript endpoint", gotTranscriptURL)
	}
	if m.Platform != PlatformInstagram {
		t.Errorf("platform = %q, want instagram", m.Platform)
	}
	if m.VideoID != "DX9a1wjKVRw" {
		t.Errorf("video id = %q, want the shortcode", m.VideoID)
	}
	if !strings.Contains(m.Caption, "Thai mango sticky rice") || !strings.Contains(m.Caption, "creamy coconut sauce") {
		t.Errorf("caption did not survive sanitization: %q", m.Caption)
	}
	if m.MediaURL != "https://scontent.cdninstagram.com/v.mp4" {
		t.Errorf("media url = %q", m.MediaURL)
	}
	if m.DurationMS != 22547 { // 22.547s rounded to ms
		t.Errorf("duration = %d ms, want 22547", m.DurationMS)
	}
	if m.Transcript == "" {
		t.Error("expected the best-effort transcript to be populated")
	}
}

func TestFetchVideo_Instagram_NotAVideo(t *testing.T) {
	const fixture = `{"success":true,"data":{"xdt_shortcode_media":{"__typename":"XDTGraphImage","shortcode":"ABC123","is_video":false}}}`
	c := NewScrapeCreatorsClient("k")
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}
	if _, err := c.FetchVideo(context.Background(), "https://www.instagram.com/p/ABC123/"); err == nil {
		t.Fatal("expected an error for a non-video (image) post")
	}
}

func TestFetchVideo_Facebook(t *testing.T) {
	const postFixture = `{"success":true,"post_id":"1417081470454978","description":"Civico - Focaccia & Pesto","video":{"id":"1680614736552635","sd_url":"https://video.fbcdn.net/sd.mp4","hd_url":"https://video.fbcdn.net/hd.mp4","length_in_second":109.859,"captions_url":null}}`
	const transcriptFixture = `{"success":true,"transcript":"Mix the flour, water, and yeast, then let it rise."}`

	c := NewScrapeCreatorsClient("k")
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		body := postFixture
		if strings.Contains(req.URL.Path, "/v1/facebook/post/transcript") {
			body = transcriptFixture
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}

	m, err := c.FetchVideo(context.Background(), "https://www.facebook.com/reel/1680614736552635")
	if err != nil {
		t.Fatalf("FetchVideo error: %v", err)
	}
	if m.Platform != PlatformFacebook {
		t.Errorf("platform = %q, want facebook", m.Platform)
	}
	if m.VideoID != "1417081470454978" {
		t.Errorf("video id = %q, want the post_id", m.VideoID)
	}
	if m.Caption != "Civico - Focaccia & Pesto" {
		t.Errorf("caption = %q", m.Caption)
	}
	if m.MediaURL != "https://video.fbcdn.net/sd.mp4" {
		t.Errorf("media url = %q, want the sd_url", m.MediaURL)
	}
	if m.DurationMS != 109859 { // 109.859s → ms
		t.Errorf("duration = %d ms, want 109859", m.DurationMS)
	}
	if m.Transcript == "" {
		t.Error("expected the best-effort transcript to be populated")
	}
}

func TestFetchVideo_Facebook_FallsBackToHD(t *testing.T) {
	const fixture = `{"success":true,"post_id":"1","description":"x","video":{"hd_url":"https://video.fbcdn.net/hd.mp4","length_in_second":12.0}}`
	c := NewScrapeCreatorsClient("k")
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}
	m, err := c.FetchVideo(context.Background(), "https://www.facebook.com/reel/1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m.MediaURL != "https://video.fbcdn.net/hd.mp4" {
		t.Errorf("media url = %q, want hd_url fallback", m.MediaURL)
	}
}

func TestFetchVideo_Facebook_NotAVideo(t *testing.T) {
	const fixture = `{"success":true,"post_id":"1","description":"a photo post","video":{}}`
	c := NewScrapeCreatorsClient("k")
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}
	if _, err := c.FetchVideo(context.Background(), "https://www.facebook.com/reel/1"); err == nil {
		t.Fatal("expected an error for a post with no downloadable video")
	}
}

func TestFetchVideo_YouTube(t *testing.T) {
	const fixture = `{"success":true,"id":"b5-8lr_F8dM","title":"Honey Garlic Chicken","description":"5 ingredients: chicken, honey, garlic, soy sauce, butter. Ready in 20 minutes.","durationMs":32000,"durationFormatted":"00:00:32"}`
	c := NewScrapeCreatorsClient("k")
	var gotURL string
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}

	m, err := c.FetchVideo(context.Background(), "https://www.youtube.com/watch?v=b5-8lr_F8dM")
	if err != nil {
		t.Fatalf("FetchVideo error: %v", err)
	}
	if !strings.Contains(gotURL, "/v1/youtube/video") {
		t.Errorf("request URL = %q", gotURL)
	}
	if m.Platform != PlatformYouTube {
		t.Errorf("platform = %q, want youtube", m.Platform)
	}
	if m.VideoID != "b5-8lr_F8dM" {
		t.Errorf("video id = %q", m.VideoID)
	}
	if !strings.Contains(m.Caption, "Honey Garlic Chicken") ||
		!strings.Contains(m.Caption, "5 ingredients") {
		t.Errorf("caption should combine title + description: %q", m.Caption)
	}
	if m.MediaURL != "" {
		t.Errorf("media url = %q, want empty (text path)", m.MediaURL)
	}
	if m.DurationMS != 32000 {
		t.Errorf("duration = %d, want 32000", m.DurationMS)
	}
}

func TestFetchVideo_Pinterest(t *testing.T) {
	const fixture = `{"success":true,"entityId":"1124351863225567517","title":"Cowboy Butter Chicken Linguine","description":"Tender chicken tossed in a rich, buttery garlic sauce over linguine.","closeupDescription":null}`
	c := NewScrapeCreatorsClient("k")
	c.httpDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}

	m, err := c.FetchVideo(context.Background(), "https://www.pinterest.com/pin/1124351863225567517/")
	if err != nil {
		t.Fatalf("FetchVideo error: %v", err)
	}
	if m.Platform != PlatformPinterest {
		t.Errorf("platform = %q, want pinterest", m.Platform)
	}
	if m.VideoID != "1124351863225567517" {
		t.Errorf("video id = %q, want the entityId", m.VideoID)
	}
	if !strings.Contains(m.Caption, "Cowboy Butter Chicken Linguine") ||
		!strings.Contains(m.Caption, "buttery garlic sauce") {
		t.Errorf("caption should combine title + description: %q", m.Caption)
	}
	if m.MediaURL != "" {
		t.Errorf("media url = %q, want empty (text path)", m.MediaURL)
	}
}

func TestSanitizeJSONControlChars(t *testing.T) {
	// Raw newline + tab inside a string value → must become valid, parseable JSON.
	raw := []byte("{\"text\":\"a\nb\tc\",\"keep\":\"x\\ny\",\"n\":7}")
	var v struct {
		Text string `json:"text"`
		Keep string `json:"keep"`
		N    int    `json:"n"`
	}
	if err := json.Unmarshal(sanitizeJSONControlChars(raw), &v); err != nil {
		t.Fatalf("sanitized JSON should parse, got: %v", err)
	}
	if v.Text != "a\nb\tc" {
		t.Errorf("text = %q, want the control chars decoded back", v.Text)
	}
	if v.Keep != "x\ny" { // already-escaped \n must be left intact
		t.Errorf("keep = %q, want already-escaped sequence preserved", v.Keep)
	}
	if v.N != 7 {
		t.Errorf("n = %d, want 7", v.N)
	}

	// Clean JSON (control chars only as structural whitespace) is unchanged.
	clean := []byte("{\n  \"a\": 1\n}")
	if got := sanitizeJSONControlChars(clean); string(got) != string(clean) {
		t.Errorf("clean JSON was modified:\n got %q\nwant %q", got, clean)
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
