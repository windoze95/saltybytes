// Package video handles importing recipes from social/video links by acquiring
// the video's caption, transcript, and downloadable media via the ScrapeCreators
// API, then (in later layers) sampling frames and extracting the recipe.
package video

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
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
	// Some providers (e.g. Instagram captions) emit raw control characters inside
	// JSON string values, which encoding/json rejects. Escape them defensively.
	return sanitizeJSONControlChars(body), nil
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
	case PlatformInstagram:
		return c.fetchInstagram(ctx, rawURL)
	case PlatformFacebook:
		return c.fetchFacebook(ctx, rawURL)
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

// instagramPostResponse is the subset of the /v1/instagram/post response we use.
type instagramPostResponse struct {
	Data struct {
		Media struct {
			Typename      string  `json:"__typename"`
			ID            string  `json:"id"`
			Shortcode     string  `json:"shortcode"`
			IsVideo       bool    `json:"is_video"`
			VideoURL      string  `json:"video_url"`
			VideoDuration float64 `json:"video_duration"` // seconds (float)
			Caption       struct {
				Edges []struct {
					Node struct {
						Text string `json:"text"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"edge_media_to_caption"`
		} `json:"xdt_shortcode_media"`
	} `json:"data"`
}

// instagramTranscriptResponse is the /v2/instagram/media/transcript response.
// transcripts entries may be null when no transcript is available.
type instagramTranscriptResponse struct {
	Transcripts []*string `json:"transcripts"`
}

func (c *ScrapeCreatorsClient) fetchInstagram(ctx context.Context, rawURL string) (*VideoMeta, error) {
	q := url.Values{}
	q.Set("url", rawURL)

	body, err := c.get(ctx, "/v1/instagram/post", q)
	if err != nil {
		return nil, err
	}
	var r instagramPostResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse instagram response: %w", err)
	}
	m := r.Data.Media
	if m.Shortcode == "" {
		return nil, fmt.Errorf("instagram response missing media detail")
	}
	if m.VideoURL == "" {
		return nil, fmt.Errorf("instagram post is not a downloadable video")
	}

	caption := ""
	if len(m.Caption.Edges) > 0 {
		caption = m.Caption.Edges[0].Node.Text
	}

	return &VideoMeta{
		Platform:   PlatformInstagram,
		VideoID:    m.Shortcode,
		Caption:    caption,
		Transcript: c.instagramTranscript(ctx, rawURL), // best-effort; often empty
		MediaURL:   m.VideoURL,
		DurationMS: int(math.Round(m.VideoDuration * 1000)),
	}, nil
}

// instagramTranscript fetches the spoken transcript for a reel. It is
// best-effort: any error or absent transcript yields an empty string, and the
// caller falls back to the caption plus sampled frames.
func (c *ScrapeCreatorsClient) instagramTranscript(ctx context.Context, rawURL string) string {
	q := url.Values{}
	q.Set("url", rawURL)
	body, err := c.get(ctx, "/v2/instagram/media/transcript", q)
	if err != nil {
		return ""
	}
	var r instagramTranscriptResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return ""
	}
	for _, t := range r.Transcripts {
		if t != nil && *t != "" {
			return *t
		}
	}
	return ""
}

// facebookPostResponse is the subset of the /v1/facebook/post response we use.
type facebookPostResponse struct {
	PostID      string `json:"post_id"`
	Description string `json:"description"`
	Video       struct {
		SDURL          string  `json:"sd_url"`
		HDURL          string  `json:"hd_url"`
		LengthInSecond float64 `json:"length_in_second"`
	} `json:"video"`
}

// facebookTranscriptResponse is the /v1/facebook/post/transcript response.
type facebookTranscriptResponse struct {
	Transcript *string `json:"transcript"`
}

func (c *ScrapeCreatorsClient) fetchFacebook(ctx context.Context, rawURL string) (*VideoMeta, error) {
	q := url.Values{}
	q.Set("url", rawURL)

	body, err := c.get(ctx, "/v1/facebook/post", q)
	if err != nil {
		return nil, err
	}
	var r facebookPostResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse facebook response: %w", err)
	}
	if r.PostID == "" {
		return nil, fmt.Errorf("facebook response missing post detail")
	}

	// Prefer the smaller SD stream — it downloads faster and stays under the
	// sampler's size cap, and frames are scaled down regardless.
	media := r.Video.SDURL
	if media == "" {
		media = r.Video.HDURL
	}
	if media == "" {
		return nil, fmt.Errorf("facebook post is not a downloadable video")
	}

	return &VideoMeta{
		Platform:   PlatformFacebook,
		VideoID:    r.PostID,
		Caption:    r.Description,
		Transcript: c.facebookTranscript(ctx, rawURL), // best-effort; often empty
		MediaURL:   media,
		DurationMS: int(math.Round(r.Video.LengthInSecond * 1000)),
	}, nil
}

// facebookTranscript fetches a reel's spoken transcript, best-effort.
func (c *ScrapeCreatorsClient) facebookTranscript(ctx context.Context, rawURL string) string {
	q := url.Values{}
	q.Set("url", rawURL)
	body, err := c.get(ctx, "/v1/facebook/post/transcript", q)
	if err != nil {
		return ""
	}
	var r facebookTranscriptResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return ""
	}
	if r.Transcript != nil {
		return *r.Transcript
	}
	return ""
}

// sanitizeJSONControlChars escapes raw control characters (U+0000–U+001F) that
// appear inside JSON string literals, which some providers emit (e.g. literal
// newlines in Instagram captions) and which encoding/json rejects per RFC 8259.
// Control characters outside of strings (structural whitespace) are left as-is,
// so well-formed JSON passes through unchanged.
func sanitizeJSONControlChars(b []byte) []byte {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(b))
	inStr := false
	escaped := false
	for i := 0; i < len(b); i++ {
		ch := b[i]
		if !inStr {
			out = append(out, ch)
			if ch == '"' {
				inStr = true
			}
			continue
		}
		if escaped {
			out = append(out, ch)
			escaped = false
			continue
		}
		switch {
		case ch == '\\':
			out = append(out, ch)
			escaped = true
		case ch == '"':
			out = append(out, ch)
			inStr = false
		case ch < 0x20:
			switch ch {
			case '\n':
				out = append(out, '\\', 'n')
			case '\r':
				out = append(out, '\\', 'r')
			case '\t':
				out = append(out, '\\', 't')
			default:
				out = append(out, '\\', 'u', '0', '0', hex[ch>>4], hex[ch&0xf])
			}
		default:
			out = append(out, ch)
		}
	}
	return out
}
