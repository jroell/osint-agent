package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NSFAward is one grant record.
type NSFAward struct {
	AwardID            string  `json:"award_id"`
	Title              string  `json:"title"`
	Abstract           string  `json:"abstract,omitempty"`
	PIFirstName        string  `json:"pi_first_name,omitempty"`
	PILastName         string  `json:"pi_last_name,omitempty"`
	PIFullName         string  `json:"pi_full_name,omitempty"`
	PIEmail            string  `json:"pi_email,omitempty"`
	CoPIs              []string `json:"co_pis,omitempty"`
	AwardeeName        string  `json:"awardee_name,omitempty"`
	AwardeeStateCode   string  `json:"awardee_state_code,omitempty"`
	AwardeeCountryCode string  `json:"awardee_country_code,omitempty"`
	AwardeeCity        string  `json:"awardee_city,omitempty"`
	FundsObligatedAmt  int64   `json:"funds_obligated_usd"`
	StartDate          string  `json:"start_date,omitempty"`
	ExpDate            string  `json:"expiry_date,omitempty"`
	FundProgramName    string  `json:"fund_program_name,omitempty"`
	URL                string  `json:"url,omitempty"`
}

// NSFPIAggregate is a PI cross-grant summary.
type NSFPIAggregate struct {
	Name         string `json:"name"`
	GrantCount   int    `json:"grant_count"`
	TotalFunding int64  `json:"total_funding"`
}

// NSFOrgAggregate counts grants per institution.
type NSFOrgAggregate struct {
	Org          string `json:"organization"`
	GrantCount   int    `json:"grant_count"`
	TotalFunding int64  `json:"total_funding"`
	YearRange    string `json:"year_range,omitempty"`
}

