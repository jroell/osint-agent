package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// SerpAPIGoogleScholar wraps the Google Scholar engine on SerpAPI
// (serpapi.com/google-scholar-api). Paid; REQUIRES `SERPAPI_KEY`.
//
// Closes the gap between OpenAlex (open-access only) and the actual
// published research literature: Google Scholar indexes paywalled
// papers, theses, books, and citations that OpenAlex misses.
//
// Modes:
//   - "search"           : keyword search with author/year filters
//   - "author"           : author profile by author_id
//   - "author_articles"  : articles by an author_id
//   - "cites"            : papers that cite a given cluster_id
//
// Knowledge-graph: emits typed entities (kind: "scholarly_work" |
// "scholar") with stable Google Scholar cluster IDs and author IDs.

type SerpScholarItem struct {
	Title           string `json:"title"`
	Link            string `json:"link,omitempty"`
	Snippet         string `json:"snippet,omitempty"`
	PublicationInfo string `json:"publication_info,omitempty"`
	CitedByCount    int    `json:"cited_by_count,omitempty"`
	ResultID        string `json:"result_id,omitempty"`
	ClusterID       string `json:"cluster_id,omitempty"`
	Year            int    `json:"year,omitempty"`
}

type SerpScholarAuthor struct {
	AuthorID    string `json:"author_id"`
	Name        string `json:"name"`
	Affiliation string `json:"affiliation,omitempty"`
	Email       string `json:"email,omitempty"`
	CitedBy     int    `json:"cited_by,omitempty"`
	HIndex      int    `json:"h_index,omitempty"`
	I10Index    int    `json:"i10_index,omitempty"`
	URL         string `json:"scholar_url,omitempty"`
}

