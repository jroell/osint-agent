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

// EOLSearch wraps the Encyclopedia of Life API (eol.org). Free, no key.
// EOL aggregates ~2M+ taxa with descriptions, common names, images, and
// references — complementary to iNaturalist (observations) and useful
// for taxonomic OSINT.
//
// Modes:
//   - "search"     : taxon search by name
//   - "page"       : EOL page by id (full taxon record)
//   - "hierarchy"  : taxonomic hierarchy for a page
//
// Knowledge-graph: emits typed entities (kind: "taxon") with stable EOL
// page IDs.

type EOLPage struct {
	ID      int    `json:"eol_id"`
	Title   string `json:"title"`
	Link    string `json:"eol_url"`
	Content string `json:"content,omitempty"`
}

type EOLEntity struct {
	Kind        string         `json:"kind"`
	EOLID       int            `json:"eol_id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type EOLSearchOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Pages             []EOLPage      `json:"pages,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []EOLEntity    `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func EOLSearch(ctx context.Context, input map[string]any) (*EOLSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["page_id"] != nil:
			mode = "page"
		case input["hierarchy_id"] != nil:
			mode = "hierarchy"
		default:
			mode = "search"
		}
	}
	out := &EOLSearchOutput{Mode: mode, Source: "eol.org/api"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("eol: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("eol HTTP %d", resp.StatusCode)
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
			"q":     []string{q},
			"page":  []string{"1"},
			"exact": []string{"false"},
		}
		body, err := get("https://eol.org/api/search/1.0.json?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("eol decode: %w", err)
		}
		for _, r := range resp.Results {
			id := int(gtFloat(r, "id"))
			out.Pages = append(out.Pages, EOLPage{
				ID:      id,
				Title:   gtString(r, "title"),
				Link:    gtString(r, "link"),
				Content: hfTruncate(gtString(r, "content"), 400),
			})
		}
	case "page":
		id := tmdbIntID(input, "page_id")
		if id == 0 {
			return nil, fmt.Errorf("input.page_id required")
		}
		out.Query = fmt.Sprintf("%d", id)
		body, err := get(fmt.Sprintf("https://eol.org/api/pages/1.0/%d.json?taxonomy=true&references=true", id))
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("eol decode: %w", err)
		}
		out.Detail = rec
		page := EOLPage{ID: id}
		if t, ok := rec["taxonConcept"].(map[string]any); ok {
			page.Title = gtString(t, "scientificName")
		}
		page.Link = fmt.Sprintf("https://eol.org/pages/%d", id)
		out.Pages = []EOLPage{page}
	case "hierarchy":
		id := tmdbIntID(input, "hierarchy_id")
		if id == 0 {
			return nil, fmt.Errorf("input.hierarchy_id required")
		}
		out.Query = fmt.Sprintf("%d", id)
		body, err := get(fmt.Sprintf("https://eol.org/api/hierarchy_entries/1.0/%d.json", id))
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("eol decode: %w", err)
		}
		out.Detail = rec
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Pages)
	out.Entities = eolBuildEntities(out)
	out.HighlightFindings = eolBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func eolBuildEntities(o *EOLSearchOutput) []EOLEntity {
	ents := []EOLEntity{}
	for _, p := range o.Pages {
		ents = append(ents, EOLEntity{
			Kind: "taxon", EOLID: p.ID, Name: p.Title, URL: p.Link,
			Description: p.Content,
		})
	}
	return ents
}

func eolBuildHighlights(o *EOLSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ eol %s: %d records", o.Mode, o.Returned)}
	for i, p := range o.Pages {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • [%d] %s — %s", p.ID, p.Title, p.Link))
	}
	return hi
}
