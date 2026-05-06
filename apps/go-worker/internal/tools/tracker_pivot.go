package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmlpkg "html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// htmlPkgUnescape is an alias for html.UnescapeString from the stdlib,
// extracted so test fixtures can target the decoding step in isolation.
var htmlPkgUnescape = htmlpkg.UnescapeString

type TrackerPivotHit struct {
	Domain    string `json:"domain"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	IP        string `json:"ip,omitempty"`
	ASN       string `json:"asn,omitempty"`
	ASNName   string `json:"asn_name,omitempty"`
	Country   string `json:"country,omitempty"`
	ScannedAt string `json:"scanned_at,omitempty"`
	Source    string `json:"source"` // ddg | urlscan | publicwww
}

type TrackerPivotOutput struct {
	TrackerID       string              `json:"tracker_id"`
	Platform        string              `json:"platform"`
	StrategyUsed    string              `json:"strategy_used"`
	Query           string              `json:"query"`
	TotalHits       int                 `json:"total_hits"`
	UniqueDomains   []string            `json:"unique_domains"`
	UniqueIPs       []string            `json:"unique_ips,omitempty"`
	UniqueASNs      []string            `json:"unique_asns,omitempty"`
	HostingClusters map[string][]string `json:"hosting_clusters,omitempty"`
	Hits            []TrackerPivotHit   `json:"hits"`
	Source          string              `json:"source"`
	TookMs          int64               `json:"tookMs"`
	Note            string              `json:"note,omitempty"`
	VerifyHint      string              `json:"verify_hint,omitempty"`
}

// TrackerPivot searches for OTHER sites running the same tracker ID surfaced
// by tracker_extract. Strategy hierarchy (best → fallback):
//
//  1. URLSCAN_API_KEY set → urlscan.io paid (exact, indexed against rendered
//     request URLs across 800M scans)
//  2. PUBLICWWW_API_KEY set → publicwww.com (exact HTML-source search across
//     ~700M indexed pages)
//  3. Free fallback → DuckDuckGo HTML scraping with quoted exact-match query
//     (respects quotes; works for any rare-ish tracker ID)
//
// Free fallback caveats:
//   - Common IDs (UA-* shared across many sites) → many hits, lower precision
//   - Very rare IDs (custom GTM-*) → high precision, low recall (DDG only sees
//     pages it indexed; client-side-rendered IDs may be missed)
//   - Always verify candidates via tracker_extract on each hit
func TrackerPivot(ctx context.Context, input map[string]any) (*TrackerPivotOutput, error) {
	trackerID, _ := input["tracker_id"].(string)
	trackerID = strings.TrimSpace(trackerID)
	if trackerID == "" {
		return nil, errors.New("input.tracker_id required (e.g. 'GTM-MZPXMPW6' or 'UA-12345-1')")
	}
	platform, _ := input["platform"].(string)
	platform = strings.TrimSpace(strings.ToLower(platform))
	if platform == "" {
		platform = guessPlatformFromID(trackerID)
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 200 {
		limit = int(v)
	}

	start := time.Now()
	out := &TrackerPivotOutput{
		TrackerID: trackerID, Platform: platform,
		Source:     "tracker_pivot",
		VerifyHint: "Run tracker_extract on top hits to confirm — DDG/Tavily index page text but client-rendered IDs may not be indexed. Confirmed match = same operator.",
	}

	// 1. URLSCAN paid?
	if k := os.Getenv("URLSCAN_API_KEY"); k != "" {
		if err := pivotViaUrlscan(ctx, trackerID, platform, limit, k, out); err == nil && len(out.Hits) > 0 {
			out.StrategyUsed = "urlscan_paid"
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		// Fall through if urlscan returned 403 or 0 hits
	}

	// 2. PUBLICWWW?
	if k := os.Getenv("PUBLICWWW_API_KEY"); k != "" {
		if err := pivotViaPublicWWW(ctx, trackerID, k, limit, out); err == nil && len(out.Hits) > 0 {
			out.StrategyUsed = "publicwww"
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	// 3. Free fallback — DDG HTML scraping with quoted exact-match (best-effort)
	ddgErr := pivotViaDDG(ctx, trackerID, limit, out)

	// 4. Add Tavily as supplementary (strips quotes but may catch indexed pages
	// for very rare tracker IDs — agent can verify via tracker_extract).
	if k := os.Getenv("TAVILY_API_KEY"); k != "" {
		_ = pivotViaTavily(ctx, trackerID, limit, k, out)
	}

	if len(out.Hits) == 0 && ddgErr != nil {
		out.Note = fmt.Sprintf("All free strategies returned no hits or were rate-limited (last error: %v). Set URLSCAN_API_KEY or PUBLICWWW_API_KEY for production-grade pivots.", ddgErr)
	} else if len(out.Hits) == 0 {
		out.Note = "No hits found via free strategies — tracker may be too rare for indexing OR client-side-only. Set URLSCAN_API_KEY or PUBLICWWW_API_KEY for production-grade pivots."
	}
	out.StrategyUsed = "free_fallback_ddg+tavily"
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func pivotViaUrlscan(ctx context.Context, id, platform string, limit int, apiKey string, out *TrackerPivotOutput) error {
	q := fmt.Sprintf(`data.requests.request.url:%q`, id)
	out.Query = q
	endpoint := fmt.Sprintf("https://urlscan.io/api/v1/search/?q=%s&size=%d", url.QueryEscape(q), limit)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("API-Key", apiKey)
	req.Header.Set("User-Agent", "osint-agent/tracker-pivot")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("urlscan status %d", resp.StatusCode)
	}
	var parsed struct {
		Total   int `json:"total"`
		Results []struct {
			Page struct {
				Domain, URL, IP, ASN, ASNName, Country, Title string `json:"-"`
			} `json:"page"`
			Task struct {
				Time string `json:"time"`
			} `json:"task"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return err
	}
	out.TotalHits = parsed.Total
	domains := map[string]bool{}
	ips := map[string]bool{}
	asns := map[string]bool{}
	clusters := map[string][]string{}
	for _, r := range parsed.Results {
		dom := strings.ToLower(r.Page.Domain)
		if dom == "" {
			continue
		}
		out.Hits = append(out.Hits, TrackerPivotHit{
			Domain: dom, URL: r.Page.URL, IP: r.Page.IP,
			ASN: r.Page.ASN, ASNName: r.Page.ASNName, Country: r.Page.Country,
			ScannedAt: r.Task.Time, Title: r.Page.Title, Source: "urlscan",
		})
		domains[dom] = true
		if r.Page.IP != "" {
			ips[r.Page.IP] = true
		}
		if r.Page.ASN != "" {
			asns[r.Page.ASN] = true
		}
		if r.Page.ASNName != "" && !contains(clusters[r.Page.ASNName], dom) {
			clusters[r.Page.ASNName] = append(clusters[r.Page.ASNName], dom)
		}
	}
	out.UniqueDomains = sortedKeys(domains)
	out.UniqueIPs = sortedKeys(ips)
	out.UniqueASNs = sortedKeys(asns)
	out.HostingClusters = clusters
	return nil
}

