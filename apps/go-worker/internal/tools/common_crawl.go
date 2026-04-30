package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CommonCrawlHit struct {
	URL       string `json:"url"`
	Timestamp string `json:"timestamp"`
	Status    string `json:"status,omitempty"`
	MimeType  string `json:"mime,omitempty"`
	Length    string `json:"length,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Filename  string `json:"filename,omitempty"`  // WARC file in the public CC dataset
	Offset    string `json:"offset,omitempty"`
}

type CommonCrawlOutput struct {
	URL    string           `json:"url"`
	Index  string           `json:"index"`
	Hits   []CommonCrawlHit `json:"hits"`
	Count  int              `json:"count"`
	TookMs int64            `json:"tookMs"`
	Source string           `json:"source"`
}

// commonCrawlIndex is the latest stable Common Crawl index. Update periodically
// when a new CC release appears (~quarterly).
const commonCrawlIndex = "CC-MAIN-2026-13"

// CommonCrawlLookup queries Common Crawl's CDX index for URLs matching a
// pattern. Free, no key. CC indexes the public web at WARC granularity —
// useful for "what existed at this URL pattern at any point" without the
// Wayback Machine's gaps.
func CommonCrawlLookup(ctx context.Context, input map[string]any) (*CommonCrawlOutput, error) {
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("input.url required (URL or URL pattern, e.g. \"*.example.com\")")
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	idx := commonCrawlIndex
	if v, ok := input["index"].(string); ok && v != "" {
		idx = v
	}

	start := time.Now()
	endpoint := fmt.Sprintf(
		"https://index.commoncrawl.org/%s-index?url=%s&output=json&limit=%d",
		url.PathEscape(idx), url.QueryEscape(rawURL), limit,
	)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("commoncrawl fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		// CDX returns 404 with a "no captures found" body for empty results — not an error.
		return &CommonCrawlOutput{URL: rawURL, Index: idx, Source: "commoncrawl.org", TookMs: time.Since(start).Milliseconds()}, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("commoncrawl %d", resp.StatusCode)
	}

	out := &CommonCrawlOutput{URL: rawURL, Index: idx, Source: "commoncrawl.org"}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var row struct {
			URL       string `json:"url"`
			Timestamp string `json:"timestamp"`
			Status    string `json:"status"`
			MimeType  string `json:"mime"`
			Length    string `json:"length"`
			Digest    string `json:"digest"`
			Filename  string `json:"filename"`
			Offset    string `json:"offset"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		out.Hits = append(out.Hits, CommonCrawlHit{
			URL: row.URL, Timestamp: row.Timestamp, Status: row.Status,
			MimeType: row.MimeType, Length: row.Length, Digest: row.Digest,
			Filename: row.Filename, Offset: row.Offset,
		})
	}
	out.Count = len(out.Hits)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
