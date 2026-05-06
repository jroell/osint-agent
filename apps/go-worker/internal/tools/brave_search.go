package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// BraveSearch wraps the Brave Search API (api.search.brave.com).
// Free tier (2k queries/mo); REQUIRES `BRAVE_SEARCH_API_KEY`.
//
// Independent index from Google/Bing/DuckDuckGo — useful for triangulating
// search results that those engines may rank-suppress or omit. Critical
// for OSINT in jurisdictions where Google rank-suppresses content.
//
// Modes:
//   - "web"    : web search
//   - "news"   : news search with date filtering
//   - "images" : image search
//   - "videos" : video search
//
// Knowledge-graph: emits typed entities (kind: "search_result") with stable URLs.

type BraveResult struct {
	Title          string `json:"title"`
	URL            string `json:"url"`
	Description    string `json:"description,omitempty"`
	Age            string `json:"age,omitempty"`
	Language       string `json:"language,omitempty"`
	FamilyFriendly *bool  `json:"family_friendly,omitempty"`
	Source         string `json:"source,omitempty"` // news source name
	Thumbnail      string `json:"thumbnail,omitempty"`
}

type BraveEntity struct {
	Kind        string         `json:"kind"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type BraveSearchOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query"`
	Returned          int            `json:"returned"`
	Results           []BraveResult  `json:"results,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []BraveEntity  `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func BraveSearch(ctx context.Context, input map[string]any) (*BraveSearchOutput, error) {
	apiKey := os.Getenv("BRAVE_SEARCH_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("BRAVE_SEARCH_API_KEY not set; free tier at api.search.brave.com")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "web"
	}
	out := &BraveSearchOutput{Mode: mode, Source: "api.search.brave.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	q, _ := input["query"].(string)
	if q == "" {
		return nil, fmt.Errorf("input.query required")
	}
	out.Query = q

	var endpoint string
	switch mode {
	case "web":
		endpoint = "/res/v1/web/search"
	case "news":
		endpoint = "/res/v1/news/search"
	case "images":
		endpoint = "/res/v1/images/search"
	case "videos":
		endpoint = "/res/v1/videos/search"
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	params := url.Values{"q": []string{q}, "count": []string{"20"}}
	if cc, ok := input["country"].(string); ok && cc != "" {
		params.Set("country", cc)
	}
	if lang, ok := input["lang"].(string); ok && lang != "" {
		params.Set("search_lang", lang)
	}
	if freshness, ok := input["freshness"].(string); ok && freshness != "" {
		params.Set("freshness", freshness) // pd, pw, pm, py
	}

	u := "https://api.search.brave.com" + endpoint + "?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("brave: unauthorized — check BRAVE_SEARCH_API_KEY")
	}
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("brave: rate limited (429); free tier is 1 q/s")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("brave HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}

	var resp2 map[string]any
	if err := json.Unmarshal(body, &resp2); err != nil {
		return nil, fmt.Errorf("brave decode: %w", err)
	}
	out.Detail = resp2

	// Each search-type wraps results differently; pick the right key.
	var results []any
	switch mode {
	case "web":
		if w, ok := resp2["web"].(map[string]any); ok {
			results, _ = w["results"].([]any)
		}
	case "news":
		results, _ = resp2["results"].([]any)
		if len(results) == 0 {
			if n, ok := resp2["news"].(map[string]any); ok {
				results, _ = n["results"].([]any)
			}
		}
	case "images", "videos":
		results, _ = resp2["results"].([]any)
	}
	for _, r := range results {
		rec, _ := r.(map[string]any)
		if rec == nil {
			continue
		}
		br := BraveResult{
			Title:       gtString(rec, "title"),
			URL:         gtString(rec, "url"),
			Description: gtString(rec, "description"),
			Age:         gtString(rec, "age"),
			Language:    gtString(rec, "language"),
			Source:      gtString(rec, "source"),
		}
		if mc, ok := rec["meta_url"].(map[string]any); ok {
			if br.Source == "" {
				br.Source = gtString(mc, "hostname")
			}
		}
		if t, ok := rec["thumbnail"].(map[string]any); ok {
			br.Thumbnail = gtString(t, "src")
		}
		if v, ok := rec["family_friendly"].(bool); ok {
			br.FamilyFriendly = &v
		}
		out.Results = append(out.Results, br)
	}

	out.Returned = len(out.Results)
	out.Entities = braveBuildEntities(out)
	out.HighlightFindings = braveBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func braveBuildEntities(o *BraveSearchOutput) []BraveEntity {
	ents := []BraveEntity{}
	for _, r := range o.Results {
		ents = append(ents, BraveEntity{
			Kind: "search_result", Name: r.Title, URL: r.URL, Date: r.Age,
			Description: r.Description,
			Attributes: map[string]any{
				"source":          r.Source,
				"language":        r.Language,
				"thumbnail":       r.Thumbnail,
				"family_friendly": r.FamilyFriendly,
				"search_engine":   "brave",
				"search_mode":     o.Mode,
			},
		})
	}
	return ents
}

func braveBuildHighlights(o *BraveSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ brave %s: %d results for %q", o.Mode, o.Returned, o.Query)}
	for i, r := range o.Results {
		if i >= 8 {
			break
		}
		src := ""
		if r.Source != "" {
			src = " [" + r.Source + "]"
		}
		hi = append(hi, fmt.Sprintf("  • %s%s — %s", hfTruncate(r.Title, 80), src, r.URL))
	}
	return hi
}
