package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SECFiling struct {
	AccessionNumber string `json:"accession_number"`
	FilingDate      string `json:"filing_date"`
	Form            string `json:"form"`
	Size            int    `json:"size,omitempty"`
	IsXBRL          bool   `json:"is_xbrl,omitempty"`
	PrimaryDocument string `json:"primary_document,omitempty"`
	URL             string `json:"url"`
}

type SECEdgarOutput struct {
	Query     string      `json:"query"`
	CIK       string      `json:"cik"`
	Name      string      `json:"name,omitempty"`
	Tickers   []string    `json:"tickers,omitempty"`
	SIC       string      `json:"sic,omitempty"`
	SICDesc   string      `json:"sic_description,omitempty"`
	Filings   []SECFiling `json:"filings"`
	Count     int         `json:"count"`
	TookMs    int64       `json:"tookMs"`
	Source    string      `json:"source"`
}

// SECEdgarFilingSearch resolves a ticker (e.g. "AAPL") or numeric CIK and
// returns the company's recent EDGAR filings. Free, no API key. The SEC
// requires a User-Agent identifying the requesting party — we set a project-
// identifying UA on every request.
func SECEdgarFilingSearch(ctx context.Context, input map[string]any) (*SECEdgarOutput, error) {
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("input.query required (ticker like \"AAPL\" or CIK like \"320193\")")
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	formFilter := strings.ToUpper(strings.TrimSpace(asString(input["form"])))

	cik, err := resolveCIK(ctx, query)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	subURL := fmt.Sprintf("https://data.sec.gov/submissions/CIK%010s.json", cik)
	body, err := secGet(ctx, subURL)
	if err != nil {
		return nil, fmt.Errorf("submissions fetch: %w", err)
	}
	var sub struct {
		Name           string   `json:"name"`
		Tickers        []string `json:"tickers"`
		SIC            string   `json:"sic"`
		SICDescription string   `json:"sicDescription"`
		Filings        struct {
			Recent struct {
				AccessionNumber []string `json:"accessionNumber"`
				FilingDate      []string `json:"filingDate"`
				Form            []string `json:"form"`
				Size            []int    `json:"size"`
				IsXBRL          []int    `json:"isXBRL"`
				PrimaryDocument []string `json:"primaryDocument"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if err := json.Unmarshal(body, &sub); err != nil {
		return nil, fmt.Errorf("submissions parse: %w", err)
	}

	out := &SECEdgarOutput{
		Query:   query,
		CIK:     cik,
		Name:    sub.Name,
		Tickers: sub.Tickers,
		SIC:     sub.SIC,
		SICDesc: sub.SICDescription,
		Source:  "data.sec.gov",
		TookMs:  time.Since(start).Milliseconds(),
	}
	r := sub.Filings.Recent
	for i := range r.AccessionNumber {
		if formFilter != "" && !strings.EqualFold(r.Form[i], formFilter) {
			continue
		}
		f := SECFiling{
			AccessionNumber: r.AccessionNumber[i],
			FilingDate:      r.FilingDate[i],
			Form:            r.Form[i],
		}
		if i < len(r.Size) {
			f.Size = r.Size[i]
		}
		if i < len(r.IsXBRL) {
			f.IsXBRL = r.IsXBRL[i] == 1
		}
		if i < len(r.PrimaryDocument) {
			f.PrimaryDocument = r.PrimaryDocument[i]
		}
		// Build the canonical filing URL.
		clean := strings.ReplaceAll(f.AccessionNumber, "-", "")
		f.URL = fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s",
			strings.TrimLeft(cik, "0"), clean, f.PrimaryDocument)
		out.Filings = append(out.Filings, f)
		if len(out.Filings) >= limit {
			break
		}
	}
	out.Count = len(out.Filings)
	return out, nil
}

// resolveCIK accepts a ticker symbol (e.g. "AAPL") or a numeric CIK string.
func resolveCIK(ctx context.Context, query string) (string, error) {
	if isPositiveInt(query) {
		return query, nil
	}
	body, err := secGet(ctx, "https://www.sec.gov/files/company_tickers.json")
	if err != nil {
		return "", fmt.Errorf("ticker map fetch: %w", err)
	}
	var raw map[string]struct {
		CIKStr int    `json:"cik_str"`
		Ticker string `json:"ticker"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("ticker map parse: %w", err)
	}
	upper := strings.ToUpper(query)
	for _, entry := range raw {
		if strings.ToUpper(entry.Ticker) == upper {
			return strconv.Itoa(entry.CIKStr), nil
		}
	}
	return "", fmt.Errorf("no SEC entity for %q (try the numeric CIK directly)", query)
}

func secGet(ctx context.Context, url string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// SEC requires a real, identifying User-Agent for all programmatic access.
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (jroell@batterii.com)")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("sec %d", resp.StatusCode)
	}
	const maxBody = 16 << 20
	body := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if len(body) > maxBody {
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	return body, nil
}

func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
