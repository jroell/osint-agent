package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type GDELTArticle struct {
	Date       string  `json:"date"`        // YYYY-MM-DDTHH:MM:SS UTC
	URL        string  `json:"url"`
	Tone       float64 `json:"tone"`
	Positive   float64 `json:"positive"`
	Negative   float64 `json:"negative"`
	Polarity   float64 `json:"polarity"`
	WordCount  int     `json:"word_count"`
	NamesNear  []string `json:"names_excerpt,omitempty"`
}

type GDELTNameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type GDELTToneSummary struct {
	ArticleCount int     `json:"article_count"`
	AvgTone      float64 `json:"avg_tone"`
	AvgPositive  float64 `json:"avg_positive"`
	AvgNegative  float64 `json:"avg_negative"`
	AvgPolarity  float64 `json:"avg_polarity"`
}

type GDELTOutput struct {
	Mode             string             `json:"mode"`
	Query            string             `json:"query"`
	StartDate        string             `json:"start_date"`
	EndDate          string             `json:"end_date"`
	Articles         []GDELTArticle     `json:"articles,omitempty"`
	TopCooccurring   []GDELTNameCount   `json:"top_cooccurring_entities,omitempty"`
	ToneSummary      *GDELTToneSummary  `json:"tone_summary,omitempty"`
	UniqueDomains    []string           `json:"unique_domains,omitempty"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source           string             `json:"source"`
	TookMs           int64              `json:"tookMs"`
	Note             string             `json:"note,omitempty"`
}

func parseTone(v2tone string) (tone, pos, neg, pol float64, words int) {
	parts := strings.Split(v2tone, ",")
	if len(parts) < 6 {
		return
	}
	fmt.Sscanf(parts[0], "%f", &tone)
	fmt.Sscanf(parts[1], "%f", &pos)
	fmt.Sscanf(parts[2], "%f", &neg)
	fmt.Sscanf(parts[3], "%f", &pol)
	if len(parts) >= 7 {
		fmt.Sscanf(parts[6], "%d", &words)
	}
	return
}

func extractDomain(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	if i := strings.Index(url, "/"); i > 0 {
		return url[:i]
	}
	return url
}

// BigQueryGDELT queries GDELT GKG (Global Knowledge Graph) on BigQuery.
//
// GDELT continuously scrapes English-language news worldwide every 15min
// and extracts entities, themes, locations, and sentiment per article.
//
// Modes:
//   - "org_mentions": find articles mentioning a name/org/keyword, with
//     sentiment summary + top co-mentioned entities
//   - "theme_search": find articles by GDELT theme code (CYBER_ATTACK,
//     ELECTION_FRAUD, FINANCE_BUDGET, etc — see gdeltproject.org/data/lookups)
//   - "cooccurrence": given an entity, return top other entities co-mentioned
//   - "tone_trend": daily sentiment-trend for a query over the date range
//
// Date partitioned via _PARTITIONTIME. Default 7 days, max 90.
func BigQueryGDELT(ctx context.Context, input map[string]any) (*GDELTOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "org_mentions"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("input.query required (entity name, theme code, or keyword)")
	}
	safeQuery := strings.ReplaceAll(query, "'", "")
	safeQuery = strings.ReplaceAll(safeQuery, "\\", "")

	daysBack := 7
	if v, ok := input["days_back"].(float64); ok && int(v) > 0 && int(v) <= 90 {
		daysBack = int(v)
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 200 {
		limit = int(v)
	}

	now := time.Now().UTC()
	endDate := now.AddDate(0, 0, -1)
	startDate := endDate.AddDate(0, 0, -(daysBack - 1))
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	partFilter := fmt.Sprintf("_PARTITIONTIME >= TIMESTAMP('%s') AND _PARTITIONTIME < TIMESTAMP('%s')",
		startStr, endDate.AddDate(0, 0, 1).Format("2006-01-02"))

	start := time.Now()
	out := &GDELTOutput{
		Mode: mode, Query: query,
		StartDate: startStr, EndDate: endStr,
		Source: "gdelt-bq.gdeltv2.gkg_partitioned",
	}

	switch mode {
	case "org_mentions":
		// Sample articles + tone summary
		articleSQL := fmt.Sprintf(`
SELECT DATE, DocumentIdentifier, V2Tone, AllNames
FROM `+"`gdelt-bq.gdeltv2.gkg_partitioned`"+`
WHERE %s
  AND LOWER(AllNames) LIKE '%%%s%%'
ORDER BY DATE DESC
LIMIT %d`, partFilter, strings.ToLower(safeQuery), limit)
		rows, err := bqQuery(ctx, articleSQL, limit)
		if err != nil {
			return nil, fmt.Errorf("article query: %w", err)
		}
		domainSet := map[string]bool{}
		toneSum, posSum, negSum, polSum := 0.0, 0.0, 0.0, 0.0
		for _, r := range rows {
			art := GDELTArticle{}
			if v, ok := r["DATE"].(string); ok {
				if len(v) >= 14 {
					art.Date = fmt.Sprintf("%s-%s-%sT%s:%s:%sZ", v[0:4], v[4:6], v[6:8], v[8:10], v[10:12], v[12:14])
				} else {
					art.Date = v
				}
			}
			if v, ok := r["DocumentIdentifier"].(string); ok {
				art.URL = v
				domainSet[extractDomain(v)] = true
			}
			if v, ok := r["V2Tone"].(string); ok {
				art.Tone, art.Positive, art.Negative, art.Polarity, art.WordCount = parseTone(v)
				toneSum += art.Tone
				posSum += art.Positive
				negSum += art.Negative
				polSum += art.Polarity
			}
			// Extract names near the query mention
			if v, ok := r["AllNames"].(string); ok {
				names := []string{}
				lowQ := strings.ToLower(safeQuery)
				for _, np := range strings.Split(v, ";") {
					name := strings.SplitN(np, ",", 2)[0]
					if strings.Contains(strings.ToLower(name), lowQ) {
						continue
					}
					if name != "" {
						names = append(names, name)
					}
					if len(names) >= 6 {
						break
					}
				}
				art.NamesNear = names
			}
			out.Articles = append(out.Articles, art)
		}
		if len(out.Articles) > 0 {
			n := float64(len(out.Articles))
			out.ToneSummary = &GDELTToneSummary{
				ArticleCount: len(out.Articles),
				AvgTone:      round1(toneSum / n),
				AvgPositive:  round1(posSum / n),
				AvgNegative:  round1(negSum / n),
				AvgPolarity:  round1(polSum / n),
			}
		}
		for d := range domainSet {
			out.UniqueDomains = append(out.UniqueDomains, d)
		}
		sort.Strings(out.UniqueDomains)

		// Top co-occurring entities
		coSQL := fmt.Sprintf(`
SELECT name, COUNT(*) AS c FROM (
  SELECT TRIM(SPLIT(part, ',')[OFFSET(0)]) AS name
  FROM `+"`gdelt-bq.gdeltv2.gkg_partitioned`"+`,
  UNNEST(SPLIT(AllNames, ';')) AS part
  WHERE %s
    AND LOWER(AllNames) LIKE '%%%s%%'
    AND part != ''
)
WHERE LOWER(name) NOT LIKE '%%%s%%' AND LENGTH(name) > 2
GROUP BY name ORDER BY c DESC LIMIT 15`, partFilter, strings.ToLower(safeQuery), strings.ToLower(safeQuery))
		corows, _ := bqQuery(ctx, coSQL, 15)
		for _, r := range corows {
			n := GDELTNameCount{}
			if v, ok := r["name"].(string); ok {
				n.Name = v
			}
			n.Count = parseBQInt(r["c"])
			out.TopCooccurring = append(out.TopCooccurring, n)
		}

	case "theme_search":
		// Search by GDELT V2Themes code
		themeSQL := fmt.Sprintf(`
SELECT DATE, DocumentIdentifier, V2Tone
FROM `+"`gdelt-bq.gdeltv2.gkg_partitioned`"+`
WHERE %s
  AND UPPER(V2Themes) LIKE '%%%s%%'
ORDER BY DATE DESC LIMIT %d`, partFilter, strings.ToUpper(safeQuery), limit)
		rows, err := bqQuery(ctx, themeSQL, limit)
		if err != nil {
			return nil, fmt.Errorf("theme search: %w", err)
		}
		domainSet := map[string]bool{}
		for _, r := range rows {
			art := GDELTArticle{}
			if v, ok := r["DATE"].(string); ok && len(v) >= 14 {
				art.Date = fmt.Sprintf("%s-%s-%sT%s:%s:%sZ", v[0:4], v[4:6], v[6:8], v[8:10], v[10:12], v[12:14])
			}
			if v, ok := r["DocumentIdentifier"].(string); ok {
				art.URL = v
				domainSet[extractDomain(v)] = true
			}
			if v, ok := r["V2Tone"].(string); ok {
				art.Tone, art.Positive, art.Negative, art.Polarity, art.WordCount = parseTone(v)
			}
			out.Articles = append(out.Articles, art)
		}
		for d := range domainSet {
			out.UniqueDomains = append(out.UniqueDomains, d)
		}
		sort.Strings(out.UniqueDomains)

	case "cooccurrence":
		// Top co-mentioned entities for a query
		coSQL := fmt.Sprintf(`
SELECT name, COUNT(*) AS c FROM (
  SELECT TRIM(SPLIT(part, ',')[OFFSET(0)]) AS name
  FROM `+"`gdelt-bq.gdeltv2.gkg_partitioned`"+`,
  UNNEST(SPLIT(AllNames, ';')) AS part
  WHERE %s
    AND LOWER(AllNames) LIKE '%%%s%%'
    AND part != ''
)
WHERE LOWER(name) NOT LIKE '%%%s%%' AND LENGTH(name) > 2
GROUP BY name ORDER BY c DESC LIMIT %d`, partFilter, strings.ToLower(safeQuery), strings.ToLower(safeQuery), limit)
		rows, err := bqQuery(ctx, coSQL, limit)
		if err != nil {
			return nil, fmt.Errorf("cooccurrence: %w", err)
		}
		for _, r := range rows {
			n := GDELTNameCount{}
			if v, ok := r["name"].(string); ok {
				n.Name = v
			}
			n.Count = parseBQInt(r["c"])
			out.TopCooccurring = append(out.TopCooccurring, n)
		}

	case "tone_trend":
		// Daily sentiment trend
		tsSQL := fmt.Sprintf(`
SELECT
  SUBSTR(CAST(DATE AS STRING), 1, 8) AS day,
  COUNT(*) AS articles,
  AVG(SAFE_CAST(SPLIT(V2Tone, ',')[OFFSET(0)] AS FLOAT64)) AS avg_tone,
  AVG(SAFE_CAST(SPLIT(V2Tone, ',')[OFFSET(1)] AS FLOAT64)) AS avg_pos,
  AVG(SAFE_CAST(SPLIT(V2Tone, ',')[OFFSET(2)] AS FLOAT64)) AS avg_neg
FROM `+"`gdelt-bq.gdeltv2.gkg_partitioned`"+`
WHERE %s
  AND LOWER(AllNames) LIKE '%%%s%%'
GROUP BY day ORDER BY day DESC LIMIT %d`, partFilter, strings.ToLower(safeQuery), daysBack)
		rows, err := bqQuery(ctx, tsSQL, daysBack)
		if err != nil {
			return nil, fmt.Errorf("tone trend: %w", err)
		}
		// Use Articles slice as a per-day summary container
		toneSum := 0.0
		nDays := 0
		for _, r := range rows {
			art := GDELTArticle{}
			if v, ok := r["day"].(string); ok && len(v) >= 8 {
				art.Date = fmt.Sprintf("%s-%s-%s", v[0:4], v[4:6], v[6:8])
			}
			art.WordCount = parseBQInt(r["articles"])
			if v, ok := r["avg_tone"].(string); ok {
				fmt.Sscanf(v, "%f", &art.Tone)
			} else if f, ok := r["avg_tone"].(float64); ok {
				art.Tone = f
			}
			if v, ok := r["avg_pos"].(string); ok {
				fmt.Sscanf(v, "%f", &art.Positive)
			} else if f, ok := r["avg_pos"].(float64); ok {
				art.Positive = f
			}
			if v, ok := r["avg_neg"].(string); ok {
				fmt.Sscanf(v, "%f", &art.Negative)
			} else if f, ok := r["avg_neg"].(float64); ok {
				art.Negative = f
			}
			toneSum += art.Tone
			nDays++
			out.Articles = append(out.Articles, art)
		}
		if nDays > 0 {
			out.ToneSummary = &GDELTToneSummary{
				ArticleCount: nDays,
				AvgTone:      round1(toneSum / float64(nDays)),
			}
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — try org_mentions, theme_search, cooccurrence, or tone_trend", mode)
	}

	// Highlights
	highlights := []string{}
	highlights = append(highlights, fmt.Sprintf("query='%s' window=%s → %s mode=%s", query, startStr, endStr, mode))
	if out.ToneSummary != nil {
		mood := "neutral"
		if out.ToneSummary.AvgTone > 1 {
			mood = "POSITIVE"
		} else if out.ToneSummary.AvgTone < -1 {
			mood = "NEGATIVE"
		}
		highlights = append(highlights, fmt.Sprintf("%d articles, avg_tone=%.1f (%s)", out.ToneSummary.ArticleCount, out.ToneSummary.AvgTone, mood))
	}
	if len(out.UniqueDomains) > 0 {
		head := out.UniqueDomains
		if len(head) > 5 {
			head = head[:5]
		}
		highlights = append(highlights, fmt.Sprintf("%d unique source domains: %s%s",
			len(out.UniqueDomains), strings.Join(head, ", "),
			func() string { if len(out.UniqueDomains) > 5 { return "..." }; return "" }()))
	}
	if len(out.TopCooccurring) > 0 {
		top := []string{}
		for i, c := range out.TopCooccurring {
			if i >= 5 {
				break
			}
			top = append(top, fmt.Sprintf("'%s'(%d)", c.Name, c.Count))
		}
		highlights = append(highlights, "top co-mentioned: "+strings.Join(top, ", "))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
