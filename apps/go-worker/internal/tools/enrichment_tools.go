package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// =============================================================================
// firecrawl_scrape — REQUIRES FIRECRAWL_API_KEY (https://firecrawl.dev)
// =============================================================================

type FirecrawlOutput struct {
	URL          string                 `json:"url"`
	Markdown     string                 `json:"markdown"`
	HTML         string                 `json:"html,omitempty"`
	HTMLLen      int                    `json:"html_bytes,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	StatusCode   int                    `json:"status_code,omitempty"`
	Source       string                 `json:"source"`
	TookMs       int64                  `json:"tookMs"`
}

// FirecrawlScrape uses Firecrawl's JS-rendering scrape API. Returns clean
// Markdown by default — much more LLM-friendly than raw HTML. Handles
// JavaScript-rendered SPAs that stealth_http_fetch cannot.
func FirecrawlScrape(ctx context.Context, input map[string]any) (*FirecrawlOutput, error) {
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("input.url required")
	}
	key := os.Getenv("FIRECRAWL_API_KEY")
	if key == "" {
		return nil, errors.New("FIRECRAWL_API_KEY env var required (Firecrawl — https://firecrawl.dev)")
	}
	includeHTML := false
	if v, ok := input["include_html"].(bool); ok {
		includeHTML = v
	}
	onlyMainContent := true
	if v, ok := input["only_main_content"].(bool); ok {
		onlyMainContent = v
	}
	mobile := false
	if v, ok := input["mobile"].(bool); ok {
		mobile = v
	}
	formats := []string{"markdown"}
	if includeHTML {
		formats = append(formats, "html")
	}

	start := time.Now()
	body, _ := json.Marshal(map[string]interface{}{
		"url":             rawURL,
		"formats":         formats,
		"onlyMainContent": onlyMainContent,
		"mobile":          mobile,
	})
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.firecrawl.dev/v1/scrape", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("firecrawl %d: %s", resp.StatusCode, truncate(string(rb), 200))
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string                 `json:"markdown"`
			HTML     string                 `json:"html"`
			Metadata map[string]interface{} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, fmt.Errorf("firecrawl parse: %w", err)
	}
	statusCode := 0
	if sc, ok := parsed.Data.Metadata["statusCode"].(float64); ok {
		statusCode = int(sc)
	}
	out := &FirecrawlOutput{
		URL: rawURL, Markdown: parsed.Data.Markdown,
		HTMLLen: len(parsed.Data.HTML), Metadata: parsed.Data.Metadata,
		StatusCode: statusCode, Source: "firecrawl.dev",
		TookMs: time.Since(start).Milliseconds(),
	}
	if includeHTML {
		out.HTML = parsed.Data.HTML
	}
	return out, nil
}

// =============================================================================
// firecrawl_search — search-and-scrape combo (REQUIRES FIRECRAWL_API_KEY)
// =============================================================================

type FirecrawlSearchOutput struct {
	Query   string                   `json:"query"`
	Count   int                      `json:"count"`
	Results []map[string]interface{} `json:"results"`
	Source  string                   `json:"source"`
	TookMs  int64                    `json:"tookMs"`
}

func FirecrawlSearch(ctx context.Context, input map[string]any) (*FirecrawlSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	key := os.Getenv("FIRECRAWL_API_KEY")
	if key == "" {
		return nil, errors.New("FIRECRAWL_API_KEY env var required")
	}
	limit := 10
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	scrape := false
	if v, ok := input["scrape_results"].(bool); ok {
		scrape = v
	}

	start := time.Now()
	payload := map[string]interface{}{
		"query": q,
		"limit": limit,
	}
	if scrape {
		payload["scrapeOptions"] = map[string]interface{}{"formats": []string{"markdown"}}
	}
	body, _ := json.Marshal(payload)
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.firecrawl.dev/v1/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl-search: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("firecrawl-search %d: %s", resp.StatusCode, truncate(string(rb), 200))
	}
	var parsed struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, fmt.Errorf("firecrawl-search parse: %w", err)
	}
	return &FirecrawlSearchOutput{
		Query: q, Count: len(parsed.Data), Results: parsed.Data,
		Source: "firecrawl.dev/search",
		TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// diffbot_extract — REQUIRES DIFFBOT_API_KEY
// =============================================================================

type DiffbotExtractOutput struct {
	URL     string                   `json:"url"`
	Type    string                   `json:"type,omitempty"`
	Objects []map[string]interface{} `json:"objects,omitempty"`
	Source  string                   `json:"source"`
	TookMs  int64                    `json:"tookMs"`
}

// DiffbotExtract uses Diffbot's Analyze API: feeds it a URL, returns
// structured entities (Article, Person, Company, Product, Image, etc.) with
// the page's parsed fields. The most reliable URL → structured-data extractor
// publicly available.
func DiffbotExtract(ctx context.Context, input map[string]any) (*DiffbotExtractOutput, error) {
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("input.url required")
	}
	key := os.Getenv("DIFFBOT_API_KEY")
	if key == "" {
		return nil, errors.New("DIFFBOT_API_KEY env var required (https://www.diffbot.com)")
	}
	start := time.Now()
	endpoint := fmt.Sprintf("https://api.diffbot.com/v3/analyze?token=%s&url=%s",
		url.QueryEscape(key), url.QueryEscape(rawURL))
	body, err := httpGetJSON(ctx, endpoint, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("diffbot: %w", err)
	}
	var parsed struct {
		Type    string                   `json:"type"`
		Objects []map[string]interface{} `json:"objects"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("diffbot parse: %w", err)
	}
	return &DiffbotExtractOutput{
		URL: rawURL, Type: parsed.Type, Objects: parsed.Objects,
		Source: "api.diffbot.com/v3/analyze",
		TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// diffbot_kg_query — Diffbot Knowledge Graph (REQUIRES DIFFBOT_API_KEY)
// =============================================================================

type DiffbotKGOutput struct {
	Query   string                   `json:"query"`
	Type    string                   `json:"type,omitempty"`
	Total   int                      `json:"total"`
	Hits    int                      `json:"hits"`
	Entities []map[string]interface{} `json:"entities"`
	Source  string                   `json:"source"`
	TookMs  int64                    `json:"tookMs"`
}

// DiffbotKGQuery runs a Diffbot DQL query against the Knowledge Graph (~10B
// entities: people, companies, articles, products). DQL examples:
//
//	type:Person name:"Linus Torvalds"
//	type:Organization name:"Anthropic"
//	type:Person employments.{employer.name:"Vurvey Labs"}
//	type:Person allOriginalImages.{url:contains:"linkedin"}
//
// This is the highest-precision people/company enrichment in the catalog.
func DiffbotKGQuery(ctx context.Context, input map[string]any) (*DiffbotKGOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (Diffbot DQL, e.g. `type:Person name:\"Linus Torvalds\"`)")
	}
	key := os.Getenv("DIFFBOT_API_KEY")
	if key == "" {
		return nil, errors.New("DIFFBOT_API_KEY env var required")
	}
	size := 10
	if v, ok := input["size"].(float64); ok && v > 0 {
		size = int(v)
	}
	entityType, _ := input["type"].(string)
	if entityType != "" && !strings.Contains(strings.ToLower(q), "type:") {
		q = fmt.Sprintf("type:%s %s", entityType, q)
	}

	start := time.Now()
	endpoint := fmt.Sprintf("https://kg.diffbot.com/kg/v3/dql?type=query&token=%s&query=%s&size=%d&format=json",
		url.QueryEscape(key), url.QueryEscape(q), size)
	body, err := httpGetJSON(ctx, endpoint, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("diffbot kg: %w", err)
	}
	// Diffbot KG response shape: data[].entity is the actual entity record
	// (wrapped alongside score + entity_ctx). Unwrap.
	var parsed struct {
		Hits  int `json:"hits"`
		Total int `json:"total"`
		Data  []struct {
			Score  float64                `json:"score"`
			Entity map[string]interface{} `json:"entity"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("diffbot kg parse: %w", err)
	}
	entities := make([]map[string]interface{}, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.Entity != nil {
			d.Entity["_diffbot_score"] = d.Score
			entities = append(entities, d.Entity)
		}
	}
	return &DiffbotKGOutput{
		Query: q, Type: entityType, Total: parsed.Total, Hits: parsed.Hits,
		Entities: entities, Source: "kg.diffbot.com/dql",
		TookMs: time.Since(start).Milliseconds(),
	}, nil
}
