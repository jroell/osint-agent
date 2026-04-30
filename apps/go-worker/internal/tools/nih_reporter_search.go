package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// NIHGrant is one grant record returned by RePORTER.
type NIHGrant struct {
	ProjectNum    string  `json:"project_num"`
	ApplID        int64   `json:"appl_id,omitempty"`
	ProjectTitle  string  `json:"project_title,omitempty"`
	Org           string  `json:"organization,omitempty"`
	OrgCity       string  `json:"organization_city,omitempty"`
	OrgState      string  `json:"organization_state,omitempty"`
	OrgCountry    string  `json:"organization_country,omitempty"`
	FiscalYear    int     `json:"fiscal_year,omitempty"`
	AwardAmount   int64   `json:"award_amount,omitempty"`
	AwardDate     string  `json:"award_notice_date,omitempty"`
	ProjectStart  string  `json:"project_start_date,omitempty"`
	ProjectEnd    string  `json:"project_end_date,omitempty"`
	Agency        string  `json:"agency,omitempty"`
	AgencyCode    string  `json:"agency_code,omitempty"`
	Mechanism     string  `json:"funding_mechanism,omitempty"`
	PIs           []NIHInvestigator `json:"pis,omitempty"`
	IsActive      bool    `json:"is_active,omitempty"`
}

// NIHInvestigator is one PI on a grant.
type NIHInvestigator struct {
	FullName  string `json:"full_name"`
	ProfileID int64  `json:"profile_id,omitempty"`
	IsContact bool   `json:"is_contact_pi,omitempty"`
}

// NIHPIAggregate counts grant participation across the result set.
type NIHPIAggregate struct {
	FullName       string `json:"full_name"`
	ProfileID      int64  `json:"profile_id,omitempty"`
	GrantsAsContact int   `json:"grants_as_contact_pi"`
	GrantsAsCoPI    int   `json:"grants_as_co_pi"`
	TotalGrants     int   `json:"total_grants"`
	TotalFunding    int64 `json:"total_funding"`
}

// NIHOrgAggregate counts grants per institution.
type NIHOrgAggregate struct {
	Org          string `json:"organization"`
	GrantCount   int    `json:"grant_count"`
	TotalFunding int64  `json:"total_funding"`
	YearRange    string `json:"year_range,omitempty"`
}

