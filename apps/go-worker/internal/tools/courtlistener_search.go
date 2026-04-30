package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// CourtListenerCase represents one search result.
type CourtListenerCase struct {
	CaseName       string   `json:"case_name,omitempty"`
	CourtID        string   `json:"court_id,omitempty"`
	CourtName      string   `json:"court,omitempty"`
	DateFiled      string   `json:"date_filed,omitempty"`
	DocketNumber   string   `json:"docket_number,omitempty"`
	Citations      []string `json:"citations,omitempty"`
	Status         string   `json:"status,omitempty"`
	JudgeName      string   `json:"judge,omitempty"`
	OpinionType    string   `json:"opinion_type,omitempty"`
	NatureOfSuit   string   `json:"nature_of_suit,omitempty"`
	Snippet        string   `json:"snippet,omitempty"`
	AbsoluteURL    string   `json:"absolute_url,omitempty"`
	DocketID       int64    `json:"docket_id,omitempty"`
	OpinionID      int64    `json:"opinion_id,omitempty"`
}

// CourtListenerCourtAggregate is the per-court mention counter.
type CourtListenerCourtAggregate struct {
	CourtID    string `json:"court_id"`
	CourtName  string `json:"court_name"`
	CaseCount  int    `json:"case_count"`
}

// CourtListenerOutput is the full response.
type CourtListenerOutput struct {
	Mode               string                        `json:"mode"`
	Query              string                        `json:"query"`
	CourtFilter        string                        `json:"court_filter,omitempty"`
	TotalRecords       int                           `json:"total_records"`
	Returned           int                           `json:"returned"`
	Cases              []CourtListenerCase           `json:"cases"`
	TopByCourt         []CourtListenerCourtAggregate `json:"top_courts,omitempty"`
	UniqueDocketNums   []string                      `json:"unique_docket_numbers,omitempty"`
	YearRange          string                        `json:"year_range,omitempty"`
	HighlightFindings  []string                      `json:"highlight_findings"`
	Source             string                        `json:"source"`
	TookMs             int64                         `json:"tookMs"`
	Note               string                        `json:"note,omitempty"`
}

type courtListenerRawResult struct {
	CaseName       string   `json:"caseName"`
	CaseNameFull   string   `json:"caseNameFull"`
	CourtID        string   `json:"court_id"`
	Court          string   `json:"court"`
	DateFiled      string   `json:"dateFiled"`
	DocketNumber   string   `json:"docketNumber"`
	Citation       []string `json:"citation"`
	Status         string   `json:"status"`
	Judge          string   `json:"judge"`
	Type           string   `json:"type"`
	NatureOfSuit   string   `json:"nature_of_suit"`
	Snippet        string   `json:"snippet"`
	AbsoluteURL    string   `json:"absolute_url"`
	DocketID       int64    `json:"docket_id"`
	ID             int64    `json:"id"`
}

type courtListenerRawResp struct {
	Count    int                      `json:"count"`
	Results  []courtListenerRawResult `json:"results"`
	Detail   string                   `json:"detail"`
}

