package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type JSEndpoint struct {
	URL        string `json:"url"`
	Kind       string `json:"kind"`        // absolute_url | api_path | graphql_op | static_asset | unknown
	APIScore   int    `json:"api_score"`   // 0-10 — how likely this is an API endpoint
	SourceFile string `json:"source_file"` // which JS file it came from
}

type JSEndpointExtractOutput struct {
	Target          string                  `json:"target"`
	HTMLStatus      int                     `json:"html_status"`
	JSFilesFound    int                     `json:"js_files_found"`
	JSFilesScanned  int                     `json:"js_files_scanned"`
	JSFilesFailed   []string                `json:"js_files_failed,omitempty"`
	UniqueEndpoints int                     `json:"unique_endpoints"`
	APIEndpoints    []JSEndpoint            `json:"api_endpoints"`              // filtered to api_score >= 5
	OtherURLs       []JSEndpoint            `json:"other_urls,omitempty"`
	GraphQLOps      []string                `json:"graphql_operations,omitempty"`
	Subdomains      []string                `json:"subdomains_referenced,omitempty"`
	PotentialSecrets []string               `json:"potential_secrets,omitempty"` // keys/tokens flagged in JS
	SourceFiles     map[string]int          `json:"endpoints_per_source_file"`
	Source          string                  `json:"source"`
	TookMs          int64                   `json:"tookMs"`
	Note            string                  `json:"note"`
}

// JSEndpointExtract is the canonical bug-bounty technique for finding APIs that
// aren't exposed in a site's UI but are baked into its JavaScript bundles.
//
// Algorithm:
//  1. Fetch target HTML
//  2. Parse <script src="..."> tags + identify inline scripts
//  3. Fetch every JS bundle (bounded concurrency)
//  4. Run regex extraction on each:
//     - Absolute URLs (https://api.example.com/...)
//     - API-style relative paths (/api/v1/users, /graphql, /admin/...)
//     - GraphQL operation names (query Foo / mutation Bar)
//     - Subdomain references
//     - Potential leaked keys/tokens (flagged conservatively)
//  5. Dedupe, classify by kind, score by API-likelihood
//
// Why this works: developers ship API base URLs, route maps, GraphQL schemas,
// and even auth tokens straight into client JS bundles. A single static-asset
// fetch reveals the entire API surface area.
func JSEndpointExtract(ctx context.Context, input map[string]any) (*JSEndpointExtractOutput, error) {
	target, _ := input["url"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.url required")
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("input.url must be absolute http(s)")
	}
	maxJSFiles := 30
	if v, ok := input["max_js_files"].(float64); ok && v > 0 {
		maxJSFiles = int(v)
	}
	concurrency := 6
	if v, ok := input["concurrency"].(float64); ok && v > 0 {
		concurrency = int(v)
	}
	includeSecrets := true
	if v, ok := input["include_potential_secrets"].(bool); ok {
		includeSecrets = v
	}

	start := time.Now()
	out := &JSEndpointExtractOutput{
		Target:      target,
		APIEndpoints: []JSEndpoint{},
		OtherURLs:   []JSEndpoint{},
		SourceFiles: map[string]int{},
		Source:      "js_endpoint_extract",
	}

	// Step 1+2: fetch HTML, find JS bundles + inline scripts.
	htmlBody, htmlStatus, err := jsFetchText(ctx, target, 15*time.Second, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, fmt.Errorf("html fetch: %w", err)
	}
	out.HTMLStatus = htmlStatus

	jsURLs := extractScriptSources(htmlBody, u)
	if len(jsURLs) > maxJSFiles {
		jsURLs = jsURLs[:maxJSFiles]
	}
	out.JSFilesFound = len(jsURLs)

	// Also include the inline scripts extracted from the HTML body itself.
	inlineScripts := extractInlineScripts(htmlBody)
	if len(inlineScripts) > 0 {
		out.SourceFiles["[inline]"] = 0
	}

	// Step 3: fetch all JS files in parallel (bounded).
	type jsFile struct {
		url    string
		body   string
		err    error
	}
	results := make([]jsFile, 0, len(jsURLs)+1)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, ju := range jsURLs {
		wg.Add(1)
		sem <- struct{}{}
		go func(ju string) {
			defer wg.Done()
			defer func() { <-sem }()
			body, _, err := jsFetchText(ctx, ju, 12*time.Second, "*/*")
			mu.Lock()
			results = append(results, jsFile{url: ju, body: body, err: err})
			mu.Unlock()
		}(ju)
	}
	wg.Wait()

	// Add inline-script content as a synthetic file.
	if len(inlineScripts) > 0 {
		results = append(results, jsFile{url: "[inline]", body: strings.Join(inlineScripts, "\n;\n")})
	}

	// Step 4: regex extraction.
	endpointsByKey := map[string]JSEndpoint{} // dedupe by url+kind
	graphqlOps := map[string]struct{}{}
	subdomains := map[string]struct{}{}
	potentialSecrets := map[string]struct{}{}

	for _, jf := range results {
		if jf.err != nil {
			out.JSFilesFailed = append(out.JSFilesFailed, jf.url)
			continue
		}
		out.JSFilesScanned++
		out.SourceFiles[jf.url] = 0

		hits := extractEndpoints(jf.body, jf.url, u)
		for _, h := range hits {
			key := h.Kind + "::" + h.URL
			if _, ok := endpointsByKey[key]; !ok {
				endpointsByKey[key] = h
				out.SourceFiles[jf.url]++
			}
		}

		// GraphQL operations.
		for _, op := range graphqlOpRe.FindAllStringSubmatch(jf.body, -1) {
			if len(op) >= 3 {
				graphqlOps[op[1]+":"+op[2]] = struct{}{}
			}
		}

		// Subdomains of target.
		host := u.Host
		apex := apexDomain(host)
		hostRe := regexp.MustCompile(`(?i)\b([a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?\.)+` + regexp.QuoteMeta(apex) + `\b`)
		for _, m := range hostRe.FindAllString(jf.body, -1) {
			s := strings.ToLower(m)
			if s != host && s != apex {
				subdomains[s] = struct{}{}
			}
		}

		if includeSecrets {
			for _, m := range secretRe.FindAllStringSubmatch(jf.body, -1) {
				if len(m) >= 2 {
					potentialSecrets[m[0][:min(80, len(m[0]))]] = struct{}{}
				}
			}
		}
	}

	// Sort endpoints into API vs other.
	for _, e := range endpointsByKey {
		if e.APIScore >= 5 {
			out.APIEndpoints = append(out.APIEndpoints, e)
		} else {
			out.OtherURLs = append(out.OtherURLs, e)
		}
	}
	sort.Slice(out.APIEndpoints, func(i, j int) bool { return out.APIEndpoints[i].APIScore > out.APIEndpoints[j].APIScore })
	sort.Slice(out.OtherURLs, func(i, j int) bool { return out.OtherURLs[i].URL < out.OtherURLs[j].URL })
	if len(out.OtherURLs) > 100 {
		out.OtherURLs = out.OtherURLs[:100]
	}

	for op := range graphqlOps {
		out.GraphQLOps = append(out.GraphQLOps, op)
	}
	sort.Strings(out.GraphQLOps)
	for s := range subdomains {
		out.Subdomains = append(out.Subdomains, s)
	}
	sort.Strings(out.Subdomains)
	for s := range potentialSecrets {
		out.PotentialSecrets = append(out.PotentialSecrets, s)
	}
	sort.Strings(out.PotentialSecrets)

	out.UniqueEndpoints = len(endpointsByKey)
	out.TookMs = time.Since(start).Milliseconds()
	out.Note = "API endpoints scored ≥5 are surfaced as `api_endpoints`. Use http_probe or stealth_http_fetch to verify auth posture on each."
	return out, nil
}

