package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type PatentRecord struct {
	PublicationNumber string   `json:"publication_number"`
	CountryCode       string   `json:"country_code,omitempty"`
	KindCode          string   `json:"kind_code,omitempty"`
	Title             string   `json:"title,omitempty"`
	PublicationDate   string   `json:"publication_date,omitempty"`
	FilingDate        string   `json:"filing_date,omitempty"`
	Assignees         []string `json:"assignees,omitempty"`
	Inventors         []string `json:"inventors,omitempty"`
	GooglePatentsURL  string   `json:"google_patents_url"`
}

type PatentAggCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type BQPatentsOutput struct {
	Mode             string             `json:"mode"`
	Query            string             `json:"query"`
	StartDate        string             `json:"filing_date_after,omitempty"`
	TotalReturned    int                `json:"total_returned"`
	Patents          []PatentRecord     `json:"patents"`
	TopAssignees     []PatentAggCount   `json:"top_assignees,omitempty"`
	TopInventors     []PatentAggCount   `json:"top_inventors,omitempty"`
	UniqueInventors  []string           `json:"unique_inventors,omitempty"`
	UniqueAssignees  []string           `json:"unique_assignees,omitempty"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source           string             `json:"source"`
	TookMs           int64              `json:"tookMs"`
	Note             string             `json:"note,omitempty"`
}

// BigQueryPatents queries patents-public-data.patents.publications — global
// patent catalog (US + EPO + WIPO + 100+ national patent offices).
//
// Modes:
//   - "assignee_search" (default): patents owned by a company name fuzzy-match
//   - "inventor_search": patents by an inventor (full name)
//   - "keyword_search": patents whose title contains a phrase
//
// Returns: patent records (publication number + title + dates + assignees + inventors),
// top assignees aggregation, top inventors aggregation, unique inventor list (recruiting intel).
//
// Use cases:
//   - "Who's patenting X technology?" — competitive intel
//   - Inventor → company mapping (recruiting)
//   - Prior-art search before filing
//   - M&A signals (orgs acquiring patent portfolios)
func BigQueryPatents(ctx context.Context, input map[string]any) (*BQPatentsOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "assignee_search"
	}
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	safeQ := strings.ReplaceAll(q, "'", "")
	safeQ = strings.ReplaceAll(safeQ, "\\", "")

	yearsBack := 5
	if v, ok := input["years_back"].(float64); ok && int(v) > 0 && int(v) <= 30 {
		yearsBack = int(v)
	}
	limit := 30
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 200 {
		limit = int(v)
	}
	cutoffDate := time.Now().AddDate(-yearsBack, 0, 0).Format("20060102")

	start := time.Now()
	out := &BQPatentsOutput{
		Mode: mode, Query: q,
		StartDate: cutoffDate,
		Source:    "patents-public-data.patents.publications",
	}

	var whereClause string
	switch mode {
	case "assignee_search":
		whereClause = fmt.Sprintf(`EXISTS (SELECT 1 FROM UNNEST(assignee_harmonized) AS a WHERE LOWER(a.name) LIKE '%%%s%%')`,
			strings.ToLower(safeQ))
	case "inventor_search":
		whereClause = fmt.Sprintf(`EXISTS (SELECT 1 FROM UNNEST(inventor_harmonized) AS i WHERE LOWER(i.name) LIKE '%%%s%%')`,
			strings.ToLower(safeQ))
	case "keyword_search":
		whereClause = fmt.Sprintf(`EXISTS (SELECT 1 FROM UNNEST(title_localized) AS t WHERE LOWER(t.text) LIKE '%%%s%%')`,
			strings.ToLower(safeQ))
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use assignee_search, inventor_search, or keyword_search", mode)
	}

	// Patents
	sql := fmt.Sprintf(`
SELECT publication_number, country_code, kind_code,
       (SELECT t.text FROM UNNEST(title_localized) AS t WHERE t.language = 'en' LIMIT 1) AS title_en,
       publication_date, filing_date,
       ARRAY(SELECT a.name FROM UNNEST(assignee_harmonized) AS a) AS assignees,
       ARRAY(SELECT i.name FROM UNNEST(inventor_harmonized) AS i) AS inventors
FROM `+"`patents-public-data.patents.publications`"+`
WHERE %s
  AND filing_date >= %s
ORDER BY publication_date DESC LIMIT %d`, whereClause, cutoffDate, limit)

	rows, err := bqQuery(ctx, sql, limit)
	if err != nil {
		return nil, fmt.Errorf("patents query: %w", err)
	}

	assigneeCounts := map[string]int{}
	inventorCounts := map[string]int{}
	for _, r := range rows {
		p := PatentRecord{}
		if v, ok := r["publication_number"].(string); ok {
			p.PublicationNumber = v
			p.GooglePatentsURL = "https://patents.google.com/patent/" + v
		}
		if v, ok := r["country_code"].(string); ok {
			p.CountryCode = v
		}
		if v, ok := r["kind_code"].(string); ok {
			p.KindCode = v
		}
		if v, ok := r["title_en"].(string); ok {
			p.Title = v
		}
		if v, ok := r["publication_date"].(string); ok {
			p.PublicationDate = formatBQDate(v)
		}
		if v, ok := r["filing_date"].(string); ok {
			p.FilingDate = formatBQDate(v)
		}
		// Assignees / Inventors from BQ are returned as JSON arrays
		if assignees, ok := r["assignees"].([]any); ok {
			for _, a := range assignees {
				if s, ok := a.(string); ok && s != "" {
					p.Assignees = append(p.Assignees, s)
					assigneeCounts[s]++
				}
			}
		}
		if inventors, ok := r["inventors"].([]any); ok {
			for _, i := range inventors {
				if s, ok := i.(string); ok && s != "" {
					p.Inventors = append(p.Inventors, s)
					inventorCounts[s]++
				}
			}
		}
		out.Patents = append(out.Patents, p)
	}
	out.TotalReturned = len(out.Patents)

	// Top assignees
	for k, v := range assigneeCounts {
		out.TopAssignees = append(out.TopAssignees, PatentAggCount{Key: k, Count: v})
		out.UniqueAssignees = append(out.UniqueAssignees, k)
	}
	sort.Slice(out.TopAssignees, func(i, j int) bool { return out.TopAssignees[i].Count > out.TopAssignees[j].Count })
	if len(out.TopAssignees) > 10 {
		out.TopAssignees = out.TopAssignees[:10]
	}
	sort.Strings(out.UniqueAssignees)

	// Top inventors
	for k, v := range inventorCounts {
		out.TopInventors = append(out.TopInventors, PatentAggCount{Key: k, Count: v})
		out.UniqueInventors = append(out.UniqueInventors, k)
	}
	sort.Slice(out.TopInventors, func(i, j int) bool { return out.TopInventors[i].Count > out.TopInventors[j].Count })
	if len(out.TopInventors) > 25 {
		out.TopInventors = out.TopInventors[:25]
	}
	sort.Strings(out.UniqueInventors)

	// Highlights
	highlights := []string{
		fmt.Sprintf("%d patents matching '%s' in mode=%s (filed since %s)", out.TotalReturned, q, mode, formatBQDate(cutoffDate)),
	}
	if len(out.TopAssignees) > 0 {
		topa := []string{}
		for i, a := range out.TopAssignees {
			if i >= 3 {
				break
			}
			topa = append(topa, fmt.Sprintf("%s(%d)", a.Key, a.Count))
		}
		highlights = append(highlights, "top assignees: "+strings.Join(topa, ", "))
	}
	if len(out.TopInventors) > 0 {
		topi := []string{}
		for i, inv := range out.TopInventors {
			if i >= 5 {
				break
			}
			topi = append(topi, fmt.Sprintf("%s(%d)", inv.Key, inv.Count))
		}
		highlights = append(highlights, "top inventors: "+strings.Join(topi, ", "))
	}
	if len(out.UniqueInventors) > 0 {
		highlights = append(highlights, fmt.Sprintf("%d unique inventors identified — recruiting goldmine", len(out.UniqueInventors)))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// formatBQDate converts BQ "20240101" to "2024-01-01"; passes ISO through.
func formatBQDate(s string) string {
	if len(s) == 8 && !strings.Contains(s, "-") {
		return s[0:4] + "-" + s[4:6] + "-" + s[6:8]
	}
	return s
}