// CourtListenerSearch queries CourtListener (free.law) for federal court
// records. CourtListener indexes ~5M federal opinions across all U.S.
// circuits + the Supreme Court, plus the RECAP archive of PACER dockets
// (active and historical case filings).
//
// Two modes:
//
//   - "opinions"  : decided cases with published opinions. Best for finding
//                   precedent, judicial reasoning, well-known parties.
//   - "dockets"   : active and historical case filings (RECAP archive).
//                   Best for finding pending litigation a person/org is
//                   named in, even before opinions are written.
//
// Why this matters for ER:
//   - A person named as plaintiff/defendant in a federal docket is a HARD
//     identity link to the named individual + their attorneys + the venue.
//   - Bankruptcy cases reveal financial state + creditor names.
//   - Securities cases expose officers/directors of public companies.
//   - Parties to FOIA suits expose journalists/researchers chasing a topic.
//   - Filters: court (e.g. "scotus" Supreme Court, "cand" N.D. California,
//     "nysd" S.D. New York, "flsd" S.D. Florida, "deb" Delaware Bankruptcy).
//   - Free without auth (lower rate limit). Set COURTLISTENER_TOKEN env var
//     for higher limits + access to bulk endpoints.
func CourtListenerSearch(ctx context.Context, input map[string]any) (*CourtListenerOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "opinions"
	}

	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (party name, case keyword, docket number, etc.)")
	}

	courtFilter, _ := input["court"].(string)
	courtFilter = strings.TrimSpace(courtFilter)

	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	// CourtListener uses type=o for opinions, type=r for RECAP/dockets
	var queryType string
	switch mode {
	case "opinions":
		queryType = "o"
	case "dockets":
		queryType = "r"
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: opinions, dockets", mode)
	}

	start := time.Now()

	params := url.Values{}
	params.Set("q", query)
	params.Set("type", queryType)
	params.Set("page_size", fmt.Sprintf("%d", limit))
	if courtFilter != "" {
		params.Set("court", courtFilter)
	}

	endpoint := "https://www.courtlistener.com/api/rest/v4/search/?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	if tok := os.Getenv("COURTLISTENER_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Token "+tok)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("courtlistener request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("courtlistener %d: %s", resp.StatusCode, string(body))
	}

	var raw courtListenerRawResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("courtlistener decode: %w", err)
	}
	if raw.Detail != "" {
		return nil, fmt.Errorf("courtlistener: %s", raw.Detail)
	}

	out := &CourtListenerOutput{
		Mode:         mode,
		Query:        query,
		CourtFilter:  courtFilter,
		TotalRecords: raw.Count,
		Source:       "courtlistener.com (free.law)",
	}

	courtAgg := map[string]*CourtListenerCourtAggregate{}
	docketSet := map[string]bool{}
	minYear, maxYear := 0, 0

	for _, r := range raw.Results {
		c := CourtListenerCase{
			CaseName:     firstNonEmptyCL(r.CaseName, r.CaseNameFull),
			CourtID:      r.CourtID,
			CourtName:    r.Court,
			DateFiled:    r.DateFiled,
			DocketNumber: r.DocketNumber,
			Citations:    r.Citation,
			Status:       r.Status,
			JudgeName:    r.Judge,
			OpinionType:  r.Type,
			NatureOfSuit: r.NatureOfSuit,
			Snippet:      cleanSnippet(r.Snippet),
		}
		if r.AbsoluteURL != "" {
			c.AbsoluteURL = "https://www.courtlistener.com" + r.AbsoluteURL
		}
		if mode == "dockets" {
			c.DocketID = r.ID
		} else {
			c.OpinionID = r.ID
			c.DocketID = r.DocketID
		}
		out.Cases = append(out.Cases, c)

		// Aggregate by court
		if r.CourtID != "" {
			ag, found := courtAgg[r.CourtID]
			if !found {
				ag = &CourtListenerCourtAggregate{CourtID: r.CourtID, CourtName: r.Court}
				courtAgg[r.CourtID] = ag
			}
			ag.CaseCount++
		}
		if r.DocketNumber != "" {
			docketSet[r.DocketNumber] = true
		}
		// Year tracking from dateFiled (YYYY-MM-DD)
		if len(r.DateFiled) >= 4 {
			var y int
			fmt.Sscanf(r.DateFiled[:4], "%d", &y)
			if y > 1700 && y < 2100 {
				if minYear == 0 || y < minYear {
					minYear = y
				}
				if y > maxYear {
					maxYear = y
				}
			}
		}
	}
	out.Returned = len(out.Cases)

	for _, ag := range courtAgg {
		out.TopByCourt = append(out.TopByCourt, *ag)
	}
	sort.SliceStable(out.TopByCourt, func(i, j int) bool {
		return out.TopByCourt[i].CaseCount > out.TopByCourt[j].CaseCount
	})
	if len(out.TopByCourt) > 10 {
		out.TopByCourt = out.TopByCourt[:10]
	}

	for d := range docketSet {
		out.UniqueDocketNums = append(out.UniqueDocketNums, d)
	}
	sort.Strings(out.UniqueDocketNums)
	if len(out.UniqueDocketNums) > 30 {
		out.UniqueDocketNums = out.UniqueDocketNums[:30]
	}

	if minYear > 0 && maxYear > 0 && minYear != maxYear {
		out.YearRange = fmt.Sprintf("%d-%d", minYear, maxYear)
	} else if maxYear > 0 {
		out.YearRange = fmt.Sprintf("%d", maxYear)
	}

	// Highlights
	hi := []string{}
	hi = append(hi, fmt.Sprintf("%d total %s records (%d returned this page)", out.TotalRecords, mode, out.Returned))
	if courtFilter != "" {
		hi = append(hi, fmt.Sprintf("scoped to court=%s", courtFilter))
	}
	if len(out.TopByCourt) > 0 {
		topCourts := []string{}
		for _, c := range out.TopByCourt[:min(5, len(out.TopByCourt))] {
			topCourts = append(topCourts, fmt.Sprintf("%s (%d)", c.CourtID, c.CaseCount))
		}
		hi = append(hi, "top courts in returned set: "+strings.Join(topCourts, ", "))
	}
	if out.YearRange != "" {
		hi = append(hi, "year range: "+out.YearRange)
	}
	if mode == "dockets" && out.TotalRecords > 0 {
		hi = append(hi, fmt.Sprintf("⚠️  query is named in %d federal docket(s) — strong ER signal for litigation history", out.TotalRecords))
	}
	if mode == "opinions" && out.TotalRecords > 0 {
		hi = append(hi, fmt.Sprintf("query appears in %d published federal opinion(s) — case law trail", out.TotalRecords))
	}
	out.HighlightFindings = hi
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func firstNonEmptyCL(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func cleanSnippet(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	s = strings.ReplaceAll(s, "</mark>", "")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}
