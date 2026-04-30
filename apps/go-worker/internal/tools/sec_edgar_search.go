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
	"sync"
	"time"
)

// SECEdgarSearch queries SEC EDGAR (free, no-auth) across three modes:
//
//   - "lookup_company"    : name or ticker → CIK + full company metadata
//                           (addresses, SIC industry code, exchange, all tickers).
//   - "company_filings"   : CIK → recent filings list, optionally filtered by
//                           form type (e.g. "4" for insider transactions,
//                           "13D" / "13G" for 5%+ beneficial ownership,
//                           "8-K" for material events, "10-K"/"10-Q" for
//                           financials, "DEF 14A" for proxy statements).
//                           Returns up to 1000 most-recent filings.
//   - "full_text_search"  : keyword search across all 30M+ SEC filings since
//                           2001 via efts.sec.gov. Optional date and form
//                           filters.
//
// REQUIRES SEC's User-Agent etiquette: must include contact email or
// SEC will rate-limit / block. We send "osint-agent/1.0
// (jroell@batterii.com)".

type SECCompanyXAddress struct {
	Street1   string `json:"street1,omitempty"`
	Street2   string `json:"street2,omitempty"`
	City      string `json:"city,omitempty"`
	State     string `json:"state,omitempty"`
	ZipCode   string `json:"zip_code,omitempty"`
	Country   string `json:"country,omitempty"`
	IsForeign bool   `json:"is_foreign_location,omitempty"`
}

type SECCompanyX struct {
	CIK              string             `json:"cik"`
	Name             string             `json:"name"`
	Tickers          []string           `json:"tickers,omitempty"`
	Exchanges        []string           `json:"exchanges,omitempty"`
	SIC              string             `json:"sic,omitempty"`
	SICDescription   string             `json:"sic_description,omitempty"`
	StateOfInc       string             `json:"state_of_incorporation,omitempty"`
	FiscalYearEnd    string             `json:"fiscal_year_end,omitempty"`
	BusinessAddress  *SECCompanyXAddress `json:"business_address,omitempty"`
	MailingAddress   *SECCompanyXAddress `json:"mailing_address,omitempty"`
	FormerNames      []string           `json:"former_names,omitempty"`
	Phone            string             `json:"phone,omitempty"`
	Website          string             `json:"website,omitempty"`
	EntityType       string             `json:"entity_type,omitempty"`
	Flags            string             `json:"flags,omitempty"`
}

type SECFilingX struct {
	AccessionNumber  string `json:"accession_number"`
	Form             string `json:"form"`
	FilingDate       string `json:"filing_date"`
	ReportDate       string `json:"report_date,omitempty"`
	AcceptanceDate   string `json:"acceptance_date,omitempty"`
	PrimaryDocument  string `json:"primary_document,omitempty"`
	PrimaryDocDesc   string `json:"primary_doc_description,omitempty"`
	Items            []string `json:"items,omitempty"` // 8-K item codes
	IsInlineXBRL     bool   `json:"is_inline_xbrl,omitempty"`
	FilingURL        string `json:"filing_url,omitempty"`
	FilingIndexURL   string `json:"filing_index_url,omitempty"`
}

type SECFTSHit struct {
	AccessionNumber string   `json:"accession_number"`
	Form            string   `json:"form"`
	FilingDate      string   `json:"filing_date"`
	CompanyNames    []string `json:"company_names,omitempty"`
	CIKs            []string `json:"ciks,omitempty"`
	FileDescription string   `json:"file_description,omitempty"`
	FileType        string   `json:"file_type,omitempty"`
	Items           []string `json:"items,omitempty"`
	URL             string   `json:"url,omitempty"`
}

type SECEdgarSearchOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	CIK               string         `json:"cik,omitempty"`

	// lookup_company results
	CompanyMatches    []SECCompanyX   `json:"company_matches,omitempty"`

	// company_filings results
	Company           *SECCompanyX    `json:"company,omitempty"`
	Filings           []SECFilingX    `json:"filings,omitempty"`
	TotalFilings      int            `json:"total_filings,omitempty"`

	// full_text_search results
	FTSHits           []SECFTSHit    `json:"fts_hits,omitempty"`
	FTSTotal          int            `json:"fts_total,omitempty"`

	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
	Note              string         `json:"note,omitempty"`
}

