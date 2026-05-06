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

// LDALobbyingSearch queries the US Senate's Lobbying Disclosure Act
// database — every federal lobbying registration and quarterly report
// filed since 1995. Free, no-auth. Closes the political-OSINT chain:
//
//     GovTrack (bills + votes + members)
//     ↓
//     FEC (campaign donations to those members)
//     ↓
//     LDA (who paid lobbyists to influence those bills)
//
// Three modes:
//
//   - "filings_search"       : by registrant (lobbying firm), client
//                              (paying corp), filing year, filing period,
//                              issue code → list of filings
//   - "filing_detail"        : by filing_uuid → full record with all
//                              activities + named lobbyists + targeted
//                              government entities + income/expenses +
//                              filing PDF + foreign entities + affiliated
//                              organizations + conviction disclosures
//   - "contributions_search" : separate "contributions" endpoint for
//                              FECA contributions reported by lobbyists
//                              (different from FEC main filings — this is
//                              specifically lobbyist personal political
//                              giving as required under HLOGA 2007)

type LDALobbyist struct {
	Name            string `json:"name"`
	CoveredPosition string `json:"covered_position,omitempty"`
}

type LDAActivity struct {
	GeneralIssueCode   string        `json:"general_issue_code,omitempty"`
	IssueArea          string        `json:"issue_area,omitempty"`
	Description        string        `json:"description,omitempty"`
	GovernmentEntities []string      `json:"government_entities,omitempty"`
	Lobbyists          []LDALobbyist `json:"lobbyists,omitempty"`
}

type LDAFiling struct {
	FilingUUID        string        `json:"filing_uuid"`
	URL               string        `json:"url,omitempty"`
	FilingYear        int           `json:"filing_year"`
	FilingPeriod      string        `json:"filing_period,omitempty"`
	FilingType        string        `json:"filing_type,omitempty"`
	FilingTypeDisplay string        `json:"filing_type_display,omitempty"`
	RegistrantName    string        `json:"registrant_name,omitempty"`
	RegistrantID      int           `json:"registrant_id,omitempty"`
	RegistrantCountry string        `json:"registrant_country,omitempty"`
	RegistrantCity    string        `json:"registrant_city,omitempty"`
	RegistrantState   string        `json:"registrant_state,omitempty"`
	ClientName        string        `json:"client_name,omitempty"`
	ClientID          int           `json:"client_id,omitempty"`
	Income            string        `json:"income,omitempty"`
	Expenses          string        `json:"expenses,omitempty"`
	PostedBy          string        `json:"posted_by,omitempty"`
	PostedAt          string        `json:"posted_at,omitempty"`
	TerminationDate   string        `json:"termination_date,omitempty"`
	FilingDocumentURL string        `json:"filing_document_url,omitempty"`
	Activities        []LDAActivity `json:"activities,omitempty"`
	ForeignEntities   []string      `json:"foreign_entities,omitempty"`
	AffiliatedOrgs    []string      `json:"affiliated_organizations,omitempty"`
	ConvictionFlag    bool          `json:"conviction_disclosed,omitempty"`
}

type LDAContribution struct {
	FilingUUID         string  `json:"filing_uuid"`
	FilingYear         int     `json:"filing_year"`
	FilingPeriod       string  `json:"filing_period,omitempty"`
	ContributorName    string  `json:"contributor_name,omitempty"`
	ContributionsTotal float64 `json:"contributions_total"`
	ContributionsCount int     `json:"contributions_count"`
}

type LDALobbyingSearchOutput struct {
	Mode          string            `json:"mode"`
	Query         string            `json:"query,omitempty"`
	TotalCount    int               `json:"total_count,omitempty"`
	Returned      int               `json:"returned"`
	Filings       []LDAFiling       `json:"filings,omitempty"`
	Filing        *LDAFiling        `json:"filing,omitempty"`
	Contributions []LDAContribution `json:"contributions,omitempty"`

	// Aggregations
	UniqueLobbyists   []string `json:"unique_lobbyists,omitempty"`
	UniqueIssueCodes  []string `json:"unique_issue_codes,omitempty"`
	UniqueGovEntities []string `json:"unique_gov_entities,omitempty"`
	TotalSpend        float64  `json:"total_spend_usd,omitempty"`

	HighlightFindings []string `json:"highlight_findings"`
	Source            string   `json:"source"`
	TookMs            int64    `json:"tookMs"`
	Note              string   `json:"note,omitempty"`
}

