package service

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// trackingParams are query parameters stripped during URL normalization.
var trackingParams = map[string]bool{
	"utm_source":   true,
	"utm_medium":   true,
	"utm_campaign": true,
	"utm_term":     true,
	"utm_content":  true,
	"fbclid":       true,
	"gclid":        true,
	"ref":          true,
}

// NormalizeURL normalizes a URL for canonical deduplication.
// It lowercases scheme+host, removes fragments and tracking params,
// strips trailing slashes (except root "/"), and sorts remaining query params.
func NormalizeURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("URL must have scheme and host")
	}

	// Lowercase scheme and host
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	// Remove fragment
	u.Fragment = ""

	// Strip tracking params and sort remaining
	q := u.Query()
	for param := range trackingParams {
		q.Del(param)
	}

	// Sort query params for determinism
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sortedQuery url.Values
	if len(keys) > 0 {
		sortedQuery = make(url.Values)
		for _, k := range keys {
			sortedQuery[k] = q[k]
		}
	}
	u.RawQuery = sortedQuery.Encode()

	// Remove trailing slash (except root path "/")
	if len(u.Path) > 1 {
		u.Path = strings.TrimRight(u.Path, "/")
	}

	return u.String(), nil
}
