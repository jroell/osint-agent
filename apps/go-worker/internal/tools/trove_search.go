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

// TroveSearch wraps the National Library of Australia's Trove API v3.
// Free, requires TROVE_API_KEY (registration at trove.nla.gov.au).
//
// Trove is the dominant Australian historical archive — ~700M+ items
// including 200+ years of digitized newspapers (1803-2010s), books,
// maps, music, archived web, photographs. Critical for AU/NZ
// biography, Indigenous history, anything where Trove holds the
// page-of-record.
//
// Modes:
//   - "search"            : full-text search across categories (newspapers,
//                           books, etc.) with date + state + paper filters
//   - "newspaper_article" : fetch a single newspaper article by id
//   - "newspaper_title"   : list articles in a paper for a date range
//
// Knowledge-graph: each result emits a typed entity envelope (kind:
// "newspaper_article" | "book" | "person") with the Trove permalink as
// stable ID, suitable for direct ingest by panel_entity_resolution.

type TroveArticle struct {
	ID           string `json:"trove_id"`
	Heading      string `json:"heading"`
	Date         string `json:"date,omitempty"`
	Page         string `json:"page,omitempty"`
	PageSequence string `json:"page_sequence,omitempty"`
	Title        string `json:"newspaper_title,omitempty"`
	State        string `json:"newspaper_state,omitempty"`
	Category     string `json:"category,omitempty"`
	WordCount    int    `json:"word_count,omitempty"`
	IllustratedQ bool   `json:"illustrated,omitempty"`
	Snippet      string `json:"snippet,omitempty"`
	URL          string `json:"trove_url"`
}

type TroveBook struct {
	ID         string `json:"trove_id"`
	Title      string `json:"title"`
	Creator    string `json:"creator,omitempty"`
	IssuedDate string `json:"issued,omitempty"`
	Format     string `json:"format,omitempty"`
	URL        string `json:"trove_url"`
}

