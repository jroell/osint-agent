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

// IAItem is one archive.org item (slim).
type IAItem struct {
	Identifier   string   `json:"identifier"`
	Title        string   `json:"title,omitempty"`
	MediaType    string   `json:"media_type,omitempty"`
	Creator      string   `json:"creator,omitempty"`
	Uploader     string   `json:"uploader,omitempty"`
	Collection   []string `json:"collections,omitempty"`
	PublicDate   string   `json:"public_date,omitempty"`
	AddedDate    string   `json:"added_date,omitempty"`
	Downloads    int      `json:"downloads,omitempty"`
	WeekDownloads int     `json:"week_downloads,omitempty"`
	Subject      []string `json:"subjects,omitempty"`
	Year         string   `json:"year,omitempty"`
	Language     []string `json:"languages,omitempty"`
	URL          string   `json:"url,omitempty"`
	DescriptionExcerpt string `json:"description_excerpt,omitempty"`
}

// IAItemDetail is the full metadata of a single item.
type IAItemDetail struct {
	IAItem
	FilesCount  int      `json:"files_count,omitempty"`
	ItemSize    int64    `json:"item_size_bytes,omitempty"`
	IsCollection bool    `json:"is_collection,omitempty"`
	ItemLastUpdated int64 `json:"item_last_updated_unix,omitempty"`
	AltLocations []string `json:"alternate_locations,omitempty"`
	FullMetadata map[string]any `json:"raw_metadata,omitempty"`
}

// IAMediaTypeAggregate counts items per mediatype.
type IAMediaTypeAggregate struct {
	MediaType string `json:"media_type"`
	Count     int    `json:"count"`
	TotalDownloads int `json:"total_downloads"`
}

// IACollectionAggregate counts items per collection.
type IACollectionAggregate struct {
	Collection string `json:"collection"`
	Count      int    `json:"count"`
}

// IAYearAggregate counts items per year.
type IAYearAggregate struct {
	Year  string `json:"year"`
	Count int    `json:"count"`
}

// IASearchOutput is the response.
type IASearchOutput struct {
	Mode             string                  `json:"mode"`
	Query            string                  `json:"query"`
	TotalFound       int                     `json:"total_found"`
	Returned         int                     `json:"returned"`
	Items            []IAItem                `json:"items,omitempty"`
	Detail           *IAItemDetail           `json:"detail,omitempty"`
	TopMediaTypes    []IAMediaTypeAggregate  `json:"top_media_types,omitempty"`
	TopCollections  []IACollectionAggregate `json:"top_collections,omitempty"`
	YearDistribution []IAYearAggregate      `json:"year_distribution,omitempty"`
	YearRange        string                  `json:"year_range,omitempty"`
	TotalDownloads   int64                   `json:"total_downloads_observed,omitempty"`
	HighlightFindings []string               `json:"highlight_findings"`
	Source           string                  `json:"source"`
	TookMs           int64                   `json:"tookMs"`
	Note             string                  `json:"note,omitempty"`
}

// raw structures
type iaSearchRaw struct {
	Response struct {
		NumFound int                      `json:"numFound"`
		Docs     []map[string]any         `json:"docs"`
	} `json:"response"`
}

type iaMetadataRaw struct {
	Metadata        map[string]any   `json:"metadata"`
	FilesCount      int              `json:"files_count"`
	ItemSize        int64            `json:"item_size"`
	IsCollection   bool              `json:"is_collection"`
	ItemLastUpdated int64            `json:"item_last_updated"`
	AltLocations    map[string]any   `json:"alternate_locations"`
}

