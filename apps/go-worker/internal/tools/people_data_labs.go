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
	"strings"
	"time"
)

// PeopleDataLabs wraps the People Data Labs Person Enrichment + Search APIs.
// Paid; REQUIRES `PEOPLE_DATA_LABS_API_KEY` (a.k.a. PDL).
//
// PDL maintains ~3B+ profiles with verified employment + education
// histories. Closes the "academic with this exact career path" class
// and B2B contact-data gaps.
//
// Modes:
//   - "person_enrich"  : enrich a person by email / phone / linkedin URL / name+company
//   - "person_search"  : Elasticsearch-style query against the PDL index
//   - "company_enrich" : enrich a company by website / linkedin URL / name
//   - "company_search" : ES-style company search
//
// Knowledge-graph: emits typed entities (kind: "person" | "organization")
// with stable PDL IDs.

type PDLPerson struct {
	PDLID       string           `json:"pdl_id,omitempty"`
	FullName    string           `json:"full_name,omitempty"`
	Emails      []string         `json:"emails,omitempty"`
	Phones      []string         `json:"phones,omitempty"`
	JobTitle    string           `json:"job_title,omitempty"`
	JobCompany  string           `json:"job_company_name,omitempty"`
	JobIndustry string           `json:"job_industry,omitempty"`
	Locations   []string         `json:"locations,omitempty"`
	LinkedIn    string           `json:"linkedin_url,omitempty"`
	GitHub      string           `json:"github_url,omitempty"`
	Twitter     string           `json:"twitter_url,omitempty"`
	Education   []map[string]any `json:"education,omitempty"`
	Experience  []map[string]any `json:"experience,omitempty"`
}

