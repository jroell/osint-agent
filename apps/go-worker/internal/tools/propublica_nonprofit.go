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

// NonprofitSearchHit is one org returned by name-search.
type NonprofitSearchHit struct {
	Name       string `json:"name"`
	EIN        string `json:"ein"`
	StrEIN     string `json:"strein,omitempty"`
	City       string `json:"city,omitempty"`
	State      string `json:"state,omitempty"`
	NTEECode   string `json:"ntee_code,omitempty"`
	SubSection string `json:"subsection,omitempty"`
	Score      float64 `json:"score,omitempty"`
}

// NonprofitFiling is a single year's Form 990.
type NonprofitFiling struct {
	TaxYear         int    `json:"tax_year"`
	TaxPeriod       string `json:"tax_period,omitempty"`
	FormType        int    `json:"form_type,omitempty"`
	PDFURL          string `json:"pdf_url,omitempty"`
	TotalRevenue    int64  `json:"total_revenue,omitempty"`
	TotalExpenses   int64  `json:"total_expenses,omitempty"`
	TotalAssets     int64  `json:"total_assets,omitempty"`
	TotalLiabs      int64  `json:"total_liabilities,omitempty"`
	OfficerComp     int64  `json:"officer_compensation,omitempty"`
	PayrollTax      int64  `json:"payroll_tax,omitempty"`
	GrantsToIndiv   int64  `json:"grants_to_individuals,omitempty"`
	GrantsToGovs    int64  `json:"grants_to_governments,omitempty"`
}

// NonprofitOrgDetail is the full org record from the EIN endpoint.
type NonprofitOrgDetail struct {
	Name             string            `json:"name"`
	EIN              string            `json:"ein"`
	StrEIN           string            `json:"strein,omitempty"`
	Address          string            `json:"address,omitempty"`
	City             string            `json:"city,omitempty"`
	State            string            `json:"state,omitempty"`
	Zipcode          string            `json:"zipcode,omitempty"`
	NTEECode         string            `json:"ntee_code,omitempty"`
	NTEEDescription  string            `json:"ntee_description,omitempty"`
	SubSection       string            `json:"subsection,omitempty"`
	ClassificationCodes string         `json:"classification,omitempty"`
	RulingDate       string            `json:"ruling_date,omitempty"`
	HaveFilingsSince int               `json:"have_filings_since,omitempty"`
	HaveExtractsSince int              `json:"have_extracts_since,omitempty"`
	HavePDFsSince    int               `json:"have_pdfs_since,omitempty"`
	FilingsCount     int               `json:"filings_count"`
	Filings          []NonprofitFiling `json:"filings"`
	LatestRevenue    int64             `json:"latest_revenue,omitempty"`
	LatestAssets     int64             `json:"latest_assets,omitempty"`
	LatestOfficerComp int64            `json:"latest_officer_compensation,omitempty"`
	YearRange        string            `json:"year_range,omitempty"`
	TotalRevenueLifetime int64         `json:"total_revenue_lifetime,omitempty"`
}

// NonprofitOutput wraps both modes.
type NonprofitOutput struct {
	Mode             string                `json:"mode"`
	Query            string                `json:"query"`
	TotalRecords     int                   `json:"total_records"`
	SearchHits       []NonprofitSearchHit  `json:"search_hits,omitempty"`
	OrgDetail        *NonprofitOrgDetail   `json:"org_detail,omitempty"`
	HighlightFindings []string             `json:"highlight_findings"`
	Source           string                `json:"source"`
	TookMs           int64                 `json:"tookMs"`
	Note             string                `json:"note,omitempty"`
}

// raw search response
type ppSearchRaw struct {
	TotalResults int `json:"total_results"`
	Organizations []struct {
		Name       string  `json:"name"`
		EIN        any     `json:"ein"`
		StrEIN     string  `json:"strein"`
		City       string  `json:"city"`
		State      string  `json:"state"`
		NTEECode   string  `json:"ntee_code"`
		SubSection any     `json:"sub_section_code"`
		Score      float64 `json:"score"`
	} `json:"organizations"`
}