func LDALobbyingSearch(ctx context.Context, input map[string]any) (*LDALobbyingSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["filing_uuid"]; ok {
			mode = "filing_detail"
		} else if _, ok := input["contributor_name"]; ok {
			mode = "contributions_search"
		} else {
			mode = "filings_search"
		}
	}

	out := &LDALobbyingSearchOutput{
		Mode:   mode,
		Source: "lda.senate.gov/api/v1",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	switch mode {
	case "filings_search":
		params := url.Values{}
		if v, ok := input["registrant_name"].(string); ok && v != "" {
			params.Set("registrant_name", v)
			out.Query = "registrant=" + v
		}
		if v, ok := input["client_name"].(string); ok && v != "" {
			params.Set("client_name", v)
			out.Query = "client=" + v
		}
		// Backward-compat: a generic "query" maps to client_name (more useful default)
		if v, ok := input["query"].(string); ok && v != "" && out.Query == "" {
			params.Set("client_name", v)
			out.Query = "client=" + v
		}
		if v, ok := input["filing_year"].(float64); ok && v > 0 {
			params.Set("filing_year", fmt.Sprintf("%d", int(v)))
		}
		if v, ok := input["filing_period"].(string); ok && v != "" {
			params.Set("filing_period", v)
		}
		if v, ok := input["filing_type"].(string); ok && v != "" {
			params.Set("filing_type", v)
		}
		if v, ok := input["issue_code"].(string); ok && v != "" {
			params.Set("filing_specific_lobbying_issues", v)
		}
		if v, ok := input["government_entity"].(string); ok && v != "" {
			params.Set("filing_government_entity", v)
		}
		// Also: filer_name lets you search lobbyist person names
		if v, ok := input["lobbyist_name"].(string); ok && v != "" {
			params.Set("filer_name", v)
		}
		if out.Query == "" {
			return nil, fmt.Errorf("at least one of registrant_name, client_name, or query required for filings_search")
		}
		pageSize := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			pageSize = int(l)
		}
		params.Set("page_size", fmt.Sprintf("%d", pageSize))
		// Newest first
		params.Set("ordering", "-dt_posted")

		body, err := ldaGet(ctx, cli, "https://lda.senate.gov/api/v1/filings/?"+params.Encode())
		if err != nil {
			return nil, err
		}
		if err := decodeLDAFilingsList(body, out); err != nil {
			return nil, err
		}

	case "filing_detail":
		uuid, _ := input["filing_uuid"].(string)
		uuid = strings.TrimSpace(uuid)
		if uuid == "" {
			return nil, fmt.Errorf("input.filing_uuid required for filing_detail")
		}
		out.Query = uuid
		body, err := ldaGet(ctx, cli, "https://lda.senate.gov/api/v1/filings/"+uuid+"/")
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("filing detail decode: %w", err)
		}
		f := convertLDAFiling(raw)
		out.Filing = &f
		out.Returned = 1

	case "contributions_search":
		params := url.Values{}
		if v, ok := input["contributor_name"].(string); ok && v != "" {
			params.Set("contributor_name", v)
			out.Query = "contributor=" + v
		}
		if v, ok := input["query"].(string); ok && v != "" && out.Query == "" {
			params.Set("contributor_name", v)
			out.Query = "contributor=" + v
		}
		if v, ok := input["filing_year"].(float64); ok && v > 0 {
			params.Set("filing_year", fmt.Sprintf("%d", int(v)))
		}
		if out.Query == "" {
			return nil, fmt.Errorf("input.contributor_name required for contributions_search")
		}
		pageSize := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			pageSize = int(l)
		}
		params.Set("page_size", fmt.Sprintf("%d", pageSize))
		body, err := ldaGet(ctx, cli, "https://lda.senate.gov/api/v1/contributions/?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Count   int              `json:"count"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("contributions decode: %w", err)
		}
		out.TotalCount = raw.Count
		for _, r := range raw.Results {
			c := LDAContribution{
				FilingUUID:   gtString(r, "filing_uuid"),
				FilingYear:   gtInt(r, "filing_year"),
				FilingPeriod: gtString(r, "filing_period"),
			}
			if cb, ok := r["contributor"].(map[string]any); ok {
				c.ContributorName = gtString(cb, "name")
			}
			c.ContributionsTotal = gtFloat(r, "contributions_total")
			if cs, ok := r["contributions"].([]any); ok {
				c.ContributionsCount = len(cs)
			}
			out.Contributions = append(out.Contributions, c)
		}
		out.Returned = len(out.Contributions)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: filings_search, filing_detail, contributions_search", mode)
	}

	// Aggregations
	lobbyistSet := map[string]struct{}{}
	issueSet := map[string]struct{}{}
	govSet := map[string]struct{}{}
	for _, f := range out.Filings {
		if amt := parseUSD(f.Income); amt > 0 {
			out.TotalSpend += amt
		}
		if amt := parseUSD(f.Expenses); amt > 0 {
			out.TotalSpend += amt
		}
		for _, a := range f.Activities {
			if a.GeneralIssueCode != "" {
				issueSet[a.GeneralIssueCode] = struct{}{}
			}
			for _, l := range a.Lobbyists {
				if l.Name != "" {
					lobbyistSet[l.Name] = struct{}{}
				}
			}
			for _, ge := range a.GovernmentEntities {
				if ge != "" {
					govSet[ge] = struct{}{}
				}
			}
		}
	}
	if out.Filing != nil {
		for _, a := range out.Filing.Activities {
			if a.GeneralIssueCode != "" {
				issueSet[a.GeneralIssueCode] = struct{}{}
			}
			for _, l := range a.Lobbyists {
				if l.Name != "" {
					lobbyistSet[l.Name] = struct{}{}
				}
			}
			for _, ge := range a.GovernmentEntities {
				govSet[ge] = struct{}{}
			}
		}
	}
	for k := range lobbyistSet {
		out.UniqueLobbyists = append(out.UniqueLobbyists, k)
	}
	sort.Strings(out.UniqueLobbyists)
	for k := range issueSet {
		out.UniqueIssueCodes = append(out.UniqueIssueCodes, k)
	}
	sort.Strings(out.UniqueIssueCodes)
	for k := range govSet {
		out.UniqueGovEntities = append(out.UniqueGovEntities, k)
	}
	sort.Strings(out.UniqueGovEntities)

	out.HighlightFindings = buildLDAHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// ---------- Helpers ----------