// Ticker map cache (refreshed once per worker process — list changes slowly)
var (
	secTickerMap     map[string]secTickerEntry // upper-case ticker → entry
	secNameMap       []secTickerEntry          // for name search (linear scan, ~13k items, fast)
	secTickerMapOnce sync.Once
	secTickerErr     error
)

type secTickerEntry struct {
	CIK    int64  `json:"cik_str"`
	Ticker string `json:"ticker"`
	Title  string `json:"title"`
}

func secLoadTickerMap(ctx context.Context) error {
	secTickerMapOnce.Do(func() {
		cli := &http.Client{Timeout: 30 * time.Second}
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.sec.gov/files/company_tickers.json", nil)
		req.Header.Set("User-Agent", "osint-agent/1.0 (jroell@batterii.com)")
		resp, err := cli.Do(req)
		if err != nil {
			secTickerErr = fmt.Errorf("ticker map fetch: %w", err)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		// Format: {"0":{"cik_str":...,"ticker":...,"title":...}, "1":{...}, ...}
		var raw map[string]secTickerEntry
		if err := json.Unmarshal(body, &raw); err != nil {
			secTickerErr = fmt.Errorf("ticker map decode: %w", err)
			return
		}
		secTickerMap = make(map[string]secTickerEntry, len(raw))
		secNameMap = make([]secTickerEntry, 0, len(raw))
		for _, e := range raw {
			secTickerMap[strings.ToUpper(e.Ticker)] = e
			secNameMap = append(secNameMap, e)
		}
	})
	return secTickerErr
}

func formatCIK(cik int64) string {
	return fmt.Sprintf("%010d", cik)
}

// SECEdgarSearch is the main entry point.
func SECEdgarSearch(ctx context.Context, input map[string]any) (*SECEdgarSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// auto-detect: cik → company_filings, query → lookup_company OR full_text
		// Disambiguate query mode by presence of "form" filter
		if _, ok := input["cik"]; ok {
			mode = "company_filings"
		} else if _, ok := input["forms"]; ok {
			mode = "full_text_search"
		} else {
			mode = "lookup_company"
		}
	}

	out := &SECEdgarSearchOutput{
		Mode:   mode,
		Source: "data.sec.gov + efts.sec.gov + www.sec.gov",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "lookup_company":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.query required for lookup_company (name or ticker)")
		}
		out.Query = q
		if err := secLoadTickerMap(ctx); err != nil {
			return nil, err
		}
		// Try ticker exact match first
		matches := []SECCompanyX{}
		if e, ok := secTickerMap[strings.ToUpper(q)]; ok {
			c, err := secFetchCompany(ctx, cli, formatCIK(e.CIK))
			if err == nil {
				matches = append(matches, *c)
			}
		}
		// If no ticker match, fuzzy name match (case-insensitive substring + prefix)
		if len(matches) == 0 {
			qLower := strings.ToLower(q)
			ranked := []struct {
				score int
				entry secTickerEntry
			}{}
			for _, e := range secNameMap {
				tLower := strings.ToLower(e.Title)
				score := 0
				switch {
				case tLower == qLower:
					score = 100
				case strings.HasPrefix(tLower, qLower):
					score = 80
				case strings.Contains(tLower, qLower):
					score = 60
				}
				if score > 0 {
					ranked = append(ranked, struct {
						score int
						entry secTickerEntry
					}{score, e})
				}
			}
			sort.SliceStable(ranked, func(i, j int) bool {
				if ranked[i].score != ranked[j].score {
					return ranked[i].score > ranked[j].score
				}
				return len(ranked[i].entry.Title) < len(ranked[j].entry.Title)
			})
			limit := 5
			if l, ok := input["limit"].(float64); ok && l > 0 && l <= 20 {
				limit = int(l)
			}
			for i, r := range ranked {
				if i >= limit {
					break
				}
				c, err := secFetchCompany(ctx, cli, formatCIK(r.entry.CIK))
				if err != nil {
					continue
				}
				matches = append(matches, *c)
			}
		}
		out.CompanyMatches = matches

	case "company_filings":
		cik, _ := input["cik"].(string)
		cik = strings.TrimSpace(cik)
		if cik == "" {
			// Resolve from query/ticker
			q, _ := input["query"].(string)
			q = strings.TrimSpace(q)
			if q == "" {
				return nil, fmt.Errorf("input.cik or input.query required")
			}
			if err := secLoadTickerMap(ctx); err != nil {
				return nil, err
			}
			if e, ok := secTickerMap[strings.ToUpper(q)]; ok {
				cik = formatCIK(e.CIK)
			} else {
				return nil, fmt.Errorf("could not resolve '%s' to a CIK — try lookup_company first", q)
			}
		}
		// Normalize to 10-digit format
		cik = strings.TrimPrefix(cik, "CIK")
		// pad with leading zeros
		for len(cik) < 10 && len(cik) > 0 {
			cik = "0" + cik
		}
		out.CIK = cik

		c, err := secFetchCompany(ctx, cli, cik)
		if err != nil {
			return nil, err
		}
		out.Company = c

		// Optional form filter (e.g. "4", "8-K", "13D" — comma-separated for multi)
		formsFilter, _ := input["forms"].(string)
		formsFilter = strings.TrimSpace(formsFilter)
		formSet := map[string]bool{}
		if formsFilter != "" {
			for _, f := range strings.Split(formsFilter, ",") {
				formSet[strings.ToUpper(strings.TrimSpace(f))] = true
			}
		}

		// Optional date filters
		startDate, _ := input["start_date"].(string)
		endDate, _ := input["end_date"].(string)

		filings, total, err := secFetchFilings(ctx, cli, cik, formSet, startDate, endDate)
		if err != nil {
			return nil, err
		}
		// Hard limit on emitted rows
		limit := 50
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
			limit = int(l)
		}
		if len(filings) > limit {
			out.Note = fmt.Sprintf("returning first %d of %d matching filings", limit, len(filings))
			filings = filings[:limit]
		}
		out.Filings = filings
		out.TotalFilings = total

	case "full_text_search":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		formsFilter, _ := input["forms"].(string)
		formsFilter = strings.TrimSpace(formsFilter)
		startDate, _ := input["start_date"].(string)
		endDate, _ := input["end_date"].(string)
		ciks, _ := input["ciks"].(string)

		if q == "" && formsFilter == "" && ciks == "" {
			return nil, fmt.Errorf("at least one of input.query, input.forms, or input.ciks required")
		}
		out.Query = q
		hits, total, err := secFullTextSearch(ctx, cli, q, formsFilter, ciks, startDate, endDate)
		if err != nil {
			return nil, err
		}
		limit := 30
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
			limit = int(l)
		}
		if len(hits) > limit {
			out.Note = fmt.Sprintf("returning first %d of %d (capped) full-text hits — total matches: %d", limit, len(hits), total)
			hits = hits[:limit]
		}
		out.FTSHits = hits
		out.FTSTotal = total

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: lookup_company, company_filings, full_text_search", mode)
	}

	out.HighlightFindings = buildSECHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// secFetchCompany pulls submissions JSON for a given CIK.
