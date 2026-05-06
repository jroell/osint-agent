package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ChroniclingAmericaSearch wraps the US Library of Congress Chronicling
// America newspaper API. Free, no key. Covers ~3.7M digitized US
// newspaper pages from 1690-1963 with OCR full-text search.
//
// Modes:
//   - "search_pages" : full-text page-level search across the corpus,
//                      with year, state, and newspaper-LCCN filters
//   - "newspaper"    : metadata for a single newspaper title by LCCN
//
// Knowledge-graph: each result emits a typed entity envelope (kind:
// "newspaper_page" | "newspaper_title") with stable LoC URL.

type CAPage struct {
	LCCN     string `json:"lccn"`
	Title    string `json:"newspaper_title,omitempty"`
	Date     string `json:"date,omitempty"`
	Place    string `json:"place_of_publication,omitempty"`
	Edition  string `json:"edition,omitempty"`
	Section  string `json:"section_label,omitempty"`
	Sequence int    `json:"page_sequence,omitempty"`
	OCR      string `json:"ocr_excerpt,omitempty"`
	URL      string `json:"loc_url,omitempty"`
	PageURL  string `json:"page_url,omitempty"`
}

type CATitle struct {
	LCCN      string `json:"lccn"`
	Name      string `json:"name"`
	State     string `json:"state,omitempty"`
	StartYear string `json:"start_year,omitempty"`
	EndYear   string `json:"end_year,omitempty"`
	Frequency string `json:"frequency,omitempty"`
	URL       string `json:"loc_url,omitempty"`
}