type SerpEntity struct {
	Kind        string         `json:"kind"`
	ID          string         `json:"id,omitempty"`
	Title       string         `json:"title,omitempty"`
	Name        string         `json:"name,omitempty"`
	URL         string         `json:"url,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type SerpAPIGoogleScholarOutput struct {
	Mode              string             `json:"mode"`
	Query             string             `json:"query,omitempty"`
	Returned          int                `json:"returned"`
	Articles          []SerpScholarItem  `json:"articles,omitempty"`
	Author            *SerpScholarAuthor `json:"author,omitempty"`
	Entities          []SerpEntity       `json:"entities"`
	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
}

func SerpAPIGoogleScholar(ctx context.Context, input map[string]any) (*SerpAPIGoogleScholarOutput, error) {
	apiKey := os.Getenv("SERPAPI_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("SERPAPI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("SERPAPI_KEY not set; subscribe at serpapi.com/google-scholar-api")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["author_id"] != nil && input["articles"] == true:
			mode = "author_articles"
		case input["author_id"] != nil:
			mode = "author"
		case input["cites"] != nil:
			mode = "cites"
		default:
			mode = "search"
		}
	}
	out := &SerpAPIGoogleScholarOutput{Mode: mode, Source: "serpapi.com/google-scholar"}
	start := time.Now()
	cli := &http.Client{Timeout: 60 * time.Second}

	get := func(params url.Values) ([]byte, error) {
		params.Set("api_key", apiKey)
		params.Set("output", "json")
		u := "https://serpapi.com/search.json?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("serpapi: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("serpapi HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		params := url.Values{
			"engine": []string{"google_scholar"},
			"q":      []string{q},
		}
		if author, ok := input["author"].(string); ok && author != "" {
			params.Set("as_sauthors", author)
		}
		if y1, ok := input["year_from"].(float64); ok && y1 > 0 {
			params.Set("as_ylo", fmt.Sprintf("%d", int(y1)))
		}
		if y2, ok := input["year_to"].(float64); ok && y2 > 0 {
			params.Set("as_yhi", fmt.Sprintf("%d", int(y2)))
		}
		body, err := get(params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			OrganicResults []map[string]any `json:"organic_results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("serpapi decode: %w", err)
		}
		for _, r := range resp.OrganicResults {
			out.Articles = append(out.Articles, parseSerpScholarItem(r))
		}
	case "author":
		id, _ := input["author_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.author_id required")
		}
		out.Query = id
		params := url.Values{
			"engine":    []string{"google_scholar_author"},
			"author_id": []string{id},
		}
		body, err := get(params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Author   map[string]any   `json:"author"`
			CitedBy  map[string]any   `json:"cited_by"`
			Articles []map[string]any `json:"articles"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("serpapi decode: %w", err)
		}
		out.Author = parseSerpAuthor(resp.Author, resp.CitedBy, id)
		for _, a := range resp.Articles {
			out.Articles = append(out.Articles, parseSerpScholarItem(a))
		}
	case "author_articles":
		id, _ := input["author_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.author_id required")
		}
		out.Query = id
		params := url.Values{
			"engine":    []string{"google_scholar_author"},
			"author_id": []string{id},
			"sort":      []string{"pubdate"},
		}
		body, err := get(params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Articles []map[string]any `json:"articles"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("serpapi decode: %w", err)
		}
		for _, a := range resp.Articles {
			out.Articles = append(out.Articles, parseSerpScholarItem(a))
		}
	case "cites":
		id, _ := input["cites"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.cites required (cluster_id)")
		}
		out.Query = id
		params := url.Values{
			"engine": []string{"google_scholar"},
			"cites":  []string{id},
		}
		body, err := get(params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			OrganicResults []map[string]any `json:"organic_results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("serpapi decode: %w", err)
		}
		for _, r := range resp.OrganicResults {
			out.Articles = append(out.Articles, parseSerpScholarItem(r))
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Articles)
	if out.Author != nil {
		out.Returned++
	}
	out.Entities = serpScholarBuildEntities(out)
	out.HighlightFindings = serpScholarBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseSerpScholarItem(m map[string]any) SerpScholarItem {
	it := SerpScholarItem{
		Title:    gtString(m, "title"),
		Link:     gtString(m, "link"),
		Snippet:  gtString(m, "snippet"),
		ResultID: gtString(m, "result_id"),
		Year:     int(gtFloat(m, "year")),
	}
	if pub, ok := m["publication_info"].(map[string]any); ok {
		it.PublicationInfo = gtString(pub, "summary")
	}
	if cb, ok := m["inline_links"].(map[string]any); ok {
		if c, ok := cb["cited_by"].(map[string]any); ok {
			it.CitedByCount = int(gtFloat(c, "total"))
			it.ClusterID = gtString(c, "cites_id")
		}
	}
	return it
}

func parseSerpAuthor(author map[string]any, citedBy map[string]any, id string) *SerpScholarAuthor {
	a := &SerpScholarAuthor{
		AuthorID:    id,
		Name:        gtString(author, "name"),
		Affiliation: gtString(author, "affiliations"),
		Email:       gtString(author, "email"),
		URL:         "https://scholar.google.com/citations?user=" + id,
	}
	if t, ok := citedBy["table"].([]any); ok {
		for _, row := range t {
			rec, _ := row.(map[string]any)
			if rec == nil {
				continue
			}
			if c, ok := rec["citations"].(map[string]any); ok {
				a.CitedBy = int(gtFloat(c, "all"))
			}
			if h, ok := rec["h_index"].(map[string]any); ok {
				a.HIndex = int(gtFloat(h, "all"))
			}
			if i10, ok := rec["i10_index"].(map[string]any); ok {
				a.I10Index = int(gtFloat(i10, "all"))
			}
		}
	}
	return a
}

func serpScholarBuildEntities(o *SerpAPIGoogleScholarOutput) []SerpEntity {
	ents := []SerpEntity{}
	if a := o.Author; a != nil {
		ents = append(ents, SerpEntity{
			Kind: "scholar", ID: a.AuthorID, Name: a.Name, URL: a.URL,
			Description: a.Affiliation,
			Attributes: map[string]any{
				"affiliation": a.Affiliation, "email": a.Email,
				"cited_by": a.CitedBy, "h_index": a.HIndex, "i10_index": a.I10Index,
			},
		})
	}
	for _, w := range o.Articles {
		date := ""
		if w.Year > 0 {
			date = fmt.Sprintf("%d", w.Year)
		}
		ents = append(ents, SerpEntity{
			Kind: "scholarly_work", ID: w.ResultID, Title: w.Title, URL: w.Link, Date: date,
			Description: w.Snippet,
			Attributes: map[string]any{
				"publication": w.PublicationInfo,
				"cited_by":    w.CitedByCount,
				"cluster_id":  w.ClusterID,
			},
		})
	}
	return ents
}

func serpScholarBuildHighlights(o *SerpAPIGoogleScholarOutput) []string {
	hi := []string{fmt.Sprintf("✓ serpapi google scholar %s: %d records", o.Mode, o.Returned)}
	if a := o.Author; a != nil {
		hi = append(hi, fmt.Sprintf("  • author %s [%s] — %s; cited %d, h=%d, i10=%d", a.Name, a.AuthorID, a.Affiliation, a.CitedBy, a.HIndex, a.I10Index))
	}
	for i, w := range o.Articles {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s (%d) — cited %d — %s", hfTruncate(w.Title, 70), w.Year, w.CitedByCount, w.Link))
	}
	return hi
}