func secFetchCompany(ctx context.Context, cli *http.Client, cik string) (*SECCompanyX, error) {
	url := fmt.Sprintf("https://data.sec.gov/submissions/CIK%s.json", cik)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0 (jroell@batterii.com)")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("submissions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("submissions HTTP %d for CIK %s", resp.StatusCode, cik)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	var raw struct {
		CIK              string   `json:"cik"`
		Name             string   `json:"name"`
		Tickers          []string `json:"tickers"`
		Exchanges        []string `json:"exchanges"`
		SIC              string   `json:"sic"`
		SICDescription   string   `json:"sicDescription"`
		StateOfIncorporation string `json:"stateOfIncorporation"`
		FiscalYearEnd    string   `json:"fiscalYearEnd"`
		Phone            string   `json:"phone"`
		Website          string   `json:"website"`
		EntityType       string   `json:"entityType"`
		FormerNames      []struct {
			Name string `json:"name"`
		} `json:"formerNames"`
		Flags        string `json:"flags"`
		Addresses    struct {
			Mailing  json.RawMessage `json:"mailing"`
			Business json.RawMessage `json:"business"`
		} `json:"addresses"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	c := &SECCompanyX{
		CIK:            raw.CIK,
		Name:           raw.Name,
		Tickers:        raw.Tickers,
		Exchanges:      raw.Exchanges,
		SIC:            raw.SIC,
		SICDescription: raw.SICDescription,
		StateOfInc:     raw.StateOfIncorporation,
		FiscalYearEnd:  raw.FiscalYearEnd,
		Phone:          raw.Phone,
		Website:        raw.Website,
		EntityType:     raw.EntityType,
	}
	for _, n := range raw.FormerNames {
		if n.Name != "" {
			c.FormerNames = append(c.FormerNames, n.Name)
		}
	}
	c.MailingAddress = parseSECAddress(raw.Addresses.Mailing)
	c.BusinessAddress = parseSECAddress(raw.Addresses.Business)
	return c, nil
}

func parseSECAddress(raw json.RawMessage) *SECCompanyXAddress {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var a struct {
		Street1 string `json:"street1"`
		Street2 string `json:"street2"`
		City    string `json:"city"`
		State   string `json:"stateOrCountry"`
		ZipCode string `json:"zipCode"`
		Country string `json:"country"`
		IsForeign int   `json:"isForeignLocation"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil
	}
	if a.Street1 == "" && a.City == "" {
		return nil
	}
	return &SECCompanyXAddress{
		Street1:   a.Street1,
		Street2:   a.Street2,
		City:      a.City,
		State:     a.State,
		ZipCode:   a.ZipCode,
		Country:   a.Country,
		IsForeign: a.IsForeign != 0,
	}
}

