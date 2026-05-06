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

// CrunchbaseLookup wraps the Crunchbase Basic v4 REST API. Paid;
// REQUIRES `CRUNCHBASE_API_KEY`.
//
// Crunchbase is the canonical source for startup funding rounds,
// founder/investor relationships, acquisitions, and company executive
// histories. Closes the "company X CEO at acquisition + spouse Columbia
// MBA" class.
//
// Modes:
//   - "search_organizations" : keyword search for companies
//   - "organization_details" : full org record by permalink/uuid
//   - "search_people"        : keyword search for people
//   - "person_details"       : full person record
//   - "funding_rounds"       : list funding rounds for an org
//
// Knowledge-graph: emits typed entities (kind: "organization" | "person"
// | "funding_round") with stable Crunchbase permalinks.

type CBOrganization struct {
	UUID             string   `json:"uuid,omitempty"`
	Permalink        string   `json:"permalink,omitempty"`
	Name             string   `json:"name"`
	ShortDescription string   `json:"short_description,omitempty"`
	Founded          string   `json:"founded_on,omitempty"`
	HQ               string   `json:"hq,omitempty"`
	Industry         []string `json:"industries,omitempty"`
	Status           string   `json:"operating_status,omitempty"`
	NumFundingRounds int      `json:"num_funding_rounds,omitempty"`
	TotalFunding     string   `json:"total_funding_usd,omitempty"`
	URL              string   `json:"crunchbase_url"`
}

type CBPerson struct {
	UUID      string   `json:"uuid,omitempty"`
	Permalink string   `json:"permalink,omitempty"`
	Name      string   `json:"name"`
	Bio       string   `json:"bio,omitempty"`
	BirthDate string   `json:"born_on,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	URL       string   `json:"crunchbase_url"`
}

type CBFundingRound struct {
	UUID        string   `json:"uuid,omitempty"`
	Permalink   string   `json:"permalink,omitempty"`
	OrgName     string   `json:"organization_name,omitempty"`
	Type        string   `json:"investment_type,omitempty"`
	AnnouncedOn string   `json:"announced_on,omitempty"`
	AmountUSD   string   `json:"money_raised_usd,omitempty"`
	Investors   []string `json:"lead_investors,omitempty"`
	URL         string   `json:"crunchbase_url"`
}

type CBEntity struct {
	Kind        string         `json:"kind"`
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type CrunchbaseLookupOutput struct {
	Mode              string           `json:"mode"`
	Query             string           `json:"query,omitempty"`
	Returned          int              `json:"returned"`
	Organizations     []CBOrganization `json:"organizations,omitempty"`
	People            []CBPerson       `json:"people,omitempty"`
	FundingRounds     []CBFundingRound `json:"funding_rounds,omitempty"`
	Detail            map[string]any   `json:"detail,omitempty"`
	Entities          []CBEntity       `json:"entities"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
}