type TroveEntity struct {
	Kind        string         `json:"kind"`
	TroveID     string         `json:"trove_id"`
	Title       string         `json:"title"`
	Date        string         `json:"date,omitempty"`
	URL         string         `json:"url,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type TroveSearchOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Total             int            `json:"total,omitempty"`
	Articles          []TroveArticle `json:"articles,omitempty"`
	Books             []TroveBook    `json:"books,omitempty"`
	Entities          []TroveEntity  `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func TroveSearch(ctx context.Context, input map[string]any) (*TroveSearchOutput, error) {
	apiKey := os.Getenv("TROVE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("TROVE_API_KEY not set; register at trove.nla.gov.au and set the env var")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["article_id"] != nil:
			mode = "newspaper_article"
		default:
			mode = "search"
		}
	}
	out := &TroveSearchOutput{Mode: mode, Source: "api.trove.nla.gov.au/v3"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(path string, params url.Values) ([]byte, error) {
		u := "https://api.trove.nla.gov.au/v3" + path
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-API-KEY", apiKey)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("trove: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 401 {
			return nil, fmt.Errorf("trove: unauthorized (check TROVE_API_KEY)")
		}
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("trove: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("trove HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 300))
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
		params := url.Values{}
		params.Set("q", q)
		params.Set("encoding", "json")
		params.Set("n", "20")
		categoryStr := "newspaper"
		if c, ok := input["category"].(string); ok && c != "" {
			categoryStr = c
		}
		params.Set("category", categoryStr) // newspaper, book, magazine, image, etc.
		// Date range filters via l-decade / l-year
		if dateFrom, ok := input["date_from"].(string); ok && dateFrom != "" {
			params.Set("l-year", dateFrom) // YYYY
		}
		if state, ok := input["state"].(string); ok && state != "" {
			params.Set("l-state", state)
		}
		if newspaper, ok := input["newspaper_title_id"].(string); ok && newspaper != "" {
			params.Set("l-title", newspaper)
		}
		body, err := get("/result", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Category []struct {
				Name    string `json:"name"`
				Records struct {
					Total   int `json:"total"`
					Article []struct {
						ID           string `json:"id"`
						Heading      string `json:"heading"`
						Date         string `json:"date"`
						Page         string `json:"page"`
						PageSequence string `json:"pageSequence"`
						TitleObj     struct {
							ID    string `json:"id"`
							Value string `json:"value"`
						} `json:"title"`
						State       string `json:"state"`
						Category    string `json:"category"`
						WordCount   int    `json:"wordCount"`
						Illustrated string `json:"illustrated"`
						Snippet     string `json:"snippet"`
						TroveURL    string `json:"troveUrl"`
					} `json:"article"`
					Work []struct {
						ID         string   `json:"id"`
						Title      string   `json:"title"`
						Contrib    []string `json:"contributor"`
						IssuedDate string   `json:"issued"`
						Format     []string `json:"type"`
						TroveURL   string   `json:"troveUrl"`
					} `json:"work"`
				} `json:"records"`
			} `json:"category"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("trove decode: %w", err)
		}
		for _, cat := range resp.Category {
			out.Total += cat.Records.Total
			for _, a := range cat.Records.Article {
				out.Articles = append(out.Articles, TroveArticle{
					ID:           a.ID,
					Heading:      a.Heading,
					Date:         a.Date,
					Page:         a.Page,
					PageSequence: a.PageSequence,
					Title:        a.TitleObj.Value,
					State:        a.State,
					Category:     a.Category,
					WordCount:    a.WordCount,
					IllustratedQ: strings.EqualFold(a.Illustrated, "Y"),
					Snippet:      a.Snippet,
					URL:          a.TroveURL,
				})
			}
			for _, w := range cat.Records.Work {
				creator := ""
				if len(w.Contrib) > 0 {
					creator = w.Contrib[0]
				}
				format := ""
				if len(w.Format) > 0 {
					format = w.Format[0]
				}
				out.Books = append(out.Books, TroveBook{
					ID:         w.ID,
					Title:      w.Title,
					Creator:    creator,
					IssuedDate: w.IssuedDate,
					Format:     format,
					URL:        w.TroveURL,
				})
			}
		}

	case "newspaper_article":
		id, _ := input["article_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.article_id required (Trove article id)")
		}
		out.Query = id
		params := url.Values{}
		params.Set("encoding", "json")
		params.Set("include", "articleText")
		body, err := get("/newspaper/"+url.PathEscape(id), params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Article struct {
				ID       string `json:"id"`
				Heading  string `json:"heading"`
				Date     string `json:"date"`
				Page     string `json:"page"`
				TitleObj struct {
					ID    string `json:"id"`
					Value string `json:"value"`
				} `json:"title"`
				State       string `json:"state"`
				Category    string `json:"category"`
				WordCount   int    `json:"wordCount"`
				Illustrated string `json:"illustrated"`
				ArticleText string `json:"articleText"`
				TroveURL    string `json:"troveUrl"`
			} `json:"article"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("trove decode: %w", err)
		}
		a := resp.Article
		out.Articles = []TroveArticle{{
			ID:           a.ID,
			Heading:      a.Heading,
			Date:         a.Date,
			Page:         a.Page,
			Title:        a.TitleObj.Value,
			State:        a.State,
			Category:     a.Category,
			WordCount:    a.WordCount,
			IllustratedQ: strings.EqualFold(a.Illustrated, "Y"),
			Snippet:      stripHTMLBare(a.ArticleText),
			URL:          a.TroveURL,
		}}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use search or newspaper_article", mode)
	}

	out.Returned = len(out.Articles) + len(out.Books)
	out.Entities = troveBuildEntities(out)
	out.HighlightFindings = troveBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func troveBuildEntities(o *TroveSearchOutput) []TroveEntity {
	ents := []TroveEntity{}
	for _, a := range o.Articles {
		desc := a.Snippet
		if len(desc) > 600 {
			desc = desc[:600] + "…"
		}
		ents = append(ents, TroveEntity{
			Kind: "newspaper_article", TroveID: a.ID, Title: a.Heading, Date: a.Date, URL: a.URL,
			Description: desc,
			Attributes: map[string]any{
				"newspaper_title": a.Title,
				"page":            a.Page,
				"state":           a.State,
				"word_count":      a.WordCount,
				"illustrated":     a.IllustratedQ,
			},
		})
	}
	for _, b := range o.Books {
		ents = append(ents, TroveEntity{
			Kind: "book", TroveID: b.ID, Title: b.Title, Date: b.IssuedDate, URL: b.URL,
			Attributes: map[string]any{"creator": b.Creator, "format": b.Format},
		})
	}
	return ents
}

func troveBuildHighlights(o *TroveSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ trove %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, a := range o.Articles {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s (%s, %s p.%s) — %s", a.Heading, a.Title, a.Date, a.Page, a.URL))
	}
	for i, b := range o.Books {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s (%s) %s", b.Title, b.Creator, b.IssuedDate, b.URL))
	}
	return hi
}