// InternetArchiveSearch queries archive.org's advanced search + metadata
// APIs. Free, no auth.
//
// Modes:
//   - "by_uploader_email" : list items uploaded by a specific email
//                           (archive.org's `uploader` field is canonical-
//                           email, e.g. "jason@textfiles.com")
//   - "search"            : full-text search across all 50M+ items
//   - "item_detail"       : full metadata for a specific item identifier
//
// Why this matters for ER:
//   - archive.org hosts ~50M items: government documents, court records,
//     leaked corporate materials, video archives, books, software,
//     personal uploads. Massively under-leveraged in OSINT.
//   - Tracing a known email → all their uploads reveals what materials
//     someone has ARCHIVED publicly (an act of intentional preservation).
//   - Mediatype + collection breakdown reveals upload patterns: is this
//     uploader curating film, court filings, software, or audio?
//   - Description + subject metadata enables fuzzy topic-based ER.
//   - Specific items have full files + descriptions queryable for content
//     evidence (e.g. specific PDFs in a leak).
func InternetArchiveSearch(ctx context.Context, input map[string]any) (*IASearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "search"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (email for by_uploader_email, keyword for search, identifier for item_detail)")
	}

	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &IASearchOutput{
		Mode:   mode,
		Query:  query,
		Source: "archive.org/advancedsearch.php + metadata",
	}
	start := time.Now()
	client := &http.Client{Timeout: 35 * time.Second}

	switch mode {
	case "by_uploader_email":
		// Validate email-ish input
		if !strings.Contains(query, "@") {
			return nil, fmt.Errorf("by_uploader_email requires an email address (e.g. 'jason@textfiles.com')")
		}
		q := fmt.Sprintf(`uploader:"%s"`, query)
		items, total, err := iaAdvancedSearch(ctx, client, q, limit, "publicdate desc")
		if err != nil {
			return nil, err
		}
		out.Items = items
		out.TotalFound = total
		out.Returned = len(items)
	case "search":
		items, total, err := iaAdvancedSearch(ctx, client, query, limit, "downloads desc")
		if err != nil {
			return nil, err
		}
		out.Items = items
		out.TotalFound = total
		out.Returned = len(items)
	case "item_detail":
		detail, err := iaItemDetail(ctx, client, query)
		if err != nil {
			return nil, err
		}
		if detail == nil {
			out.Note = fmt.Sprintf("no archive.org item with identifier '%s'", query)
			out.HighlightFindings = []string{out.Note}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		out.Detail = detail
		out.TotalFound = 1
		out.Returned = 1
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: by_uploader_email, search, item_detail", mode)
	}

	// Aggregations (search/by_uploader_email modes only)
	if len(out.Items) > 0 {
		mtMap := map[string]int{}
		mtDl := map[string]int{}
		colMap := map[string]int{}
		yrMap := map[string]int{}
		var totalDl int64
		minYr, maxYr := "", ""
		for _, it := range out.Items {
			if it.MediaType != "" {
				mtMap[it.MediaType]++
				mtDl[it.MediaType] += it.Downloads
			}
			for _, c := range it.Collection {
				if c != "" {
					colMap[c]++
				}
			}
			if it.Year != "" {
				yrMap[it.Year]++
				if minYr == "" || it.Year < minYr {
					minYr = it.Year
				}
				if it.Year > maxYr {
					maxYr = it.Year
				}
			}
			totalDl += int64(it.Downloads)
		}
		for mt, c := range mtMap {
			out.TopMediaTypes = append(out.TopMediaTypes, IAMediaTypeAggregate{MediaType: mt, Count: c, TotalDownloads: mtDl[mt]})
		}
		sort.SliceStable(out.TopMediaTypes, func(i, j int) bool { return out.TopMediaTypes[i].Count > out.TopMediaTypes[j].Count })
		for col, c := range colMap {
			out.TopCollections = append(out.TopCollections, IACollectionAggregate{Collection: col, Count: c})
		}
		sort.SliceStable(out.TopCollections, func(i, j int) bool { return out.TopCollections[i].Count > out.TopCollections[j].Count })
		if len(out.TopCollections) > 10 {
			out.TopCollections = out.TopCollections[:10]
		}
		for y, c := range yrMap {
			out.YearDistribution = append(out.YearDistribution, IAYearAggregate{Year: y, Count: c})
		}
		sort.SliceStable(out.YearDistribution, func(i, j int) bool { return out.YearDistribution[i].Year < out.YearDistribution[j].Year })
		out.TotalDownloads = totalDl
		if minYr != "" && maxYr != "" {
			if minYr == maxYr {
				out.YearRange = minYr
			} else {
				out.YearRange = minYr + "-" + maxYr
			}
		}
	}

	out.HighlightFindings = buildIAHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func iaAdvancedSearch(ctx context.Context, client *http.Client, q string, rows int, sortClause string) ([]IAItem, int, error) {
	params := url.Values{}
	params.Set("q", q)
	params.Set("output", "json")
	params.Set("rows", fmt.Sprintf("%d", rows))
	for _, f := range []string{"identifier", "title", "creator", "uploader", "mediatype", "collection", "publicdate", "addeddate", "downloads", "week", "subject", "year", "language", "description"} {
		params.Add("fl[]", f)
	}
	if sortClause != "" {
		params.Add("sort[]", sortClause)
	}
	endpoint := "https://archive.org/advancedsearch.php?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("archive.org search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, 0, fmt.Errorf("archive.org %d: %s", resp.StatusCode, string(body))
	}
	var raw iaSearchRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	items := []IAItem{}
	for _, doc := range raw.Response.Docs {
		it := IAItem{
			Identifier:    iaToString(doc["identifier"]),
			Title:         iaToString(doc["title"]),
			MediaType:     iaToString(doc["mediatype"]),
			Creator:       iaToString(doc["creator"]),
			Uploader:      iaToString(doc["uploader"]),
			PublicDate:    iaToString(doc["publicdate"]),
			AddedDate:     iaToString(doc["addeddate"]),
			Downloads:     iaToInt(doc["downloads"]),
			WeekDownloads: iaToInt(doc["week"]),
			Year:          iaToString(doc["year"]),
			DescriptionExcerpt: iaTruncate(iaToString(doc["description"]), 300),
		}
		if it.Identifier != "" {
			it.URL = "https://archive.org/details/" + it.Identifier
		}
		// collection + subject + language can be string or array
		it.Collection = iaToStringArray(doc["collection"])
		it.Subject = iaToStringArray(doc["subject"])
		it.Language = iaToStringArray(doc["language"])
		items = append(items, it)
	}
	return items, raw.Response.NumFound, nil
}