func pivotViaPublicWWW(ctx context.Context, id, apiKey string, limit int, out *TrackerPivotOutput) error {
	q := id
	out.Query = q
	endpoint := fmt.Sprintf("https://publicwww.com/websites/%s/?key=%s&export=urls&format=json&max=%d",
		url.PathEscape(`"`+q+`"`), apiKey, limit)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("publicwww status %d", resp.StatusCode)
	}
	// publicwww returns simple JSON list of URLs
	var urls []string
	if err := json.Unmarshal(body, &urls); err != nil {
		// Fallback: line-separated URLs
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && strings.HasPrefix(line, "http") {
				urls = append(urls, line)
			}
		}
	}
	domains := map[string]bool{}
	for _, u := range urls {
		host := extractHost(u)
		if host == "" {
			continue
		}
		domains[host] = true
		out.Hits = append(out.Hits, TrackerPivotHit{
			Domain: host, URL: u, Source: "publicwww",
		})
	}
	out.UniqueDomains = sortedKeys(domains)
	out.TotalHits = len(urls)
	return nil
}

// DDG HTML scraping — respects quoted strings (Tavily silently strips them).
// We use the no-JS html.duckduckgo.com endpoint which renders results in
// plain HTML with stable result anchors.
var ddgResultRE = regexp.MustCompile(`<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>([^<]+)</a>`)
var ddgRedirectRE = regexp.MustCompile(`uddg=([^&]+)`)