// NSFAwardsOutput is the response.
type NSFAwardsOutput struct {
	Mode              string            `json:"mode"`
	Query             string            `json:"query"`
	Returned          int               `json:"returned"`
	Awards            []NSFAward        `json:"awards"`
	TopPIs            []NSFPIAggregate  `json:"top_pis,omitempty"`
	TopOrgs           []NSFOrgAggregate `json:"top_orgs,omitempty"`
	UniqueStateSet    []string          `json:"unique_states,omitempty"`
	UniquePrograms    []string          `json:"unique_funding_programs,omitempty"`
	YearRange         string            `json:"year_range,omitempty"`
	TotalFundingObs   int64             `json:"total_funding_observed,omitempty"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source            string            `json:"source"`
	TookMs            int64             `json:"tookMs"`
	Note              string            `json:"note,omitempty"`
}

// raw NSF API response
type nsfRawResponse struct {
	Response struct {
		Award []map[string]any `json:"award"`
	} `json:"response"`
}

// NSFAwardsSearch queries the NSF Awards public API. Free, no auth.
//
// Why this matters for ER:
//   - NSF funds non-biomedical US research (engineering, math, physics,
//     CS, social sciences, geography, education, oceanography, etc.) —
//     direct complement to NIH RePORTER (biomed). Together they cover
//     most US federal academic funding.
//   - PI history reveals career trajectory: Geoffrey Hinton's NSF history
//     shows him at Carnegie Mellon in 1986 ("Search Methods for Massively
//     Parallel Networks") — pre-Google, pre-deep-learning revival. That's
//     a 40-year temporal ER chain.
//   - Co-PIs surface collaboration networks that may not appear in
//     publication co-authorship.
//   - Institution at award time = employer-of-record snapshot.
//
// Modes:
//   - "pi_name"   : grants for a named PI (uses pdPIName filter)
//   - "keyword"   : full-text search across title + abstract
//   - "org_name"  : grants at an institution (uses awardeeName filter)
//   - "award_id"  : direct lookup by NSF award ID
func NSFAwardsSearch(ctx context.Context, input map[string]any) (*NSFAwardsOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "pi_name"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required")
	}

	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &NSFAwardsOutput{
		Mode:   mode,
		Query:  query,
		Source: "research.gov/awardapi-service/v1",
	}
	start := time.Now()

	params := url.Values{}
	params.Set("printFields", "id,title,abstractText,piFirstName,piLastName,piEmail,coPDPI,fundsObligatedAmt,startDate,expDate,awardeeName,awardeeStateCode,awardeeCountryCode,awardeeCity,fundProgramName")
	switch mode {
	case "pi_name":
		params.Set("pdPIName", query)
	case "keyword":
		params.Set("keyword", query)
	case "org_name":
		params.Set("awardeeName", query)
	case "award_id":
		params.Set("id", query)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: pi_name, keyword, org_name, award_id", mode)
	}

	endpoint := "https://www.research.gov/awardapi-service/v1/awards.json?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nsf request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("nsf %d: %s", resp.StatusCode, string(body))
	}
	var raw nsfRawResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("nsf decode: %w", err)
	}

	piAgg := map[string]*NSFPIAggregate{}
	orgAgg := map[string]*NSFOrgAggregate{}
	orgYears := map[string][2]int{}
	stateSet := map[string]struct{}{}
	progSet := map[string]struct{}{}
	minYear, maxYear := 0, 0
	var totalFunding int64

	for i, r := range raw.Response.Award {
		if i >= limit {
			break
		}
		a := NSFAward{
			AwardID:           anyToString(r["id"]),
			Title:             anyToString(r["title"]),
			Abstract:          truncateAbstract(anyToString(r["abstractText"]), 600),
			PIFirstName:       anyToString(r["piFirstName"]),
			PILastName:        anyToString(r["piLastName"]),
			PIEmail:           anyToString(r["piEmail"]),
			AwardeeName:       anyToString(r["awardeeName"]),
			AwardeeStateCode:  anyToString(r["awardeeStateCode"]),
			AwardeeCountryCode: anyToString(r["awardeeCountryCode"]),
			AwardeeCity:       anyToString(r["awardeeCity"]),
			StartDate:         anyToString(r["startDate"]),
			ExpDate:           anyToString(r["expDate"]),
			FundProgramName:   anyToString(r["fundProgramName"]),
		}
		a.PIFullName = strings.TrimSpace(a.PIFirstName + " " + a.PILastName)
		// funds obligated may be string or number
		if v, ok := r["fundsObligatedAmt"].(string); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				a.FundsObligatedAmt = n
			}
		} else if v, ok := r["fundsObligatedAmt"].(float64); ok {
			a.FundsObligatedAmt = int64(v)
		}
		// coPDPI is a comma-separated string usually
		if cp, ok := r["coPDPI"].(string); ok && cp != "" {
			parts := strings.Split(cp, ",")
			for _, p := range parts {
				if cn := strings.TrimSpace(p); cn != "" {
					a.CoPIs = append(a.CoPIs, cn)
				}
			}
		}
		if a.AwardID != "" {
			a.URL = "https://www.nsf.gov/awardsearch/showAward?AWD_ID=" + a.AwardID
		}
		out.Awards = append(out.Awards, a)
		out.Returned++

		// aggregates
		if a.PIFullName != "" {
			ag, ok := piAgg[a.PIFullName]
			if !ok {
				ag = &NSFPIAggregate{Name: a.PIFullName}
				piAgg[a.PIFullName] = ag
			}
			ag.GrantCount++
			ag.TotalFunding += a.FundsObligatedAmt
		}
		for _, c := range a.CoPIs {
			ag, ok := piAgg[c]
			if !ok {
				ag = &NSFPIAggregate{Name: c}
				piAgg[c] = ag
			}
			ag.GrantCount++
		}
		if a.AwardeeName != "" {
			oa, ok := orgAgg[a.AwardeeName]
			if !ok {
				oa = &NSFOrgAggregate{Org: a.AwardeeName}
				orgAgg[a.AwardeeName] = oa
			}
			oa.GrantCount++
			oa.TotalFunding += a.FundsObligatedAmt
			// year tracking
			year := nsfExtractYear(a.StartDate)
			if year > 0 {
				yrs := orgYears[a.AwardeeName]
				if yrs[0] == 0 || year < yrs[0] {
					yrs[0] = year
				}
				if year > yrs[1] {
					yrs[1] = year
				}
				orgYears[a.AwardeeName] = yrs
			}
		}
		if a.AwardeeStateCode != "" {
			stateSet[a.AwardeeStateCode] = struct{}{}
		}
		if a.FundProgramName != "" {
			progSet[a.FundProgramName] = struct{}{}
		}
		// global year range
		year := nsfExtractYear(a.StartDate)
		if year > 0 {
			if minYear == 0 || year < minYear {
				minYear = year
			}
			if year > maxYear {
				maxYear = year
			}
		}
		totalFunding += a.FundsObligatedAmt
	}

	// materialize aggregations
	for _, ag := range piAgg {
		out.TopPIs = append(out.TopPIs, *ag)
	}
	sort.SliceStable(out.TopPIs, func(i, j int) bool {
		if out.TopPIs[i].GrantCount != out.TopPIs[j].GrantCount {
			return out.TopPIs[i].GrantCount > out.TopPIs[j].GrantCount
		}
		return out.TopPIs[i].TotalFunding > out.TopPIs[j].TotalFunding
	})
	if len(out.TopPIs) > 15 {
		out.TopPIs = out.TopPIs[:15]
	}
	for _, oa := range orgAgg {
		yrs := orgYears[oa.Org]
		if yrs[0] > 0 && yrs[1] > 0 {
			if yrs[0] == yrs[1] {
				oa.YearRange = fmt.Sprintf("%d", yrs[0])
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

	for s := range stateSet {
		out.UniqueStateSet = append(out.UniqueStateSet, s)
	}
	sort.Strings(out.UniqueStateSet)
	for p := range progSet {
		out.UniquePrograms = append(out.UniquePrograms, p)
	}
	sort.Strings(out.UniquePrograms)

	if minYear > 0 && maxYear > 0 && minYear != maxYear {
		out.YearRange = fmt.Sprintf("%d-%d", minYear, maxYear)
	} else if maxYear > 0 {
		out.YearRange = fmt.Sprintf("%d", maxYear)
	}
	out.TotalFundingObs = totalFunding

	if out.Returned == 0 {
		out.Note = fmt.Sprintf("no NSF awards matching '%s' in mode=%s", query, mode)
	}

	out.HighlightFindings = buildNSFHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func nsfExtractYear(date string) int {
	// Format: MM/DD/YYYY
	if len(date) < 10 {
		return 0
	}
	yearStr := date[6:10]
	y, err := strconv.Atoi(yearStr)
	if err != nil {
		return 0
	}
	if y < 1950 || y > 2100 {
		return 0
	}
	return y
}

func truncateAbstract(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

func buildNSFHighlights(o *NSFAwardsOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("%d NSF awards returned for mode=%s query='%s'", o.Returned, o.Mode, o.Query))
	if o.YearRange != "" {
		hi = append(hi, "year range: "+o.YearRange)
	}
	if o.TotalFundingObs > 0 {
		hi = append(hi, fmt.Sprintf("💰 total observed funding: $%s across returned awards", fmtUSD(o.TotalFundingObs)))
	}
	if len(o.TopPIs) > 0 {
		topPIs := []string{}
		for _, p := range o.TopPIs[:min2(5, len(o.TopPIs))] {
			topPIs = append(topPIs, fmt.Sprintf("%s (%d, $%s)", p.Name, p.GrantCount, fmtUSD(p.TotalFunding)))
		}
		hi = append(hi, "🧪 top PIs/co-PIs in returned set: "+strings.Join(topPIs, " | "))
	}
	if len(o.TopOrgs) > 0 {
		topOrgs := []string{}
		for _, oa := range o.TopOrgs[:min2(4, len(o.TopOrgs))] {
			topOrgs = append(topOrgs, fmt.Sprintf("%s (%dx, $%s, %s)", oa.Org, oa.GrantCount, fmtUSD(oa.TotalFunding), oa.YearRange))
		}
		hi = append(hi, "🏛  top institutions: "+strings.Join(topOrgs, " | "))
		if len(o.TopOrgs) > 1 && o.Mode == "pi_name" {
			hi = append(hi, fmt.Sprintf("⚡ %d distinct institutions across PI's NSF history — career mobility signal", len(o.TopOrgs)))
		}
	}
	if len(o.UniqueStateSet) > 1 {
		hi = append(hi, fmt.Sprintf("US states represented: %s", strings.Join(o.UniqueStateSet, ", ")))
	}
	if len(o.UniquePrograms) > 0 && len(o.UniquePrograms) <= 8 {
		hi = append(hi, "NSF programs: "+strings.Join(o.UniquePrograms, ", "))
	}
	return hi
}
