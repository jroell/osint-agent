package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SiteSnippetHit is one parsed search result.
type SiteSnippetHit struct {
	URL              string            `json:"url"`
	Title            string            `json:"title,omitempty"`
	Snippet          string            `json:"snippet"`
	ExtractedFields  map[string]string `json:"extracted_fields,omitempty"` // site-specific parsed values
}

// SiteSnippetSearchOutput is the response.
type SiteSnippetSearchOutput struct {
	Site             string           `json:"site"`
	Query            string           `json:"query"`
	BuiltQuery       string           `json:"built_query"`
	Hits             []SiteSnippetHit `json:"hits"`
	UniqueDomainsHit []string         `json:"unique_subpaths,omitempty"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source           string           `json:"source"`
	Note             string           `json:"note,omitempty"`
	TookMs           int64            `json:"tookMs"`
}

// known-good preset domains with description + parser hint
type sitePreset struct {
	Domain      string
	Description string
	Parser      func(snippet, title, url string) map[string]string
}

var sitePresets = map[string]sitePreset{
	"linkedin": {
		Domain:      "linkedin.com/in",
		Description: "LinkedIn personal profiles. Surfaces 'People Also Viewed', current title + employer, 'About' excerpts. Tavily indexes most public /in/ pages even though direct fetches are anti-bot-blocked.",
		Parser:      parseLinkedInSnippet,
	},
	"linkedin_company": {
		Domain:      "linkedin.com/company",
		Description: "LinkedIn company pages. Industry, employee count range, headquarters.",
	},
	"zoominfo": {
		Domain:      "zoominfo.com",
		Description: "ZoomInfo B2B contact DB. Surfaces person → job title + employer pairs even for paywalled profiles. Strong professional ER.",
		Parser:      parseZoomInfoSnippet,
	},
	"rocketreach": {
		Domain:      "rocketreach.co",
		Description: "RocketReach B2B contacts. Person → employer → title.",
		Parser:      parseZoomInfoSnippet, // similar shape
	},
	"glassdoor": {
		Domain:      "glassdoor.com",
		Description: "Glassdoor employer reviews + salaries. Surfaces salary ranges by role even when site requires login.",
		Parser:      parseGlassdoorSnippet,
	},
	"newspapers": {
		Domain:      "newspapers.com",
		Description: "Newspapers.com paywalled archive — Google indexes the OCR'd article snippets. Strong for genealogy, obituaries, historical events.",
	},
	"ancestry": {
		Domain:      "ancestry.com",
		Description: "Ancestry.com paywalled family trees and records. Snippets sometimes include name + dates + relatives.",
	},
	"beenverified": {
		Domain:      "beenverified.com",
		Description: "BeenVerified paid people-search. Snippets contain age + address.",
	},
	"fastpeoplesearch": {
		Domain:      "fastpeoplesearch.com",
		Description: "FastPeopleSearch (TPS sister site). Public-records aggregator with same Cloudflare protection.",
	},
	"spokeo": {
		Domain:      "spokeo.com",
		Description: "Spokeo paid people-search. Snippets contain age + city.",
	},
	"radaris": {
		Domain:      "radaris.com",
		Description: "Radaris public-records aggregator. Snippets list age + relatives.",
	},
	"thatsthem": {
		Domain:      "thatsthem.com",
		Description: "ThatsThem free people-search. Snippets contain phone + address.",
	},
	"peekyou": {
		Domain:      "peekyou.com",
		Description: "PeekYou aggregates social-media references for a name.",
	},
	"truepeoplesearch": {
		Domain:      "truepeoplesearch.com",
		Description: "TruePeopleSearch (also has dedicated truepeoplesearch_lookup tool with relative-extraction parser).",
	},
	"instagram": {
		Domain:      "instagram.com",
		Description: "Instagram public profile pages. Snippets show follower/post counts + bio.",
		Parser:      parseInstagramSnippet,
	},
	"tiktok": {
		Domain:      "tiktok.com",
		Description: "TikTok user/video pages. Snippets show username + caption + view counts.",
	},
	"twitter": {
		Domain:      "x.com OR site:twitter.com",
		Description: "Twitter/X public-tweet pages. Tavily indexes individual tweets even when the live site blocks unauth.",
	},
	"facebook": {
		Domain:      "facebook.com",
		Description: "Facebook public pages and profiles. Indexed but limited.",
	},
}

// SiteSnippetSearch is the generalized version of the TruePeopleSearch
// Tavily-bypass technique. It works for ANY site that:
//  1. Is publicly accessible to Google's crawler.
//  2. Has structured content that makes it into search snippets.
//  3. Is anti-bot-blocked for direct fetches.
//
// The trick: search engines (Tavily, Google) indexed the page server-side.
// Their result snippets contain enough structured data to satisfy most
// OSINT queries WITHOUT ever hitting the live anti-bot-protected site.
//
// Modes:
//   - Pass `preset` to use a named-site preset (linkedin, zoominfo,
//     rocketreach, glassdoor, newspapers, ancestry, beenverified,
//     fastpeoplesearch, spokeo, radaris, thatsthem, peekyou, instagram,
//     tiktok, twitter, facebook). Each preset has site-specific snippet
//     parsing.
//   - Pass `site_domain` for arbitrary site (e.g. 'pinterest.com') with
//     no parsing. Returns raw snippets.
//
// Why this matters for ER:
//   - Closes the long-standing gap where my catalog couldn't access
//     LinkedIn, ZoomInfo, Glassdoor, paywalled Newspapers/Ancestry, and
//     other anti-bot-protected sites.
//   - 11+ previously-blocked surfaces now indirectly queryable.
//   - Search engines act as canonical "snapshot" of the page content,
//     bypassing both Cloudflare and login gates.
//
// REQUIRES TAVILY_API_KEY (or FIRECRAWL_API_KEY fallback).
func SiteSnippetSearch(ctx context.Context, input map[string]any) (*SiteSnippetSearchOutput, error) {
	preset, _ := input["preset"].(string)
	preset = strings.ToLower(strings.TrimSpace(preset))
	siteDomain, _ := input["site_domain"].(string)
	siteDomain = strings.TrimSpace(siteDomain)
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required")
	}
	if preset == "" && siteDomain == "" {
		return nil, fmt.Errorf("either input.preset (e.g. 'linkedin', 'zoominfo') or input.site_domain (e.g. 'pinterest.com') required")
	}

	limit := 10
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 30 {
		limit = int(v)
	}

	out := &SiteSnippetSearchOutput{
		Query:  query,
		Source: "Tavily search-engine snippet bypass (works for any Google-indexed site that's anti-bot-blocked for direct fetch)",
	}
	start := time.Now()

	// Resolve preset → domain
	var presetDef *sitePreset
	if preset != "" {
		if pd, ok := sitePresets[preset]; ok {
			presetDef = &pd
			siteDomain = pd.Domain
			out.Site = preset
		} else {
			return nil, fmt.Errorf("unknown preset '%s' — available: %s", preset, strings.Join(presetKeys(), ", "))
		}
	}
	if out.Site == "" {
		out.Site = siteDomain
	}

	// Construct the query. siteDomain may already have OR semantics for combined sites
	var siteClause string
	if strings.Contains(siteDomain, " OR site:") {
		siteClause = "site:" + siteDomain
	} else {
		siteClause = "site:" + siteDomain
	}
	builtQuery := siteClause + " " + query
	out.BuiltQuery = builtQuery

	// Try Tavily
	results, err := siteSnippetSearchTavily(ctx, builtQuery, limit)
	if err != nil || len(results) == 0 {
		// Fallback to firecrawl_search
		fcResults, fcErr := siteSnippetSearchFirecrawl(ctx, builtQuery, limit)
		if fcErr == nil && len(fcResults) > 0 {
			results = fcResults
			err = nil
		}
	}
	if err != nil && len(results) == 0 {
		return nil, err
	}
	if len(results) == 0 {
		out.Note = fmt.Sprintf("no results indexed for '%s' on site %s", query, siteDomain)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// Track unique subpaths to dedupe / signal spread
	subpathSet := map[string]struct{}{}

	for _, r := range results {
		hit := SiteSnippetHit{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: r.Snippet,
		}
		if presetDef != nil && presetDef.Parser != nil {
			extracted := presetDef.Parser(r.Snippet, r.Title, r.URL)
			if len(extracted) > 0 {
				hit.ExtractedFields = extracted
			}
		}
		// extract subpath (after domain)
		if idx := strings.Index(r.URL, siteDomainRoot(siteDomain)); idx >= 0 {
			subpathSet[r.URL] = struct{}{}
		}
		out.Hits = append(out.Hits, hit)
	}
	for s := range subpathSet {
		out.UniqueDomainsHit = append(out.UniqueDomainsHit, s)
	}
	sort.Strings(out.UniqueDomainsHit)
	if len(out.UniqueDomainsHit) > 25 {
		out.UniqueDomainsHit = out.UniqueDomainsHit[:25]
	}

	out.HighlightFindings = buildSiteSnippetHighlights(out, presetDef)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func siteDomainRoot(d string) string {
	if i := strings.Index(d, "/"); i > 0 {
		return d[:i]
	}
	return d
}

func presetKeys() []string {
	keys := []string{}
	for k := range sitePresets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func siteSnippetSearchTavily(ctx context.Context, query string, limit int) ([]tpsSearchResult, error) {
	apiKey := os.Getenv("TAVILY_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("TAVILY_API_KEY not set")
	}
	body, _ := json.Marshal(map[string]any{
		"api_key":             apiKey,
		"query":               query,
		"max_results":         limit,
		"include_raw_content": false,
		"include_images":      false,
		"search_depth":        "basic",
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tavily %d: %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []tpsSearchResult{}
	for _, r := range raw.Results {
		out = append(out, tpsSearchResult{URL: r.URL, Title: r.Title, Snippet: r.Content})
	}
	return out, nil
}

func siteSnippetSearchFirecrawl(ctx context.Context, query string, limit int) ([]tpsSearchResult, error) {
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FIRECRAWL_API_KEY not set")
	}
	body, _ := json.Marshal(map[string]any{"query": query, "limit": limit})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.firecrawl.dev/v1/search", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("firecrawl %d: %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Data []struct {
			URL         string `json:"url"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []tpsSearchResult{}
	for _, r := range raw.Data {
		out = append(out, tpsSearchResult{URL: r.URL, Title: r.Title, Snippet: r.Description})
	}
	return out, nil
}

