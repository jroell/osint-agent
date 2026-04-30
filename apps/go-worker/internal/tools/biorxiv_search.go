package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// BioRxivSearch wraps the bioRxiv / medRxiv preprint API. Free, no auth.
//
// **Why preprints matter for OSINT**: bioRxiv is where biomedical research
// breaks first — typically 6-18 months before peer-reviewed publication.
// medRxiv covers clinical / health-policy / public-health work. New
// outbreak surveillance, vaccine candidates, drug-trial results, and
// epidemiological updates appear here weeks before they hit PubMed.
//
// Closes the academic chain alongside `pubmed_search` (peer-reviewed),
// `arxiv_search` (CS/physics/math), `crossref_paper_search` (DOI-indexed),
// `openalex_search` (open citation graph).
//
// Two modes:
//
//   - "lookup_doi"        : DOI → full paper detail (title, authors,
//                           category, posting date, version, abstract,
//                           JATS XML URL for full-text access, license)
//   - "recent_preprints"  : date range + optional category filter →
//                           list of recent preprints with metadata.
//                           Date range is required; max 100/page so
//                           we paginate up to 5 pages (500 rows).
//
// Server defaults to "biorxiv" (life sciences); pass server="medrxiv"
// for clinical / health policy.

type BioRxivPaper struct {
	DOI       string `json:"doi"`
	Title     string `json:"title"`
	Authors   string `json:"authors,omitempty"`
	Category  string `json:"category,omitempty"`
	Date      string `json:"date,omitempty"`
	Version   string `json:"version,omitempty"`
	Server    string `json:"server,omitempty"`
	License   string `json:"license,omitempty"`
	Abstract  string `json:"abstract,omitempty"`
	JATSXMLURL string `json:"jats_xml_url,omitempty"`
	URL       string `json:"url,omitempty"`
	PublishedJournal string `json:"published_journal,omitempty"`
	PublishedDOI     string `json:"published_doi,omitempty"`
}

type BioRxivSearchOutput struct {
	Mode              string         `json:"mode"`
	Server            string         `json:"server,omitempty"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Paper             *BioRxivPaper  `json:"paper,omitempty"`
	Papers            []BioRxivPaper `json:"papers,omitempty"`

	// Aggregations
	UniqueCategories  []string       `json:"unique_categories,omitempty"`
	CategoryCounts    map[string]int `json:"category_counts,omitempty"`

	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
	Note              string         `json:"note,omitempty"`
}

func BioRxivSearch(ctx context.Context, input map[string]any) (*BioRxivSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if _, ok := input["doi"]; ok {
			mode = "lookup_doi"
		} else {
			mode = "recent_preprints"
		}
	}

	server, _ := input["server"].(string)
	server = strings.ToLower(strings.TrimSpace(server))
	if server == "" {
		server = "biorxiv"
	}
	if server != "biorxiv" && server != "medrxiv" {
		return nil, fmt.Errorf("server must be 'biorxiv' or 'medrxiv'")
	}

	out := &BioRxivSearchOutput{
		Mode:   mode,
		Server: server,
		Source: "api.biorxiv.org",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	switch mode {
	case "lookup_doi":
		doi, _ := input["doi"].(string)
		doi = strings.TrimSpace(doi)
		// Strip "doi:" prefix or full URL
		doi = strings.TrimPrefix(doi, "doi:")
		doi = strings.TrimPrefix(doi, "https://doi.org/")
		doi = strings.TrimPrefix(doi, "http://doi.org/")
		if doi == "" {
			return nil, fmt.Errorf("input.doi required for lookup_doi mode")
		}
		out.Query = doi
		urlStr := fmt.Sprintf("https://api.biorxiv.org/details/%s/%s", server, doi)
		body, err := bioRxivGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Collection []map[string]any `json:"collection"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("biorxiv decode: %w", err)
		}
		if len(raw.Collection) == 0 {
			out.Note = fmt.Sprintf("DOI %s not found in %s preprints", doi, server)
			break
		}
		// Take latest version (last in array)
		p := convertBioRxivPaper(raw.Collection[len(raw.Collection)-1])
		out.Paper = &p
		out.Returned = 1

	case "recent_preprints":
		startDate, _ := input["start_date"].(string)
		endDate, _ := input["end_date"].(string)
		startDate = strings.TrimSpace(startDate)
		endDate = strings.TrimSpace(endDate)
		if startDate == "" || endDate == "" {
			return nil, fmt.Errorf("input.start_date and input.end_date required (YYYY-MM-DD)")
		}
		out.Query = fmt.Sprintf("%s preprints %s..%s", server, startDate, endDate)

		categoryFilter, _ := input["category"].(string)
		categoryFilter = strings.ToLower(strings.TrimSpace(categoryFilter))

		limit := 100
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 500 {
			limit = int(l)
		}

		// API returns 100 per page; paginate up to limit
		offset := 0
		categoryCount := map[string]int{}
		for offset < limit && offset < 500 {
			urlStr := fmt.Sprintf("https://api.biorxiv.org/details/%s/%s/%s/%d", server, startDate, endDate, offset)
			body, err := bioRxivGet(ctx, cli, urlStr)
			if err != nil {
				return nil, err
			}
			var raw struct {
				Messages []struct {
					Status string `json:"status"`
					Total  any    `json:"total"`
				} `json:"messages"`
				Collection []map[string]any `json:"collection"`
			}
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, fmt.Errorf("biorxiv decode: %w", err)
			}
			if len(raw.Collection) == 0 {
				break
			}
			for _, c := range raw.Collection {
				p := convertBioRxivPaper(c)
				cat := strings.ToLower(p.Category)
				categoryCount[p.Category]++
				if categoryFilter != "" && !strings.Contains(cat, categoryFilter) {
					continue
				}
				out.Papers = append(out.Papers, p)
				if len(out.Papers) >= limit {
					break
				}
			}
			if len(out.Papers) >= limit {
				break
			}
			offset += 100
			if len(raw.Collection) < 100 {
				break
			}
		}
		out.Returned = len(out.Papers)
		out.CategoryCounts = categoryCount
		// Sort papers most-recent first
		sort.SliceStable(out.Papers, func(i, j int) bool { return out.Papers[i].Date > out.Papers[j].Date })
		// Top categories list
		for c := range categoryCount {
			out.UniqueCategories = append(out.UniqueCategories, c)
		}
		sort.SliceStable(out.UniqueCategories, func(i, j int) bool {
			return categoryCount[out.UniqueCategories[i]] > categoryCount[out.UniqueCategories[j]]
		})
		if len(out.UniqueCategories) > 12 {
			out.UniqueCategories = out.UniqueCategories[:12]
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: lookup_doi, recent_preprints", mode)
	}

	out.HighlightFindings = buildBioRxivHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func convertBioRxivPaper(c map[string]any) BioRxivPaper {
	doi := gtString(c, "doi")
	p := BioRxivPaper{
		DOI:              doi,
		Title:            gtString(c, "title"),
		Authors:          gtString(c, "authors"),
		Category:         gtString(c, "category"),
		Date:             gtString(c, "date"),
		Version:          gtString(c, "version"),
		Server:           gtString(c, "server"),
		License:          gtString(c, "license"),
		Abstract:         hfTruncate(gtString(c, "abstract"), 600),
		JATSXMLURL:       gtString(c, "jatsxml"),
		PublishedJournal: gtString(c, "published_journal"),
		PublishedDOI:     gtString(c, "published_doi"),
	}
	if doi != "" {
		p.URL = "https://doi.org/" + doi
	}
	return p
}

func bioRxivGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("biorxiv: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("biorxiv HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildBioRxivHighlights(o *BioRxivSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "lookup_doi":
		if o.Paper == nil {
			hi = append(hi, fmt.Sprintf("✗ DOI %s not found in %s", o.Query, o.Server))
			break
		}
		p := o.Paper
		hi = append(hi, fmt.Sprintf("✓ %s [%s]", p.Title, p.Server))
		hi = append(hi, fmt.Sprintf("  posted: %s · category: %s · v%s · license: %s",
			p.Date, p.Category, p.Version, p.License))
		hi = append(hi, "  authors: "+hfTruncate(p.Authors, 200))
		if p.Abstract != "" {
			hi = append(hi, "  abstract: "+hfTruncate(p.Abstract, 250))
		}
		if p.PublishedJournal != "" {
			hi = append(hi, fmt.Sprintf("  📑 published in: %s · DOI %s", p.PublishedJournal, p.PublishedDOI))
		}
		if p.JATSXMLURL != "" {
			hi = append(hi, "  full text (JATS XML): "+p.JATSXMLURL)
		}

	case "recent_preprints":
		hi = append(hi, fmt.Sprintf("✓ %d preprints returned for %s", o.Returned, o.Query))
		if len(o.UniqueCategories) > 0 {
			parts := []string{}
			for i, c := range o.UniqueCategories {
				if i >= 6 {
					break
				}
				parts = append(parts, fmt.Sprintf("%s×%d", c, o.CategoryCounts[c]))
			}
			hi = append(hi, "  top categories: "+strings.Join(parts, " · "))
		}
		for i, p := range o.Papers {
			if i >= 6 {
				break
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s (%s)", p.Date, hfTruncate(p.Title, 80), p.Category))
			hi = append(hi, fmt.Sprintf("    %s", hfTruncate(p.Authors, 100)))
			if p.PublishedJournal != "" {
				hi = append(hi, fmt.Sprintf("    📑 published in %s", p.PublishedJournal))
			}
		}
	}
	return hi
}
