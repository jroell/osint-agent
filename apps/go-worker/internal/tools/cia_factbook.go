package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CIAFactbookLookup fetches CIA World Factbook country pages from the
// open mirror at factbook.bz / factbook.io / data feeds. We use the
// publicly-mirrored JSON dataset at factbook.json.
//
// CIA Factbook is the canonical country-level reference for population,
// GDP, geography, government, military, and demographics. Useful for
// sanity-checking national-totals constraints (e.g., "1.8M manufacturing
// workers" against country labor force).
//
// Modes:
//   - "country" : fetch one country's full Factbook record by code
//   - "list"    : list all known country codes + names
//
// Knowledge-graph: emits typed entities (kind: "country") with stable
// CIA country codes as IDs.

type CIACountry struct {
	Code       string `json:"factbook_code"`
	Name       string `json:"name"`
	Population string `json:"population,omitempty"`
	Area       string `json:"area,omitempty"`
	Capital    string `json:"capital,omitempty"`
	Government string `json:"government,omitempty"`
	GDP        string `json:"gdp,omitempty"`
	LaborForce string `json:"labor_force,omitempty"`
	Languages  string `json:"languages,omitempty"`
	URL        string `json:"factbook_url"`
}

type CIAEntity struct {
	Kind        string         `json:"kind"`
	Code        string         `json:"factbook_code"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type CIAFactbookOutput struct {
	Mode              string       `json:"mode"`
	Query             string       `json:"query,omitempty"`
	Returned          int          `json:"returned"`
	Country           *CIACountry  `json:"country,omitempty"`
	Codes             []CIACountry `json:"codes,omitempty"`
	Entities          []CIAEntity  `json:"entities"`
	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
}

func CIAFactbookLookup(ctx context.Context, input map[string]any) (*CIAFactbookOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if input["code"] != nil || input["name"] != nil {
			mode = "country"
		} else {
			mode = "list"
		}
	}
	out := &CIAFactbookOutput{Mode: mode, Source: "github.com/factbook/factbook.json"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("cia factbook: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("cia factbook HTTP %d", resp.StatusCode)
		}
		return body, nil
	}

	switch mode {
	case "country":
		code, _ := input["code"].(string)
		code = strings.ToLower(strings.TrimSpace(code))
		if code == "" {
			name, _ := input["name"].(string)
			code = ciaCodeForName(name)
		}
		if code == "" {
			return nil, fmt.Errorf("input.code or input.name required (e.g. 'us', 'United States')")
		}
		out.Query = code

		// CIA Factbook regions: africa, central-america-n-caribbean,
		// east-n-southeast-asia, europe, middle-east, north-america,
		// oceania, south-america, central-asia, south-asia. Plain code
		// without region only works for codes mapped 1:1; we try each
		// region in turn.
		regions := []string{"africa", "central-america-n-caribbean", "east-n-southeast-asia",
			"europe", "middle-east", "north-america", "oceania",
			"south-america", "central-asia", "south-asia",
			"australia-oceania", "antarctica"}
		var country map[string]any
		var u string
		for _, r := range regions {
			u = fmt.Sprintf("https://raw.githubusercontent.com/factbook/factbook.json/master/%s/%s.json", r, code)
			body, err := get(u)
			if err == nil {
				if json.Unmarshal(body, &country) == nil && country != nil {
					break
				}
			}
		}
		if country == nil {
			return nil, fmt.Errorf("cia factbook: no country found for code %q", code)
		}
		c := parseCIACountry(country, code)
		c.URL = u
		out.Country = &c

	case "list":
		// Hardcoded code → name map (factbook codes); kept short for memory size.
		// Full canonical list lives at the github mirror; this is a useful
		// quick-lookup for the most common queries.
		out.Codes = ciaWellKnownCodes()

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Codes)
	if out.Country != nil {
		out.Returned = 1
	}
	out.Entities = ciaBuildEntities(out)
	out.HighlightFindings = ciaBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseCIACountry(m map[string]any, code string) CIACountry {
	c := CIACountry{Code: code}
	if intro, ok := m["Introduction"].(map[string]any); ok {
		if bg, ok := intro["Background"].(map[string]any); ok {
			c.Name = gtString(bg, "country_name") // rarely present
		}
	}
	// People and society
	if ps, ok := m["People and Society"].(map[string]any); ok {
		if pop, ok := ps["Population"].(map[string]any); ok {
			c.Population = gtString(pop, "text")
		}
		if langs, ok := ps["Languages"].(map[string]any); ok {
			c.Languages = gtString(langs, "text")
		}
	}
	// Geography
	if g, ok := m["Geography"].(map[string]any); ok {
		if a, ok := g["Area"].(map[string]any); ok {
			if total, ok := a["total"].(map[string]any); ok {
				c.Area = gtString(total, "text")
			}
		}
	}
	// Government
	if gov, ok := m["Government"].(map[string]any); ok {
		if cap, ok := gov["Capital"].(map[string]any); ok {
			if name, ok := cap["name"].(map[string]any); ok {
				c.Capital = gtString(name, "text")
			}
		}
		if gt, ok := gov["Government type"].(map[string]any); ok {
			c.Government = gtString(gt, "text")
		}
	}
	// Economy
	if ec, ok := m["Economy"].(map[string]any); ok {
		if gdp, ok := ec["GDP (purchasing power parity)"].(map[string]any); ok {
			c.GDP = gtString(gdp, "text")
		}
		if lf, ok := ec["Labor force"].(map[string]any); ok {
			c.LaborForce = gtString(lf, "text")
		}
	}
	if c.Name == "" {
		c.Name = strings.ToUpper(code)
	}
	return c
}

func ciaWellKnownCodes() []CIACountry {
	pairs := [][2]string{
		{"us", "United States"}, {"uk", "United Kingdom"}, {"ca", "Canada"},
		{"fr", "France"}, {"gm", "Germany"}, {"it", "Italy"}, {"sp", "Spain"},
		{"ja", "Japan"}, {"ch", "China"}, {"in", "India"}, {"as", "Australia"},
		{"br", "Brazil"}, {"mx", "Mexico"}, {"rs", "Russia"}, {"ks", "South Korea"},
	}
	out := []CIACountry{}
	for _, p := range pairs {
		out = append(out, CIACountry{Code: p[0], Name: p[1]})
	}
	return out
}

func ciaCodeForName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, c := range ciaWellKnownCodes() {
		if strings.ToLower(c.Name) == n {
			return c.Code
		}
	}
	return ""
}

func ciaBuildEntities(o *CIAFactbookOutput) []CIAEntity {
	ents := []CIAEntity{}
	if c := o.Country; c != nil {
		desc := ""
		if c.Capital != "" {
			desc = "Capital: " + c.Capital
		}
		ents = append(ents, CIAEntity{
			Kind: "country", Code: c.Code, Name: c.Name, URL: c.URL,
			Description: desc,
			Attributes: map[string]any{
				"population":  c.Population,
				"area":        c.Area,
				"capital":     c.Capital,
				"government":  c.Government,
				"gdp":         c.GDP,
				"labor_force": c.LaborForce,
				"languages":   c.Languages,
			},
		})
	}
	for _, c := range o.Codes {
		ents = append(ents, CIAEntity{
			Kind: "country", Code: c.Code, Name: c.Name,
			Attributes: map[string]any{"role": "list_entry"},
		})
	}
	return ents
}

func ciaBuildHighlights(o *CIAFactbookOutput) []string {
	hi := []string{fmt.Sprintf("✓ cia factbook %s: %d records", o.Mode, o.Returned)}
	if c := o.Country; c != nil {
		hi = append(hi, fmt.Sprintf("  • %s [%s] capital=%s gov=%s",
			c.Name, c.Code, c.Capital, hfTruncate(c.Government, 60)))
		if c.Population != "" {
			hi = append(hi, "    pop: "+hfTruncate(c.Population, 100))
		}
		if c.LaborForce != "" {
			hi = append(hi, "    labor: "+hfTruncate(c.LaborForce, 100))
		}
	}
	for i, c := range o.Codes {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s", c.Code, c.Name))
	}
	return hi
}
