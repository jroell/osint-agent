package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// NPIRegistryLookup queries the CMS National Provider Identifier registry —
// every US healthcare provider (doctors, dentists, NPs, PAs, physical
// therapists, etc.) and every healthcare ORGANIZATION (hospitals, clinics,
// pharmacies) has a 10-digit NPI mandated by HIPAA. ~2.5M individual + 1M
// organizational records. Free, no auth.
//
// Why this is high-value ER:
//   - Maps a name → all matching healthcare providers in the US, with
//     **practice + mailing addresses, phone, fax, specialties, state
//     license numbers, and organization affiliations** — none of which are
//     available from generic people-search tools.
//   - State license number is uniquely valuable: it lets you cross-check
//     state medical board records, malpractice databases, etc.
//   - Organizations expose authorized officials + addresses + sub-NPIs
//     (e.g. "Cleveland Clinic" → all the satellite NPIs it operates).
//   - Active/inactive status flag is the single best signal of whether
//     a provider is currently practicing.
//
// Three modes:
//   - "search"      : by first/last name + optional state/city/postal/
//                      specialty filters. Matches both providers (NPI-1)
//                      and orgs (NPI-2) unless enumeration_type is set.
//   - "by_npi"      : direct lookup by 10-digit NPI.
//   - "org_search"  : by organization name → list of NPI-2 records.

type NPIAddress struct {
	Address1     string `json:"address_1,omitempty"`
	Address2     string `json:"address_2,omitempty"`
	City         string `json:"city,omitempty"`
	State        string `json:"state,omitempty"`
	PostalCode   string `json:"postal_code,omitempty"`
	CountryCode  string `json:"country_code,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Fax          string `json:"fax,omitempty"`
	Purpose      string `json:"purpose,omitempty"` // MAILING | LOCATION | PRIMARY | SECONDARY
	Type         string `json:"type,omitempty"`    // DOM | FGN | MIL
}

type NPITaxonomy struct {
	Code        string `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
	Primary     bool   `json:"primary,omitempty"`
	License     string `json:"license_number,omitempty"`
	State       string `json:"license_state,omitempty"`
}

type NPIIdentifier struct {
	Code       string `json:"code,omitempty"`
	Identifier string `json:"identifier,omitempty"`
	State      string `json:"state,omitempty"`
	Issuer     string `json:"issuer,omitempty"`
	Desc       string `json:"description,omitempty"`
}

type NPIRecord struct {
	NPI               string         `json:"npi"`
	EnumerationType   string         `json:"enumeration_type"` // NPI-1 (provider) | NPI-2 (org)
	Status            string         `json:"status,omitempty"`
	EnumerationDate   string         `json:"enumeration_date,omitempty"`
	LastUpdated       string         `json:"last_updated,omitempty"`

	// Individual provider (NPI-1) fields
	FirstName         string         `json:"first_name,omitempty"`
	LastName          string         `json:"last_name,omitempty"`
	MiddleName        string         `json:"middle_name,omitempty"`
	NamePrefix        string         `json:"name_prefix,omitempty"`
	NameSuffix        string         `json:"name_suffix,omitempty"`
	Credential        string         `json:"credential,omitempty"`
	Gender            string         `json:"gender,omitempty"`
	SoleProprietor    string         `json:"sole_proprietor,omitempty"`

	// Organization (NPI-2) fields
	OrganizationName  string         `json:"organization_name,omitempty"`
	AuthorizedOfficial string        `json:"authorized_official,omitempty"`
	AuthorizedTitle   string         `json:"authorized_official_title,omitempty"`

	Addresses         []NPIAddress   `json:"addresses,omitempty"`
	Taxonomies        []NPITaxonomy  `json:"taxonomies,omitempty"`
	OtherIdentifiers  []NPIIdentifier `json:"other_identifiers,omitempty"`
	OtherNames        []string       `json:"other_names,omitempty"`

	// Surfaced summary
	PrimarySpecialty  string         `json:"primary_specialty,omitempty"`
	PrimaryAddress    *NPIAddress    `json:"primary_address,omitempty"`
}

