package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type WaybackURL struct {
	URL          string `json:"url"`
	Path         string `json:"path"`
	FirstSeen    string `json:"first_seen"`
	LastSeen     string `json:"last_seen"`
	Captures     int    `json:"captures"`
	StatusCodes  []string `json:"status_codes,omitempty"`
	APIScore     int    `json:"api_score"`
}

type WaybackEndpointExtractOutput struct {
	Target           string       `json:"target"`
	TotalCaptures    int          `json:"total_captures"`
	UniqueURLs       int          `json:"unique_urls"`
	APIEndpoints     []WaybackURL `json:"api_endpoints"`     // score >= 5
	OtherURLs        []WaybackURL `json:"other_urls,omitempty"`
	UniqueAPIPaths   int          `json:"unique_api_paths"`
	Subdomains       []string     `json:"subdomains_observed,omitempty"`
	StatusBreakdown  map[string]int `json:"status_breakdown"`
	OldestCapture    string       `json:"oldest_capture,omitempty"`
	NewestCapture    string       `json:"newest_capture,omitempty"`
	Source           string       `json:"source"`
	TookMs           int64        `json:"tookMs"`
	Note             string       `json:"note,omitempty"`
}

// WaybackEndpointExtract queries the Wayback Machine's CDX API for ALL URLs
// ever archived under a target domain, then filters to those matching API
// patterns. Wayback's archive contains 858+ billion captures going back
// to 1996 — frequently exposes old endpoints that are still live but no
// longer linked from current UI. Classic bug-bounty technique: "what was
// here BEFORE the developers tightened things up?"
//
// Returns deduped URLs with first/last-seen timestamps, capture count
// (frequency = signal of importance), and API-likelihood score (same
// scoring as js_endpoint_extract for cross-tool comparability).
//
// Free, no API key. Subject to rate limiting on large targets.
func WaybackEndpointExtract(ctx context.Context, input map[string]any) (*WaybackEndpointExtractOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil, errors.New("input.target required (apex domain, e.g. 'vurvey.app')")
	}
	limit := 5000
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	matchType := "domain"
	if v, ok := input["match_type"].(string); ok && v != "" {
		matchType = v
	}
	// Optional: filter by mime-type prefix (e.g. "application/json" only).
	mimeFilter, _ := input["mime_prefix"].(string)
	// Optional: only include URLs returning 200 (default true).
	successOnly := true
	if v, ok := input["success_only"].(bool); ok {
		successOnly = v
	}

	start := time.Now()

	// Build CDX query.
	q := url.Values{}
	q.Set("url", target+"/*")
	q.Set("matchType", matchType)
	q.Set("output", "json")
	q.Set("fl", "timestamp,original,statuscode,mimetype")
	q.Set("limit", fmt.Sprint(limit))
	q.Set("collapse", "urlkey") // dedup by URL key (Wayback's normalized form)
	if successOnly {
		q.Set("filter", "statuscode:200")
	}
	endpoint := "https://web.archive.org/cdx/search/cdx?" + q.Encode()

	body, err := httpGetJSON(ctx, endpoint, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("wayback CDX: %w", err)
	}
	var rows [][]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("wayback parse: %w", err)
	}

	out := &WaybackEndpointExtractOutput{
		Target:          target,
		APIEndpoints:    []WaybackURL{},
		StatusBreakdown: map[string]int{},
		Source:          "web.archive.org/cdx (Wayback Machine, 858B+ captures)",
	}
	if len(rows) <= 1 {
		out.Note = "Wayback returned no captures for this domain pattern."
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// First row is header — skip it.
	out.TotalCaptures = len(rows) - 1
	urlAgg := map[string]*WaybackURL{}
	subdomains := map[string]struct{}{}
	for _, r := range rows[1:] {
		if len(r) < 2 {
			continue
		}
		ts, urlStr := r[0], r[1]
		statusCode := ""
		mime := ""
		if len(r) > 2 {
			statusCode = r[2]
		}
		if len(r) > 3 {
			mime = r[3]
		}
		// Optional mime filter.
		if mimeFilter != "" && !strings.HasPrefix(mime, mimeFilter) {
			continue
		}
		out.StatusBreakdown[statusCode]++

		// Extract subdomain.
		if u, err := url.Parse(urlStr); err == nil {
			h := strings.ToLower(u.Host)
			if h != target && strings.HasSuffix(h, "."+target) {
				subdomains[h] = struct{}{}
			}
		}

		agg, ok := urlAgg[urlStr]
		if !ok {
			pathPart := urlStr
			if u, err := url.Parse(urlStr); err == nil {
				pathPart = u.Path
				if u.RawQuery != "" {
					pathPart += "?" + u.RawQuery
				}
			}
			agg = &WaybackURL{
				URL: urlStr, Path: pathPart, FirstSeen: ts, LastSeen: ts, Captures: 0,
				APIScore: scoreAsAPI(urlStr),
			}
			urlAgg[urlStr] = agg
		}
		agg.Captures++
		if ts < agg.FirstSeen {
			agg.FirstSeen = ts
		}
		if ts > agg.LastSeen {
			agg.LastSeen = ts
		}
		if statusCode != "" {
			already := false
			for _, s := range agg.StatusCodes {
				if s == statusCode {
					already = true
					break
				}
			}
			if !already {
				agg.StatusCodes = append(agg.StatusCodes, statusCode)
			}
		}
	}
	out.UniqueURLs = len(urlAgg)

	apiPaths := map[string]struct{}{}
	for _, u := range urlAgg {
		if u.APIScore >= 5 {
			out.APIEndpoints = append(out.APIEndpoints, *u)
			apiPaths[u.Path] = struct{}{}
		} else {
			out.OtherURLs = append(out.OtherURLs, *u)
		}
	}
	out.UniqueAPIPaths = len(apiPaths)

	// Sort API endpoints: highest score first, then most-captured (signal of importance).
	sort.Slice(out.APIEndpoints, func(i, j int) bool {
		if out.APIEndpoints[i].APIScore != out.APIEndpoints[j].APIScore {
			return out.APIEndpoints[i].APIScore > out.APIEndpoints[j].APIScore
		}
		return out.APIEndpoints[i].Captures > out.APIEndpoints[j].Captures
	})
	sort.Slice(out.OtherURLs, func(i, j int) bool {
		return out.OtherURLs[i].Captures > out.OtherURLs[j].Captures
	})
	if len(out.OtherURLs) > 100 {
		out.OtherURLs = out.OtherURLs[:100]
	}

	for s := range subdomains {
		out.Subdomains = append(out.Subdomains, s)
	}
	sort.Strings(out.Subdomains)

	if len(out.APIEndpoints) > 0 {
		out.OldestCapture = out.APIEndpoints[0].FirstSeen
		out.NewestCapture = out.APIEndpoints[0].LastSeen
		// Find global oldest/newest across ALL URLs.
		for _, u := range urlAgg {
			if u.FirstSeen < out.OldestCapture {
				out.OldestCapture = u.FirstSeen
			}
			if u.LastSeen > out.NewestCapture {
				out.NewestCapture = u.LastSeen
			}
		}
	}

	// Filter regex — matches paths that look API-shaped even without scoring (extra surface).
	apiRe := regexp.MustCompile(`(?i)/(api|v\d+|graphql|gql|rest|rpc|admin|internal|private|oauth|auth|token)(/|\.json|\.xml|$)`)
	if matched := 0; len(apiRe.FindAllString(target, -1)) >= 0 {
		// (count only — used by note)
		for _, u := range urlAgg {
			if apiRe.MatchString(u.URL) {
				matched++
			}
		}
		if matched > 0 {
			out.Note = fmt.Sprintf("%d/%d unique URLs match API-shape regex; %d scored ≥5 (likely real APIs)", matched, out.UniqueURLs, len(out.APIEndpoints))
		}
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
