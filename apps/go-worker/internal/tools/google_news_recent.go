package tools

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// GNewsItem is one news article.
type GNewsItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Source      string `json:"source"`
	SourceURL   string `json:"source_url,omitempty"`
	PubDateRaw  string `json:"pub_date_raw,omitempty"`
	PubDateISO  string `json:"pub_date_iso,omitempty"`
	Description string `json:"description,omitempty"`
}

// GNewsSourceAggregate counts articles per source.
type GNewsSourceAggregate struct {
	Source       string `json:"source"`
	ArticleCount int    `json:"article_count"`
}

// GNewsTimeBucket counts articles per day.
type GNewsTimeBucket struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// GNewsRecentOutput is the response.
type GNewsRecentOutput struct {
	Query             string                 `json:"query"`
	Country           string                 `json:"country,omitempty"`
	Language          string                 `json:"language,omitempty"`
	TimeFilter        string                 `json:"time_filter,omitempty"`
	TotalReturned     int                    `json:"total_returned"`
	Items             []GNewsItem            `json:"items"`
	TopSources        []GNewsSourceAggregate `json:"top_sources,omitempty"`
	UniqueDomains     []string               `json:"unique_source_domains,omitempty"`
	DateDistribution  []GNewsTimeBucket      `json:"date_distribution,omitempty"`
	OldestPubDate     string                 `json:"oldest_pub_date,omitempty"`
	NewestPubDate     string                 `json:"newest_pub_date,omitempty"`
	HighlightFindings []string               `json:"highlight_findings"`
	Source            string                 `json:"source"`
	TookMs            int64                  `json:"tookMs"`
	Note              string                 `json:"note,omitempty"`
}

// raw RSS parsing structs
type rssChannel struct {
	XMLName xml.Name  `xml:"rss"`
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
			Source      struct {
				URL  string `xml:"url,attr"`
				Text string `xml:",chardata"`
			} `xml:"source"`
		} `xml:"item"`
	} `xml:"channel"`
}