type NPIRegistryLookupOutput struct {
	Mode              string       `json:"mode"`
	Query             string       `json:"query,omitempty"`
	ResultCount       int          `json:"result_count"`
	Records           []NPIRecord  `json:"records,omitempty"`

	// Aggregations
	UniqueSpecialties []string     `json:"unique_specialties,omitempty"`
	UniqueStates      []string     `json:"unique_states,omitempty"`
	ActiveCount       int          `json:"active_count,omitempty"`
	InactiveCount     int          `json:"inactive_count,omitempty"`

	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
	Note              string       `json:"note,omitempty"`
}

func NPIRegistryLookup(ctx context.Context, input map[string]any) (*NPIRegistryLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["npi"]; ok {
			mode = "by_npi"
		} else if _, ok := input["organization"]; ok {
			mode = "org_search"
		} else {
			mode = "search"
		}
	}

	out := &NPIRegistryLookupOutput{
		Mode:   mode,
		Source: "npiregistry.cms.hhs.gov/api",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	params := url.Values{}
	params.Set("version", "2.1")

	switch mode {
	case "search":
		first, _ := input["first_name"].(string)
		last, _ := input["last_name"].(string)
		first = strings.TrimSpace(first)
		last = strings.TrimSpace(last)
		if first == "" && last == "" {
			return nil, fmt.Errorf("input.first_name or input.last_name required for search")
		}
		out.Query = fmt.Sprintf("%s %s", first, last)
		if first != "" {
			params.Set("first_name", first)
		}
		if last != "" {
			params.Set("last_name", last)
		}
		if v, ok := input["state"].(string); ok && v != "" {
			params.Set("state", strings.ToUpper(v))
		}
		if v, ok := input["city"].(string); ok && v != "" {
			params.Set("city", v)
		}
		if v, ok := input["postal_code"].(string); ok && v != "" {
			params.Set("postal_code", v)
		}
		if v, ok := input["taxonomy_description"].(string); ok && v != "" {
			params.Set("taxonomy_description", v)
		}
		// Default: provider only (NPI-1). Override with enumeration_type
		if v, ok := input["enumeration_type"].(string); ok && v != "" {
			params.Set("enumeration_type", v)
		} else {
			params.Set("enumeration_type", "NPI-1")
		}

	case "by_npi":
		npi, _ := input["npi"].(string)
		npi = strings.TrimSpace(npi)
		if npi == "" {
			return nil, fmt.Errorf("input.npi required for by_npi")
		}
		out.Query = npi
		params.Set("number", npi)

	case "org_search":
		org, _ := input["organization"].(string)
		org = strings.TrimSpace(org)
		if org == "" {
			return nil, fmt.Errorf("input.organization required for org_search")
		}
		out.Query = org
		params.Set("organization_name", org)
		params.Set("enumeration_type", "NPI-2")
		if v, ok := input["state"].(string); ok && v != "" {
			params.Set("state", strings.ToUpper(v))
		}
		if v, ok := input["city"].(string); ok && v != "" {
			params.Set("city", v)
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, by_npi, org_search", mode)
	}

	limit := 20
	if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
		limit = int(l)
	}
	params.Set("limit", fmt.Sprintf("%d", limit))

	endpoint := "https://npiregistry.cms.hhs.gov/api/?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("NPI: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("NPI HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	var raw struct {
		ResultCount int `json:"result_count"`
		Results     []struct {
			Number          any    `json:"number"`
			EnumerationType string `json:"enumeration_type"`
			LastUpdated     string `json:"last_updated_epoch"`
			Created         string `json:"created_epoch"`
			Basic           struct {
				FirstName        string `json:"first_name"`
				LastName         string `json:"last_name"`
				MiddleName       string `json:"middle_name"`
				NamePrefix       string `json:"name_prefix"`
				NameSuffix       string `json:"name_suffix"`
				Credential       string `json:"credential"`
				SoleProprietor   string `json:"sole_proprietor"`
				Gender           string `json:"gender"`
				EnumerationDate  string `json:"enumeration_date"`
				LastUpdated      string `json:"last_updated"`
				Status           string `json:"status"`
				OrganizationName string `json:"organization_name"`
				AuthorizedOfficial struct {
					FirstName  string `json:"first_name"`
					LastName   string `json:"last_name"`
					MiddleName string `json:"middle_name"`
					Title      string `json:"title"`
				} `json:"authorized_official"`
				AuthorizedOfficialTitle string `json:"authorized_official_title_or_position"`
				AuthorizedFirstName     string `json:"authorized_official_first_name"`
				AuthorizedLastName      string `json:"authorized_official_last_name"`
			} `json:"basic"`
			Addresses []struct {
				CountryCode    string `json:"country_code"`
				CountryName    string `json:"country_name"`
				AddressPurpose string `json:"address_purpose"`
				AddressType    string `json:"address_type"`
				Address1       string `json:"address_1"`
				Address2       string `json:"address_2"`
				City           string `json:"city"`
				State          string `json:"state"`
				PostalCode     string `json:"postal_code"`
				Telephone      string `json:"telephone_number"`
				Fax            string `json:"fax_number"`
			} `json:"addresses"`
			Taxonomies []struct {
				Code    string `json:"code"`
				Desc    string `json:"desc"`
				Primary bool   `json:"primary"`
				License string `json:"license"`
				State   string `json:"state"`
			} `json:"taxonomies"`
			Identifiers []struct {
				Code       string `json:"code"`
				Identifier string `json:"identifier"`
				State      string `json:"state"`
				Issuer     string `json:"issuer"`
				Desc       string `json:"desc"`
			} `json:"identifiers"`
			OtherNames []struct {
				FirstName  string `json:"first_name"`
				LastName   string `json:"last_name"`
				MiddleName string `json:"middle_name"`
				Type       string `json:"type"`
			} `json:"other_names"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("NPI decode: %w", err)
	}

	out.ResultCount = raw.ResultCount
	stateSet := map[string]struct{}{}
	specialtySet := map[string]struct{}{}

	for _, r := range raw.Results {
		rec := NPIRecord{
			NPI:             fmt.Sprintf("%v", r.Number),
			EnumerationType: r.EnumerationType,
			Status:          r.Basic.Status,
			EnumerationDate: r.Basic.EnumerationDate,
			LastUpdated:     r.Basic.LastUpdated,
			FirstName:       r.Basic.FirstName,
			LastName:        r.Basic.LastName,
			MiddleName:      r.Basic.MiddleName,
			NamePrefix:      r.Basic.NamePrefix,
			NameSuffix:      r.Basic.NameSuffix,
			Credential:      r.Basic.Credential,
			Gender:          r.Basic.Gender,
			SoleProprietor:  r.Basic.SoleProprietor,
			OrganizationName: r.Basic.OrganizationName,
			AuthorizedTitle: r.Basic.AuthorizedOfficialTitle,
		}

		// Authorized official name (orgs)
		if r.Basic.AuthorizedOfficial.FirstName != "" {
			parts := []string{r.Basic.AuthorizedOfficial.FirstName, r.Basic.AuthorizedOfficial.MiddleName, r.Basic.AuthorizedOfficial.LastName}
			rec.AuthorizedOfficial = strings.TrimSpace(strings.Join(parts, " "))
			if r.Basic.AuthorizedOfficial.Title != "" {
				rec.AuthorizedTitle = r.Basic.AuthorizedOfficial.Title
			}
		} else if r.Basic.AuthorizedFirstName != "" {
			rec.AuthorizedOfficial = strings.TrimSpace(r.Basic.AuthorizedFirstName + " " + r.Basic.AuthorizedLastName)
		}

		for _, a := range r.Addresses {
			rec.Addresses = append(rec.Addresses, NPIAddress{
				Address1:    a.Address1,
				Address2:    a.Address2,
				City:        a.City,
				State:       a.State,
				PostalCode:  a.PostalCode,
				CountryCode: a.CountryCode,
				Phone:       a.Telephone,
				Fax:         a.Fax,
				Purpose:     a.AddressPurpose,
				Type:        a.AddressType,
			})
			if a.State != "" {
				stateSet[a.State] = struct{}{}
			}
		}
		for _, t := range r.Taxonomies {
			rec.Taxonomies = append(rec.Taxonomies, NPITaxonomy{
				Code:        t.Code,
				Description: t.Desc,
				Primary:     t.Primary,
				License:     t.License,
				State:       t.State,
			})
			if t.Primary && rec.PrimarySpecialty == "" {
				rec.PrimarySpecialty = t.Desc
			}
			if t.Desc != "" {
				specialtySet[t.Desc] = struct{}{}
			}
		}
		// Fallback to first taxonomy if no primary
		if rec.PrimarySpecialty == "" && len(rec.Taxonomies) > 0 {
			rec.PrimarySpecialty = rec.Taxonomies[0].Description
		}

		for _, ident := range r.Identifiers {
			rec.OtherIdentifiers = append(rec.OtherIdentifiers, NPIIdentifier{
				Code:       ident.Code,
				Identifier: ident.Identifier,
				State:      ident.State,
				Issuer:     ident.Issuer,
				Desc:       ident.Desc,
			})
		}
		for _, on := range r.OtherNames {
			full := strings.TrimSpace(strings.Join([]string{on.FirstName, on.MiddleName, on.LastName}, " "))
			if full != "" {
				rec.OtherNames = append(rec.OtherNames, full+" ("+on.Type+")")
			}
		}

		// Surface "primary" address: prefer LOCATION (practice) over MAILING
		for i := range rec.Addresses {
			if rec.Addresses[i].Purpose == "LOCATION" {
				addr := rec.Addresses[i]
				rec.PrimaryAddress = &addr
				break
			}
		}
		if rec.PrimaryAddress == nil && len(rec.Addresses) > 0 {
			addr := rec.Addresses[0]
			rec.PrimaryAddress = &addr
		}

		// Active/inactive count
		if rec.Status == "A" {
			out.ActiveCount++
		} else {
			out.InactiveCount++
		}

		out.Records = append(out.Records, rec)
	}

	for s := range stateSet {
		out.UniqueStates = append(out.UniqueStates, s)
	}
	sort.Strings(out.UniqueStates)
	for s := range specialtySet {
		out.UniqueSpecialties = append(out.UniqueSpecialties, s)
	}
	sort.Strings(out.UniqueSpecialties)

	if out.ResultCount > len(out.Records) {
		out.Note = fmt.Sprintf("API capped to %d results (total matches: %d) — narrow filters or use limit", len(out.Records), out.ResultCount)
	}

	out.HighlightFindings = buildNPIHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildNPIHighlights(o *NPIRegistryLookupOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d providers match '%s' (%d active, %d inactive)", o.ResultCount, o.Query, o.ActiveCount, o.InactiveCount))
		if len(o.UniqueStates) > 0 {
			hi = append(hi, fmt.Sprintf("  states: %s", strings.Join(o.UniqueStates, ", ")))
		}
		if len(o.UniqueSpecialties) > 0 {
			specs := o.UniqueSpecialties
			suffix := ""
			if len(specs) > 4 {
				specs = specs[:4]
				suffix = fmt.Sprintf(" … +%d", len(o.UniqueSpecialties)-4)
			}
			hi = append(hi, fmt.Sprintf("  specialties: %s%s", strings.Join(specs, ", "), suffix))
		}
		for i, r := range o.Records {
			if i >= 6 {
				break
			}
			fullName := strings.TrimSpace(r.NamePrefix + " " + r.FirstName + " " + r.MiddleName + " " + r.LastName + " " + r.NameSuffix)
			cred := ""
			if r.Credential != "" {
				cred = ", " + r.Credential
			}
			locStr := ""
			if r.PrimaryAddress != nil {
				locStr = fmt.Sprintf(" — %s, %s", r.PrimaryAddress.City, r.PrimaryAddress.State)
			}
			statusMarker := ""
			if r.Status != "A" {
				statusMarker = " [INACTIVE]"
			}
			hi = append(hi, fmt.Sprintf("  • NPI %s — %s%s%s — %s%s", r.NPI, fullName, cred, locStr, r.PrimarySpecialty, statusMarker))
		}

	case "by_npi":
		if o.ResultCount == 0 {
			hi = append(hi, fmt.Sprintf("✗ No NPI record found for '%s'", o.Query))
			break
		}
		r := o.Records[0]
		if r.EnumerationType == "NPI-2" {
			// Organization
			hi = append(hi, fmt.Sprintf("✓ NPI %s [ORG] — %s", r.NPI, r.OrganizationName))
			if r.AuthorizedOfficial != "" {
				title := ""
				if r.AuthorizedTitle != "" {
					title = " (" + r.AuthorizedTitle + ")"
				}
				hi = append(hi, fmt.Sprintf("  authorized official: %s%s", r.AuthorizedOfficial, title))
			}
		} else {
			fullName := strings.TrimSpace(r.NamePrefix + " " + r.FirstName + " " + r.MiddleName + " " + r.LastName + " " + r.NameSuffix)
			cred := ""
			if r.Credential != "" {
				cred = ", " + r.Credential
			}
			hi = append(hi, fmt.Sprintf("✓ NPI %s — %s%s", r.NPI, fullName, cred))
			hi = append(hi, fmt.Sprintf("  status: %s · enumerated: %s · last updated: %s", r.Status, r.EnumerationDate, r.LastUpdated))
		}
		if r.PrimarySpecialty != "" {
			hi = append(hi, fmt.Sprintf("  primary specialty: %s", r.PrimarySpecialty))
		}
		// All licenses
		for _, t := range r.Taxonomies {
			if t.License != "" {
				marker := ""
				if t.Primary {
					marker = " [PRIMARY]"
				}
				hi = append(hi, fmt.Sprintf("  license: %s in %s — %s%s", t.License, t.State, t.Description, marker))
			}
		}
		// Addresses
		for _, a := range r.Addresses {
			parts := []string{a.Address1}
			if a.Address2 != "" {
				parts = append(parts, a.Address2)
			}
			parts = append(parts, fmt.Sprintf("%s, %s %s", a.City, a.State, a.PostalCode))
			contact := []string{}
			if a.Phone != "" {
				contact = append(contact, "📞 "+a.Phone)
			}
			if a.Fax != "" {
				contact = append(contact, "📠 "+a.Fax)
			}
			contactStr := ""
			if len(contact) > 0 {
				contactStr = " · " + strings.Join(contact, " ")
			}
			hi = append(hi, fmt.Sprintf("  [%s] %s%s", a.Purpose, strings.Join(parts, ", "), contactStr))
		}
		if len(r.OtherNames) > 0 {
			hi = append(hi, fmt.Sprintf("  other names: %s", strings.Join(r.OtherNames, "; ")))
		}

	case "org_search":
		hi = append(hi, fmt.Sprintf("✓ %d organizations match '%s'", o.ResultCount, o.Query))
		for i, r := range o.Records {
			if i >= 8 {
				break
			}
			loc := ""
			if r.PrimaryAddress != nil {
				loc = fmt.Sprintf(" — %s, %s", r.PrimaryAddress.City, r.PrimaryAddress.State)
			}
			off := ""
			if r.AuthorizedOfficial != "" {
				off = fmt.Sprintf(" · official: %s", r.AuthorizedOfficial)
			}
			hi = append(hi, fmt.Sprintf("  • NPI %s — %s%s%s", r.NPI, r.OrganizationName, loc, off))
		}
	}
	return hi
}
