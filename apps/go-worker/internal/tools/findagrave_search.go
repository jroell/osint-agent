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
	"sort"
	"strings"
	"time"
)

// re-export so stripTagsBody compiles even if not used elsewhere
var _ = regexp.MustCompile

type FAGMemorial struct {
	URL          string   `json:"url"`
	Name         string   `json:"name,omitempty"`
	Birth        string   `json:"birth,omitempty"`
	Death        string   `json:"death,omitempty"`
	BirthYear    string   `json:"birth_year,omitempty"`
	DeathYear    string   `json:"death_year,omitempty"`
	Location     string   `json:"burial_location,omitempty"`
	Relationships []FAGRelationship `json:"relationships,omitempty"`
	BiographyExcerpt string `json:"biography_excerpt,omitempty"`
}

type FAGRelationship struct {
	RelationshipType string `json:"relationship_type"` // spouse | parent | child | sibling | survived_by | predeceased_by
	OtherName        string `json:"other_name"`
	OtherURL         string `json:"other_url,omitempty"`
}

type FindAGraveOutput struct {
	Query           string        `json:"query"`
	LocationFilter  string        `json:"location_filter,omitempty"`
	TotalReturned   int           `json:"total_returned"`
	Memorials       []FAGMemorial `json:"memorials"`
	UniqueRelativeNames []string  `json:"unique_relative_names,omitempty"`
	HighlightFindings []string    `json:"highlight_findings"`
	Source          string        `json:"source"`
	TookMs          int64         `json:"tookMs"`
	Note            string        `json:"note,omitempty"`
}

var fagMemorialLinkRE = regexp.MustCompile(`href="(\/memorial\/(\d+)\/([a-z0-9-]+))"`)
var fagDateRangeRE = regexp.MustCompile(`(\d{1,2}\s+\w+\s+\d{4}|\d{4})\s*[–-]\s*(\d{1,2}\s+\w+\s+\d{4}|\d{4})`)
var fagBoldNameRE = regexp.MustCompile(`(?i)<a[^>]+href="/memorial/[^"]+"[^>]*>([^<]+)</a>`)
var fagFamilyHeaderRE = regexp.MustCompile(`(?i)<h\d[^>]*>(?:Family|Spouse|Spouses|Parents?|Children|Sibling|Half\s+Sibling|Survived[^<]*)<\/h\d>`)

