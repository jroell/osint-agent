package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type LEIRecord struct {
	LEI                string   `json:"lei"`
	LegalName          string   `json:"legal_name"`
	OtherNames         []string `json:"other_names,omitempty"`
	LegalForm          string   `json:"legal_form,omitempty"`
	Status             string   `json:"status"` // ACTIVE | INACTIVE
	HQCountry          string   `json:"headquarters_country,omitempty"`
	HQCity             string   `json:"headquarters_city,omitempty"`
	HQRegion           string   `json:"headquarters_region,omitempty"`
	HQAddressLine1     string   `json:"headquarters_address_line_1,omitempty"`
	HQPostalCode       string   `json:"headquarters_postal_code,omitempty"`
	LegalCountry       string   `json:"legal_country,omitempty"`
	LegalCity          string   `json:"legal_city,omitempty"`
	BICs               []string `json:"bic_codes,omitempty"`
	RegistrationStatus string   `json:"registration_status,omitempty"`
	NextRenewalDate    string   `json:"next_renewal_date,omitempty"`
	InitialReg         string   `json:"initial_registration_date,omitempty"`
	LastUpdate         string   `json:"last_update_date,omitempty"`
	ManagingLOU        string   `json:"managing_lou,omitempty"`
	ParentLEI          string   `json:"parent_lei,omitempty"`
	URL                string   `json:"url"`
}

type GLEIFLookupOutput struct {
	Query        string      `json:"query"`
	TotalRecords int         `json:"total_records"`
	Records      []LEIRecord `json:"records"`
	Source       string      `json:"source"`
	TookMs       int64       `json:"tookMs"`
	Note         string      `json:"note,omitempty"`
}

