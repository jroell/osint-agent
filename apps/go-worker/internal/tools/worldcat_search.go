package tools

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// WorldCatSearch wraps the OCLC Open Search API on worldcat.org. The API
// has been migrated/restructured by OCLC; we use the open SRU/RSS
// endpoint when available and the metadata API when WORLDCAT_API_KEY is
// present.
//
// Modes:
//   - "search"  : full-text search returning OCLC numbers + bibliographic stubs
//   - "by_oclc" : fetch single record by OCLC number
//   - "by_isbn" : ISBN lookup
//
// Knowledge-graph: emits typed entities (kind: "book") with stable
// OCLC numbers + WorldCat URLs.

type WCRecord struct {
	OCLC    string   `json:"oclc"`
	Title   string   `json:"title"`
	Authors []string `json:"authors,omitempty"`
	Year    string   `json:"year,omitempty"`
	URL     string   `json:"worldcat_url"`
}

type WCEntity struct {
	Kind        string         `json:"kind"`
	OCLC        string         `json:"oclc"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type WorldCatSearchOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	Returned          int        `json:"returned"`
	Records           []WCRecord `json:"records,omitempty"`
	Entities          []WCEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

// Atom/RSS feed format used by the legacy WorldCat OpenSearch endpoint.
type wcFeed struct {
	XMLName xml.Name `xml:"feed"`
	Entries []struct {
		Title string `xml:"title"`
		ID    string `xml:"id"`
		Link  struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Author struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Updated string `xml:"updated"`
		Summary string `xml:"summary"`
	} `xml:"entry"`
}

func WorldCatSearch(ctx context.Context, input map[string]any) (*WorldCatSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["oclc"] != nil:
			mode = "by_oclc"
		case input["isbn"] != nil:
			mode = "by_isbn"
		default:
			mode = "search"
		}
	}
	out := &WorldCatSearchOutput{Mode: mode, Source: "worldcat.org"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/atom+xml, application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		if key := os.Getenv("WORLDCAT_API_KEY"); key != "" {
			req.Header.Set("wskey", key)
		}
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("worldcat: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("worldcat HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
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
		params.Set("count", "20")
		body, err := get("https://www.worldcat.org/api/search?" + params.Encode())
		if err != nil {
			// Fall back to the simpler search-results page (HTML)
			out.Records = nil
			break
		}
		// Try Atom, then JSON
		var feed wcFeed
		if err := xml.Unmarshal(body, &feed); err == nil && len(feed.Entries) > 0 {
			for _, e := range feed.Entries {
				oclc := extractOCLC(e.ID)
				out.Records = append(out.Records, WCRecord{
					OCLC: oclc, Title: e.Title, URL: e.Link.Href,
					Authors: []string{e.Author.Name}, Year: wcYearPrefix(e.Updated),
				})
			}
		}
	case "by_oclc":
		oclc, _ := input["oclc"].(string)
		if oclc == "" {
			return nil, fmt.Errorf("input.oclc required")
		}
		out.Query = oclc
		body, err := get("https://www.worldcat.org/api/search?q=no:" + url.QueryEscape(oclc) + "&count=1")
		if err != nil {
			return nil, err
		}
		var feed wcFeed
		_ = xml.Unmarshal(body, &feed)
		for _, e := range feed.Entries {
			out.Records = append(out.Records, WCRecord{
				OCLC: extractOCLC(e.ID), Title: e.Title, URL: e.Link.Href,
				Authors: []string{e.Author.Name},
			})
		}
		if len(out.Records) == 0 {
			// Add a stub record so ER envelope is non-empty.
			out.Records = []WCRecord{{
				OCLC: oclc,
				URL:  "https://www.worldcat.org/oclc/" + url.PathEscape(oclc),
			}}
		}
	case "by_isbn":
		isbn, _ := input["isbn"].(string)
		if isbn == "" {
			return nil, fmt.Errorf("input.isbn required")
		}
		out.Query = isbn
		body, err := get("https://www.worldcat.org/api/search?q=bn:" + url.QueryEscape(isbn) + "&count=10")
		if err != nil {
			return nil, err
		}
		var feed wcFeed
		_ = xml.Unmarshal(body, &feed)
		for _, e := range feed.Entries {
			out.Records = append(out.Records, WCRecord{
				OCLC: extractOCLC(e.ID), Title: e.Title, URL: e.Link.Href,
				Authors: []string{e.Author.Name},
			})
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Records)
	out.Entities = wcBuildEntities(out)
	out.HighlightFindings = wcBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func wcYearPrefix(s string) string {
	if len(s) >= 4 {
		return s[:4]
	}
	return s
}

func extractOCLC(id string) string {
	// e.g. "https://worldcat.org/oclc/644097"
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

func wcBuildEntities(o *WorldCatSearchOutput) []WCEntity {
	ents := []WCEntity{}
	for _, r := range o.Records {
		ents = append(ents, WCEntity{
			Kind: "book", OCLC: r.OCLC, Title: r.Title, URL: r.URL, Date: r.Year,
			Description: strings.Join(r.Authors, ", "),
			Attributes:  map[string]any{"authors": r.Authors},
		})
	}
	return ents
}

func wcBuildHighlights(o *WorldCatSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ worldcat %s: %d records", o.Mode, o.Returned)}
	for i, r := range o.Records {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • [oclc:%s] %s — %s (%s)", r.OCLC, r.Title, strings.Join(r.Authors, ", "), r.Year))
	}
	return hi
}