// raw org-detail response
type ppOrgRaw struct {
	Organization struct {
		Name             string `json:"name"`
		EIN              any    `json:"ein"`
		StrEIN           string `json:"strein"`
		Address          string `json:"address"`
		City             string `json:"city"`
		State            string `json:"state"`
		Zipcode          string `json:"zipcode"`
		NTEECode         string `json:"ntee_code"`
		NTEEDescription  string `json:"ntee_description"`
		SubSection       any    `json:"subseccd"`
		Classification   string `json:"classification_codes"`
		RulingDate       string `json:"ruling_date"`
		HaveFilingsSince any    `json:"have_filings_since_2001"`
		HaveExtractsSince any   `json:"have_extracts_since_2001"`
		HavePDFsSince    any    `json:"have_pdfs_since_2001"`
	} `json:"organization"`
	FilingsWithData []struct {
		TaxPrd          any    `json:"tax_prd"`
		TaxPrdYr        int    `json:"tax_prd_yr"`
		FormType        int    `json:"formtype"`
		PDFURL          string `json:"pdf_url"`
		TotalRevenue    int64  `json:"totrevenue"`
		TotalFuncExpns  int64  `json:"totfuncexpns"`
		TotalAssetsEnd  int64  `json:"totassetsend"`
		TotalLiabsEnd   int64  `json:"totliabend"`
		CompCurrOfcr    int64  `json:"compnsatncurrofcr"`
		PayrollTax      int64  `json:"payrolltx"`
		GrantsToIndiv   int64  `json:"grntstoindiv"`
		GrantsToGovt    int64  `json:"grntstogovt"`
	} `json:"filings_with_data"`
}