// NIHReporterOutput is the response.
type NIHReporterOutput struct {
	Mode            string             `json:"mode"`
	Query           string             `json:"query"`
	TotalRecords    int                `json:"total_records"`
	Returned        int                `json:"returned"`
	Grants          []NIHGrant         `json:"grants"`
	TopPIs          []NIHPIAggregate   `json:"top_pis,omitempty"`
	TopOrgs         []NIHOrgAggregate  `json:"top_orgs,omitempty"`
	UniqueProfileIDs []int64           `json:"unique_profile_ids,omitempty"`
	UniqueAgencies  []string           `json:"unique_agencies,omitempty"`
	YearRange       string             `json:"year_range,omitempty"`
	TotalFundingObserved int64         `json:"total_funding_observed,omitempty"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source          string             `json:"source"`
	TookMs          int64              `json:"tookMs"`
	Note            string             `json:"note,omitempty"`
}

type nihRawPI struct {
	FullName  string `json:"full_name"`
	ProfileID int64  `json:"profile_id"`
	IsContactPI bool `json:"is_contact_pi"`
}

type nihRawOrg struct {
	OrgName     string `json:"org_name"`
	OrgCity     string `json:"org_city"`
	OrgState    string `json:"org_state"`
	OrgCountry  string `json:"org_country"`
}

type nihRawAgency struct {
	Name string `json:"name"`
	Code string `json:"code"`
	Abbreviation string `json:"abbreviation"`
}

type nihRawGrant struct {
	ProjectNum         string         `json:"project_num"`
	ApplID             int64          `json:"appl_id"`
	ProjectTitle       string         `json:"project_title"`
	Organization       nihRawOrg      `json:"organization"`
	FiscalYear         int            `json:"fiscal_year"`
	AwardAmount        int64          `json:"award_amount"`
	AwardNoticeDate    string         `json:"award_notice_date"`
	ProjectStartDate   string         `json:"project_start_date"`
	ProjectEndDate     string         `json:"project_end_date"`
	AgencyICAdmin      nihRawAgency   `json:"agency_ic_admin"`
	AgencyICFundings   []nihRawAgency `json:"agency_ic_fundings"`
	FundingMechanism   string         `json:"funding_mechanism"`
	IsActive           bool           `json:"is_active"`
	PrincipalInvestigators []nihRawPI `json:"principal_investigators"`
	FullFoa            string         `json:"full_foa"`
}

type nihRawResp struct {
	Meta    struct {
		Total int `json:"total"`
	} `json:"meta"`
	Results []nihRawGrant `json:"results"`
}

// NIHReporterSearch queries the NIH RePORTER public API for federally-funded
// biomedical research grants. RePORTER indexes every NIH-funded grant since
// 1985 (~3M grants, ~$300B in cumulative funding). Free, no auth.
//
// Modes:
//   - "pi_name"    : grants for a named principal investigator
//   - "org_name"   : grants at an institution (e.g. "Stanford University")
//   - "text"       : full-text search across project titles & abstracts
//   - "project_num": lookup specific grant by project number (e.g. "5R01CA123456-04")
//
// Why this matters for ER:
//   - PIs get a stable `profile_id` from NIH — hard cross-grant identity
//     link even when name format varies ("Carl June" vs "CARL H. JUNE").
//   - Co-investigators on grants reveal collaboration networks not visible
//     in publication co-authorship (some collaborators are funded but
//     contribute infrastructure not publications).
//   - Institutional history (organization on each grant) reveals career
//     moves between universities.
//   - Funding amounts reveal seniority + research scale — a $50M U54 lead
//     PI is a different career signal from a $200K R03 first-time PI.
//   - Agency (NCI, NIAID, NIMH, etc.) reveals research domain.
func NIHReporterSearch(ctx context.Context, input map[string]any) (*NIHReporterOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "pi_name"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (PI name, org name, project number, or text)")
	}

	limit := 50
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 500 {
		limit = int(v)
	}

	criteria := map[string]any{}
	switch mode {
	case "pi_name":
		criteria["pi_names"] = []map[string]string{{"any_name": query}}
	case "org_name":
		criteria["org_names"] = []string{query}
	case "text":
		criteria["advanced_text_search"] = map[string]any{
			"operator":     "and",
			"search_field": "all",
			"search_text":  query,
		}
	case "project_num":
		criteria["project_nums"] = []string{query}
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: pi_name, org_name, text, project_num", mode)
	}

	body := map[string]any{
		"criteria":   criteria,
		"limit":      limit,
		"offset":     0,
		"sort_field": "fiscal_year",
		"sort_order": "desc",
	}
	bodyBytes, _ := json.Marshal(body)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.reporter.nih.gov/v2/projects/search",
		bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nih reporter request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("nih reporter %d: %s", resp.StatusCode, string(body))
	}

	var raw nihRawResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("nih reporter decode: %w", err)
	}

	out := &NIHReporterOutput{
		Mode:         mode,
		Query:        query,
		TotalRecords: raw.Meta.Total,
		Source:       "api.reporter.nih.gov",
	}

	piAgg := map[int64]*NIHPIAggregate{}
	piByName := map[string]*NIHPIAggregate{} // for PIs without profile_id
	orgAgg := map[string]*NIHOrgAggregate{}
	orgYears := map[string][2]int{} // org → [minYear, maxYear]
	profileIDSet := map[int64]struct{}{}
	agencySet := map[string]struct{}{}
	minYear, maxYear := 0, 0
	var totalFunding int64

	for _, r := range raw.Results {
		// flatten PIs
		var pis []NIHInvestigator
		for _, p := range r.PrincipalInvestigators {
			pis = append(pis, NIHInvestigator{
				FullName:  p.FullName,
				ProfileID: p.ProfileID,
				IsContact: p.IsContactPI,
			})
			// aggregate
			var ag *NIHPIAggregate
			if p.ProfileID > 0 {
				ag = piAgg[p.ProfileID]
				if ag == nil {
					ag = &NIHPIAggregate{FullName: p.FullName, ProfileID: p.ProfileID}
					piAgg[p.ProfileID] = ag
				}
				profileIDSet[p.ProfileID] = struct{}{}
			} else {
				key := strings.ToLower(strings.TrimSpace(p.FullName))
				ag = piByName[key]
				if ag == nil {
					ag = &NIHPIAggregate{FullName: p.FullName}
					piByName[key] = ag
				}
			}
			ag.TotalGrants++
			if p.IsContactPI {
				ag.GrantsAsContact++
			} else {
				ag.GrantsAsCoPI++
			}
			ag.TotalFunding += r.AwardAmount
		}

		grant := NIHGrant{
			ProjectNum:    r.ProjectNum,
			ApplID:        r.ApplID,
			ProjectTitle:  r.ProjectTitle,
			Org:           r.Organization.OrgName,
			OrgCity:       r.Organization.OrgCity,
			OrgState:      r.Organization.OrgState,
			OrgCountry:    r.Organization.OrgCountry,
			FiscalYear:    r.FiscalYear,
			AwardAmount:   r.AwardAmount,
			AwardDate:     r.AwardNoticeDate,
			ProjectStart:  r.ProjectStartDate,
			ProjectEnd:    r.ProjectEndDate,
			Agency:        r.AgencyICAdmin.Name,
			AgencyCode:    r.AgencyICAdmin.Code,
			Mechanism:     r.FundingMechanism,
			PIs:           pis,
			IsActive:      r.IsActive,
		}
		out.Grants = append(out.Grants, grant)

		if r.Organization.OrgName != "" {
			oa := orgAgg[r.Organization.OrgName]
			if oa == nil {
				oa = &NIHOrgAggregate{Org: r.Organization.OrgName}
				orgAgg[r.Organization.OrgName] = oa
			}
			oa.GrantCount++
			oa.TotalFunding += r.AwardAmount
			yrs := orgYears[r.Organization.OrgName]
			if r.FiscalYear > 0 {
				if yrs[0] == 0 || r.FiscalYear < yrs[0] {
					yrs[0] = r.FiscalYear
				}
				if r.FiscalYear > yrs[1] {
					yrs[1] = r.FiscalYear
				}
				orgYears[r.Organization.OrgName] = yrs
			}
		}

		if r.AgencyICAdmin.Name != "" {
			agencySet[r.AgencyICAdmin.Name] = struct{}{}
		}
		if r.FiscalYear > 0 {
			if minYear == 0 || r.FiscalYear < minYear {
				minYear = r.FiscalYear
			}
			if r.FiscalYear > maxYear {
				maxYear = r.FiscalYear
			}
		}
		totalFunding += r.AwardAmount
	}
	out.Returned = len(out.Grants)
	out.TotalFundingObserved = totalFunding

	// Materialize PIs — by profile_id first, then by name
	for _, ag := range piAgg {
		out.TopPIs = append(out.TopPIs, *ag)
	}
	for _, ag := range piByName {
		out.TopPIs = append(out.TopPIs, *ag)
	}
	sort.SliceStable(out.TopPIs, func(i, j int) bool {
		if out.TopPIs[i].TotalGrants != out.TopPIs[j].TotalGrants {
			return out.TopPIs[i].TotalGrants > out.TopPIs[j].TotalGrants
		}
		return out.TopPIs[i].TotalFunding > out.TopPIs[j].TotalFunding
	})
	if len(out.TopPIs) > 20 {
		out.TopPIs = out.TopPIs[:20]
	}

	// Materialize orgs
	for _, oa := range orgAgg {
		yrs := orgYears[oa.Org]
		if yrs[0] > 0 && yrs[1] > 0 {
			if yrs[0] == yrs[1] {
				oa.YearRange = fmt.Sprintf("%d", yrs[1])
			} else {
				oa.YearRange = fmt.Sprintf("%d-%d", yrs[0], yrs[1])
			}
		}
		out.TopOrgs = append(out.TopOrgs, *oa)
	}
	sort.SliceStable(out.TopOrgs, func(i, j int) bool {
		return out.TopOrgs[i].GrantCount > out.TopOrgs[j].GrantCount
	})
	if len(out.TopOrgs) > 10 {
		out.TopOrgs = out.TopOrgs[:10]
	}

	for id := range profileIDSet {
		out.UniqueProfileIDs = append(out.UniqueProfileIDs, id)
	}
	sort.Slice(out.UniqueProfileIDs, func(i, j int) bool { return out.UniqueProfileIDs[i] < out.UniqueProfileIDs[j] })

	for a := range agencySet {
		out.UniqueAgencies = append(out.UniqueAgencies, a)
	}
	sort.Strings(out.UniqueAgencies)

	if minYear > 0 && maxYear > 0 && minYear != maxYear {
		out.YearRange = fmt.Sprintf("%d-%d", minYear, maxYear)
	} else if maxYear > 0 {
		out.YearRange = fmt.Sprintf("%d", maxYear)
	}

	// Highlights
	hi := []string{}
	hi = append(hi, fmt.Sprintf("%d total matching grants (%d returned)", out.TotalRecords, out.Returned))
	if out.YearRange != "" {
		hi = append(hi, fmt.Sprintf("year range: %s", out.YearRange))
	}
	if totalFunding > 0 {
		hi = append(hi, fmt.Sprintf("total observed funding: $%s across returned grants", fmtUSD(totalFunding)))
	}
	if len(out.UniqueProfileIDs) > 0 {
		hi = append(hi, fmt.Sprintf("✓ %d unique NIH profile_ids — hard cross-grant ER signal (resolves name variants like 'Carl June' vs 'CARL H. JUNE')", len(out.UniqueProfileIDs)))
	}
	if mode == "pi_name" && len(out.UniqueProfileIDs) >= 2 {
		hi = append(hi, fmt.Sprintf("⚠️  %d distinct profile_ids match this name — likely namesakes; disambiguate by org/agency", len(out.UniqueProfileIDs)))
	}
	if len(out.TopOrgs) > 0 {
		topOrg := out.TopOrgs[0]
		hi = append(hi, fmt.Sprintf("primary institution: %s (%d grants, $%s, %s)", topOrg.Org, topOrg.GrantCount, fmtUSD(topOrg.TotalFunding), topOrg.YearRange))
		if len(out.TopOrgs) > 1 {
			hi = append(hi, fmt.Sprintf("⚡ %d distinct institutions in result set — career mobility / cross-institutional collaborations", len(out.TopOrgs)))
		}
	}
	if mode == "pi_name" && len(out.TopPIs) > 1 {
		// list top co-PIs
		coPIs := []string{}
		for _, p := range out.TopPIs[1:min2(6, len(out.TopPIs))] {
			coPIs = append(coPIs, fmt.Sprintf("%s (%d grants)", p.FullName, p.TotalGrants))
		}
		if len(coPIs) > 0 {
			hi = append(hi, "top co-PIs in returned grants: "+strings.Join(coPIs, ", "))
		}
	}
	if len(out.UniqueAgencies) > 0 && len(out.UniqueAgencies) <= 5 {
		hi = append(hi, "funding agencies: "+strings.Join(out.UniqueAgencies, ", "))
	} else if len(out.UniqueAgencies) > 5 {
		hi = append(hi, fmt.Sprintf("%d distinct funding agencies (broad domain)", len(out.UniqueAgencies)))
	}
	out.HighlightFindings = hi
	out.TookMs = time.Since(start).Milliseconds()

	if out.TotalRecords == 0 {
		out.Note = fmt.Sprintf("no grants found matching '%s' in mode=%s", query, mode)
	}
	return out, nil
}