// secFetchFilings pulls the filings.recent block from submissions and
// converts it to typed filings, filtered by form set and date range.
func secFetchFilings(ctx context.Context, cli *http.Client, cik string, formSet map[string]bool, startDate, endDate string) ([]SECFilingX, int, error) {
	url := fmt.Sprintf("https://data.sec.gov/submissions/CIK%s.json", cik)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0 (jroell@batterii.com)")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("filings fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	var raw struct {
		CIK     string `json:"cik"`
		Filings struct {
			Recent struct {
				AccessionNumber  []string   `json:"accessionNumber"`
				FilingDate       []string   `json:"filingDate"`
				ReportDate       []string   `json:"reportDate"`
				AcceptanceDateTime []string `json:"acceptanceDateTime"`
				Form             []string   `json:"form"`
				PrimaryDocument  []string   `json:"primaryDocument"`
				PrimaryDocDescription []string `json:"primaryDocDescription"`
				Items            []string   `json:"items"`
				IsInlineXBRL     []int      `json:"isInlineXBRL"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, err
	}

	r := raw.Filings.Recent
	total := len(r.AccessionNumber)
	out := []SECFilingX{}
	for i := 0; i < total; i++ {
		form := upperOrEmpty(r.Form, i)
		fd := safeIdx(r.FilingDate, i)
		if len(formSet) > 0 && !formSet[form] {
			continue
		}
		if startDate != "" && fd < startDate {
			continue
		}
		if endDate != "" && fd > endDate {
			continue
		}
		acc := safeIdx(r.AccessionNumber, i)
		accNoDash := strings.ReplaceAll(acc, "-", "")
		// strip leading zeros from CIK for archive URL
		cikNum := strings.TrimLeft(cik, "0")
		if cikNum == "" {
			cikNum = "0"
		}
		filingURL := fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s",
			cikNum, accNoDash, safeIdx(r.PrimaryDocument, i))
		indexURL := fmt.Sprintf("https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=%s&action=getcompany",
			cik)
		items := []string{}
		if it := safeIdx(r.Items, i); it != "" {
			items = strings.Split(it, ",")
			for j, it := range items {
				items[j] = strings.TrimSpace(it)
			}
		}
		isInline := false
		if i < len(r.IsInlineXBRL) {
			isInline = r.IsInlineXBRL[i] == 1
		}
		out = append(out, SECFilingX{
			AccessionNumber: acc,
			Form:            safeIdx(r.Form, i),
			FilingDate:      fd,
			ReportDate:      safeIdx(r.ReportDate, i),
			AcceptanceDate:  safeIdx(r.AcceptanceDateTime, i),
			PrimaryDocument: safeIdx(r.PrimaryDocument, i),
			PrimaryDocDesc:  safeIdx(r.PrimaryDocDescription, i),
			Items:           items,
			IsInlineXBRL:    isInline,
			FilingURL:       filingURL,
			FilingIndexURL:  indexURL,
		})
	}
	return out, total, nil
}

// secFullTextSearch hits the public efts.sec.gov full-text endpoint.
func secFullTextSearch(ctx context.Context, cli *http.Client, q, forms, ciks, startDate, endDate string) ([]SECFTSHit, int, error) {
	params := url.Values{}
	if q != "" {
		params.Set("q", "\""+q+"\"")
	}
	if forms != "" {
		params.Set("forms", forms)
	}
	if ciks != "" {
		params.Set("ciks", ciks)
	}
	if startDate != "" {
		params.Set("dateRange", "custom")
		params.Set("startdt", startDate)
		if endDate == "" {
			endDate = time.Now().Format("2006-01-02")
		}
		params.Set("enddt", endDate)
	} else if endDate != "" {
		params.Set("dateRange", "custom")
		params.Set("startdt", "2001-01-01")
		params.Set("enddt", endDate)
	}
	endpoint := "https://efts.sec.gov/LATEST/search-index?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0 (jroell@batterii.com)")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("FTS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("FTS HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var raw struct {
		Hits struct {
			Total struct{ Value int `json:"value"` } `json:"total"`
			Hits  []struct {
				ID     string `json:"_id"`
				Source struct {
					CIKs            []string `json:"ciks"`
					DisplayNames    []string `json:"display_names"`
					Form            string   `json:"form"`
					FileDate        string   `json:"file_date"`
					Adsh            string   `json:"adsh"`
					FileType        string   `json:"file_type"`
					FileDescription string   `json:"file_description"`
					Items           []string `json:"items"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, err
	}
	hits := []SECFTSHit{}
	for _, h := range raw.Hits.Hits {
		s := h.Source
		// build URL to the filing
		fileURL := ""
		if len(s.CIKs) > 0 && s.Adsh != "" {
			cikNum := strings.TrimLeft(s.CIKs[0], "0")
			if cikNum == "" {
				cikNum = "0"
			}
			adshNoDash := strings.ReplaceAll(s.Adsh, "-", "")
			// _id format: "<adsh>:<filename>"
			parts := strings.Split(h.ID, ":")
			fname := ""
			if len(parts) > 1 {
				fname = parts[1]
			}
			if fname != "" {
				fileURL = fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s", cikNum, adshNoDash, fname)
			}
		}
		hits = append(hits, SECFTSHit{
			AccessionNumber: s.Adsh,
			Form:            s.Form,
			FilingDate:      s.FileDate,
			CompanyNames:    s.DisplayNames,
			CIKs:            s.CIKs,
			FileDescription: s.FileDescription,
			FileType:        s.FileType,
			Items:           s.Items,
			URL:             fileURL,
		})
	}
	return hits, raw.Hits.Total.Value, nil
}

func upperOrEmpty(arr []string, i int) string {
	return strings.ToUpper(safeIdx(arr, i))
}

func safeIdx(arr []string, i int) string {
	if i < 0 || i >= len(arr) {
		return ""
	}
	return arr[i]
}

func buildSECHighlights(o *SECEdgarSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "lookup_company":
		hi = append(hi, fmt.Sprintf("✓ %d company match(es) for '%s'", len(o.CompanyMatches), o.Query))
		for i, c := range o.CompanyMatches {
			if i >= 5 {
				break
			}
			tickers := strings.Join(c.Tickers, ", ")
			if tickers == "" {
				tickers = "(no tickers)"
			}
			ex := strings.Join(c.Exchanges, "/")
			loc := ""
			if c.BusinessAddress != nil {
				loc = c.BusinessAddress.City + ", " + c.BusinessAddress.State
			}
			hi = append(hi, fmt.Sprintf("  • %s — CIK %s — %s [%s] — %s — %s", c.Name, c.CIK, tickers, ex, c.SICDescription, loc))
			if len(c.FormerNames) > 0 {
				hi = append(hi, "    former names: "+strings.Join(c.FormerNames, "; "))
			}
		}
	case "company_filings":
		if o.Company != nil {
			hi = append(hi, fmt.Sprintf("✓ %s (CIK %s) — %s", o.Company.Name, o.Company.CIK, o.Company.SICDescription))
			if len(o.Company.Tickers) > 0 {
				hi = append(hi, "  tickers: "+strings.Join(o.Company.Tickers, ", "))
			}
		}
		hi = append(hi, fmt.Sprintf("filings returned: %d (of %d in recent index)", len(o.Filings), o.TotalFilings))
		// Group by form for a quick breakdown
		formCount := map[string]int{}
		for _, f := range o.Filings {
			formCount[f.Form]++
		}
		formKeys := make([]string, 0, len(formCount))
		for k := range formCount {
			formKeys = append(formKeys, k)
		}
		sort.SliceStable(formKeys, func(i, j int) bool { return formCount[formKeys[i]] > formCount[formKeys[j]] })
		breakdown := []string{}
		for _, k := range formKeys {
			breakdown = append(breakdown, fmt.Sprintf("%s×%d", k, formCount[k]))
		}
		if len(breakdown) > 0 {
			hi = append(hi, "  by form: "+strings.Join(breakdown, "  "))
		}
		// Surface insider-transactions / 5%+ holders specifically
		insider := []SECFilingX{}
		holders := []SECFilingX{}
		material := []SECFilingX{}
		for _, f := range o.Filings {
			switch {
			case f.Form == "4" || f.Form == "3" || f.Form == "5":
				insider = append(insider, f)
			case strings.HasPrefix(f.Form, "SC 13") || f.Form == "13F-HR" || f.Form == "13D" || f.Form == "13G":
				holders = append(holders, f)
			case f.Form == "8-K":
				material = append(material, f)
			}
		}
		if len(insider) > 0 {
			hi = append(hi, fmt.Sprintf("  📊 INSIDER TRANSACTIONS (Forms 3/4/5): %d", len(insider)))
			for i, f := range insider {
				if i >= 3 {
					break
				}
				hi = append(hi, fmt.Sprintf("    [%s] %s — %s", f.FilingDate, f.Form, f.PrimaryDocDesc))
			}
		}
		if len(holders) > 0 {
			hi = append(hi, fmt.Sprintf("  🏛️  BENEFICIAL OWNERS (13D/13G/13F): %d", len(holders)))
			for i, f := range holders {
				if i >= 3 {
					break
				}
				hi = append(hi, fmt.Sprintf("    [%s] %s — %s", f.FilingDate, f.Form, f.PrimaryDocDesc))
			}
		}
		if len(material) > 0 {
			hi = append(hi, fmt.Sprintf("  ⚠️  MATERIAL EVENTS (8-K): %d", len(material)))
			for i, f := range material {
				if i >= 3 {
					break
				}
				it := strings.Join(f.Items, ",")
				if it != "" {
					it = " items=" + it
				}
				hi = append(hi, fmt.Sprintf("    [%s]%s — %s", f.FilingDate, it, f.PrimaryDocDesc))
			}
		}
	case "full_text_search":
		hi = append(hi, fmt.Sprintf("✓ %d total hits (returning %d) for query '%s'", o.FTSTotal, len(o.FTSHits), o.Query))
		for i, h := range o.FTSHits {
			if i >= 5 {
				break
			}
			name := ""
			if len(h.CompanyNames) > 0 {
				name = h.CompanyNames[0]
			}
			hi = append(hi, fmt.Sprintf("  [%s] %s — %s — %s", h.FilingDate, h.Form, name, h.FileDescription))
		}
	}
	return hi
}