func CrunchbaseLookup(ctx context.Context, input map[string]any) (*CrunchbaseLookupOutput, error) {
	apiKey := os.Getenv("CRUNCHBASE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("CRUNCHBASE_API_KEY not set; subscribe at crunchbase.com")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["org_permalink"] != nil:
			if input["funding"] == true {
				mode = "funding_rounds"
			} else {
				mode = "organization_details"
			}
		case input["person_permalink"] != nil:
			mode = "person_details"
		case input["query"] != nil && input["who"] == "person":
			mode = "search_people"
		default:
			mode = "search_organizations"
		}
	}
	out := &CrunchbaseLookupOutput{Mode: mode, Source: "api.crunchbase.com/api/v4"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(path string, params url.Values) (map[string]any, error) {
		params.Set("user_key", apiKey)
		u := "https://api.crunchbase.com/api/v4" + path + "?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-cb-user-key", apiKey)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("crunchbase: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 401 {
			return nil, fmt.Errorf("crunchbase: unauthorized")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("crunchbase HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("crunchbase decode: %w", err)
		}
		return m, nil
	}

	post := func(path string, body map[string]any) (map[string]any, error) {
		raw, _ := json.Marshal(body)
		u := "https://api.crunchbase.com/api/v4" + path
		req, _ := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-cb-user-key", apiKey)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("crunchbase: %w", err)
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("crunchbase HTTP %d: %s", resp.StatusCode, hfTruncate(string(respBody), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(respBody, &m); err != nil {
			return nil, fmt.Errorf("crunchbase decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "search_organizations":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		body := map[string]any{
			"query": []map[string]any{
				{"type": "predicate", "field_id": "name", "operator_id": "contains", "values": []string{q}},
			},
			"limit":     20,
			"order":     []map[string]any{{"field_id": "rank_org", "sort": "asc"}},
			"field_ids": []string{"name", "short_description", "founded_on", "operating_status", "num_funding_rounds", "industries"},
		}
		m, err := post("/searches/organizations", body)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if entities, ok := m["entities"].([]any); ok {
			for _, e := range entities {
				rec, _ := e.(map[string]any)
				if rec == nil {
					continue
				}
				out.Organizations = append(out.Organizations, parseCBOrg(rec))
			}
		}
	case "organization_details":
		permalink, _ := input["org_permalink"].(string)
		if permalink == "" {
			return nil, fmt.Errorf("input.org_permalink required")
		}
		out.Query = permalink
		params := url.Values{
			"field_ids": []string{"name,short_description,founded_on,operating_status,num_funding_rounds,industries,total_funding_usd,location_identifiers"},
		}
		m, err := get("/entities/organizations/"+url.PathEscape(permalink), params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if data, ok := m["properties"].(map[string]any); ok {
			out.Organizations = []CBOrganization{parseCBOrg(map[string]any{"properties": data, "uuid": gtString(m, "uuid")})}
		}
	case "search_people":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		body := map[string]any{
			"query": []map[string]any{
				{"type": "predicate", "field_id": "name", "operator_id": "contains", "values": []string{q}},
			},
			"limit":     20,
			"field_ids": []string{"name", "short_description", "born_on"},
		}
		m, err := post("/searches/people", body)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if entities, ok := m["entities"].([]any); ok {
			for _, e := range entities {
				rec, _ := e.(map[string]any)
				if rec == nil {
					continue
				}
				out.People = append(out.People, parseCBPerson(rec))
			}
		}
	case "person_details":
		permalink, _ := input["person_permalink"].(string)
		if permalink == "" {
			return nil, fmt.Errorf("input.person_permalink required")
		}
		out.Query = permalink
		params := url.Values{
			"field_ids": []string{"name,short_description,born_on"},
		}
		m, err := get("/entities/people/"+url.PathEscape(permalink), params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if data, ok := m["properties"].(map[string]any); ok {
			out.People = []CBPerson{parseCBPerson(map[string]any{"properties": data, "uuid": gtString(m, "uuid")})}
		}
	case "funding_rounds":
		permalink, _ := input["org_permalink"].(string)
		if permalink == "" {
			return nil, fmt.Errorf("input.org_permalink required")
		}
		out.Query = permalink
		params := url.Values{
			"card_ids":  []string{"raised_funding_rounds"},
			"field_ids": []string{"name"},
		}
		m, err := get("/entities/organizations/"+url.PathEscape(permalink), params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if cards, ok := m["cards"].(map[string]any); ok {
			if rounds, ok := cards["raised_funding_rounds"].([]any); ok {
				for _, r := range rounds {
					if rec, ok := r.(map[string]any); ok {
						out.FundingRounds = append(out.FundingRounds, parseCBRound(rec))
					}
				}
			}
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Organizations) + len(out.People) + len(out.FundingRounds)
	out.Entities = cbBuildEntities(out)
	out.HighlightFindings = cbBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseCBOrg(m map[string]any) CBOrganization {
	uuid := gtString(m, "uuid")
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		props = m
	}
	identifier := gtString(props, "identifier")
	permalink := identifier
	if id, ok := m["identifier"].(map[string]any); ok {
		permalink = gtString(id, "permalink")
	}
	c := CBOrganization{
		UUID: uuid, Permalink: permalink,
		Name:             gtString(props, "name"),
		ShortDescription: gtString(props, "short_description"),
		Founded:          gtString(props, "founded_on"),
		Status:           gtString(props, "operating_status"),
		NumFundingRounds: int(gtFloat(props, "num_funding_rounds")),
		TotalFunding:     gtString(props, "total_funding_usd"),
		URL:              "https://www.crunchbase.com/organization/" + permalink,
	}
	if inds, ok := props["industries"].([]any); ok {
		for _, x := range inds {
			if s, ok := x.(string); ok {
				c.Industry = append(c.Industry, s)
			}
		}
	}
	return c
}

func parseCBPerson(m map[string]any) CBPerson {
	uuid := gtString(m, "uuid")
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		props = m
	}
	permalink := ""
	if id, ok := m["identifier"].(map[string]any); ok {
		permalink = gtString(id, "permalink")
	}
	return CBPerson{
		UUID: uuid, Permalink: permalink,
		Name:      gtString(props, "name"),
		Bio:       gtString(props, "short_description"),
		BirthDate: gtString(props, "born_on"),
		URL:       "https://www.crunchbase.com/person/" + permalink,
	}
}

func parseCBRound(m map[string]any) CBFundingRound {
	uuid := gtString(m, "uuid")
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		props = m
	}
	permalink := ""
	if id, ok := m["identifier"].(map[string]any); ok {
		permalink = gtString(id, "permalink")
	}
	return CBFundingRound{
		UUID: uuid, Permalink: permalink,
		Type:        gtString(props, "investment_type"),
		AnnouncedOn: gtString(props, "announced_on"),
		AmountUSD:   gtString(props, "money_raised_usd"),
		URL:         "https://www.crunchbase.com/funding_round/" + permalink,
	}
}

func cbBuildEntities(o *CrunchbaseLookupOutput) []CBEntity {
	ents := []CBEntity{}
	for _, c := range o.Organizations {
		ents = append(ents, CBEntity{
			Kind: "organization", ID: c.Permalink, Name: c.Name, URL: c.URL,
			Date: c.Founded, Description: c.ShortDescription,
			Attributes: map[string]any{
				"founded_on": c.Founded, "operating_status": c.Status,
				"num_funding_rounds": c.NumFundingRounds,
				"total_funding_usd":  c.TotalFunding,
				"industries":         c.Industry,
				"hq":                 c.HQ,
				"uuid":               c.UUID,
			},
		})
	}
	for _, p := range o.People {
		ents = append(ents, CBEntity{
			Kind: "person", ID: p.Permalink, Name: p.Name, URL: p.URL,
			Date: p.BirthDate, Description: p.Bio,
			Attributes: map[string]any{"uuid": p.UUID, "roles": p.Roles},
		})
	}
	for _, r := range o.FundingRounds {
		ents = append(ents, CBEntity{
			Kind: "funding_round", ID: r.Permalink, Name: r.Type, URL: r.URL,
			Date: r.AnnouncedOn,
			Attributes: map[string]any{
				"organization":    r.OrgName,
				"investment_type": r.Type,
				"amount_usd":      r.AmountUSD,
				"investors":       r.Investors,
				"uuid":            r.UUID,
			},
		})
	}
	return ents
}

func cbBuildHighlights(o *CrunchbaseLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ crunchbase %s: %d records", o.Mode, o.Returned)}
	for i, c := range o.Organizations {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s] — %s (founded %s, %d rounds, %s) %s",
			c.Name, c.Permalink, hfTruncate(c.ShortDescription, 60), c.Founded, c.NumFundingRounds, c.TotalFunding, c.URL))
	}
	for i, p := range o.People {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s] (%s) — %s", p.Name, p.Permalink, p.BirthDate, p.URL))
	}
	for i, r := range o.FundingRounds {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • round [%s] %s on %s — %s", r.Type, r.AmountUSD, r.AnnouncedOn, r.URL))
	}
	return hi
}
