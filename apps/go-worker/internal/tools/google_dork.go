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
	"regexp"
	"strings"
	"time"
)

type DorkResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	Engine  string `json:"engine"`
}

type DorkOutput struct {
	Query             string       `json:"query"`
	EnginesQueried    []string     `json:"engines_queried"`
	EnginesResponded  []string     `json:"engines_responded"`
	Results           []DorkResult `json:"results"`
	Count             int          `json:"count"`
	UniqueDomains     int          `json:"unique_domains"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
	Note              string       `json:"note,omitempty"`
}

// GoogleDorkSearch runs a (typically site-restricted) search across multiple
// keyless HTML search engines IN PARALLEL and aggregates results.
//
// Engines: DuckDuckGo HTML, Mojeek, Bing HTML — all keyless, all scrape the
// HTML response page. If 1-2 are blocked/slow, the others fill in. This is
// the canonical agentic OSINT pattern for queries like:
//   site:linkedin.com/in/ "Paul Graham"
//   site:github.com "@vurvey.com"
//   intitle:"index of" "passwords.txt"
//   site:twitter.com OR site:x.com "<target>"
//
// Note: most engines aggressively rate-limit scraped HTML; rotation across
// 3 engines is what makes this reliable enough to be useful day-to-day.
func GoogleDorkSearch(ctx context.Context, input map[string]any) (*DorkOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (full search query, including any 'site:' or 'intitle:' operators)")
	}
	limit := 30
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	engines := []string{"duckduckgo", "mojeek", "bing"}
	// Tavily is API-keyed but vastly more reliable than scraped HTML — auto-add
	// it to the engine rotation when the user has TAVILY_API_KEY exported.
	if os.Getenv("TAVILY_API_KEY") != "" {
		engines = append(engines, "tavily")
	}
	if v, ok := input["engines"].([]interface{}); ok && len(v) > 0 {
		engines = nil
		for _, e := range v {
			if s, ok := e.(string); ok {
				engines = append(engines, s)
			}
		}
	}

	start := time.Now()
	type engResult struct {
		engine  string
		results []DorkResult
		err     error
	}
	resCh := make(chan engResult, len(engines))
	for _, e := range engines {
		go func(engine string) {
			r, err := dorkSearchEngine(ctx, engine, q, limit)
			resCh <- engResult{engine, r, err}
		}(e)
	}

	out := &DorkOutput{
		Query:          q,
		EnginesQueried: engines,
		Source:         "duckduckgo+mojeek+bing (keyless rotation)",
	}
	seen := map[string]struct{}{}
	for range engines {
		r := <-resCh
		if r.err != nil || len(r.results) == 0 {
			continue
		}
		out.EnginesResponded = append(out.EnginesResponded, r.engine)
		for _, hit := range r.results {
			// Dedupe by URL.
			if _, ok := seen[hit.URL]; ok {
				continue
			}
			seen[hit.URL] = struct{}{}
			out.Results = append(out.Results, hit)
		}
	}
	out.Count = len(out.Results)
	domains := map[string]struct{}{}
	for _, r := range out.Results {
		if u, err := url.Parse(r.URL); err == nil {
			domains[u.Host] = struct{}{}
		}
	}
	out.UniqueDomains = len(domains)
	if len(out.EnginesResponded) < len(engines) {
		out.Note = fmt.Sprintf("%d/%d engines responded (the rest rate-limited, redirected, or returned 0 results)",
			len(out.EnginesResponded), len(engines))
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func dorkSearchEngine(ctx context.Context, engine, q string, limit int) ([]DorkResult, error) {
	switch engine {
	case "duckduckgo":
		return dorkDuckDuckGo(ctx, q, limit)
	case "mojeek":
		return dorkMojeek(ctx, q, limit)
	case "bing":
		return dorkBing(ctx, q, limit)
	case "tavily":
		return dorkTavily(ctx, q, limit)
	default:
		return nil, fmt.Errorf("unknown engine: %s", engine)
	}
}

// dorkTavily uses Tavily's AI-search API. Reliable, JSON-clean, designed for
// agentic use cases. Requires TAVILY_API_KEY env var.
func dorkTavily(ctx context.Context, q string, limit int) ([]DorkResult, error) {
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("TAVILY_API_KEY not set")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"api_key":     key,
		"query":       q,
		"search_depth": "basic",
		"max_results": limit,
	})
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tavily %d", resp.StatusCode)
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var parsed struct {
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	out := make([]DorkResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		out = append(out, DorkResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: truncate(r.Content, 280),
			Engine:  "tavily",
		})
	}
	return out, nil
}

// DuckDuckGo HTML interface. Stable, no JS required.
var ddgResultRe = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>.*?<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)

func dorkDuckDuckGo(ctx context.Context, q string, limit int) ([]DorkResult, error) {
	body, err := dorkHTTPGet(ctx, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(q),
		"https://html.duckduckgo.com/")
	if err != nil {
		return nil, err
	}
	matches := ddgResultRe.FindAllSubmatch(body, limit)
	out := make([]DorkResult, 0, len(matches))
	for _, m := range matches {
		raw := string(m[1])
		// DDG wraps result URLs in `//duckduckgo.com/l/?uddg=<encoded-url>...`. Unwrap.
		if u, err := url.Parse(raw); err == nil && strings.Contains(u.Path, "/l/") {
			if real := u.Query().Get("uddg"); real != "" {
				raw = real
			}
		}
		out = append(out, DorkResult{
			Title:   stripHTML(string(m[2]), 200),
			URL:     raw,
			Snippet: stripHTML(string(m[3]), 280),
			Engine:  "duckduckgo",
		})
	}
	return out, nil
}