// FindAGraveSearch queries findagrave.com via stealth_http (rnet/JA4+ TLS
// impersonation needed; FAG is Cloudflare-protected). Modes:
//   - "search_by_name" (default): list of memorial summaries matching a name
//     filter, optionally constrained to a US state or country
//   - "memorial_detail": full extraction of a memorial page including
//     parent/spouse/sibling/child relationships from the Family section
//
// Use case: genealogical OSINT. Obituary/memorial pages on FindAGrave often
// list relatives explicitly ("survived by daughter X married to Y") which
// is the killer relationship-discovery signal for personal investigations.
//
// Free, no key. Requires `stealth_http_fetch` (py-worker rnet) to bypass
// Cloudflare bot management.
func FindAGraveSearch(ctx context.Context, input map[string]any) (*FindAGraveOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "search_by_name"
	}

	start := time.Now()
	out := &FindAGraveOutput{Source: "findagrave.com"}

	switch mode {
	case "search_by_name":
		first, _ := input["first_name"].(string)
		last, _ := input["last_name"].(string)
		first = strings.TrimSpace(first)
		last = strings.TrimSpace(last)
		if last == "" {
			return nil, errors.New("input.last_name required for search_by_name")
		}
		out.Query = strings.TrimSpace(first + " " + last)

		location, _ := input["location"].(string)
		out.LocationFilter = location

		params := url.Values{}
		params.Set("firstname", first)
		params.Set("lastname", last)
		if location != "" {
			params.Set("location", location)
		}
		searchURL := "https://www.findagrave.com/memorial/search?" + params.Encode()
		body, err := stealthHTTPFetch(ctx, searchURL)
		if err != nil {
			return nil, fmt.Errorf("stealth fetch: %w", err)
		}
		out.Memorials = parseFAGSearchResults(body, 25)
		out.TotalReturned = len(out.Memorials)

	case "memorial_detail":
		memURL, _ := input["url"].(string)
		memURL = strings.TrimSpace(memURL)
		if memURL == "" {
			return nil, errors.New("input.url required for memorial_detail (e.g. https://www.findagrave.com/memorial/123/jane-doe)")
		}
		body, err := stealthHTTPFetch(ctx, memURL)
		if err != nil {
			return nil, fmt.Errorf("stealth fetch: %w", err)
		}
		mem := parseFAGMemorialDetail(body)
		mem.URL = memURL
		out.Memorials = []FAGMemorial{mem}
		out.TotalReturned = 1

	default:
		return nil, fmt.Errorf("unknown mode %q (use search_by_name or memorial_detail)", mode)
	}

	// Aggregate unique relative names
	seen := map[string]bool{}
	for _, m := range out.Memorials {
		for _, r := range m.Relationships {
			if r.OtherName != "" && !seen[r.OtherName] {
				seen[r.OtherName] = true
				out.UniqueRelativeNames = append(out.UniqueRelativeNames, r.OtherName)
			}
		}
	}
	sort.Strings(out.UniqueRelativeNames)

	// Highlights
	highlights := []string{
		fmt.Sprintf("%d memorials returned for query '%s'", out.TotalReturned, out.Query),
	}
	if len(out.UniqueRelativeNames) > 0 {
		head := out.UniqueRelativeNames
		if len(head) > 5 {
			head = head[:5]
		}
		highlights = append(highlights, fmt.Sprintf("%d unique relatives mentioned: %s%s",
			len(out.UniqueRelativeNames), strings.Join(head, ", "),
			func() string { if len(out.UniqueRelativeNames) > 5 { return "..." } ; return "" }()))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// parseFAGSearchResults extracts memorial summaries from a search results page.
func parseFAGSearchResults(body string, max int) []FAGMemorial {
	mems := []FAGMemorial{}
	seen := map[string]bool{}
	for _, m := range fagMemorialLinkRE.FindAllStringSubmatch(body, -1) {
		if len(m) < 4 {
			continue
		}
		path := m[1]
		if seen[path] {
			continue
		}
		seen[path] = true
		fullURL := "https://www.findagrave.com" + path
		// Best-effort: derive name from slug (e.g. "adam-erhardt-roell" → "Adam Erhardt Roell")
		slug := m[3]
		nameParts := strings.Split(slug, "-")
		titled := []string{}
		for _, p := range nameParts {
			if p == "" {
				continue
			}
			titled = append(titled, strings.ToUpper(p[:1])+p[1:])
		}
		mem := FAGMemorial{
			URL:  fullURL,
			Name: strings.Join(titled, " "),
		}
		mems = append(mems, mem)
		if len(mems) >= max {
			break
		}
	}
	return mems
}

// parseFAGMemorialDetail extracts a single memorial's facts including family
// relationships. FAG pages have stable structures; we look for the "Family
// Members" / "Spouse" / "Parents" / "Children" / "Siblings" sections.
func parseFAGMemorialDetail(body string) FAGMemorial {
	mem := FAGMemorial{}

	// Name from <h1 id="bio-name">
	if m := regexp.MustCompile(`(?is)<h1[^>]*id="bio-name"[^>]*>(.*?)</h1>`).FindStringSubmatch(body); len(m) > 1 {
		mem.Name = strings.TrimSpace(stripTagsBody(m[1]))
	} else if m := regexp.MustCompile(`(?is)<title>([^<]+) \([^)]+\) - Find a Grave Memorial`).FindStringSubmatch(body); len(m) > 1 {
		mem.Name = strings.TrimSpace(m[1])
	}

	// Birth / death from itemprop=birthDate / deathDate
	if m := regexp.MustCompile(`(?i)<time\s+id="birthDateLabel"[^>]*>([^<]+)</time>`).FindStringSubmatch(body); len(m) > 1 {
		mem.Birth = strings.TrimSpace(m[1])
	}
	if m := regexp.MustCompile(`(?i)<time\s+id="deathDateLabel"[^>]*>([^<]+)</time>`).FindStringSubmatch(body); len(m) > 1 {
		mem.Death = strings.TrimSpace(m[1])
	}
	// Years
	if y := regexp.MustCompile(`\b(19|20)\d{2}\b`).FindString(mem.Birth); y != "" {
		mem.BirthYear = y
	}
	if y := regexp.MustCompile(`\b(19|20)\d{2}\b`).FindString(mem.Death); y != "" {
		mem.DeathYear = y
	}

	// Burial location
	if m := regexp.MustCompile(`(?is)<a[^>]+id="cemeteryNameLabel"[^>]*>([^<]+)</a>`).FindStringSubmatch(body); len(m) > 1 {
		mem.Location = strings.TrimSpace(m[1])
	}

	// Biography excerpt
	if m := regexp.MustCompile(`(?is)<div[^>]+id="inscriptionValue"[^>]*>(.*?)</div>`).FindStringSubmatch(body); len(m) > 1 {
		mem.BiographyExcerpt = truncate(stripTagsBody(m[1]), 600)
	} else if m := regexp.MustCompile(`(?is)<p[^>]+id="fullBio"[^>]*>(.*?)</p>`).FindStringSubmatch(body); len(m) > 1 {
		mem.BiographyExcerpt = truncate(stripTagsBody(m[1]), 600)
	}

	// Family relationships — find section headers + parse the next ~2KB for memorial links
	for _, headerType := range []struct {
		Pattern, RelationType string
	}{
		{`(?i)>\s*Spouses?\s*<`, "spouse"},
		{`(?i)>\s*Parents?\s*<`, "parent"},
		{`(?i)>\s*Children\s*<`, "child"},
		{`(?i)>\s*(?:Half\s+)?Siblings?\s*<`, "sibling"},
	} {
		re := regexp.MustCompile(headerType.Pattern)
		if loc := re.FindStringIndex(body); loc != nil {
			// Look in next 3KB for memorial links
			endIdx := loc[1] + 3000
			if endIdx > len(body) {
				endIdx = len(body)
			}
			segment := body[loc[1]:endIdx]
			for _, m := range fagMemorialLinkRE.FindAllStringSubmatch(segment, -1) {
				if len(m) < 4 {
					continue
				}
				slug := m[3]
				nameParts := strings.Split(slug, "-")
				titled := []string{}
				for _, p := range nameParts {
					if p == "" {
						continue
					}
					titled = append(titled, strings.ToUpper(p[:1])+p[1:])
				}
				mem.Relationships = append(mem.Relationships, FAGRelationship{
					RelationshipType: headerType.RelationType,
					OtherName:        strings.Join(titled, " "),
					OtherURL:         "https://www.findagrave.com" + m[1],
				})
				if len(mem.Relationships) > 30 {
					break
				}
			}
		}
	}

	return mem
}

func stripTagsBody(s string) string {
	return strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " "))
}