// GoogleNewsRecent queries Google News RSS for current-events articles
// matching a query. Free, no auth.
//
// Pairs with bigquery_gdelt (historical news) for full news coverage:
//   - GoogleNewsRecent → last 30-90 days, current-events context
//   - bigquery_gdelt → 2017-present, full archival news with sentiment
//
// Why this matters for ER:
//   - Current-events context for any entity (person, org, topic).
//   - Top-sources aggregation reveals which outlets cover the entity
//     (Reuters/NYT/Axios = mainstream; niche-blog = specialty).
//   - Date distribution shows news cadence (one-time event vs sustained
//     coverage = different OPSEC implications).
//   - The query can include Google News operators: `when:7d` (last 7
//     days), `when:1y`, `intitle:`, etc. — pass via the `query` param.
//
// Localization: use country (ISO-2, e.g. 'US') + language (e.g. 'en') for
// non-English / non-US news markets.
func GoogleNewsRecent(ctx context.Context, input map[string]any) (*GNewsRecentOutput, error) {
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required")
	}
	country, _ := input["country"].(string)
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "" {
		country = "US"
	}
	language, _ := input["language"].(string)
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		language = "en"
	}
	timeFilter, _ := input["time_filter"].(string)
	timeFilter = strings.ToLower(strings.TrimSpace(timeFilter))
	limit := 50
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &GNewsRecentOutput{
		Query:    query,
		Country:  country,
		Language: language,
		TimeFilter: timeFilter,
		Source:   "news.google.com/rss/search",
	}
	start := time.Now()

	// Build query with optional time filter
	qWithTime := query
	if timeFilter != "" {
		qWithTime = query + " when:" + timeFilter
	}
	params := url.Values{}
	params.Set("q", qWithTime)
	params.Set("hl", language+"-"+country)
	params.Set("gl", country)
	params.Set("ceid", country+":"+language)
	endpoint := "https://news.google.com/rss/search?" + params.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; osint-agent/0.1)")
	req.Header.Set("Accept", "application/rss+xml, application/xml")

	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google news fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("google news %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}

	var raw rssChannel
	if err := xml.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("rss decode: %w", err)
	}

	// Aggregations setup
	sourceCount := map[string]int{}
	dateCount := map[string]int{}
	domainSet := map[string]struct{}{}
	var oldestPub, newestPub time.Time

	count := 0
	for _, raw := range raw.Channel.Items {
		if count >= limit {
			break
		}
		count++
		// Parse pubDate "Wed, 29 Apr 2026 02:18:41 GMT"
		var pubISO string
		if t, err := time.Parse(time.RFC1123, raw.PubDate); err == nil {
			pubISO = t.UTC().Format(time.RFC3339)
			day := t.UTC().Format("2006-01-02")
			dateCount[day]++
			if oldestPub.IsZero() || t.Before(oldestPub) {
				oldestPub = t
			}
			if t.After(newestPub) {
				newestPub = t
			}
		}
		item := GNewsItem{
			Title:       cleanGNewsText(raw.Title),
			URL:         raw.Link,
			Source:      cleanGNewsText(raw.Source.Text),
			SourceURL:   raw.Source.URL,
			PubDateRaw:  raw.PubDate,
			PubDateISO:  pubISO,
			Description: cleanGNewsText(extractDescriptionText(raw.Description)),
		}
		out.Items = append(out.Items, item)

		if item.Source != "" {
			sourceCount[item.Source]++
		}
		if u, err := url.Parse(raw.Source.URL); err == nil && u.Host != "" {
			domainSet[strings.TrimPrefix(u.Host, "www.")] = struct{}{}
		}
	}
	out.TotalReturned = len(out.Items)

	for s, c := range sourceCount {
		out.TopSources = append(out.TopSources, GNewsSourceAggregate{Source: s, ArticleCount: c})
	}
	sort.SliceStable(out.TopSources, func(i, j int) bool { return out.TopSources[i].ArticleCount > out.TopSources[j].ArticleCount })
	if len(out.TopSources) > 15 {
		out.TopSources = out.TopSources[:15]
	}

	for d := range domainSet {
		out.UniqueDomains = append(out.UniqueDomains, d)
	}
	sort.Strings(out.UniqueDomains)

	for d, c := range dateCount {
		out.DateDistribution = append(out.DateDistribution, GNewsTimeBucket{Date: d, Count: c})
	}
	sort.SliceStable(out.DateDistribution, func(i, j int) bool { return out.DateDistribution[i].Date > out.DateDistribution[j].Date })
	if len(out.DateDistribution) > 30 {
		out.DateDistribution = out.DateDistribution[:30]
	}

	if !oldestPub.IsZero() {
		out.OldestPubDate = oldestPub.UTC().Format(time.RFC3339)
	}
	if !newestPub.IsZero() {
		out.NewestPubDate = newestPub.UTC().Format(time.RFC3339)
	}

	out.HighlightFindings = buildGNewsHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func cleanGNewsText(s string) string {
	// Decode common HTML entities
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&apos;", "'")
	return strings.TrimSpace(s)
}

// extractDescriptionText pulls visible text from Google News' HTML description
// (which is typically a list of <a href> links wrapped in <ol>).
func extractDescriptionText(s string) string {
	// Remove HTML tags
	out := []byte{}
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '<' {
			depth++
			continue
		}
		if c == '>' && depth > 0 {
			depth--
			continue
		}
		if depth == 0 {
			out = append(out, c)
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(string(out)), " "))
}

func buildGNewsHighlights(o *GNewsRecentOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d Google News articles for query '%s' (lang=%s, country=%s)", o.TotalReturned, o.Query, o.Language, o.Country))
	if o.OldestPubDate != "" && o.NewestPubDate != "" {
		hi = append(hi, fmt.Sprintf("📅 date range: %s → %s", o.OldestPubDate[:10], o.NewestPubDate[:10]))
	}
	if len(o.TopSources) > 0 {
		topS := []string{}
		for _, s := range o.TopSources[:min2(6, len(o.TopSources))] {
			topS = append(topS, fmt.Sprintf("%s (%d)", s.Source, s.ArticleCount))
		}
		hi = append(hi, "📰 top sources: "+strings.Join(topS, ", "))
	}
	if len(o.Items) > 0 {
		// Surface the most-recent headline
		latest := o.Items[0]
		hi = append(hi, fmt.Sprintf("most recent: [%s] %s — %s", latest.Source, hfTruncate(latest.Title, 80), latest.PubDateISO[:10]))
	}
	if len(o.DateDistribution) > 1 {
		// Coverage cadence signal
		dates := []string{}
		total := 0
		for _, b := range o.DateDistribution[:min2(7, len(o.DateDistribution))] {
			dates = append(dates, fmt.Sprintf("%s=%d", b.Date, b.Count))
			total += b.Count
		}
		hi = append(hi, "🕒 last 7-day cadence: "+strings.Join(dates, ", "))
	}
	return hi
}