// scriptSrcRe + scriptInlineRe — find <script src="..."> and <script>...</script>
var scriptSrcRe = regexp.MustCompile(`(?is)<script[^>]+src\s*=\s*["']([^"']+)["']`)
var scriptInlineRe = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)

func extractScriptSources(html string, base *url.URL) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, m := range scriptSrcRe.FindAllStringSubmatch(html, -1) {
		if len(m) < 2 {
			continue
		}
		src := strings.TrimSpace(m[1])
		if src == "" {
			continue
		}
		// Resolve relative URLs against base.
		ref, err := url.Parse(src)
		if err != nil {
			continue
		}
		full := base.ResolveReference(ref).String()
		if !strings.HasPrefix(full, "http") {
			continue
		}
		if _, dup := seen[full]; dup {
			continue
		}
		seen[full] = struct{}{}
		out = append(out, full)
	}
	return out
}

func extractInlineScripts(html string) []string {
	out := []string{}
	for _, m := range scriptInlineRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 2 && len(m[1]) > 50 {
			out = append(out, m[1])
		}
	}
	return out
}

// linkfinderRe is a Go port of the canonical LinkFinder regex (GerbenJavado/LinkFinder).
// Catches absolute URLs, relative paths, REST routes, file references with extensions.
var linkfinderRe = regexp.MustCompile(
	`(?i)(?:"|'|` + "`" + `)` + // opening quote
		`(` +
		`((?:[a-zA-Z]{1,10}://|//)[^"'\x60/]{1,}\.[a-zA-Z]{2,}[^"'\x60]{0,})` + // absolute URL
		`|` +
		`((?:/|\./|\.\./)[^"'\x60><,; ()*$%]{1,}[^"'\x60><,;|()])` + // relative paths starting with /, ./, ../
		`|` +
		`([a-zA-Z0-9_\-]{1,}\.(?:php|asp|aspx|jsp|json|action|html|js|txt|xml|wasm)(?:\?[^"'\x60]{0,}|))` + // filenames
		`)` +
		`(?:"|'|` + "`" + `)`)

// graphqlOpRe — finds GraphQL operation declarations.
var graphqlOpRe = regexp.MustCompile(`(?i)\b(query|mutation|subscription)\s+([A-Z][A-Za-z0-9_]{2,})\b`)

