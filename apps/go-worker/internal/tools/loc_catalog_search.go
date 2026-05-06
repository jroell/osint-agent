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

// LOCCatalogSearch wraps the US Library of Congress catalog/search API
// (www.loc.gov/api/). Free, no key. Covers books, maps, prints,
// photographs, manuscripts, and the LoC's authority records (id.loc.gov).
//
// Modes:
//   - "search"     : full-text search across all loc.gov collections
//   - "item"       : fetch a specific LoC item by URL slug
//   - "subject_authority" : id.loc.gov authority lookup for a subject term
//
// Knowledge-graph: each result emits typed entity (kind:
// "library_item" | "subject_authority" | "person") with stable LoC URLs.

type LOCItem struct {
	ID           string   `json:"loc_id"`
	Title        string   `json:"title"`
	Date         string   `json:"date,omitempty"`
	Format       string   `json:"format,omitempty"`
	Subjects     []string `json:"subjects,omitempty"`
	Description  string   `json:"description,omitempty"`
	URL          string   `json:"loc_url"`
	Online       bool     `json:"online,omitempty"`
	Library      string   `json:"library,omitempty"`
	Contributors []string `json:"contributors,omitempty"`
}

type LOCAuthority struct {
	ID          string `json:"loc_id"`
	Label       string `json:"preferred_label"`
	URI         string `json:"uri"`
	BroaderTerm string `json:"broader_term,omitempty"`
	Type        string `json:"type,omitempty"`
}

