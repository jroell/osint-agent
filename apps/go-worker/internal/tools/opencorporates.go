package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type OCCompany struct {
	Name             string `json:"name"`
	CompanyNumber    string `json:"company_number,omitempty"`
	Jurisdiction     string `json:"jurisdiction_code,omitempty"`
	IncorporationDate string `json:"incorporation_date,omitempty"`
	DissolutionDate  string `json:"dissolution_date,omitempty"`
	CompanyType      string `json:"company_type,omitempty"`
	Status           string `json:"current_status,omitempty"`
	URL              string `json:"opencorporates_url,omitempty"`
	RegisteredAddr   string `json:"registered_address_in_full,omitempty"`
}

type OpenCorporatesOutput struct {
	Query      string      `json:"query"`
	TotalCount int         `json:"total_count"`
	Companies  []OCCompany `json:"companies"`
	Page       int         `json:"page"`
	PerPage    int         `json:"per_page"`
	Source     string      `json:"source"`
	TookMs     int64       `json:"tookMs"`
	Note       string      `json:"note,omitempty"`
}

// OpenCorporatesSearch queries the OpenCorporates v0.4 API for companies
// matching a name. Note: OpenCorporates retired the unauthenticated free tier
// in 2024 — an OPENCORPORATES_API_KEY is required for ALL queries (free
// trial available at https://opencorporates.com/api_accounts/new).
func OpenCorporatesSearch(ctx context.Context, input map[string]any) (*OpenCorporatesOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (company name to search)")
	}
	key := os.Getenv("OPENCORPORATES_API_KEY")
	if key == "" {
		return nil, errors.New("OPENCORPORATES_API_KEY env var required (the unauthenticated tier was retired in 2024; sign up for a free trial at https://opencorporates.com/api_accounts/new)")
	}
	page := 1
	if v, ok := input["page"].(float64); ok && v > 0 {
		page = int(v)
	}
	perPage := 30
	if v, ok := input["per_page"].(float64); ok && v > 0 {
		perPage = int(v)
		if perPage > 100 {
			perPage = 100
		}
	}
	juris, _ := input["jurisdiction_code"].(string)

	start := time.Now()
	args := url.Values{}
	args.Set("q", q)
	args.Set("page", fmt.Sprint(page))
	args.Set("per_page", fmt.Sprint(perPage))
	if juris != "" {
		args.Set("jurisdiction_code", juris)
	}
	args.Set("api_token", key)
	endpoint := "https://api.opencorporates.com/v0.4/companies/search?" + args.Encode()

	body, err := httpGetJSON(ctx, endpoint, 20*time.Second)
	if err != nil {
		return nil, fmt.Errorf("opencorporates fetch: %w", err)
	}
	var resp struct {
		Results struct {
			TotalCount int `json:"total_count"`
			Companies  []struct {
				Company OCCompany `json:"company"`
			} `json:"companies"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("opencorporates parse: %w", err)
	}
	out := &OpenCorporatesOutput{
		Query:      q,
		TotalCount: resp.Results.TotalCount,
		Page:       page,
		PerPage:    perPage,
		Source:     "opencorporates.com",
		TookMs:     time.Since(start).Milliseconds(),
	}
	for _, c := range resp.Results.Companies {
		out.Companies = append(out.Companies, c.Company)
	}
	return out, nil
}
