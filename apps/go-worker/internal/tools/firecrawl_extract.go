package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// FirecrawlExtractOutput is the response.
type FirecrawlExtractOutput struct {
	URL              string         `json:"url"`
	Mode             string         `json:"mode"`
	Prompt           string         `json:"prompt,omitempty"`
	Extracted        map[string]any `json:"extracted_fields,omitempty"`
	Markdown         string         `json:"markdown_excerpt,omitempty"`
	Title            string         `json:"page_title,omitempty"`
	StatusCode       int            `json:"status_code,omitempty"`
	HighlightFindings []string      `json:"highlight_findings"`
	Source           string         `json:"source"`
	TookMs           int64          `json:"tookMs"`
	Note             string         `json:"note,omitempty"`
}

// FirecrawlExtract calls Firecrawl's /scrape endpoint with structured-JSON
// extraction (formats: ["json"] + jsonOptions). Free for low-volume use
// with FIRECRAWL_API_KEY set.
//
// Why this matters for ER:
//   - Eliminates per-site HTML parsing for any URL Firecrawl can scrape.
//     Pass a natural-language extraction prompt or a JSON schema, and
//     Firecrawl's underlying LLM extracts structured fields directly.
//   - Pairs with site_snippet_search (iter-65) for the catalog's two
//     scraping strategies: snippet-bypass for indexed-but-blocked sites,
//     and full-page LLM extraction for sites Firecrawl can scrape directly.
//   - Especially useful for arbitrary structured-data extraction from one-off
//     pages (org charts, company "about" pages, news articles, blog posts)
//     where writing a regex parser would be wasted effort.
//
// Modes:
//   - "extract" : returns extracted_fields object (default). Works best when
//     `prompt` describes specific fields you want.
//   - "schema"  : strict JSON-schema-driven extraction. Pass `schema` as a
//     JSON object describing the fields, types, and required fields.
//
// Both modes use Firecrawl's render-JS scraper, so they work on SPAs and
// many anti-bot pages.
func FirecrawlExtract(ctx context.Context, input map[string]any) (*FirecrawlExtractOutput, error) {
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FIRECRAWL_API_KEY env var required")
	}
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("input.url required")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "extract"
	}
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	schema, _ := input["schema"].(map[string]any)

	// One of prompt or schema is required
	if prompt == "" && schema == nil {
		return nil, fmt.Errorf("input.prompt (natural-language) or input.schema (JSON schema object) required")
	}
	wantMarkdown := false
	if v, ok := input["include_markdown"].(bool); ok {
		wantMarkdown = v
	}
	stealth := false
	if v, ok := input["stealth_proxy"].(bool); ok {
		stealth = v
	}

	out := &FirecrawlExtractOutput{
		URL:    rawURL,
		Mode:   mode,
		Prompt: prompt,
		Source: "firecrawl.dev /v1/scrape (json format)",
	}
	start := time.Now()

	// Build the payload — Firecrawl v1 wants string format names + top-level jsonOptions
	formats := []string{"json"}
	if wantMarkdown {
		formats = append(formats, "markdown")
	}
	jsonOpts := map[string]any{}
	if prompt != "" {
		jsonOpts["prompt"] = prompt
	}
	if schema != nil {
		jsonOpts["schema"] = schema
	}

	body := map[string]any{
		"url":             rawURL,
		"formats":         formats,
		"jsonOptions":     jsonOpts,
		"onlyMainContent": true,
	}
	if stealth {
		body["proxy"] = "stealth"
	}

	bodyBytes, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.firecrawl.dev/v1/scrape", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")

	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req = req.WithContext(cctx)

	cli := &http.Client{Timeout: 100 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl extract: %w", err)
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(io.LimitReader(resp.Body, 8_000_000))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("firecrawl %d: %s", resp.StatusCode, hfTruncate(string(rawResp), 300))
	}

	var parsed struct {
		Success bool `json:"success"`
		Data    struct {
			JSON     map[string]any         `json:"json"`
			Markdown string                 `json:"markdown"`
			Metadata map[string]any         `json:"metadata"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rawResp, &parsed); err != nil {
		return nil, fmt.Errorf("firecrawl decode: %w (body: %s)", err, hfTruncate(string(rawResp), 200))
	}
	if !parsed.Success {
		errMsg := parsed.Error
		if errMsg == "" {
			errMsg = hfTruncate(string(rawResp), 200)
		}
		return nil, fmt.Errorf("firecrawl extraction failed: %s", errMsg)
	}

	out.Extracted = parsed.Data.JSON
	if wantMarkdown {
		md := parsed.Data.Markdown
		if len(md) > 4000 {
			md = md[:4000] + "..."
		}
		out.Markdown = md
	}
	if t, ok := parsed.Data.Metadata["title"].(string); ok {
		out.Title = t
	}
	if sc, ok := parsed.Data.Metadata["statusCode"].(float64); ok {
		out.StatusCode = int(sc)
	}

	out.HighlightFindings = buildFirecrawlExtractHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildFirecrawlExtractHighlights(o *FirecrawlExtractOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ extracted from %s (status=%d)", o.URL, o.StatusCode))
	if o.Title != "" {
		hi = append(hi, "page title: "+o.Title)
	}
	if len(o.Extracted) > 0 {
		hi = append(hi, fmt.Sprintf("📋 %d structured fields extracted via LLM", len(o.Extracted)))
		// Show top fields
		i := 0
		for k, v := range o.Extracted {
			if i >= 8 {
				break
			}
			vs := fmt.Sprintf("%v", v)
			if len(vs) > 120 {
				vs = vs[:120] + "..."
			}
			hi = append(hi, fmt.Sprintf("  %s = %s", k, vs))
			i++
		}
	}
	return hi
}