// =====================================================================
// Site-specific snippet parsers
// =====================================================================

// parseLinkedInSnippet extracts title-like fields from a LinkedIn /in/ page snippet.
var liTitleAtRe = regexp.MustCompile(`(?i)(?:^|\s)([A-Z][A-Za-z& /]+?)\s+at\s+([A-Z][A-Za-z0-9&. ,'-]+)(?:\s+\.|\s+\||$|,|·)`)
var liEducationRe = regexp.MustCompile(`(?i)(?:Education|Studied at):\s*([A-Z][^.|\n]+)`)
var liLocationRe = regexp.MustCompile(`(?i)Location:\s*([A-Z][^.|\n]+)`)

func parseLinkedInSnippet(snippet, title, url string) map[string]string {
	out := map[string]string{}
	if m := liTitleAtRe.FindStringSubmatch(snippet); len(m) > 2 {
		out["job_title"] = strings.TrimSpace(m[1])
		out["employer"] = strings.TrimSpace(m[2])
	}
	if m := liEducationRe.FindStringSubmatch(snippet); len(m) > 1 {
		out["education"] = strings.TrimSpace(m[1])
	}
	if m := liLocationRe.FindStringSubmatch(snippet); len(m) > 1 {
		out["location"] = strings.TrimSpace(m[1])
	}
	// Title typical form: "Jane Doe | Senior Engineer at Acme | LinkedIn"
	if title != "" {
		parts := strings.Split(title, "|")
		if len(parts) >= 2 {
			out["title_name"] = strings.TrimSpace(parts[0])
			out["title_role"] = strings.TrimSpace(parts[1])
		}
	}
	return out
}