// Mojeek — keyless, EU-based, doesn't aggressively rate-limit.
var mojeekResultRe = regexp.MustCompile(`(?s)<a class="ob"[^>]*href="([^"]+)"[^>]*>(.*?)</a>.*?<p class="s">(.*?)</p>`)

func dorkMojeek(ctx context.Context, q string, limit int) ([]DorkResult, error) {
	body, err := dorkHTTPGet(ctx, "https://www.mojeek.com/search?q="+url.QueryEscape(q),
		"https://www.mojeek.com/")
	if err != nil {
		return nil, err
	}
	matches := mojeekResultRe.FindAllSubmatch(body, limit)
	out := make([]DorkResult, 0, len(matches))
	for _, m := range matches {
		out = append(out, DorkResult{
			Title:   stripHTML(string(m[2]), 200),
			URL:     string(m[1]),
			Snippet: stripHTML(string(m[3]), 280),
			Engine:  "mojeek",
		})
	}
	return out, nil
}

// Bing HTML.
var bingResultRe = regexp.MustCompile(`(?s)<li class="b_algo"[^>]*>.*?<h2><a[^>]+href="([^"]+)"[^>]*>(.*?)</a></h2>.*?<div class="b_caption"[^>]*>.*?<p[^>]*>(.*?)</p>`)

func dorkBing(ctx context.Context, q string, limit int) ([]DorkResult, error) {
	body, err := dorkHTTPGet(ctx, "https://www.bing.com/search?q="+url.QueryEscape(q),
		"https://www.bing.com/")
	if err != nil {
		return nil, err
	}
	matches := bingResultRe.FindAllSubmatch(body, limit)
	out := make([]DorkResult, 0, len(matches))
	for _, m := range matches {
		out = append(out, DorkResult{
			Title:   stripHTML(string(m[2]), 200),
			URL:     string(m[1]),
			Snippet: stripHTML(string(m[3]), 280),
			Engine:  "bing",
		})
	}
	return out, nil
}

func dorkHTTPGet(ctx context.Context, target, referer string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Use a real-browser UA — the keyless HTML interfaces aggressively block
	// non-browser UAs.
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", referer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
