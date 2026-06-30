// Package video handles importing recipes from social/video links by acquiring
// the video's caption, transcript, and downloadable media via the ScrapeCreators
// API, then (in later layers) sampling frames and extracting the recipe.
package video

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Platform identifies a supported source platform.
type Platform string

// Supported platforms.
const (
	PlatformTikTok    Platform = "tiktok"
	PlatformInstagram Platform = "instagram"
	PlatformYouTube   Platform = "youtube"
	PlatformFacebook  Platform = "facebook"
	PlatformPinterest Platform = "pinterest"
)

// DetectPlatform classifies a URL by host. ok is false for unsupported hosts.
func DetectPlatform(rawURL string) (Platform, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	h := strings.ToLower(u.Hostname())
	switch {
	case strings.Contains(h, "tiktok.com"):
		return PlatformTikTok, true
	case strings.Contains(h, "instagram.com"):
		return PlatformInstagram, true
	case strings.Contains(h, "youtube.com"), strings.Contains(h, "youtu.be"):
		return PlatformYouTube, true
	case strings.Contains(h, "facebook.com"), strings.Contains(h, "fb.watch"), strings.Contains(h, "fb.com"):
		return PlatformFacebook, true
	case strings.Contains(h, "pinterest.com"), strings.Contains(h, "pin.it"):
		return PlatformPinterest, true
	}
	return "", false
}

// VideoMeta is the normalized result of a ScrapeCreators lookup.
type VideoMeta struct {
	Platform   Platform
	VideoID    string
	Caption    string
	Transcript string // may be empty; falls back to audio transcription downstream
	MediaURL   string // downloadable video URL, no-watermark preferred
	DurationMS int
}

const scrapeCreatorsBaseURL = "https://api.scrapecreators.com"

// ScrapeCreatorsClient fetches video metadata, media URLs, and transcripts.
type ScrapeCreatorsClient struct {
	apiKey  string
	baseURL string
	// httpDo is a test seam; nil uses http.DefaultClient.
	httpDo func(req *http.Request) (*http.Response, error)
}

// NewScrapeCreatorsClient creates a client for the given API key.
func NewScrapeCreatorsClient(apiKey string) *ScrapeCreatorsClient {
	return &ScrapeCreatorsClient{apiKey: apiKey, baseURL: scrapeCreatorsBaseURL}
}

func (c *ScrapeCreatorsClient) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.apiKey)

	doer := c.httpDo
	if doer == nil {
		doer = http.DefaultClient.Do
	}
	resp, err := doer(req)
	if err != nil {
		return nil, fmt.Errorf("scrapecreators request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read scrapecreators response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrapecreators %s returned status %d", path, resp.StatusCode)
	}
	return body, nil
}

// FetchVideo resolves a supported video URL to normalized metadata (caption,
// transcript, downloadable media URL).
func (c *ScrapeCreatorsClient) FetchVideo(ctx context.Context, rawURL string) (*VideoMeta, error) {
	platform, ok := DetectPlatform(rawURL)
	if !ok {
		return nil, fmt.Errorf("unsupported video platform for url")
	}
	switch platform {
	case PlatformTikTok:
		return c.fetchTikTok(ctx, rawURL)
	default:
		return nil, fmt.Errorf("platform %q not yet supported", platform)
	}
}

// tiktokVideoResponse is the subset of the /v2/tiktok/video response we use.
type tiktokVideoResponse struct {
	Success     bool   `json:"success"`
	Transcript  string `json:"transcript"`
	AwemeDetail struct {
		AwemeID string `json:"aweme_id"`
		Desc    string `json:"desc"`
		Video   struct {
			Duration                int  `json:"duration"`
			HasWatermark            bool `json:"has_watermark"`
			DownloadNoWatermarkAddr struct {
				URLList []string `json:"url_list"`
			} `json:"download_no_watermark_addr"`
			PlayAddr struct {
				URLList []string `json:"url_list"`
			} `json:"play_addr"`
		} `json:"video"`
	} `json:"aweme_detail"`
}

func (c *ScrapeCreatorsClient) fetchTikTok(ctx context.Context, rawURL string) (*VideoMeta, error) {
	q := url.Values{}
	q.Set("url", rawURL)
	q.Set("get_transcript", "true")

	body, err := c.get(ctx, "/v2/tiktok/video", q)
	if err != nil {
		return nil, err
	}
	var r tiktokVideoResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse tiktok response: %w", err)
	}
	if r.AwemeDetail.AwemeID == "" {
		return nil, fmt.Errorf("tiktok response missing video detail")
	}

	media := firstNonEmpty(r.AwemeDetail.Video.DownloadNoWatermarkAddr.URLList)
	if media == "" {
		media = firstNonEmpty(r.AwemeDetail.Video.PlayAddr.URLList)
	}

	return &VideoMeta{
		Platform:   PlatformTikTok,
		VideoID:    r.AwemeDetail.AwemeID,
		Caption:    r.AwemeDetail.Desc,
		Transcript: r.Transcript,
		MediaURL:   media,
		DurationMS: r.AwemeDetail.Video.Duration,
	}, nil
}

func firstNonEmpty(list []string) string {
	for _, s := range list {
		if s != "" {
			return s
		}
	}
	return ""
}
