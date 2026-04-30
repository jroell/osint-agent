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
	"strconv"
	"strings"
	"time"
)

// TPSPerson is one person record assembled from search-engine snippets.
type TPSPerson struct {
	URL              string   `json:"url"`
	PersonID         string   `json:"person_id,omitempty"`
	Name             string   `json:"name,omitempty"`
	Age              int      `json:"age,omitempty"`
	BornMonthYear    string   `json:"born_month_year,omitempty"`
	CurrentAddress   string   `json:"current_address,omitempty"`
	City             string   `json:"city,omitempty"`
	State            string   `json:"state,omitempty"`
	LivedSince       string   `json:"lived_since,omitempty"`
	PreviousAddresses []string `json:"previous_addresses,omitempty"`
	Relatives        []string `json:"relatives,omitempty"`
	Associates       []string `json:"associates,omitempty"`
	Phones           []string `json:"phones,omitempty"`
	Emails           []string `json:"emails,omitempty"`
	SnippetSource    string   `json:"snippet_source,omitempty"` // tavily | firecrawl
	RawSnippet       string   `json:"raw_snippet,omitempty"`
}

// TPSLookupOutput is the response.
type TPSLookupOutput struct {
	Query              string      `json:"query"`
	City               string      `json:"city,omitempty"`
	State              string      `json:"state,omitempty"`
	People             []TPSPerson `json:"people"`
	UniqueRelatives    []string    `json:"unique_relatives_aggregated,omitempty"`
	UniqueAddresses    []string    `json:"unique_addresses_aggregated,omitempty"`
	HighlightFindings  []string    `json:"highlight_findings"`
	Source             string      `json:"source"`
	Note               string      `json:"note,omitempty"`
	TookMs             int64       `json:"tookMs"`
}