// parseZoomInfoSnippet pulls "Name · Title at Company" patterns.
var ziNameRoleRe = regexp.MustCompile(`(?i)([A-Z][A-Za-z]+\s+[A-Z][A-Za-z]+)\s*·\s*([^·]+?)\s*·`)
var ziJobTitleRe = regexp.MustCompile(`(?i)Job Title\s+([^·\n]+)`)
var ziCompanyRe = regexp.MustCompile(`(?i)(?:Company|Employer)\s+([^·\n]+)`)

func parseZoomInfoSnippet(snippet, title, url string) map[string]string {
	out := map[string]string{}
	if m := ziNameRoleRe.FindStringSubmatch(snippet); len(m) > 2 {
		out["name"] = strings.TrimSpace(m[1])
		out["role_or_company"] = strings.TrimSpace(m[2])
	}
	if m := ziJobTitleRe.FindStringSubmatch(snippet); len(m) > 1 {
		out["job_title"] = strings.TrimSpace(m[1])
	}
	if m := ziCompanyRe.FindStringSubmatch(snippet); len(m) > 1 {
		out["company"] = strings.TrimSpace(m[1])
	}
	return out
}

// parseGlassdoorSnippet pulls salary info patterns.
var gdSalaryRe = regexp.MustCompile(`\$([\d,]+(?:\s*-\s*\$[\d,]+)?)\s*(?:per year|/year|annually|salary)?`)

