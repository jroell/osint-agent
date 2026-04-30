package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type WaybackSnapshot struct {
	Timestamp  string `json:"timestamp"`   // YYYYMMDDhhmmss
	OriginalURL string `json:"original"`
	MimeType   string `json:"mimetype,omitempty"`
	StatusCode string `json:"status_code,omitempty"`
	Digest     string `json:"digest,omitempty"`
	ArchiveURL string `json:"archive_url"`
}

type WaybackOutput struct {
	URL       string            `json:"url"`
	Snapshots []WaybackSnapshot `json:"snapshots"`
	Count     int               `json:"count"`
	First     string            `json:"first_seen,omitempty"`
	Last      string            `json:"last_seen,omitempty"`
	TookMs    int64             `json:"tookMs"`
	Source    string            `json:"source"`
}

// WaybackHistory queries the Internet Archive's CDX API for archived snapshots
// of a URL. Free, no API key. Returns up to `limit` deduped snapshots ordered
// by capture time. Excellent for "what did this page look like before X?"
// or "what subdomains/paths existed historically?"
func WaybackHistory(ctx context.Context, input map[string]any) (*WaybackOutput, error) {
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("input.url required")
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	matchType := "exact" // exact|prefix|host|domain
	if mt, ok := input["match_type"].(string); ok && mt != "" {
		matchType = mt
	}

	start := time.Now()
	endpoint := fmt.Sprintf(
		"https://web.archive.org/cdx/search/cdx?url=%s&output=json&limit=%d&matchType=%s&fl=timestamp,original,mimetype,statuscode,digest",
		url.QueryEscape(rawURL), limit, url.QueryEscape(matchType),
	)
	// CDX latency varies wildly (1s to 60s); allow ample headroom.
	body, err := httpGetJSON(ctx, endpoint, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("wayback fetch: %w", err)
	}
	// CDX returns [["timestamp","original","mimetype","statuscode","digest"], ...rows...]
	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("wayback parse: %w", err)
	}
	out := &WaybackOutput{
		URL:    rawURL,
		Source: "web.archive.org/cdx",
		TookMs: time.Since(start).Milliseconds(),
	}
	if len(rows) <= 1 {
		return out, nil
	}
	for _, r := range rows[1:] {
		if len(r) < 2 {
			continue
		}
		s := WaybackSnapshot{Timestamp: r[0], OriginalURL: r[1]}
		if len(r) > 2 {
			s.MimeType = r[2]
		}
		if len(r) > 3 {
			s.StatusCode = r[3]
		}
		if len(r) > 4 {
			s.Digest = r[4]
		}
		s.ArchiveURL = "https://web.archive.org/web/" + s.Timestamp + "/" + s.OriginalURL
		out.Snapshots = append(out.Snapshots, s)
	}
	out.Count = len(out.Snapshots)
	if out.Count > 0 {
		out.First = out.Snapshots[0].Timestamp
		out.Last = out.Snapshots[len(out.Snapshots)-1].Timestamp
	}
	return out, nil
}
