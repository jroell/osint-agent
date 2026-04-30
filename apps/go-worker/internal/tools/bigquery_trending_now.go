package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type TrendingTerm struct {
	Term       string `json:"term"`
	Score      int    `json:"score"`
	Rank       int    `json:"rank,omitempty"`
	Week       string `json:"week"`
	DMAName    string `json:"dma_name,omitempty"`
	Country    string `json:"country_name,omitempty"`
}

type BigQueryTrendingNowOutput struct {
	Mode             string         `json:"mode"`             // global_top | global_rising | by_country | by_dma | search
	Geo              string         `json:"geo,omitempty"`
	Country          string         `json:"country,omitempty"`
	WeekRange        string         `json:"week_range,omitempty"`
	Terms            []TrendingTerm `json:"terms"`
	TotalReturned    int            `json:"total_returned"`
	UniqueTermsCount int            `json:"unique_terms_count"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source           string         `json:"source"`
	TookMs           int64          `json:"tookMs"`
	Note             string         `json:"note,omitempty"`
}

// BigQueryTrendingNow uses the official Google Trends BigQuery dataset:
//   - bigquery-public-data.google_trends.top_terms (US DMA-level top 25)
//   - bigquery-public-data.google_trends.top_rising_terms (newly-trending)
//   - bigquery-public-data.google_trends.international_top_terms (per country)
//   - bigquery-public-data.google_trends.international_top_rising_terms
//
// Modes:
//   - "global_top": top US trending terms (current week, all DMAs)
//   - "global_rising": top US rising terms (newly-trending)
//   - "by_country": top trending in a specific country (use 'country' input)
//   - "search": find a specific term in the dataset (only works if it's
//     in the top 25 for some DMA — niche keywords won't appear)
//
// Pairs with `google_trends_lookup` (pytrends, supports any keyword but rate-
// limited). Use `bigquery_trending_now` for current pulse, `google_trends_lookup`
// for arbitrary keyword history.
//
// CAVEAT: only top 25 terms per DMA per week. Niche keywords won't appear.
// CAVEAT: requires gcloud-authenticated host; production needs ADC migration.
func BigQueryTrendingNow(ctx context.Context, input map[string]any) (*BigQueryTrendingNowOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "global_top"
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}
	weeksBack := 1
	if v, ok := input["weeks_back"].(float64); ok && int(v) > 0 && int(v) <= 52 {
		weeksBack = int(v)
	}
	country, _ := input["country"].(string)
	country = strings.TrimSpace(country)
	searchTerm, _ := input["search_term"].(string)
	searchTerm = strings.TrimSpace(strings.ToLower(searchTerm))

	start := time.Now()
	out := &BigQueryTrendingNowOutput{Mode: mode, Source: "bigquery-public-data.google_trends"}

	var sql string
	switch mode {
	case "global_top":
		sql = fmt.Sprintf(`
SELECT term, MAX(score) AS score, MIN(rank) AS rank, week
FROM `+"`bigquery-public-data.google_trends.top_terms`"+`
WHERE week = (SELECT MAX(week) FROM `+"`bigquery-public-data.google_trends.top_terms`"+`)
GROUP BY term, week
ORDER BY rank ASC
LIMIT %d`, limit)
	case "global_rising":
		sql = fmt.Sprintf(`
SELECT term, MAX(score) AS score, MIN(rank) AS rank, week
FROM `+"`bigquery-public-data.google_trends.top_rising_terms`"+`
WHERE week = (SELECT MAX(week) FROM `+"`bigquery-public-data.google_trends.top_rising_terms`"+`)
GROUP BY term, week
ORDER BY rank ASC
LIMIT %d`, limit)
	case "by_country":
		if country == "" {
			return nil, errors.New("input.country required for mode='by_country' (e.g. 'United States', 'Germany', 'India')")
		}
		// Sanitize country to prevent SQL injection
		safeCountry := strings.ReplaceAll(country, "'", "")
		sql = fmt.Sprintf(`
SELECT term, MAX(score) AS score, MIN(rank) AS rank, week, country_name
FROM `+"`bigquery-public-data.google_trends.international_top_terms`"+`
WHERE country_name = '%s'
  AND week = (SELECT MAX(week) FROM `+"`bigquery-public-data.google_trends.international_top_terms`"+` WHERE country_name = '%s')
GROUP BY term, week, country_name
ORDER BY rank ASC
LIMIT %d`, safeCountry, safeCountry, limit)
	case "search":
		if searchTerm == "" {
			return nil, errors.New("input.search_term required for mode='search'")
		}
		safeTerm := strings.ReplaceAll(searchTerm, "'", "")
		// Search in last N weeks
		sql = fmt.Sprintf(`
SELECT term, MAX(score) AS score, MIN(rank) AS rank, week
FROM `+"`bigquery-public-data.google_trends.top_terms`"+`
WHERE LOWER(term) LIKE '%%%s%%'
  AND week >= DATE_SUB(CURRENT_DATE(), INTERVAL %d WEEK)
GROUP BY term, week
ORDER BY week DESC, rank ASC
LIMIT %d`, safeTerm, weeksBack, limit)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — try global_top, global_rising, by_country, or search", mode)
	}

	rows, err := bqQuery(ctx, sql, limit)
	if err != nil {
		return nil, err
	}

	uniqueTerms := map[string]bool{}
	for _, r := range rows {
		t := TrendingTerm{}
		if v, ok := r["term"].(string); ok {
			t.Term = v
			uniqueTerms[v] = true
		}
		if v, ok := r["score"].(string); ok {
			fmt.Sscanf(v, "%d", &t.Score)
		} else if v, ok := r["score"].(float64); ok {
			t.Score = int(v)
		}
		if v, ok := r["rank"].(string); ok {
			fmt.Sscanf(v, "%d", &t.Rank)
		} else if v, ok := r["rank"].(float64); ok {
			t.Rank = int(v)
		}
		if v, ok := r["week"].(string); ok {
			t.Week = v
		}
		if v, ok := r["country_name"].(string); ok {
			t.Country = v
		}
		if v, ok := r["dma_name"].(string); ok {
			t.DMAName = v
		}
		out.Terms = append(out.Terms, t)
	}
	out.TotalReturned = len(out.Terms)
	out.UniqueTermsCount = len(uniqueTerms)

	if mode == "by_country" {
		out.Country = country
	}

	// Highlights
	highlights := []string{}
	if out.TotalReturned == 0 {
		highlights = append(highlights, "No data — for niche keywords, try google_trends_lookup (pytrends) instead")
	} else if mode == "global_top" {
		top3 := []string{}
		for i, t := range out.Terms {
			if i >= 3 {
				break
			}
			top3 = append(top3, fmt.Sprintf("'%s' (rank=%d)", t.Term, t.Rank))
		}
		highlights = append(highlights, fmt.Sprintf("week %s top: %s", out.Terms[0].Week, strings.Join(top3, ", ")))
	} else if mode == "global_rising" {
		top3 := []string{}
		for i, t := range out.Terms {
			if i >= 3 {
				break
			}
			top3 = append(top3, fmt.Sprintf("'%s' (score=%d)", t.Term, t.Score))
		}
		highlights = append(highlights, fmt.Sprintf("week %s rising: %s", out.Terms[0].Week, strings.Join(top3, ", ")))
	} else if mode == "by_country" {
		top3 := []string{}
		for i, t := range out.Terms {
			if i >= 3 {
				break
			}
			top3 = append(top3, fmt.Sprintf("'%s'", t.Term))
		}
		highlights = append(highlights, fmt.Sprintf("%s week %s: %s", country, out.Terms[0].Week, strings.Join(top3, ", ")))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