// secretRe — conservative regex for keys/tokens that look like leaked credentials.
// Intentionally narrow — no false-positive ferries.
var secretRe = regexp.MustCompile(
	`(?i)["'\x60]?(` +
		`AKIA[0-9A-Z]{16}` + // AWS access key
		`|sk-[a-zA-Z0-9]{20,}` + // OpenAI / Anthropic style
		`|sk_live_[a-zA-Z0-9]{16,}` + // Stripe
		`|sk_test_[a-zA-Z0-9]{16,}` +
		`|xox[baprs]-[a-zA-Z0-9-]{10,}` + // Slack
		`|ghp_[a-zA-Z0-9]{36}` + // GitHub PAT
		`|gho_[a-zA-Z0-9]{36}` +
		`|github_pat_[a-zA-Z0-9_]{20,}` +
		`|AIza[a-zA-Z0-9_\-]{35}` + // Google API
		`|firebase[A-Za-z0-9_]*[ =:][^"'\x60]{20,}` +
		`|(?:secret|password|api_key|apikey|access_token)\s*[:=]\s*["']([a-zA-Z0-9_\-]{20,})` +
		`)["'\x60]?`)

// extractEndpoints applies linkfinderRe + API-pattern scoring to a JS body.
func extractEndpoints(body, sourceFile string, base *url.URL) []JSEndpoint {
	out := []JSEndpoint{}
	matches := linkfinderRe.FindAllStringSubmatch(body, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimSpace(m[1])
		if raw == "" || len(raw) < 2 {
			continue
		}
		ep := JSEndpoint{URL: raw, SourceFile: sourceFile}
		// Classify
		switch {
		case strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "//"):
			ep.Kind = "absolute_url"
		case strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../"):
			ep.Kind = "path"
		default:
			ep.Kind = "filename"
		}
		// API score: how likely this is an API endpoint vs. a static asset.
		ep.APIScore = scoreAsAPI(raw)
		out = append(out, ep)
	}
	// Dedupe within file.
	seen := map[string]struct{}{}
	deduped := []JSEndpoint{}
	for _, e := range out {
		if _, ok := seen[e.URL]; ok {
			continue
		}
		seen[e.URL] = struct{}{}
		deduped = append(deduped, e)
	}
	return deduped
}

func scoreAsAPI(s string) int {
	score := 0
	low := strings.ToLower(s)
	hadAPIHit := false

	apiPatterns := []string{
		"/api/", "/v1/", "/v2/", "/v3/", "/v4/", "/graphql", "/gql",
		"/rest/", "/rpc/", "/admin/", "/internal/", "/private/",
		"/oauth/", "/auth/", "/token/", "/session/",
		".json", ".xml",
		"/users/", "/user/", "/account/", "/me/",
		"/search/", "/query/", "/posts/", "/items/",
	}
	for _, p := range apiPatterns {
		if strings.Contains(low, p) {
			score += 2
			hadAPIHit = true
		}
	}

	// Path-suffix matches (no trailing slash) — handles /api/users/me, /v1/auth, etc.
	suffixOrQuery := func(path, frag string) bool {
		return strings.HasSuffix(path, frag) || strings.Contains(path, frag+"?") || strings.Contains(path, frag+"/")
	}
	for _, p := range []string{"/me", "/users", "/posts", "/items", "/search", "/query", "/login", "/logout", "/register", "/signup", "/profile"} {
		if suffixOrQuery(low, p) {
			score += 1
			hadAPIHit = true
		}
	}

	// High-confidence boost — these are unambiguously API.
	for _, p := range []string{"/graphql", "/gql", "/api/", "/rest/", "/rpc/"} {
		if strings.Contains(low, p) {
			score += 1
			hadAPIHit = true
		}
	}

	staticExts := []string{".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".woff", ".woff2", ".ttf", ".ico", ".webp", ".map"}
	if !hadAPIHit {
		// Pure static asset — heavy penalty.
		for _, p := range staticExts {
			if strings.HasSuffix(low, p) {
				score -= 5
			}
		}
		if strings.HasSuffix(low, ".html") {
			score -= 3
		}
	} else {
		// Endpoint that happens to end in a static extension (e.g. SPA shell, doc page) — soft penalty.
		for _, p := range staticExts {
			if strings.HasSuffix(low, p) {
				score -= 2
			}
		}
		if strings.HasSuffix(low, ".html") {
			score -= 1
		}
	}

	if score > 10 {
		score = 10
	}
	if score < 0 {
		score = 0
	}
	return score
}

// jsFetchText is a minimal text-fetcher with a configurable accept type.
func jsFetchText(ctx context.Context, target string, timeout time.Duration, acceptType string) (string, int, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", acceptType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(body), resp.StatusCode, nil
}

// apexDomain returns the apex SLD+TLD (e.g. "vurvey.app" from "users.vurvey.app").
// Naive 2-label split — works for most TLDs without needing a public-suffix list.
func apexDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// (min is already provided in http_probe.go)

// (json import is reserved for future DB integration — silence compile)
var _ = json.Unmarshal