type LOCEntity struct {
	Kind        string         `json:"kind"`
	LOCID       string         `json:"loc_id"`
	Title       string         `json:"title"`
	URL         string         `json:"url,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type LOCCatalogSearchOutput struct {
	Mode              string        `json:"mode"`
	Query             string        `json:"query,omitempty"`
	Returned          int           `json:"returned"`
	Total             int           `json:"total,omitempty"`
	Items             []LOCItem     `json:"items,omitempty"`
	Authority         *LOCAuthority `json:"authority,omitempty"`
	Entities          []LOCEntity   `json:"entities"`
	HighlightFindings []string      `json:"highlight_findings"`
	Source            string        `json:"source"`
	TookMs            int64         `json:"tookMs"`
}

func LOCCatalogSearch(ctx context.Context, input map[string]any) (*LOCCatalogSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["loc_url_slug"] != nil:
			mode = "item"
		case input["subject"] != nil:
			mode = "subject_authority"
		default:
			mode = "search"
		}
	}
	out := &LOCCatalogSearchOutput{Mode: mode, Source: "www.loc.gov"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("loc: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("loc: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("loc HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
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
		params.Set("fo", "json")
		if c, ok := input["collection"].(string); ok && c != "" {
			params.Set("c", c)
		}
		if onlyOnline, ok := input["only_online"].(bool); ok && onlyOnline {
			params.Set("fa", "online-format:online")
		}
		params.Set("c", "20") // 20 results
		body, err := get("https://www.loc.gov/search/?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			Pagination struct {
				Of int `json:"of"`
			} `json:"pagination"`
			Results []struct {
				ID           string   `json:"id"`
				Title        string   `json:"title"`
				Date         string   `json:"date"`
				URL          string   `json:"url"`
				Description  []string `json:"description"`
				Subject      []string `json:"subject"`
				Online       []string `json:"online_format"`
				Format       []string `json:"original_format"`
				Library      []string `json:"partof"`
				Contributors []string `json:"contributor"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("loc decode: %w", err)
		}
		out.Total = resp.Pagination.Of
		for _, it := range resp.Results {
			format := ""
			if len(it.Format) > 0 {
				format = it.Format[0]
			}
			library := ""
			if len(it.Library) > 0 {
				library = it.Library[0]
			}
			desc := ""
			if len(it.Description) > 0 {
				desc = it.Description[0]
				if len(desc) > 600 {
					desc = desc[:600] + "…"
				}
			}
			out.Items = append(out.Items, LOCItem{
				ID:           it.ID,
				Title:        it.Title,
				Date:         it.Date,
				Format:       format,
				Subjects:     it.Subject,
				Description:  desc,
				URL:          it.URL,
				Online:       len(it.Online) > 0,
				Library:      library,
				Contributors: it.Contributors,
			})
		}

	case "item":
		slug, _ := input["loc_url_slug"].(string)
		if slug == "" {
			return nil, fmt.Errorf("input.loc_url_slug required (e.g. 'item/96526449')")
		}
		out.Query = slug
		body, err := get("https://www.loc.gov/" + strings.TrimPrefix(slug, "/") + "/?fo=json")
		if err != nil {
			return nil, err
		}
		var resp struct {
			Item struct {
				ID          string   `json:"id"`
				Title       string   `json:"title"`
				Date        string   `json:"date"`
				URL         string   `json:"url"`
				Subject     []string `json:"subjects"`
				Description []string `json:"description"`
			} `json:"item"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("loc decode: %w", err)
		}
		desc := ""
		if len(resp.Item.Description) > 0 {
			desc = resp.Item.Description[0]
		}
		out.Items = []LOCItem{{
			ID:          resp.Item.ID,
			Title:       resp.Item.Title,
			Date:        resp.Item.Date,
			Subjects:    resp.Item.Subject,
			Description: desc,
			URL:         resp.Item.URL,
		}}

	case "subject_authority":
		subj, _ := input["subject"].(string)
		if subj == "" {
			return nil, fmt.Errorf("input.subject required")
		}
		out.Query = subj
		params := url.Values{}
		params.Set("q", subj)
		params.Set("format", "json")
		body, err := get("https://id.loc.gov/authorities/subjects/suggest2?" + params.Encode())
		if err != nil {
			return nil, err
		}
		// id.loc.gov suggest2 returns: {"q":..., "hits":[{"suggestLabel":..., "uri":..., "token":...}]}
		var resp struct {
			Hits []struct {
				SuggestLabel string `json:"suggestLabel"`
				URI          string `json:"uri"`
				Token        string `json:"token"`
				ALabel       string `json:"aLabel"`
			} `json:"hits"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("id.loc.gov decode: %w", err)
		}
		if len(resp.Hits) > 0 {
			h := resp.Hits[0]
			out.Authority = &LOCAuthority{
				ID:    h.Token,
				Label: h.SuggestLabel,
				URI:   h.URI,
				Type:  "subject",
			}
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Items)
	if out.Authority != nil {
		out.Returned++
	}
	out.Entities = locBuildEntities(out)
	out.HighlightFindings = locBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func extractLOCID(uri string) string {
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

func locBuildEntities(o *LOCCatalogSearchOutput) []LOCEntity {
	ents := []LOCEntity{}
	for _, it := range o.Items {
		ents = append(ents, LOCEntity{
			Kind: "library_item", LOCID: it.ID, Title: it.Title, URL: it.URL, Date: it.Date,
			Description: it.Description,
			Attributes: map[string]any{
				"format":       it.Format,
				"subjects":     it.Subjects,
				"library":      it.Library,
				"online":       it.Online,
				"contributors": it.Contributors,
			},
		})
	}
	if a := o.Authority; a != nil {
		ents = append(ents, LOCEntity{
			Kind: "subject_authority", LOCID: a.ID, Title: a.Label, URL: a.URI,
			Attributes: map[string]any{"type": a.Type, "broader_term": a.BroaderTerm},
		})
	}
	return ents
}

func locBuildHighlights(o *LOCCatalogSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ loc %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, it := range o.Items {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s (%s) — %s", it.Title, it.Date, it.URL))
	}
	if a := o.Authority; a != nil {
		hi = append(hi, fmt.Sprintf("  • authority: %s — %s", a.Label, a.URI))
	}
	return hi
}
