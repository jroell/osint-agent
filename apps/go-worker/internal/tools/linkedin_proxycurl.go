package tools

import (
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

// LinkedInProxycurl wraps Nubela Proxycurl LinkedIn endpoints. Paid;
// REQUIRES `PROXYCURL_API_KEY` env var. Ported from vurvey-api
// `linkedin-tools.ts`.
//
// Modes:
//   - "company_profile"      : full company profile by LinkedIn URL
//   - "company_employee_count" : just headcount + employees-on-LI
//   - "lookup_company_by_domain": resolve a corp domain to LI company
//   - "person_profile"       : full person profile by LinkedIn URL
//   - "lookup_person_by_email": resolve work email to LI profile
//   - "person_email"         : retrieve verified personal email for a LI profile
//   - "find_company_role"    : find people in a role at a company
//
// Knowledge-graph: emits typed entities (kind: "person" | "organization")
// with stable LinkedIn URLs.

type LIPerson struct {
	LinkedInURL     string `json:"linkedin_url"`
	FullName        string `json:"full_name"`
	Headline        string `json:"headline,omitempty"`
	Country         string `json:"country,omitempty"`
	City            string `json:"city,omitempty"`
	OccupationCount int    `json:"occupation_count,omitempty"`
	Email           string `json:"email,omitempty"`
}

type LICompany struct {
	LinkedInURL   string `json:"linkedin_url"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Website       string `json:"website,omitempty"`
	Industry      string `json:"industry,omitempty"`
	CompanySize   []int  `json:"company_size,omitempty"`
	Founded       int    `json:"founded_year,omitempty"`
	HQ            string `json:"hq,omitempty"`
	EmployeeCount int    `json:"employee_count,omitempty"`
}

type LIEntity struct {
	Kind        string         `json:"kind"`
	URL         string         `json:"url"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type LinkedInProxycurlOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Person            *LIPerson      `json:"person,omitempty"`
	Company           *LICompany     `json:"company,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []LIEntity     `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func LinkedInProxycurlLookup(ctx context.Context, input map[string]any) (*LinkedInProxycurlOutput, error) {
	apiKey := os.Getenv("PROXYCURL_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("PROXYCURL_API_KEY not set; required (paid via nubela.co/proxycurl)")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["domain"] != nil:
			mode = "lookup_company_by_domain"
		case input["work_email"] != nil:
			mode = "lookup_person_by_email"
		case input["person_url"] != nil:
			if input["want_email"] == true {
				mode = "person_email"
			} else {
				mode = "person_profile"
			}
		case input["company_url"] != nil:
			mode = "company_profile"
		case input["company"] != nil && input["role"] != nil:
			mode = "find_company_role"
		case input["url"] != nil:
			// Backwards-compat: original tool used `url` for person_profile.
			u, _ := input["url"].(string)
			input["person_url"] = u
			mode = "person_profile"
		default:
			return nil, fmt.Errorf("required: domain | work_email | person_url | company_url | company+role | url")
		}
	}
	out := &LinkedInProxycurlOutput{Mode: mode, Source: "nubela.co/proxycurl"}
	start := time.Now()
	cli := &http.Client{Timeout: 60 * time.Second}

	get := func(path string, params url.Values) (map[string]any, error) {
		u := "https://nubela.co/proxycurl/api" + path
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("proxycurl: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 401 {
			return nil, fmt.Errorf("proxycurl: unauthorized — check PROXYCURL_API_KEY")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("proxycurl HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("proxycurl decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "company_profile":
		u, _ := input["company_url"].(string)
		if u == "" {
			return nil, fmt.Errorf("input.company_url required")
		}
		out.Query = u
		params := url.Values{"url": []string{u}, "use_cache": []string{"if-present"}}
		m, err := get("/v2/linkedin/company", params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Company = parseLICompany(m, u)
	case "company_employee_count":
		u, _ := input["company_url"].(string)
		if u == "" {
			return nil, fmt.Errorf("input.company_url required")
		}
		out.Query = u
		m, err := get("/linkedin/company/employees/count", url.Values{"url": []string{u}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		count := int(gtFloat(m, "total_employee"))
		out.Company = &LICompany{LinkedInURL: u, EmployeeCount: count}
	case "lookup_company_by_domain":
		d, _ := input["domain"].(string)
		if d == "" {
			return nil, fmt.Errorf("input.domain required")
		}
		out.Query = d
		m, err := get("/linkedin/company/resolve", url.Values{"company_domain": []string{d}, "use_cache": []string{"if-present"}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		liURL := gtString(m, "url")
		out.Company = &LICompany{LinkedInURL: liURL, Name: gtString(m, "name")}
	case "person_profile":
		u, _ := input["person_url"].(string)
		if u == "" {
			return nil, fmt.Errorf("input.person_url required")
		}
		out.Query = u
		m, err := get("/v2/linkedin", url.Values{"url": []string{u}, "use_cache": []string{"if-present"}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Person = parseLIPerson(m, u)
	case "lookup_person_by_email":
		em, _ := input["work_email"].(string)
		if em == "" {
			return nil, fmt.Errorf("input.work_email required")
		}
		out.Query = em
		m, err := get("/linkedin/profile/resolve/email", url.Values{"work_email": []string{em}, "lookup_depth": []string{"deep"}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		profileURL := gtString(m, "url")
		if profileURL != "" {
			out.Person = &LIPerson{LinkedInURL: profileURL}
		}
	case "person_email":
		u, _ := input["person_url"].(string)
		if u == "" {
			return nil, fmt.Errorf("input.person_url required")
		}
		out.Query = u
		m, err := get("/contact-api/personal-email", url.Values{"linkedin_profile_url": []string{u}, "page_size": []string{"3"}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		emails, _ := m["personal_emails"].([]any)
		first := ""
		if len(emails) > 0 {
			if s, ok := emails[0].(string); ok {
				first = s
			}
		}
		out.Person = &LIPerson{LinkedInURL: u, Email: first}
	case "find_company_role":
		c, _ := input["company"].(string)
		r, _ := input["role"].(string)
		if c == "" || r == "" {
			return nil, fmt.Errorf("input.company and input.role required")
		}
		out.Query = c + ":" + r
		m, err := get("/find/company/role/", url.Values{"company": []string{c}, "role": []string{r}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		profileURL := gtString(m, "linkedin_profile_url")
		if profileURL != "" {
			out.Person = &LIPerson{LinkedInURL: profileURL, Headline: gtString(m, "role")}
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = 0
	if out.Person != nil {
		out.Returned++
	}
	if out.Company != nil {
		out.Returned++
	}
	out.Entities = liBuildEntities(out)
	out.HighlightFindings = liBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseLIPerson(m map[string]any, urlStr string) *LIPerson {
	p := &LIPerson{LinkedInURL: urlStr}
	first := gtString(m, "first_name")
	last := gtString(m, "last_name")
	p.FullName = strings.TrimSpace(first + " " + last)
	if p.FullName == "" {
		p.FullName = gtString(m, "full_name")
	}
	p.Headline = gtString(m, "headline")
	p.Country = gtString(m, "country")
	p.City = gtString(m, "city")
	if exp, ok := m["experiences"].([]any); ok {
		p.OccupationCount = len(exp)
	}
	return p
}

func parseLICompany(m map[string]any, urlStr string) *LICompany {
	c := &LICompany{
		LinkedInURL: urlStr,
		Name:        gtString(m, "name"),
		Description: gtString(m, "description"),
		Website:     gtString(m, "website"),
		Industry:    gtString(m, "industry"),
		Founded:     int(gtFloat(m, "founded_year")),
	}
	if hq, ok := m["hq"].(map[string]any); ok {
		parts := []string{}
		if x := gtString(hq, "city"); x != "" {
			parts = append(parts, x)
		}
		if x := gtString(hq, "state"); x != "" {
			parts = append(parts, x)
		}
		if x := gtString(hq, "country"); x != "" {
			parts = append(parts, x)
		}
		c.HQ = strings.Join(parts, ", ")
	}
	if cs, ok := m["company_size"].([]any); ok {
		for _, x := range cs {
			if n, ok := x.(float64); ok {
				c.CompanySize = append(c.CompanySize, int(n))
			}
		}
	}
	c.EmployeeCount = int(gtFloat(m, "company_size_on_linkedin"))
	return c
}

func liBuildEntities(o *LinkedInProxycurlOutput) []LIEntity {
	ents := []LIEntity{}
	if p := o.Person; p != nil {
		ents = append(ents, LIEntity{
			Kind: "person", URL: p.LinkedInURL, Name: p.FullName,
			Description: p.Headline,
			Attributes: map[string]any{
				"country": p.Country, "city": p.City,
				"experiences_count": p.OccupationCount,
				"email":             p.Email,
			},
		})
	}
	if c := o.Company; c != nil {
		ents = append(ents, LIEntity{
			Kind: "organization", URL: c.LinkedInURL, Name: c.Name,
			Description: c.Description,
			Attributes: map[string]any{
				"website": c.Website, "industry": c.Industry,
				"founded": c.Founded, "hq": c.HQ,
				"employee_count": c.EmployeeCount,
				"company_size":   c.CompanySize,
			},
		})
	}
	return ents
}

func liBuildHighlights(o *LinkedInProxycurlOutput) []string {
	hi := []string{fmt.Sprintf("✓ proxycurl %s: %d records", o.Mode, o.Returned)}
	if p := o.Person; p != nil {
		hi = append(hi, fmt.Sprintf("  • person %s — %s [%s, %s] %s",
			p.FullName, p.Headline, p.City, p.Country, p.LinkedInURL))
		if p.Email != "" {
			hi = append(hi, "    email: "+p.Email)
		}
	}
	if c := o.Company; c != nil {
		hi = append(hi, fmt.Sprintf("  • company %s — %s (%s, founded %d, %d emp) %s",
			c.Name, c.Industry, c.HQ, c.Founded, c.EmployeeCount, c.LinkedInURL))
	}
	return hi
}