func decodeLDAFilingsList(body []byte, out *LDALobbyingSearchOutput) error {
	var raw struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("filings list decode: %w", err)
	}
	out.TotalCount = raw.Count
	for _, r := range raw.Results {
		out.Filings = append(out.Filings, convertLDAFiling(r))
	}
	out.Returned = len(out.Filings)
	return nil
}

func convertLDAFiling(r map[string]any) LDAFiling {
	f := LDAFiling{
		FilingUUID:        gtString(r, "filing_uuid"),
		URL:               gtString(r, "url"),
		FilingYear:        gtInt(r, "filing_year"),
		FilingPeriod:      gtString(r, "filing_period"),
		FilingType:        gtString(r, "filing_type"),
		FilingTypeDisplay: gtString(r, "filing_type_display"),
		Income:            gtString(r, "income"),
		Expenses:          gtString(r, "expenses"),
		PostedBy:          gtString(r, "posted_by_name"),
		PostedAt:          gtString(r, "dt_posted"),
		TerminationDate:   gtString(r, "termination_date"),
		FilingDocumentURL: gtString(r, "filing_document_url"),
		RegistrantCity:    gtString(r, "registrant_city"),
		RegistrantState:   gtString(r, "registrant_state"),
		RegistrantCountry: gtString(r, "registrant_country"),
	}
	// Registrant + client are nested
	if reg, ok := r["registrant"].(map[string]any); ok {
		f.RegistrantName = gtString(reg, "name")
		f.RegistrantID = gtInt(reg, "id")
	}
	if cl, ok := r["client"].(map[string]any); ok {
		f.ClientName = gtString(cl, "name")
		f.ClientID = gtInt(cl, "id")
	}
	// Lobbying activities
	if acts, ok := r["lobbying_activities"].([]any); ok {
		for _, ai := range acts {
			am, ok := ai.(map[string]any)
			if !ok {
				continue
			}
			a := LDAActivity{
				GeneralIssueCode: gtString(am, "general_issue_code_display"),
				IssueArea:        gtString(am, "general_issue_code"),
				Description:      gtString(am, "description"),
			}
			// Some endpoints use "general_issue_code_display" for label; fallback if not set
			if a.GeneralIssueCode == "" {
				a.GeneralIssueCode = gtString(am, "general_issue_code")
			}
			if ges, ok := am["government_entities"].([]any); ok {
				for _, gei := range ges {
					if gem, ok := gei.(map[string]any); ok {
						if name := gtString(gem, "name"); name != "" {
							a.GovernmentEntities = append(a.GovernmentEntities, name)
						}
					}
				}
			}
			if lobs, ok := am["lobbyists"].([]any); ok {
				for _, li := range lobs {
					lm, ok := li.(map[string]any)
					if !ok {
						continue
					}
					name := ""
					cov := ""
					if pp, ok := lm["lobbyist"].(map[string]any); ok {
						first := gtString(pp, "first_name")
						last := gtString(pp, "last_name")
						name = strings.TrimSpace(first + " " + last)
						cov = gtString(pp, "covered_position")
					}
					if name == "" {
						continue
					}
					a.Lobbyists = append(a.Lobbyists, LDALobbyist{
						Name:            name,
						CoveredPosition: cov,
					})
				}
			}
			f.Activities = append(f.Activities, a)
		}
	}
	// Foreign entities
	if fes, ok := r["foreign_entities"].([]any); ok {
		for _, fei := range fes {
			if fem, ok := fei.(map[string]any); ok {
				if name := gtString(fem, "name"); name != "" {
					f.ForeignEntities = append(f.ForeignEntities, name)
				}
			}
		}
	}
	// Affiliated organizations
	if aos, ok := r["affiliated_organizations"].([]any); ok {
		for _, aoi := range aos {
			if aom, ok := aoi.(map[string]any); ok {
				if name := gtString(aom, "name"); name != "" {
					f.AffiliatedOrgs = append(f.AffiliatedOrgs, name)
				}
			}
		}
	}
	// Conviction flag
	if cd, ok := r["conviction_disclosures"].([]any); ok {
		f.ConvictionFlag = len(cd) > 0
	}
	return f
}