type PDLCompany struct {
	PDLID    string `json:"pdl_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Website  string `json:"website,omitempty"`
	Industry string `json:"industry,omitempty"`
	Size     string `json:"size,omitempty"`
	Founded  int    `json:"founded,omitempty"`
	HQ       string `json:"headquarters,omitempty"`
	Twitter  string `json:"twitter_url,omitempty"`
	LinkedIn string `json:"linkedin_url,omitempty"`
}

type PDLEntity struct {
	Kind        string         `json:"kind"`
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type PeopleDataLabsOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	People            []PDLPerson    `json:"people,omitempty"`
	Companies         []PDLCompany   `json:"companies,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []PDLEntity    `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func PeopleDataLabsLookup(ctx context.Context, input map[string]any) (*PeopleDataLabsOutput, error) {
	apiKey := os.Getenv("PEOPLE_DATA_LABS_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("PDL_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("PEOPLE_DATA_LABS_API_KEY (or PDL_API_KEY) not set; subscribe at peopledatalabs.com")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["person_es"] != nil:
			mode = "person_search"
		case input["company_es"] != nil:
			mode = "company_search"
		case input["company_website"] != nil || input["company_name"] != nil || input["company_linkedin"] != nil:
			mode = "company_enrich"
		default:
			mode = "person_enrich"
		}
	}
	out := &PeopleDataLabsOutput{Mode: mode, Source: "api.peopledatalabs.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(path string, params url.Values) (map[string]any, error) {
		params.Set("api_key", apiKey)
		u := "https://api.peopledatalabs.com/v5" + path + "?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("pdl: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 401 {
			return nil, fmt.Errorf("pdl: unauthorized — check API key")
		}
		if resp.StatusCode == 404 {
			// PDL returns 404 for "no person matched" enrichments
			return map[string]any{"status": 404}, nil
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("pdl HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("pdl decode: %w", err)
		}
		return m, nil
	}

	post := func(path string, body map[string]any) (map[string]any, error) {
		body["api_key"] = apiKey
		raw, _ := json.Marshal(body)
		u := "https://api.peopledatalabs.com/v5" + path
		req, _ := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("pdl: %w", err)
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("pdl HTTP %d: %s", resp.StatusCode, hfTruncate(string(respBody), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(respBody, &m); err != nil {
			return nil, fmt.Errorf("pdl decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "person_enrich":
		params := url.Values{}
		if v, ok := input["email"].(string); ok && v != "" {
			params.Set("email", v)
		}
		if v, ok := input["phone"].(string); ok && v != "" {
			params.Set("phone", v)
		}
		if v, ok := input["profile"].(string); ok && v != "" {
			params.Set("profile", v)
		}
		if v, ok := input["name"].(string); ok && v != "" {
			params.Set("name", v)
		}
		if v, ok := input["company"].(string); ok && v != "" {
			params.Set("company", v)
		}
		if len(params) == 0 {
			return nil, fmt.Errorf("at least one of email/phone/profile/(name+company) required")
		}
		params.Set("min_likelihood", "6")
		m, err := get("/person/enrich", params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if data, ok := m["data"].(map[string]any); ok {
			out.People = []PDLPerson{parsePDLPerson(data)}
		}
	case "person_search":
		es, ok := input["person_es"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input.person_es (Elasticsearch query) required")
		}
		size := 10
		if s, ok := input["size"].(float64); ok && s > 0 && s <= 100 {
			size = int(s)
		}
		body := map[string]any{
			"query": es,
			"size":  size,
		}
		m, err := post("/person/search", body)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if data, ok := m["data"].([]any); ok {
			for _, x := range data {
				if rec, ok := x.(map[string]any); ok {
					out.People = append(out.People, parsePDLPerson(rec))
				}
			}
		}
	case "company_enrich":
		params := url.Values{}
		if v, ok := input["company_website"].(string); ok && v != "" {
			params.Set("website", v)
		}
		if v, ok := input["company_name"].(string); ok && v != "" {
			params.Set("name", v)
		}
		if v, ok := input["company_linkedin"].(string); ok && v != "" {
			params.Set("profile", v)
		}
		if len(params) == 0 {
			return nil, fmt.Errorf("at least one of company_website/company_name/company_linkedin required")
		}
		m, err := get("/company/enrich", params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Companies = []PDLCompany{parsePDLCompany(m)}
	case "company_search":
		es, ok := input["company_es"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input.company_es required")
		}
		size := 10
		if s, ok := input["size"].(float64); ok && s > 0 && s <= 100 {
			size = int(s)
		}
		m, err := post("/company/search", map[string]any{"query": es, "size": size})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if data, ok := m["data"].([]any); ok {
			for _, x := range data {
				if rec, ok := x.(map[string]any); ok {
					out.Companies = append(out.Companies, parsePDLCompany(rec))
				}
			}
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.People) + len(out.Companies)
	out.Entities = pdlBuildEntities(out)
	out.HighlightFindings = pdlBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parsePDLPerson(m map[string]any) PDLPerson {
	p := PDLPerson{
		PDLID:       gtString(m, "id"),
		FullName:    gtString(m, "full_name"),
		JobTitle:    gtString(m, "job_title"),
		JobCompany:  gtString(m, "job_company_name"),
		JobIndustry: gtString(m, "job_industry"),
		LinkedIn:    gtString(m, "linkedin_url"),
		GitHub:      gtString(m, "github_url"),
		Twitter:     gtString(m, "twitter_url"),
	}
	if emails, ok := m["emails"].([]any); ok {
		for _, e := range emails {
			if rec, ok := e.(map[string]any); ok {
				if addr := gtString(rec, "address"); addr != "" {
					p.Emails = append(p.Emails, addr)
				}
			} else if s, ok := e.(string); ok {
				p.Emails = append(p.Emails, s)
			}
		}
	}
	if phones, ok := m["phone_numbers"].([]any); ok {
		for _, ph := range phones {
			if s, ok := ph.(string); ok {
				p.Phones = append(p.Phones, s)
			}
		}
	}
	if locs, ok := m["location_names"].([]any); ok {
		for _, l := range locs {
			if s, ok := l.(string); ok {
				p.Locations = append(p.Locations, s)
			}
		}
	}
	if edu, ok := m["education"].([]any); ok {
		for _, e := range edu {
			if rec, ok := e.(map[string]any); ok {
				p.Education = append(p.Education, rec)
			}
		}
	}
	if exp, ok := m["experience"].([]any); ok {
		for _, e := range exp {
			if rec, ok := e.(map[string]any); ok {
				p.Experience = append(p.Experience, rec)
			}
		}
	}
	return p
}

func parsePDLCompany(m map[string]any) PDLCompany {
	src := m
	if data, ok := m["data"].(map[string]any); ok {
		src = data
	} else if status, ok := m["status"].(float64); ok && status != 200 {
		// enrich responses have direct fields
	}
	c := PDLCompany{
		PDLID:    gtString(src, "id"),
		Name:     gtString(src, "name"),
		Website:  gtString(src, "website"),
		Industry: gtString(src, "industry"),
		Size:     gtString(src, "size"),
		Founded:  int(gtFloat(src, "founded")),
		LinkedIn: gtString(src, "linkedin_url"),
		Twitter:  gtString(src, "twitter_url"),
	}
	if loc, ok := src["headquarters_location"].(map[string]any); ok {
		c.HQ = gtString(loc, "name")
	}
	if c.HQ == "" {
		c.HQ = gtString(src, "location_name")
	}
	return c
}

func pdlBuildEntities(o *PeopleDataLabsOutput) []PDLEntity {
	ents := []PDLEntity{}
	for _, p := range o.People {
		ents = append(ents, PDLEntity{
			Kind: "person", ID: p.PDLID, Name: p.FullName, URL: p.LinkedIn,
			Description: fmt.Sprintf("%s @ %s", p.JobTitle, p.JobCompany),
			Attributes: map[string]any{
				"emails": p.Emails, "phones": p.Phones,
				"job_title": p.JobTitle, "job_company": p.JobCompany,
				"industry": p.JobIndustry, "locations": p.Locations,
				"github": p.GitHub, "twitter": p.Twitter,
				"experience_count": len(p.Experience),
				"education_count":  len(p.Education),
			},
		})
	}
	for _, c := range o.Companies {
		ents = append(ents, PDLEntity{
			Kind: "organization", ID: c.PDLID, Name: c.Name, URL: c.Website,
			Description: fmt.Sprintf("%s — %s — %s", c.Industry, c.Size, c.HQ),
			Attributes: map[string]any{
				"website": c.Website, "industry": c.Industry, "size": c.Size,
				"founded": c.Founded, "hq": c.HQ,
				"linkedin": c.LinkedIn, "twitter": c.Twitter,
			},
		})
	}
	return ents
}

func pdlBuildHighlights(o *PeopleDataLabsOutput) []string {
	hi := []string{fmt.Sprintf("✓ pdl %s: %d records", o.Mode, o.Returned)}
	for i, p := range o.People {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s @ %s [%s] — %s", p.FullName, p.JobCompany, p.JobTitle, p.LinkedIn))
		if len(p.Emails) > 0 {
			hi = append(hi, "    emails: "+strings.Join(p.Emails, ", "))
		}
	}
	for i, c := range o.Companies {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s (%s, founded %d) %s", c.Name, c.Industry, c.HQ, c.Founded, c.Website))
	}
	return hi
}