func iaItemDetail(ctx context.Context, client *http.Client, identifier string) (*IAItemDetail, error) {
	endpoint := "https://archive.org/metadata/" + url.PathEscape(identifier)
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("archive.org metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("archive.org %d", resp.StatusCode)
	}
	var raw iaMetadataRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.Metadata == nil || iaToString(raw.Metadata["identifier"]) == "" {
		return nil, nil
	}
	m := raw.Metadata
	d := &IAItemDetail{
		IAItem: IAItem{
			Identifier:    iaToString(m["identifier"]),
			Title:         iaToString(m["title"]),
			MediaType:     iaToString(m["mediatype"]),
			Creator:       iaToString(m["creator"]),
			Uploader:      iaToString(m["uploader"]),
			PublicDate:    iaToString(m["publicdate"]),
			AddedDate:     iaToString(m["addeddate"]),
			Downloads:     iaToInt(m["downloads"]),
			Year:          iaToString(m["year"]),
			URL:           "https://archive.org/details/" + iaToString(m["identifier"]),
			DescriptionExcerpt: iaTruncate(iaToString(m["description"]), 600),
		},
		FilesCount:  raw.FilesCount,
		ItemSize:    raw.ItemSize,
		IsCollection: raw.IsCollection,
		ItemLastUpdated: raw.ItemLastUpdated,
		FullMetadata: m,
	}
	d.Collection = iaToStringArray(m["collection"])
	d.Subject = iaToStringArray(m["subject"])
	d.Language = iaToStringArray(m["language"])
	if al := raw.AltLocations; al != nil {
		if servers, ok := al["servers"].([]any); ok {
			for _, s := range servers {
				if str, ok := s.(string); ok {
					d.AltLocations = append(d.AltLocations, str)
				}
			}
		}
	}
	return d, nil
}

func iaToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		if len(x) > 0 {
			if s, ok := x[0].(string); ok {
				return s
			}
		}
	case float64:
		return fmt.Sprintf("%.0f", x)
	}
	return ""
}

func iaToInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		var n int
		fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

func iaToStringArray(v any) []string {
	switch x := v.(type) {
	case []any:
		out := []string{}
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	}
	return nil
}

func iaTruncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func buildIAHighlights(o *IASearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "by_uploader_email":
		hi = append(hi, fmt.Sprintf("✓ uploader='%s' has %d total items on archive.org (%d returned)", o.Query, o.TotalFound, o.Returned))
		if len(o.TopMediaTypes) > 0 {
			parts := []string{}
			for _, m := range o.TopMediaTypes[:min2(5, len(o.TopMediaTypes))] {
				parts = append(parts, fmt.Sprintf("%s=%d", m.MediaType, m.Count))
			}
			hi = append(hi, "📦 mediatype breakdown: "+strings.Join(parts, ", "))
		}
		if o.TotalDownloads > 0 {
			hi = append(hi, fmt.Sprintf("📥 total downloads across returned items: %d", o.TotalDownloads))
		}
		if o.YearRange != "" {
			hi = append(hi, "year range: "+o.YearRange)
		}
		if len(o.TopCollections) > 0 {
			parts := []string{}
			for _, c := range o.TopCollections[:min2(4, len(o.TopCollections))] {
				parts = append(parts, fmt.Sprintf("%s(%d)", c.Collection, c.Count))
			}
			hi = append(hi, "🗂  top collections: "+strings.Join(parts, ", "))
		}
	case "search":
		hi = append(hi, fmt.Sprintf("%d archive.org items match '%s' (returned %d, sorted by downloads)", o.TotalFound, o.Query, o.Returned))
		if len(o.TopMediaTypes) > 0 {
			parts := []string{}
			for _, m := range o.TopMediaTypes[:min2(5, len(o.TopMediaTypes))] {
				parts = append(parts, fmt.Sprintf("%s=%d", m.MediaType, m.Count))
			}
			hi = append(hi, "mediatype breakdown of returned: "+strings.Join(parts, ", "))
		}
		if o.YearRange != "" {
			hi = append(hi, "year range of returned: "+o.YearRange)
		}
	case "item_detail":
		if o.Detail == nil {
			break
		}
		d := o.Detail
		hi = append(hi, fmt.Sprintf("✓ %s — '%s'", d.Identifier, d.Title))
		if d.MediaType != "" {
			hi = append(hi, "mediatype: "+d.MediaType)
		}
		if d.Creator != "" {
			hi = append(hi, "creator: "+d.Creator)
		}
		if d.Uploader != "" {
			hi = append(hi, "🔗 uploader (email): "+d.Uploader)
		}
		if d.PublicDate != "" {
			hi = append(hi, "publicdate: "+d.PublicDate)
		}
		if len(d.Collection) > 0 {
			hi = append(hi, "collections: "+strings.Join(d.Collection, ", "))
		}
		if d.FilesCount > 0 {
			hi = append(hi, fmt.Sprintf("📁 %d files, %.2f MB total", d.FilesCount, float64(d.ItemSize)/(1024*1024)))
		}
		if d.IsCollection {
			hi = append(hi, "ℹ️  this is a COLLECTION (curates other items)")
		}
	}
	return hi
}