// gtFloat extracts a number from m[key] across the wide range of value
// shapes that real upstream APIs serve.
//
// Handled types:
//   - float64, float32
//   - int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64
//   - json.Number (when a decoder uses UseNumber())
//   - bool (true → 1.0, false → 0.0)
//   - string via strconv.ParseFloat — strips commas (so "1,234.56" → 1234.56)
//     and surrounding whitespace; rejects partial parses (so "42abc" → 0,
//     not 42, which is the safe behavior for downstream comparisons)
//
// The empty-key and unknown-type cases return 0. See
// TestGtFloat_BroadTypeCoverageQuantitative for the proof.
func gtFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		// Strip thousands separators that real APIs commonly emit.
		s = strings.ReplaceAll(s, ",", "")
		// Strip a single trailing % so "42%" → 42.
		s = strings.TrimSuffix(s, "%")
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return 0
}

func parseUSD(s string) float64 {
	if s == "" {
		return 0
	}
	clean := strings.NewReplacer(",", "", "$", "").Replace(s)
	var f float64
	if _, err := fmt.Sscanf(clean, "%f", &f); err == nil {
		return f
	}
	return 0
}

func ldaGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LDA: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("LDA HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildLDAHighlights(o *LDALobbyingSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "filings_search":
		hi = append(hi, fmt.Sprintf("✓ %d filings match '%s' (returning %d, newest first)", o.TotalCount, o.Query, o.Returned))
		if o.TotalSpend > 0 {
			hi = append(hi, fmt.Sprintf("  💰 reported spend across results: %s", formatUSD(o.TotalSpend)))
		}
		if len(o.UniqueLobbyists) > 0 {
			lobs := o.UniqueLobbyists
			suffix := ""
			if len(lobs) > 6 {
				lobs = lobs[:6]
				suffix = fmt.Sprintf(" … +%d", len(o.UniqueLobbyists)-6)
			}
			hi = append(hi, fmt.Sprintf("  👤 unique lobbyists: %s%s", strings.Join(lobs, ", "), suffix))
		}
		if len(o.UniqueIssueCodes) > 0 {
			hi = append(hi, fmt.Sprintf("  📋 issues: %s", strings.Join(o.UniqueIssueCodes, ", ")))
		}
		if len(o.UniqueGovEntities) > 0 {
			gov := o.UniqueGovEntities
			suffix := ""
			if len(gov) > 4 {
				gov = gov[:4]
				suffix = fmt.Sprintf(" … +%d", len(o.UniqueGovEntities)-4)
			}
			hi = append(hi, fmt.Sprintf("  🏛️  targeted gov entities: %s%s", strings.Join(gov, ", "), suffix))
		}
		for i, f := range o.Filings {
			if i >= 5 {
				break
			}
			money := ""
			if f.Income != "" {
				money = " · income $" + f.Income
			} else if f.Expenses != "" {
				money = " · expenses $" + f.Expenses
			}
			activities := []string{}
			for _, a := range f.Activities {
				if a.Description != "" {
					activities = append(activities, hfTruncate(a.Description, 60))
				}
				if len(activities) >= 2 {
					break
				}
			}
			actStr := ""
			if len(activities) > 0 {
				actStr = " — " + strings.Join(activities, "; ")
			}
			hi = append(hi, fmt.Sprintf("  • [%d %s] %s for %s [%s]%s%s",
				f.FilingYear, f.FilingPeriod, f.RegistrantName, f.ClientName, f.FilingTypeDisplay, money, actStr))
		}

	case "filing_detail":
		if o.Filing == nil {
			hi = append(hi, fmt.Sprintf("✗ no filing for uuid %s", o.Query))
			break
		}
		f := o.Filing
		hi = append(hi, fmt.Sprintf("✓ %s — %s for client %s", f.FilingTypeDisplay, f.RegistrantName, f.ClientName))
		hi = append(hi, fmt.Sprintf("  filing year: %d %s · posted: %s by %s", f.FilingYear, f.FilingPeriod, f.PostedAt, f.PostedBy))
		if f.Income != "" {
			hi = append(hi, "  💰 income: $"+f.Income)
		}
		if f.Expenses != "" {
			hi = append(hi, "  💰 expenses: $"+f.Expenses)
		}
		if f.TerminationDate != "" {
			hi = append(hi, "  ⛔ terminated: "+f.TerminationDate)
		}
		if f.ConvictionFlag {
			hi = append(hi, "  ⚠️  has conviction disclosures")
		}
		if len(f.ForeignEntities) > 0 {
			hi = append(hi, "  🌐 foreign entities: "+strings.Join(f.ForeignEntities, ", "))
		}
		if len(f.AffiliatedOrgs) > 0 {
			hi = append(hi, "  🤝 affiliated orgs: "+strings.Join(f.AffiliatedOrgs, ", "))
		}
		for i, a := range f.Activities {
			if i >= 5 {
				break
			}
			lobs := []string{}
			for _, l := range a.Lobbyists {
				if l.CoveredPosition != "" {
					lobs = append(lobs, fmt.Sprintf("%s [%s]", l.Name, l.CoveredPosition))
				} else {
					lobs = append(lobs, l.Name)
				}
			}
			gov := strings.Join(a.GovernmentEntities, ", ")
			if gov != "" {
				gov = " → " + gov
			}
			lobStr := ""
			if len(lobs) > 0 {
				lobStr = " · lobbyists: " + strings.Join(lobs, "; ")
			}
			hi = append(hi, fmt.Sprintf("  📋 [%s] %s%s%s", a.GeneralIssueCode, hfTruncate(a.Description, 80), gov, lobStr))
		}
		if f.FilingDocumentURL != "" {
			hi = append(hi, "  📄 PDF: "+f.FilingDocumentURL)
		}

	case "contributions_search":
		hi = append(hi, fmt.Sprintf("✓ %d contribution filings match '%s' (returning %d)", o.TotalCount, o.Query, o.Returned))
		for i, c := range o.Contributions {
			if i >= 8 {
				break
			}
			tot := ""
			if c.ContributionsTotal > 0 {
				tot = fmt.Sprintf(" · total %s", formatUSD(c.ContributionsTotal))
			}
			hi = append(hi, fmt.Sprintf("  • [%d %s] %s — %d contributions%s",
				c.FilingYear, c.FilingPeriod, c.ContributorName, c.ContributionsCount, tot))
		}
	}
	return hi
}
