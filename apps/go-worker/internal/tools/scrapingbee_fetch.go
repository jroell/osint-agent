package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ScrapingBeeOutput is the response.
type ScrapingBeeOutput struct {
	URL              string            `json:"url"`
	StatusCode       int               `json:"status_code,omitempty"`
	OriginalSize     int               `json:"original_size_bytes,omitempty"`
	HTML             string            `json:"html,omitempty"`
	HTMLTruncated    bool              `json:"html_truncated,omitempty"`
	ProxyMode        string            `json:"proxy_mode,omitempty"` // basic | premium | stealth
	CountryCode      string            `json:"country_code,omitempty"`
	JSRendered       bool              `json:"js_rendered"`
	Headers          map[string]string `json:"response_headers,omitempty"`
	Title            string            `json:"page_title,omitempty"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source           string            `json:"source"`
	TookMs           int64             `json:"tookMs"`
	Note             string            `json:"note,omitempty"`
}

// ScrapingBeeFetch is a fallback bypass for sites Firecrawl + Tavily-snippet
// can't handle. ScrapingBee uses a different proxy network (residential IPs
// in 60+ countries) and Chromium-rendering pipeline, so some sites that
// reject Firecrawl's IP ranges may pass through SB cleanly (and vice versa).
//
// Three proxy modes:
//   - "basic"   : default, fast (1 credit per request)
//   - "premium" : datacenter-rotating, US-by-default (10-25 credits)
//   - "stealth" : residential IPs + browser fingerprint masking (75 credits)
//
// REQUIRES SCRAPING_BEE_API_KEY.
//
// Why this matters for ER:
//   - For sites Firecrawl scraping fails on (Cloudflare 5xx, JS-detect, rate
//     limits), SB is the second route. Different proxy ASN footprint =
//     different anti-bot fingerprint at the network layer.
//   - For sites where Firecrawl works fine (most of the web), prefer
//     `firecrawl_extract` which gives LLM-extracted structured fields.
//   - Residential mode ($$$) is for the very-aggressive-defense tier
//     (TPS-style sites, some ad networks). Set sparingly.
func ScrapingBeeFetch(ctx context.Context, input map[string]any) (*ScrapingBeeOutput, error) {
	apiKey := os.Getenv("SCRAPING_BEE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("SCRAPING_BEE_API_KEY env var required")
	}
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("input.url required")
	}
	mode, _ := input["proxy_mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "basic"
	}
	switch mode {
	case "basic", "premium", "stealth":
	default:
		return nil, fmt.Errorf("proxy_mode must be one of: basic, premium, stealth")
	}
	renderJS := true
	if v, ok := input["render_js"].(bool); ok {
		renderJS = v
	}
	countryCode, _ := input["country_code"].(string)
	countryCode = strings.ToLower(strings.TrimSpace(countryCode))

	out := &ScrapingBeeOutput{
		URL:        rawURL,
		ProxyMode:  mode,
		CountryCode: countryCode,
		JSRendered: renderJS,
		Source:     "app.scrapingbee.com",
	}
	start := time.Now()

	// Build SB request URL
	params := url.Values{}
	params.Set("api_key", apiKey)
	params.Set("url", rawURL)
	if renderJS {
		params.Set("render_js", "true")
	} else {
		params.Set("render_js", "false")
	}
	switch mode {
	case "premium":
		params.Set("premium_proxy", "true")
	case "stealth":
		params.Set("stealth_proxy", "true")
	}
	if countryCode != "" {
		params.Set("country_code", countryCode)
	}
	// Don't block resources by default — captures full DOM
	params.Set("block_resources", "false")

	endpoint := "https://app.scrapingbee.com/api/v1/?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")

	cli := &http.Client{Timeout: 180 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrapingbee: %w", err)
	}
	defer resp.Body.Close()

	out.StatusCode = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read sb body: %w", err)
	}
	out.OriginalSize = len(body)

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("scrapingbee %d: %s", resp.StatusCode, hfTruncate(string(body), 300))
	}

	html := string(body)
	if len(html) > 200_000 {
		html = html[:200_000]
		out.HTMLTruncated = true
	}
	out.HTML = html

	// Extract title
	low := strings.ToLower(html)
	if i := strings.Index(low, "<title"); i >= 0 {
		end := strings.Index(low[i:], "</title>")
		if end > 0 {
			seg := html[i : i+end]
			if gt := strings.Index(seg, ">"); gt > 0 {
				out.Title = strings.TrimSpace(seg[gt+1:])
				if len(out.Title) > 200 {
					out.Title = out.Title[:200]
				}
			}
		}
	}

	// Surface useful response headers
	out.Headers = map[string]string{}
	for _, h := range []string{"Content-Type", "Server", "X-Powered-By", "Cf-Cache-Status", "Cf-Ray"} {
		if v := resp.Header.Get(h); v != "" {
			out.Headers[h] = v
		}
	}

	out.HighlightFindings = buildScrapingBeeHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildScrapingBeeHighlights(o *ScrapingBeeOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ fetched %s — status=%d, %d bytes (proxy_mode=%s, js_rendered=%v)",
		o.URL, o.StatusCode, o.OriginalSize, o.ProxyMode, o.JSRendered))
	if o.Title != "" {
		hi = append(hi, "page title: "+o.Title)
	}
	// Detection
	low := strings.ToLower(o.HTML)
	indicators := []string{}
	if strings.Contains(low, "captcha") || strings.Contains(low, "just a moment") {
		indicators = append(indicators, "⚠️  Captcha/Cloudflare challenge in body")
	}
	if strings.Contains(low, "enable js") || strings.Contains(low, "javascript is required") {
		indicators = append(indicators, "⚠️  JS-required page (try render_js=true)")
	}
	if strings.Contains(low, "access denied") || strings.Contains(low, "blocked") {
		indicators = append(indicators, "⚠️  Access-denied indicators in body")
	}
	if len(indicators) > 0 {
		hi = append(hi, indicators...)
	}
	if o.HTMLTruncated {
		hi = append(hi, "ℹ️  HTML response truncated to 200K chars")
	}
	if cf := o.Headers["Cf-Ray"]; cf != "" {
		hi = append(hi, "Cf-Ray: "+cf+" (request reached Cloudflare edge)")
	}
	return hi
}