type CAEntity struct {
	Kind        string         `json:"kind"`
	LCCN        string         `json:"lccn,omitempty"`
	Title       string         `json:"title"`
	Date        string         `json:"date,omitempty"`
	URL         string         `json:"url"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type ChroniclingAmericaSearchOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	Returned          int        `json:"returned"`
	Total             int        `json:"total,omitempty"`
	Pages             []CAPage   `json:"pages,omitempty"`
	Title             *CATitle   `json:"title_record,omitempty"`
	Entities          []CAEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func ChroniclingAmericaSearch(ctx context.Context, input map[string]any) (*ChroniclingAmericaSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["lccn"] != nil:
			mode = "newspaper"
		default:
			mode = "search_pages"
		}
	}
	out := &ChroniclingAmericaSearchOutput{Mode: mode, Source: "chroniclingamerica.loc.gov"}
	start := time.Now()
	cli := &http.Client{
		Timeout: 45 * time.Second,
		// Follow 301/302 to handle the LoC's chronicling-america → loc.gov migration redirect.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("chroniclingamerica: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("chroniclingamerica: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("chroniclingamerica HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search_pages":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		params := url.Values{}
		params.Set("andtext", q)
		params.Set("format", "json")
		if y1, ok := input["year_from"].(float64); ok && y1 > 0 {
			params.Set("dateFilterType", "yearRange")
			params.Set("date1", fmt.Sprintf("%d", int(y1)))
		}
		if y2, ok := input["year_to"].(float64); ok && y2 > 0 {
			params.Set("dateFilterType", "yearRange")
			params.Set("date2", fmt.Sprintf("%d", int(y2)))
		}
		if state, ok := input["state"].(string); ok && state != "" {
			params.Set("state", state)
		}
		if lccn, ok := input["lccn"].(string); ok && lccn != "" {
			params.Set("lccn", lccn)
		}
		if rows, ok := input["limit"].(float64); ok && rows > 0 {
			params.Set("rows", fmt.Sprintf("%d", int(rows)))
		} else {
			params.Set("rows", "20")
		}
		// LoC migrated chroniclingamerica.loc.gov → www.loc.gov/collections/chronicling-america/ in 2025.
		params.Set("fo", "json")
		params.Del("format")
		body, err := get("https://www.loc.gov/collections/chronicling-america/?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			Pagination struct {
				Of int `json:"of"`
			} `json:"pagination"`
			Results []struct {
				ID            string   `json:"id"`
				URL           string   `json:"url"`
				Title         string   `json:"title"`
				Date          string   `json:"date"`
				PartOf        []string `json:"partof"`
				NumberLCCN    []string `json:"number_lccn"`
				NumberPage    []string `json:"number_page"`
				LocationCity  []string `json:"location_city"`
				LocationState []string `json:"location_state"`
				Description   []string `json:"description"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("chroniclingamerica decode: %w", err)
		}
		out.Total = resp.Pagination.Of
		for _, it := range resp.Results {
			lccn := ""
			if len(it.NumberLCCN) > 0 {
				lccn = it.NumberLCCN[0]
			}
			pageNum := 0
			if len(it.NumberPage) > 0 {
				_, _ = fmt.Sscanf(it.NumberPage[0], "%d", &pageNum)
			}
			place := ""
			if len(it.LocationCity) > 0 {
				place = it.LocationCity[0]
				if len(it.LocationState) > 0 {
					place += ", " + it.LocationState[0]
				}
			}
			// title comes as "Image N of [Newspaper Name] ([City]), [Date]"
			newspaper := it.Title
			if idx := strings.Index(newspaper, " of "); idx >= 0 && strings.HasPrefix(newspaper, "Image ") {
				newspaper = newspaper[idx+4:]
				if comma := strings.LastIndex(newspaper, ", "); comma >= 0 {
					newspaper = newspaper[:comma]
				}
			}
			ocr := ""
			if len(it.Description) > 0 {
				ocr = it.Description[0]
				if len(ocr) > 800 {
					ocr = ocr[:800] + "…"
				}
			}
			out.Pages = append(out.Pages, CAPage{
				LCCN:     lccn,
				Title:    newspaper,
				Place:    place,
				Date:     it.Date,
				Sequence: pageNum,
				OCR:      ocr,
				URL:      it.URL,
				PageURL:  it.URL,
			})
		}

	case "newspaper":
		lccn, _ := input["lccn"].(string)
		if lccn == "" {
			return nil, fmt.Errorf("input.lccn required (Library of Congress Control Number)")
		}
		out.Query = lccn
		body, err := get("https://chroniclingamerica.loc.gov/lccn/" + url.PathEscape(lccn) + ".json")
		if err != nil {
			return nil, err
		}
		var resp struct {
			Name      string   `json:"name"`
			LCCN      string   `json:"lccn"`
			Place     []string `json:"place"`
			StartYear string   `json:"start_year"`
			EndYear   string   `json:"end_year"`
			Subject   []string `json:"subject"`
			URL       string   `json:"url"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("chroniclingamerica decode: %w", err)
		}
		state := ""
		if len(resp.Place) > 0 {
			state = resp.Place[0]
		}
		out.Title = &CATitle{
			LCCN:      resp.LCCN,
			Name:      resp.Name,
			State:     state,
			StartYear: resp.StartYear,
			EndYear:   resp.EndYear,
			URL:       resp.URL,
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use search_pages or newspaper", mode)
	}

	out.Returned = len(out.Pages)
	if out.Title != nil {
		out.Returned++
	}
	out.Entities = caBuildEntities(out)
	out.HighlightFindings = caBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func caBuildEntities(o *ChroniclingAmericaSearchOutput) []CAEntity {
	ents := []CAEntity{}
	for _, p := range o.Pages {
		ents = append(ents, CAEntity{
			Kind: "newspaper_page", LCCN: p.LCCN,
			Title:       fmt.Sprintf("%s p.%d", p.Title, p.Sequence),
			Date:        p.Date,
			URL:         p.PageURL,
			Description: p.OCR,
			Attributes: map[string]any{
				"newspaper_title": p.Title,
				"place":           p.Place,
				"sequence":        p.Sequence,
			},
		})
	}
	if t := o.Title; t != nil {
		ents = append(ents, CAEntity{
			Kind: "newspaper_title", LCCN: t.LCCN, Title: t.Name, URL: t.URL,
			Attributes: map[string]any{"state": t.State, "start_year": t.StartYear, "end_year": t.EndYear},
		})
	}
	return ents
}

func caBuildHighlights(o *ChroniclingAmericaSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ chroniclingamerica %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, p := range o.Pages {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s p.%d (%s) — %s", p.Title, p.Sequence, p.Date, p.PageURL))
	}
	if t := o.Title; t != nil {
		hi = append(hi, fmt.Sprintf("  • title %s [%s] %s-%s — %s", t.Name, t.LCCN, t.StartYear, t.EndYear, t.URL))
	}
	return hi
}