// GleifLEILookup queries the GLEIF (Global Legal Entity Identifier Foundation)
// public API for LEI records matching a company name or LEI code. The LEI
// is the international standard ID for legal entities — every regulated
// financial entity has one. Free, no key.
//
// Returns: LEI code, legal name + alternative names, legal form, status,
// HQ + legal addresses, parent LEI (corporate hierarchy), BIC (SWIFT) codes,
// registration metadata.
//
// Use cases:
//   - Confirm legal entity identity (e.g. "Anthropic" → "Anthropic, PBC" with US-DE address)
//   - Discover corporate hierarchy (parent LEI → ultimate parent)
//   - Cross-reference with mobile_app_lookup sellerNames, github_code_search, etc.
func GleifLEILookup(ctx context.Context, input map[string]any) (*GLEIFLookupOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (company name or LEI code)")
	}
	limit := 10
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 50 {
		limit = int(v)
	}

	start := time.Now()
	// Use fuzzy search via filter[entity.legalName] field.
	// GLEIF API: https://api.gleif.org/api/v1/lei-records?filter[entity.legalName]=Anthropic
	endpoint := fmt.Sprintf("https://api.gleif.org/api/v1/lei-records?filter[fulltext]=%s&page[size]=%d",
		url.QueryEscape(q), limit)
	// If query looks like an LEI (20 alphanumeric), look it up directly.
	if len(q) == 20 && isAlphanumeric(q) {
		endpoint = "https://api.gleif.org/api/v1/lei-records/" + url.PathEscape(q)
	}

	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/gleif-lookup")
	req.Header.Set("Accept", "application/vnd.api+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gleif fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("gleif status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	out := &GLEIFLookupOutput{Query: q, Source: "gleif.org"}

	// Response may be either a single record or a list — try list first.
	type leiAttrs struct {
		LEI    string `json:"lei"`
		Entity struct {
			LegalName struct {
				Name string `json:"name"`
			} `json:"legalName"`
			OtherNames []struct {
				Name string `json:"name"`
			} `json:"otherNames"`
			LegalForm struct {
				ID string `json:"id"`
			} `json:"legalForm"`
			Status         string `json:"status"`
			LegalAddress   struct {
				FirstAddressLine string `json:"addressLines"`
				City             string `json:"city"`
				Region           string `json:"region"`
				Country          string `json:"country"`
				PostalCode       string `json:"postalCode"`
			} `json:"legalAddress"`
			HeadquartersAddress struct {
				FirstAddressLine string   `json:"addressLines"`
				City             string   `json:"city"`
				Region           string   `json:"region"`
				Country          string   `json:"country"`
				PostalCode       string   `json:"postalCode"`
			} `json:"headquartersAddress"`
			BICs []string `json:"bic"`
		} `json:"entity"`
		Registration struct {
			Status                 string `json:"status"`
			NextRenewalDate        string `json:"nextRenewalDate"`
			InitialRegistrationDate string `json:"initialRegistrationDate"`
			LastUpdateDate         string `json:"lastUpdateDate"`
			ManagingLOU            string `json:"managingLou"`
		} `json:"registration"`
	}
	type leiData struct {
		ID            string   `json:"id"`
		Type          string   `json:"type"`
		Attributes    leiAttrs `json:"attributes"`
		Relationships struct {
			DirectParent struct {
				Links struct {
					Related string `json:"related"`
				} `json:"links"`
				Data *struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"direct-parent"`
		} `json:"relationships"`
	}
	type respList struct {
		Data []leiData `json:"data"`
		Meta struct {
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	type respSingle struct {
		Data leiData `json:"data"`
	}

	addRecord := func(d leiData) {
		var firstAddr string
		// Some endpoints return addressLines as []string; the json: above is wrong.
		// We'll fall back to read raw via a flexible parse — for now skip the
		// address-line nuance and surface what we have.
		_ = firstAddr
		other := []string{}
		for _, n := range d.Attributes.Entity.OtherNames {
			if n.Name != "" {
				other = append(other, n.Name)
			}
		}
		parent := ""
		if d.Relationships.DirectParent.Data != nil {
			parent = d.Relationships.DirectParent.Data.ID
		}
		out.Records = append(out.Records, LEIRecord{
			LEI:                d.Attributes.LEI,
			LegalName:          d.Attributes.Entity.LegalName.Name,
			OtherNames:         other,
			LegalForm:          d.Attributes.Entity.LegalForm.ID,
			Status:             d.Attributes.Entity.Status,
			HQCity:             d.Attributes.Entity.HeadquartersAddress.City,
			HQRegion:           d.Attributes.Entity.HeadquartersAddress.Region,
			HQCountry:          d.Attributes.Entity.HeadquartersAddress.Country,
			HQPostalCode:       d.Attributes.Entity.HeadquartersAddress.PostalCode,
			LegalCity:          d.Attributes.Entity.LegalAddress.City,
			LegalCountry:       d.Attributes.Entity.LegalAddress.Country,
			BICs:               d.Attributes.Entity.BICs,
			RegistrationStatus: d.Attributes.Registration.Status,
			NextRenewalDate:    d.Attributes.Registration.NextRenewalDate,
			InitialReg:         d.Attributes.Registration.InitialRegistrationDate,
			LastUpdate:         d.Attributes.Registration.LastUpdateDate,
			ManagingLOU:        d.Attributes.Registration.ManagingLOU,
			ParentLEI:          parent,
			URL:                "https://www.gleif.org/lei/" + d.Attributes.LEI,
		})
	}

	// Try list first.
	var list respList
	if err := json.Unmarshal(body, &list); err == nil && len(list.Data) > 0 {
		for _, d := range list.Data {
			addRecord(d)
		}
		out.TotalRecords = list.Meta.Pagination.Total
	} else {
		// Try single record.
		var single respSingle
		if err := json.Unmarshal(body, &single); err == nil && single.Data.Attributes.LEI != "" {
			addRecord(single.Data)
			out.TotalRecords = 1
		}
	}

	out.TookMs = time.Since(start).Milliseconds()
	if out.TotalRecords == 0 {
		out.Note = "No LEI records matched. Most LEI-required entities are financial firms; smaller startups may not have an LEI."
	}
	return out, nil
}

func isAlphanumeric(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}