// ProPublicaNonprofit queries the ProPublica Nonprofit Explorer API for U.S.
// exempt organizations. Backed by IRS Form 990 filings (every U.S. tax-exempt
// org files annually). 1.8M+ orgs indexed.
//
// Two modes:
//   - "search"   : fuzzy name search → returns multiple orgs (great for ER
//                  disambiguation when "Mozilla Foundation" actually has 6
//                  variants in the DB).
//   - "org_detail" : full org + up to 13 years of filings by EIN.
//                  Pass either "200097189" or "20-0097189" (StrEIN).
//
// Why this matters for ER:
//   - Many people sit on nonprofit boards — Form 990 PDFs disclose officers
//     and directors. The JSON gives aggregate compensation; PDFs (linked
//     here) have individual names.
//   - Org compensation reveals scale + executive identity ($1M+ comp = a
//     short list of senior figures).
//   - Filing history reveals founding/dissolution timelines.
//   - NTEE codes classify the org (T22 = private grantmaking, U40 = computer
//     science research, etc.).
//   - Free, no auth.
func ProPublicaNonprofit(ctx context.Context, input map[string]any) (*NonprofitOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "search"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (org name for search mode, or EIN for org_detail mode)")
	}

	stateFilter, _ := input["state"].(string)
	stateFilter = strings.ToUpper(strings.TrimSpace(stateFilter))

	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &NonprofitOutput{
		Mode:   mode,
		Query:  query,
		Source: "projects.propublica.org/nonprofits",
	}
	start := time.Now()

	client := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "search":
		params := url.Values{}
		params.Set("q", query)
		if stateFilter != "" {
			params.Set("state[id]", stateFilter)
		}
		endpoint := "https://projects.propublica.org/nonprofits/api/v2/search.json?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/0.1")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("propublica search: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			return nil, fmt.Errorf("propublica %d: %s", resp.StatusCode, string(body))
		}
		var raw ppSearchRaw
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, fmt.Errorf("propublica decode: %w", err)
		}
		out.TotalRecords = raw.TotalResults
		for i, o := range raw.Organizations {
			if i >= limit {
				break
			}
			out.SearchHits = append(out.SearchHits, NonprofitSearchHit{
				Name:       o.Name,
				EIN:        anyToString(o.EIN),
				StrEIN:     o.StrEIN,
				City:       o.City,
				State:      o.State,
				NTEECode:   o.NTEECode,
				SubSection: anyToString(o.SubSection),
				Score:      o.Score,
			})
		}
		out.HighlightFindings = buildSearchHighlights(out)

	case "org_detail":
		ein := strings.ReplaceAll(query, "-", "")
		ein = strings.TrimSpace(ein)
		if ein == "" {
			return nil, fmt.Errorf("EIN cannot be empty in org_detail mode")
		}
		endpoint := "https://projects.propublica.org/nonprofits/api/v2/organizations/" + url.PathEscape(ein) + ".json"
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/0.1")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("propublica detail: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			out.Note = fmt.Sprintf("EIN %s not found in ProPublica index (org may not be tax-exempt or may be too new)", ein)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			return nil, fmt.Errorf("propublica %d: %s", resp.StatusCode, string(body))
		}
		var raw ppOrgRaw
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, fmt.Errorf("propublica detail decode: %w", err)
		}
		o := raw.Organization
		einStr := anyToString(o.EIN)
		strein := o.StrEIN
		if strein == "" && len(einStr) == 9 {
			strein = einStr[:2] + "-" + einStr[2:]
		}
		// Detect empty org (API returns 200 with all-empty fields for unknown EIN)
		if einStr == "" && o.Name == "" {
			out.Note = fmt.Sprintf("EIN %s not found in ProPublica index (org may not be tax-exempt or filings are too new)", ein)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		// API returns a "Unknown Organization" placeholder when EIN format is valid but
		// the org isn't in the ProPublica dataset. Surface this clearly.
		if strings.EqualFold(strings.TrimSpace(o.Name), "unknown organization") && len(raw.FilingsWithData) == 0 {
			out.Note = fmt.Sprintf("EIN %s returned 'Unknown Organization' placeholder with 0 filings — not in ProPublica's indexed Form 990 dataset", ein)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		detail := &NonprofitOrgDetail{
			Name:                o.Name,
			EIN:                 einStr,
			StrEIN:              strein,
			Address:             o.Address,
			City:                o.City,
			State:               o.State,
			Zipcode:             o.Zipcode,
			NTEECode:            o.NTEECode,
			NTEEDescription:     o.NTEEDescription,
			SubSection:          anyToString(o.SubSection),
			ClassificationCodes: o.Classification,
			RulingDate:          o.RulingDate,
			HaveFilingsSince:    anyToInt(o.HaveFilingsSince),
			HaveExtractsSince:   anyToInt(o.HaveExtractsSince),
			HavePDFsSince:       anyToInt(o.HavePDFsSince),
		}
		// filings — keep most recent first; sort by tax year desc to be safe
		raws := raw.FilingsWithData
		sort.SliceStable(raws, func(i, j int) bool {
			return raws[i].TaxPrdYr > raws[j].TaxPrdYr
		})
		minYr, maxYr := 0, 0
		var totalLifetimeRevenue int64
		for _, f := range raws {
			detail.Filings = append(detail.Filings, NonprofitFiling{
				TaxYear:       f.TaxPrdYr,
				TaxPeriod:     anyToString(f.TaxPrd),
				FormType:      f.FormType,
				PDFURL:        f.PDFURL,
				TotalRevenue:  f.TotalRevenue,
				TotalExpenses: f.TotalFuncExpns,
				TotalAssets:   f.TotalAssetsEnd,
				TotalLiabs:    f.TotalLiabsEnd,
				OfficerComp:   f.CompCurrOfcr,
				PayrollTax:    f.PayrollTax,
				GrantsToIndiv: f.GrantsToIndiv,
				GrantsToGovs:  f.GrantsToGovt,
			})
			totalLifetimeRevenue += f.TotalRevenue
			if f.TaxPrdYr > 0 {
				if minYr == 0 || f.TaxPrdYr < minYr {
					minYr = f.TaxPrdYr
				}
				if f.TaxPrdYr > maxYr {
					maxYr = f.TaxPrdYr
				}
			}
		}
		detail.FilingsCount = len(detail.Filings)
		if len(detail.Filings) > 0 {
			latest := detail.Filings[0]
			detail.LatestRevenue = latest.TotalRevenue
			detail.LatestAssets = latest.TotalAssets
			detail.LatestOfficerComp = latest.OfficerComp
		}
		if minYr > 0 && maxYr > 0 && minYr != maxYr {
			detail.YearRange = fmt.Sprintf("%d-%d", minYr, maxYr)
		} else if maxYr > 0 {
			detail.YearRange = fmt.Sprintf("%d", maxYr)
		}
		detail.TotalRevenueLifetime = totalLifetimeRevenue
		out.OrgDetail = detail
		out.TotalRecords = 1
		out.HighlightFindings = buildOrgDetailHighlights(detail)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, org_detail", mode)
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildSearchHighlights(out *NonprofitOutput) []string {
	hi := []string{fmt.Sprintf("%d total matching tax-exempt orgs (%d returned)", out.TotalRecords, len(out.SearchHits))}
	if len(out.SearchHits) >= 2 {
		hi = append(hi, fmt.Sprintf("⚠️  %d distinct orgs match — disambiguate by city/state/EIN before drilling down", len(out.SearchHits)))
	}
	stateAgg := map[string]int{}
	for _, h := range out.SearchHits {
		if h.State != "" {
			stateAgg[h.State]++
		}
	}
	if len(stateAgg) > 0 {
		states := []string{}
		for s, c := range stateAgg {
			states = append(states, fmt.Sprintf("%s=%d", s, c))
		}
		sort.Strings(states)
		hi = append(hi, "states: "+strings.Join(states, ", "))
	}
	if len(out.SearchHits) > 0 {
		topNames := []string{}
		for _, h := range out.SearchHits[:min2(3, len(out.SearchHits))] {
			topNames = append(topNames, fmt.Sprintf("'%s' (EIN %s, %s, %s)", h.Name, h.StrEIN, h.City, h.State))
		}
		hi = append(hi, "top results: "+strings.Join(topNames, "; "))
	}
	return hi
}

func buildOrgDetailHighlights(d *NonprofitOrgDetail) []string {
	hi := []string{
		fmt.Sprintf("%s — EIN %s — %s, %s", d.Name, d.StrEIN, d.City, d.State),
		fmt.Sprintf("%d annual Form 990 filings (years: %s)", d.FilingsCount, d.YearRange),
	}
	if d.NTEECode != "" {
		desc := d.NTEEDescription
		if desc == "" {
			desc = "(no description)"
		}
		hi = append(hi, fmt.Sprintf("NTEE classification: %s — %s", d.NTEECode, desc))
	}
	if d.LatestRevenue > 0 {
		hi = append(hi, fmt.Sprintf("most-recent filing: revenue $%s, assets $%s, officer compensation $%s",
			fmtUSD(d.LatestRevenue), fmtUSD(d.LatestAssets), fmtUSD(d.LatestOfficerComp)))
	}
	if d.TotalRevenueLifetime > 0 && d.FilingsCount > 1 {
		hi = append(hi, fmt.Sprintf("lifetime cumulative revenue across %d filings: $%s", d.FilingsCount, fmtUSD(d.TotalRevenueLifetime)))
	}
	if d.LatestOfficerComp > 1_000_000 {
		hi = append(hi, fmt.Sprintf("⚠️  officer compensation exceeds $1M — high-profile executive likely; pull most-recent PDF for officer name"))
	}
	if len(d.Filings) > 0 && d.Filings[0].PDFURL != "" {
		hi = append(hi, "most-recent 990 PDF: "+d.Filings[0].PDFURL)
	}
	return hi
}

func fmtUSD(v int64) string {
	if v == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", v)
	out := []byte{}
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	}
	return ""
}

func anyToInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case bool:
		if x {
			return 1
		}
	case string:
		var n int
		fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
