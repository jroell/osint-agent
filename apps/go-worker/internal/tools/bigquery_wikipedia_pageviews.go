package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type WikipediaPageviewPoint struct {
	DateHour string `json:"date_hour"`
	Views    int    `json:"views"`
}

type WikipediaPageviewsOutput struct {
	Title           string                  `json:"title"`
	Wiki            string                  `json:"wiki"`
	StartDate       string                  `json:"start_date"`
	EndDate         string                  `json:"end_date"`
	Granularity     string                  `json:"granularity"` // hourly | daily
	TotalViews      int                     `json:"total_views_in_window"`
	AverageDaily    float64                 `json:"average_daily_views"`
	PeakHour        string                  `json:"peak_hour,omitempty"`
	PeakHourViews   int                     `json:"peak_hour_views,omitempty"`
	Series          []WikipediaPageviewPoint `json:"series"`
	HighlightFindings []string              `json:"highlight_findings"`
	Source          string                  `json:"source"`
	TookMs          int64                   `json:"tookMs"`
	Note            string                  `json:"note,omitempty"`
}

// BigQueryWikipediaPageviews queries `bigquery-public-data.wikipedia.pageviews_<YYYY>`
// for hourly view counts of a Wikipedia article. Triangulates with Google Trends
// (search interest) and GDELT (news mentions) for cultural-attention measurement.
//
// Tables: `pageviews_2024`, `pageviews_2025`, `pageviews_2026` etc. — these
// are massive (hundreds of TB total) but date-partitioned via `datehour`.
//
// We constrain to specific dates + the `en` wiki + a single title to keep
// scan size in single-digit GB.
func BigQueryWikipediaPageviews(ctx context.Context, input map[string]any) (*WikipediaPageviewsOutput, error) {
	title, _ := input["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("input.title required (Wikipedia article title — case-sensitive, use underscores or spaces, e.g. 'Anthropic' or 'Claude_(language_model)')")
	}
	// Wikipedia uses underscores in URLs but the BQ table stores the version
	// with underscores. Replace spaces.
	titleBQ := strings.ReplaceAll(title, " ", "_")
	safeTitle := strings.ReplaceAll(titleBQ, "'", "")

	wiki, _ := input["wiki"].(string)
	if wiki == "" {
		wiki = "en"
	}
	safeWiki := strings.ReplaceAll(wiki, "'", "")

	daysBack := 30
	if v, ok := input["days_back"].(float64); ok && int(v) > 0 && int(v) <= 365 {
		daysBack = int(v)
	}
	granularity, _ := input["granularity"].(string)
	if granularity != "hourly" && granularity != "daily" {
		granularity = "daily"
	}

	now := time.Now().UTC()
	endDate := now
	startDate := endDate.AddDate(0, 0, -(daysBack - 1))
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	// Determine which year tables to query (e.g. 2025 + 2026)
	years := map[int]bool{}
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		years[d.Year()] = true
	}
	yearList := []int{}
	for y := range years {
		yearList = append(yearList, y)
	}
	sort.Ints(yearList)
	tableUnion := []string{}
	for _, y := range yearList {
		tableUnion = append(tableUnion, fmt.Sprintf("`bigquery-public-data.wikipedia.pageviews_%d`", y))
	}
	if len(tableUnion) == 0 {
		return nil, fmt.Errorf("no year tables for window")
	}
	fromClause := tableUnion[0]
	if len(tableUnion) > 1 {
		parts := []string{}
		for _, t := range tableUnion {
			parts = append(parts, "SELECT title, wiki, datehour, views FROM "+t)
		}
		fromClause = "(" + strings.Join(parts, " UNION ALL ") + ")"
	}

	start := time.Now()
	out := &WikipediaPageviewsOutput{
		Title: title, Wiki: wiki,
		StartDate: startStr, EndDate: endStr,
		Granularity: granularity,
		Source:      "bigquery-public-data.wikipedia.pageviews_*",
	}

	var sql string
	if granularity == "hourly" {
		sql = fmt.Sprintf(`
SELECT FORMAT_TIMESTAMP('%%Y-%%m-%%dT%%H:00:00Z', datehour) AS datehour_str,
       views
FROM %s
WHERE wiki = '%s'
  AND title = '%s'
  AND datehour >= TIMESTAMP('%s 00:00:00')
  AND datehour <= TIMESTAMP('%s 23:59:59')
ORDER BY datehour DESC LIMIT 720`, fromClause, safeWiki, safeTitle, startStr, endStr)
	} else {
		sql = fmt.Sprintf(`
SELECT FORMAT_DATE('%%Y-%%m-%%d', DATE(datehour)) AS day,
       SUM(views) AS views
FROM %s
WHERE wiki = '%s'
  AND title = '%s'
  AND datehour >= TIMESTAMP('%s 00:00:00')
  AND datehour <= TIMESTAMP('%s 23:59:59')
GROUP BY day ORDER BY day DESC LIMIT %d`, fromClause, safeWiki, safeTitle, startStr, endStr, daysBack+1)
	}

	rows, err := bqQuery(ctx, sql, daysBack*24+50)
	if err != nil {
		return nil, fmt.Errorf("pageviews query: %w", err)
	}

	totalViews := 0
	peakViews := 0
	peakHour := ""
	for _, r := range rows {
		p := WikipediaPageviewPoint{}
		if granularity == "hourly" {
			if v, ok := r["datehour_str"].(string); ok {
				p.DateHour = v
			}
		} else {
			if v, ok := r["day"].(string); ok {
				p.DateHour = v
			}
		}
		p.Views = parseBQInt(r["views"])
		totalViews += p.Views
		if p.Views > peakViews {
			peakViews = p.Views
			peakHour = p.DateHour
		}
		out.Series = append(out.Series, p)
	}
	out.TotalViews = totalViews
	if daysBack > 0 {
		out.AverageDaily = float64(totalViews) / float64(daysBack)
	}
	out.PeakHour = peakHour
	out.PeakHourViews = peakViews

	// Highlights
	highlights := []string{
		fmt.Sprintf("article: '%s' (%s.wiki)", title, wiki),
		fmt.Sprintf("window: %s → %s (%d days, %s granularity)", startStr, endStr, daysBack, granularity),
		fmt.Sprintf("total views: %d (avg daily: %.1f)", totalViews, out.AverageDaily),
	}
	if peakViews > 0 {
		highlights = append(highlights, fmt.Sprintf("peak: %d views @ %s", peakViews, peakHour))
	}
	if len(out.Series) == 0 {
		highlights = append(highlights, "⚠️  No data — title may be wrong (case-sensitive!) or article doesn't exist")
		out.Note = "Wikipedia titles are CASE SENSITIVE in BigQuery. Try the exact title from the Wikipedia URL (e.g. 'Anthropic' not 'anthropic'). Use spaces or underscores — both work."
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
