package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// FirecrawlMapOutput is the response.
type FirecrawlMapOutput struct {
	URL              string   `json:"url"`
	Search           string   `json:"search,omitempty"`
	TotalLinks       int      `json:"total_links"`
	Links            []string `json:"links"`
	UniqueSubdomains []string `json:"unique_subdomains,omitempty"`
	UniquePaths      []string `json:"top_path_prefixes,omitempty"`
	HighlightFindings []string `json:"highlight_findings"`
	Source           string   `json:"source"`
	TookMs           int64    `json:"tookMs"`
	Note             string   `json:"note,omitempty"`
}

// FirecrawlMap calls Firecrawl's /map endpoint to discover all URLs on a
// website in a single call. Free tier with FIRECRAWL_API_KEY.
//
// Why this matters for ER:
//   - Single-call site URL enumeration is a core OSINT primitive that
//     pairs naturally with subfinder (subdomain discovery), wayback_url_history
//     (temporal recon), js_endpoint_extract (API endpoints from JS),
//     swagger_openapi_finder (API schemas), and well_known_recon
//     (.well-known paths).
//   - Optional `search` parameter filters URLs by keyword — useful for
//     "find all login/admin/api/dashboard pages on this domain".
//   - Returns subdomain enumeration as a side effect — Vurvey's map
//     surfaced help.vurvey.com (separate help center subdomain).
//   - Aggregates: unique subdomains, top path prefixes (what sections
//     does the site have? /blog /careers /api etc.).
func FirecrawlMap(ctx context.Context, input map[string]any) (*FirecrawlMapOutput, error) {
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FIRECRAWL_API_KEY env var required")
	}
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("input.url required (e.g. 'https://example.com')")
	}
	search, _ := input["search"].(string)
	limit := 100
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 5000 {
		limit = int(v)
	}
	includeSubdomains := true
	if v, ok := input["include_subdomains"].(bool); ok {
		includeSubdomains = v
	}

	out := &FirecrawlMapOutput{
		URL:    rawURL,
		Search: search,
		Source: "firecrawl.dev /v1/map",
	}
	start := time.Now()

	bodyMap := map[string]any{
		"url":               rawURL,
		"limit":             limit,
		"includeSubdomains": includeSubdomains,
	}
	if search != "" {
		bodyMap["search"] = search
	}
	body, _ := json.Marshal(bodyMap)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.firecrawl.dev/v1/map", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")

	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl map: %w", err)
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(io.LimitReader(resp.Body, 8_000_000))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("firecrawl %d: %s", resp.StatusCode, hfTruncate(string(rawResp), 300))
	}

	var parsed struct {
		Success bool     `json:"success"`
		Links   []string `json:"links"`
		Error   string   `json:"error"`
	}
	if err := json.Unmarshal(rawResp, &parsed); err != nil {
		return nil, fmt.Errorf("firecrawl decode: %w", err)
	}
	if !parsed.Success {
		errMsg := parsed.Error
		if errMsg == "" {
			errMsg = hfTruncate(string(rawResp), 200)
		}
		return nil, fmt.Errorf("firecrawl map failed: %s", errMsg)
	}

	out.Links = parsed.Links
	out.TotalLinks = len(parsed.Links)

	// Aggregations
	subdomainSet := map[string]struct{}{}
	pathPrefixCount := map[string]int{}
	for _, l := range parsed.Links {
		u, err := url.Parse(l)
		if err != nil || u.Host == "" {
			continue
		}
		host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		subdomainSet[host] = struct{}{}
		// First path segment
		path := strings.Trim(u.Path, "/")
		if path != "" {
			seg := strings.SplitN(path, "/", 2)[0]
			if seg != "" {
				pathPrefixCount["/"+seg]++
			}
		}
	}
	for s := range subdomainSet {
		out.UniqueSubdomains = append(out.UniqueSubdomains, s)
	}
	sort.Strings(out.UniqueSubdomains)

	type pp struct {
		Prefix string
		Count  int
	}
	prefixes := []pp{}
	for p, c := range pathPrefixCount {
		prefixes = append(prefixes, pp{p, c})
	}
	sort.SliceStable(prefixes, func(i, j int) bool { return prefixes[i].Count > prefixes[j].Count })
	for i, p := range prefixes {
		if i >= 15 {
			break
		}
		out.UniquePaths = append(out.UniquePaths, fmt.Sprintf("%s (%d)", p.Prefix, p.Count))
	}

	out.HighlightFindings = buildFirecrawlMapHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildFirecrawlMapHighlights(o *FirecrawlMapOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d URLs discovered on %s", o.TotalLinks, o.URL))
	if o.Search != "" {
		hi = append(hi, "search filter: "+o.Search)
	}
	if len(o.UniqueSubdomains) > 1 {
		hi = append(hi, fmt.Sprintf("🌐 %d unique subdomains: %s", len(o.UniqueSubdomains), strings.Join(o.UniqueSubdomains, ", ")))
	}
	if len(o.UniquePaths) > 0 {
		hi = append(hi, "📁 top path prefixes: "+strings.Join(o.UniquePaths, ", "))
	}
	// Surface high-value paths
	interesting := []string{}
	for _, l := range o.Links {
		low := strings.ToLower(l)
		for _, kw := range []string{"login", "admin", "api", "dashboard", "auth", "signin", "signup", ".well-known", "wp-admin", "phpmyadmin", "graphql", "swagger"} {
			if strings.Contains(low, kw) {
				interesting = append(interesting, l)
				break
			}
		}
	}
	// Dedupe interesting
	seen := map[string]bool{}
	uniqueInteresting := []string{}
	for _, u := range interesting {
		if !seen[u] {
			seen[u] = true
			uniqueInteresting = append(uniqueInteresting, u)
		}
	}
	if len(uniqueInteresting) > 0 {
		hi = append(hi, fmt.Sprintf("⚠️  %d high-value URLs (login/admin/api/auth/dashboard):", len(uniqueInteresting)))
		for i, u := range uniqueInteresting {
			if i >= 10 {
				break
			}
			hi = append(hi, "  "+u)
		}
	}
	return hi
}