// stealthHTTPFetch calls our py-worker stealth_http_fetch tool to retrieve
// a URL with realistic Chrome JA4+ TLS fingerprint, bypassing most
// Cloudflare/Imperva bot management.
//
// Production should switch to ADC + signed envelope worker auth; for now
// we go through the API at localhost:3030/mcp.
func stealthHTTPFetch(ctx context.Context, target string) (string, error) {
	mcpURL := os.Getenv("MCP_URL")
	if mcpURL == "" {
		mcpURL = "http://localhost:3030/mcp"
	}
	rpc := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "stealth_http_fetch",
			"arguments": map[string]any{
				"url":         target,
				"impersonate": "chrome",
			},
		},
	}
	bodyJSON, _ := json.Marshal(rpc)
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, mcpURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("mcp status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var parsed struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("mcp parse: %w", err)
	}
	if parsed.Result.IsError || len(parsed.Result.Content) == 0 {
		return "", fmt.Errorf("stealth_http_fetch failed")
	}
	var output struct {
		Body       string `json:"body"`
		StatusCode int    `json:"status_code"`
	}
	if err := json.Unmarshal([]byte(parsed.Result.Content[0].Text), &output); err != nil {
		return "", fmt.Errorf("output parse: %w", err)
	}
	return output.Body, nil
}