func pivotViaTavily(ctx context.Context, id string, limit int, apiKey string, out *TrackerPivotOutput) error {
	body, _ := json.Marshal(map[string]any{
		"api_key":      apiKey,
		"query":        id,
		"max_results":  min(limit, 20),
		"search_depth": "basic",
	})
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.tavily.com/search", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("tavily status %d", resp.StatusCode)
	}
	var parsed struct {
		Results []struct {
			URL     string  `json:"url"`
			Title   string  `json:"title"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return err
	}
	domains := map[string]bool{}
	for _, d := range out.UniqueDomains {
		domains[d] = true
	}
	for _, r := range parsed.Results {
		host := extractHost(r.URL)
		if host == "" || domains[host] {
			continue
		}
		// Tavily de-emphasizes exact match — only include if the ID actually
		// appears in the snippet content.
		if !strings.Contains(strings.ToLower(r.Content), strings.ToLower(id)) {
			continue
		}
		domains[host] = true
		out.Hits = append(out.Hits, TrackerPivotHit{
			Domain: host, URL: r.URL, Title: r.Title, Source: "tavily",
		})
	}
	out.UniqueDomains = sortedKeys(domains)
	out.TotalHits = len(out.Hits)
	return nil
}

func pivotViaDDG(ctx context.Context, id string, limit int, out *TrackerPivotOutput) error {
	// Quote the ID for exact match. Add common-vendor exclusions to filter out
	// pages discussing the platforms generically.
	q := fmt.Sprintf(`%q -site:googletagmanager.com -site:google.com -site:facebook.com -site:linkedin.com -site:hotjar.com -site:stripe.com`, id)
	out.Query = q
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	html := string(body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("ddg status %d (may be rate-limited)", resp.StatusCode)
	}

	domains := map[string]bool{}
	matches := ddgResultRE.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		raw := m[1]
		title := stripHTMLBare(m[2])
		// DDG redirects via /l/?uddg=<urlencoded>
		actual := raw
		if rm := ddgRedirectRE.FindStringSubmatch(raw); len(rm) >= 2 {
			if u, err := url.QueryUnescape(rm[1]); err == nil {
				actual = u
			}
		}
		host := extractHost(actual)
		if host == "" {
			continue
		}
		domains[host] = true
		out.Hits = append(out.Hits, TrackerPivotHit{
			Domain: host, URL: actual, Title: title, Source: "ddg",
		})
		if len(out.Hits) >= limit {
			break
		}
	}
	out.UniqueDomains = sortedKeys(domains)
	out.TotalHits = len(out.Hits)
	if out.TotalHits == 0 {
		out.Note = "No DDG hits — tracker ID may be too rare for indexing, client-side-only loaded, or DDG rate-limited the request."
	}
	return nil
}

func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Host)
	if strings.HasPrefix(host, "www.") {
		host = host[4:]
	}
	return host
}

var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

// stripHTMLBare removes HTML/XML tags AND decodes HTML character
// references (&amp;, &#39;, &#x27;, &nbsp;, &copy;, etc.). 18+ tools in
// the catalog scrape HTML and pass the result through this function
// before name comparison or entity ingestion, so silently leaving
// "Smith &amp; Jones" un-decoded was breaking downstream ER. See
// TestStripHTMLBare_EntityDecodingQuantitative for the proof.
//
// After decoding, exotic whitespace characters that HTML-derived
// strings commonly carry (NBSP U+00A0, narrow NBSP U+202F, zero-width
// space U+200B, BOM U+FEFF, line separator U+2028, paragraph
// separator U+2029) are collapsed/normalized so downstream string
// comparison treats them like ordinary whitespace.
func stripHTMLBare(s string) string {
	stripped := htmlTagRE.ReplaceAllString(s, "")
	decoded := htmlUnescape(stripped)
	decoded = normalizeHTMLWhitespace(decoded)
	return strings.TrimSpace(decoded)
}

// normalizeHTMLWhitespace maps HTML-flavored whitespace characters to
// their common ASCII equivalents and drops zero-width characters.
func normalizeHTMLWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\u00A0', '\u202F', '\u2028', '\u2029':
			b.WriteByte(' ')
		case '\u200B', '\u200C', '\u200D', '\uFEFF':
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// htmlUnescape wraps html.UnescapeString. Pulled into a separate symbol
// so callers (and future regression tests) can target it directly.
func htmlUnescape(s string) string {
	return htmlPkgUnescape(s)
}

func guessPlatformFromID(id string) string {
	low := strings.ToLower(id)
	switch {
	case strings.HasPrefix(id, "GTM-"):
		return "google_tag_manager"
	case strings.HasPrefix(id, "UA-"):
		return "google_analytics_universal"
	case strings.HasPrefix(id, "G-"):
		return "google_analytics_4"
	case strings.HasPrefix(low, "pk_live_") || strings.HasPrefix(low, "pk_test_"):
		return "stripe_publishable_key"
	case len(id) >= 10 && len(id) <= 18 && allDigits(id):
		return "facebook_pixel"
	default:
		return "generic"
	}
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