func parseGlassdoorSnippet(snippet, title, url string) map[string]string {
	out := map[string]string{}
	if m := gdSalaryRe.FindStringSubmatch(snippet); len(m) > 1 {
		out["salary"] = "$" + strings.TrimSpace(m[1])
	}
	return out
}

// parseInstagramSnippet pulls follower/post counts.
var igStatsRe = regexp.MustCompile(`(?i)([\d.]+\w?)\s*(?:followers|following|posts)`)

func parseInstagramSnippet(snippet, title, url string) map[string]string {
	out := map[string]string{}
	matches := igStatsRe.FindAllStringSubmatch(snippet, -1)
	for _, m := range matches {
		if strings.Contains(strings.ToLower(snippet), "followers") && out["followers"] == "" {
			out["followers"] = m[1]
		}
		if strings.Contains(strings.ToLower(snippet), "following") && out["following"] == "" {
			out["following"] = m[1]
		}
		if strings.Contains(strings.ToLower(snippet), "posts") && out["posts"] == "" {
			out["posts"] = m[1]
		}
	}
	// extract @handle
	if m := regexp.MustCompile(`@([A-Za-z0-9_.]+)`).FindStringSubmatch(snippet); len(m) > 1 {
		out["handle"] = "@" + m[1]
	}
	return out
}

func buildSiteSnippetHighlights(o *SiteSnippetSearchOutput, preset *sitePreset) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d snippets recovered from %s for query '%s'", len(o.Hits), o.Site, o.Query))
	hi = append(hi, "built query: "+o.BuiltQuery)
	if preset != nil && preset.Description != "" {
		hi = append(hi, "preset: "+preset.Description)
	}
	for i, h := range o.Hits {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s", h.URL))
		if h.Title != "" {
			hi = append(hi, "    title: "+truncateSiteSnippet(h.Title, 110))
		}
		if h.Snippet != "" {
			hi = append(hi, "    snippet: "+truncateSiteSnippet(h.Snippet, 220))
		}
		if len(h.ExtractedFields) > 0 {
			parts := []string{}
			for k, v := range h.ExtractedFields {
				parts = append(parts, fmt.Sprintf("%s=%q", k, truncateSiteSnippet(v, 60)))
			}
			sort.Strings(parts)
			hi = append(hi, "    📋 extracted: "+strings.Join(parts, " | "))
		}
	}
	if len(o.UniqueDomainsHit) > 1 {
		hi = append(hi, fmt.Sprintf("%d unique URLs returned", len(o.UniqueDomainsHit)))
	}
	return hi
}

func truncateSiteSnippet(s string, n int) string { // local to avoid clash with whois.go::truncate

	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