// TruePeopleSearchLookup queries truepeoplesearch.com indirectly via search-
// engine snippets. The live TPS site is aggressively Cloudflare-protected and
// requires JS execution + a session cookie that even firecrawl's stealth mode
// fails to defeat. However, search engines (Tavily, Google) have indexed TPS
// pages server-side and their result snippets contain the same structured
// data: name, age, current address, "lived since" date, and crucially the
// **relatives/associates list** ("X has been associated with Name1, Name2
// and others").
//
// This tool:
//  1. Issues a Tavily search with `site:truepeoplesearch.com NAME CITY STATE`
//  2. Parses each result snippet for: age, address, lived-since date, relatives.
//  3. Aggregates relatives across all returned hits — the killer feature for
//     family-tree OSINT (e.g. "find a person's father-in-law" requires
//     enumerating spouse → spouse's parents).
//
// Free tier requires TAVILY_API_KEY (which the user already has). Falls back
// to firecrawl_search if Tavily is unavailable.
//
// Why this matters for ER:
//  - TPS aggregates from public records: voter rolls, property records,
//    court filings, marriage records, etc. Their relatives-list is one of
//    the few easily-queryable family-tree surfaces in the catalog.
//  - The "father-in-law" stress test from iter-36 got 6/7 hops. This tool
//    closes the family-relationship gap.
func TruePeopleSearchLookup(ctx context.Context, input map[string]any) (*TPSLookupOutput, error) {
	name, _ := input["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("input.name required (e.g. 'Jason Roell')")
	}
	city, _ := input["city"].(string)
	city = strings.TrimSpace(city)
	state, _ := input["state"].(string)
	state = strings.TrimSpace(state)
	limit := 10
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 30 {
		limit = int(v)
	}

	out := &TPSLookupOutput{
		Query:  name,
		City:   city,
		State:  state,
		Source: "truepeoplesearch.com (via Tavily search-engine snippets — direct fetch is Cloudflare-blocked)",
	}
	start := time.Now()

	// Build the search query
	q := fmt.Sprintf(`site:truepeoplesearch.com "%s"`, name)
	if city != "" {
		q += " " + city
	}
	if state != "" {
		q += " " + state
	}

	// Try Tavily
	results, source, err := tpsSearchTavily(ctx, q, limit)
	if err != nil || len(results) == 0 {
		// Fallback to firecrawl_search if Tavily is missing/empty
		fcResults, fcErr := tpsSearchFirecrawl(ctx, q, limit)
		if fcErr == nil && len(fcResults) > 0 {
			results = fcResults
			source = "firecrawl"
			err = nil
		}
	}
	if err != nil && len(results) == 0 {
		return nil, err
	}
	if len(results) == 0 {
		out.Note = fmt.Sprintf("no truepeoplesearch.com results indexed for '%s' in %s, %s — try a more specific query or a different name spelling", name, city, state)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// Parse each snippet into a person record
	for _, r := range results {
		// Filter to actual person pages (TPS person URLs match /find/person/ID)
		if !strings.Contains(r.URL, "/find/person/") {
			continue
		}
		p := parseTPSSnippet(r.URL, r.Snippet)
		// Improve name extraction with title fallback
		if p.Name == "" || isCommonPrefixWord(p.Name) {
			if r.Title != "" {
				if m := tpsTitleRe.FindStringSubmatch(r.Title); len(m) > 1 {
					p.Name = strings.TrimSpace(m[1])
				}
			}
		}
		// Try snippet leading-name pattern next
		if p.Name == "" || isCommonPrefixWord(p.Name) {
			if m := tpsNameInSnippetRe.FindStringSubmatch(r.Snippet); len(m) > 1 {
				p.Name = strings.TrimSpace(m[1])
			}
		}
		// Try anchor-pattern next ("residence of NAME" / "associated with NAME")
		if p.Name == "" || isCommonPrefixWord(p.Name) {
			if m := tpsAnchorPersonRe.FindStringSubmatch(r.Snippet); len(m) > 1 {
				p.Name = strings.TrimSpace(m[1])
			}
		}
		p.SnippetSource = source
		out.People = append(out.People, p)
	}

	// Aggregations
	relSet := map[string]struct{}{}
	addrSet := map[string]struct{}{}
	for _, p := range out.People {
		for _, r := range p.Relatives {
			relSet[r] = struct{}{}
		}
		for _, r := range p.Associates {
			relSet[r] = struct{}{}
		}
		if p.CurrentAddress != "" {
			addrSet[p.CurrentAddress] = struct{}{}
		}
		for _, a := range p.PreviousAddresses {
			addrSet[a] = struct{}{}
		}
	}
	for r := range relSet {
		out.UniqueRelatives = append(out.UniqueRelatives, r)
	}
	sort.Strings(out.UniqueRelatives)
	for a := range addrSet {
		out.UniqueAddresses = append(out.UniqueAddresses, a)
	}
	sort.Strings(out.UniqueAddresses)

	out.HighlightFindings = buildTPSHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

type tpsSearchResult struct {
	URL     string
	Title   string
	Snippet string
}

// tpsExtractNameFromTitle pulls the person name from typical TPS title format:
// "Jason W Roeller (45) - Cincinnati, OH | TruePeopleSearch"
// "Jennifer Horn - Public Records & Background ..."
var tpsTitleRe = regexp.MustCompile(`^([A-Z][A-Za-z'.-]+(?:\s+[A-Z][A-Za-z'.-]+){0,4})(?:\s*\(|\s+-\s+|\s+\|\s+)`)
// Also try snippet-leading "[Name] is X years old" / "[Name] has resided"
var tpsNameInSnippetRe = regexp.MustCompile(`(?m)^([A-Z][A-Za-z'.-]+(?:\s+[A-Z]\.?)?(?:\s+[A-Z][A-Za-z'.-]+){1,3})\s+(?:is\s+\d+\s+years|has\s+resided|was\s+born|currently\s+lives|address\s+is|associated\s+with)`)
var tpsAnchorPersonRe = regexp.MustCompile(`(?:reach|associated with|residence of|address for|family of|relatives of)\s+([A-Z][A-Za-z'.-]+(?:\s+[A-Z]\.?)?(?:\s+[A-Z][A-Za-z'.-]+){1,3})\b`)

func tpsSearchTavily(ctx context.Context, query string, limit int) ([]tpsSearchResult, string, error) {
	apiKey := os.Getenv("TAVILY_API_KEY")
	if apiKey == "" {
		return nil, "", fmt.Errorf("TAVILY_API_KEY not set")
	}
	body, _ := json.Marshal(map[string]any{
		"api_key":          apiKey,
		"query":            query,
		"max_results":      limit,
		"include_raw_content": false,
		"include_images":   false,
		"search_depth":     "basic",
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("tavily %d: %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, "", err
	}
	out := []tpsSearchResult{}
	for _, r := range raw.Results {
		out = append(out, tpsSearchResult{URL: r.URL, Title: r.Title, Snippet: r.Content})
	}
	return out, "tavily", nil
}

func tpsSearchFirecrawl(ctx context.Context, query string, limit int) ([]tpsSearchResult, error) {
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FIRECRAWL_API_KEY not set")
	}
	body, _ := json.Marshal(map[string]any{
		"query": query,
		"limit": limit,
	})
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

// regex patterns for snippet parsing
var (
	tpsRePersonID  = regexp.MustCompile(`/find/person/([a-z0-9]+)`)
	tpsReAge       = regexp.MustCompile(`(?i)(?:is|At|At least|approximately) (\d{1,3}) years? old`)
	tpsReBornMY    = regexp.MustCompile(`(?i)was born in ([A-Za-z]+ \d{4})`)
	tpsReAddress   = regexp.MustCompile(`(?i)(?:address is|resided at|lives at|currently lives at|has resided at) ([0-9][^.]+?(?:\bin\b|,)\s+[A-Z][a-z]+(?:\s+[A-Z][a-z]+)*,\s*[A-Z]{2})`)
	tpsReLivedSince = regexp.MustCompile(`(?i)(?:lived|resided|has lived|since|moved (?:in|to)) since ([A-Za-z]+ \d{4})|since ([A-Za-z]+ \d{4})`)
	tpsReAssociated = regexp.MustCompile(`(?i)(?:has been associated with|is related to|family includes|relatives include|associates include) ([A-Z][^.]+?)(?:\s*(?:and others|\.|$))`)
	tpsRePhone      = regexp.MustCompile(`\(?\d{3}\)?\s*[-.]?\s*\d{3}\s*[-.]?\s*\d{4}`)
	tpsReEmail      = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	tpsReNameTitle  = regexp.MustCompile(`^([A-Z][a-z]+(?:\s+[A-Z]\.?)?(?:\s+[A-Z][a-z]+){0,3})\b`)
	// Final extraction of a person's name from the title (TPS title format: "Name Age - City State - TruePeopleSearch")
	tpsReTitleName  = regexp.MustCompile(`^([^,|\-]+?)(?:[,|\-]|$)`)
)

func parseTPSSnippet(url, snippet string) TPSPerson {
	p := TPSPerson{URL: url, RawSnippet: snippet}
	if m := tpsRePersonID.FindStringSubmatch(url); len(m) > 1 {
		p.PersonID = m[1]
	}
	if m := tpsReNameTitle.FindStringSubmatch(snippet); len(m) > 1 {
		p.Name = strings.TrimSpace(m[1])
	}
	if m := tpsReAge.FindStringSubmatch(snippet); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			p.Age = n
		}
	}
	if m := tpsReBornMY.FindStringSubmatch(snippet); len(m) > 1 {
		p.BornMonthYear = strings.TrimSpace(m[1])
	}
	if m := tpsReAddress.FindStringSubmatch(snippet); len(m) > 1 {
		full := strings.TrimSpace(m[1])
		// extract city + state
		if csIdx := strings.LastIndex(full, ","); csIdx > 0 {
			p.CurrentAddress = strings.TrimSpace(full[:csIdx])
			cs := strings.TrimSpace(full[csIdx+1:])
			parts := strings.SplitN(cs, " ", 2)
			if len(parts) == 2 {
				// Handle "in CityName, ST" form (the regex captures "in Cincinnati, OH")
				if strings.HasPrefix(strings.ToLower(p.CurrentAddress), "in ") {
					cityPart := strings.TrimPrefix(strings.ToLower(p.CurrentAddress), "in ")
					p.City = strings.Title(cityPart)
					p.CurrentAddress = ""
				}
			}
		}
		if p.CurrentAddress == "" {
			// Try simpler: "ADDRESS in CITY, STATE"
			if inIdx := strings.LastIndex(strings.ToLower(full), " in "); inIdx > 0 {
				p.CurrentAddress = strings.TrimSpace(full[:inIdx])
				rest := strings.TrimSpace(full[inIdx+4:])
				if csIdx := strings.LastIndex(rest, ","); csIdx > 0 {
					p.City = strings.TrimSpace(rest[:csIdx])
					p.State = strings.TrimSpace(rest[csIdx+1:])
				}
			}
		}
	}
	if m := tpsReLivedSince.FindStringSubmatch(snippet); len(m) > 1 {
		// The regex has two alternation groups; pick the non-empty one
		for i := 1; i < len(m); i++ {
			if m[i] != "" {
				p.LivedSince = strings.TrimSpace(m[i])
				break
			}
		}
	}
	if m := tpsReAssociated.FindStringSubmatch(snippet); len(m) > 1 {
		raw := strings.TrimSpace(m[1])
		// Split on "," and "and"
		parts := regexp.MustCompile(`,\s*|\s+and\s+`).Split(raw, -1)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			// Avoid sentence-fragments — names should be 2-4 words, capitalized
			if part == "" || len(part) > 60 {
				continue
			}
			words := strings.Fields(part)
			if len(words) < 2 || len(words) > 5 {
				continue
			}
			ok := true
			for _, w := range words {
				if len(w) > 0 && !(w[0] >= 'A' && w[0] <= 'Z') {
					ok = false
					break
				}
			}
			if ok {
				p.Relatives = append(p.Relatives, part)
			}
		}
	}
	for _, ph := range tpsRePhone.FindAllString(snippet, -1) {
		p.Phones = append(p.Phones, ph)
	}
	for _, em := range tpsReEmail.FindAllString(snippet, -1) {
		p.Emails = append(p.Emails, em)
	}
	return p
}

// isCommonPrefixWord catches false-positive name extractions like "Born", "This", "Property",
// "Phone", "Email" that get matched by the name-prefix regex on snippet starts.
func isCommonPrefixWord(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	switch low {
	case "born", "this", "property", "phone", "email", "address", "page", "the", "a", "see", "view", "find", "search":
		return true
	}
	// single-word "names" are usually noise
	return !strings.Contains(low, " ")
}

func buildTPSHighlights(o *TPSLookupOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d truepeoplesearch.com person records recovered (via search-engine snippets — live page is Cloudflare-blocked)", len(o.People)))
	if o.City != "" || o.State != "" {
		hi = append(hi, fmt.Sprintf("query scope: name='%s' city='%s' state='%s'", o.Query, o.City, o.State))
	}
	for i, p := range o.People {
		if i >= 5 {
			break
		}
		desc := []string{}
		if p.Name != "" {
			desc = append(desc, p.Name)
		}
		if p.Age > 0 {
			desc = append(desc, fmt.Sprintf("age %d", p.Age))
		}
		if p.CurrentAddress != "" {
			desc = append(desc, p.CurrentAddress)
		} else if p.City != "" {
			desc = append(desc, p.City)
		}
		if p.LivedSince != "" {
			desc = append(desc, "since "+p.LivedSince)
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s", strings.Join(desc, ", "), p.URL))
		if len(p.Relatives) > 0 {
			hi = append(hi, "    🌳 relatives: "+strings.Join(p.Relatives, ", "))
		}
	}
	if len(o.UniqueRelatives) > 0 {
		hi = append(hi, fmt.Sprintf("📛 %d unique relatives/associates aggregated across all hits: %s", len(o.UniqueRelatives), strings.Join(o.UniqueRelatives, ", ")))
	}
	return hi
}
