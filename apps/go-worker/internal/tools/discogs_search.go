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

// DiscogsSearch wraps the Discogs API (api.discogs.com).
// Free with optional DISCOGS_TOKEN for higher rate limits.
//
// Discogs has tracklist-level metadata and rare/regional releases that
// MusicBrainz misses. Critical for first-album-with-tracks questions
// and obscure release identification.
//
// Modes:
//   - "search"        : full-text search across releases, masters, artists, labels
//   - "release"       : fetch a release by id (with full tracklist)
//   - "master_release": fetch a master release (canonical version) by id
//   - "artist"        : fetch artist record by id
//
// Knowledge-graph: emits typed entities (kind: "release" | "master_release"
// | "artist" | "label") with stable Discogs IDs.

type DCRelease struct {
	ID      int      `json:"discogs_id"`
	Title   string   `json:"title"`
	Year    int      `json:"year,omitempty"`
	Country string   `json:"country,omitempty"`
	Format  []string `json:"format,omitempty"`
	Genre   []string `json:"genre,omitempty"`
	Style   []string `json:"style,omitempty"`
	Artist  string   `json:"artist,omitempty"`
	Label   []string `json:"label,omitempty"`
	URL     string   `json:"discogs_url"`
	Type    string   `json:"type,omitempty"` // release | master | artist | label
}

type DCEntity struct {
	Kind        string         `json:"kind"`
	DiscogsID   int            `json:"discogs_id"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type DiscogsSearchOutput struct {
	Mode              string           `json:"mode"`
	Query             string           `json:"query,omitempty"`
	Returned          int              `json:"returned"`
	Total             int              `json:"total,omitempty"`
	Results           []DCRelease      `json:"results,omitempty"`
	Detail            map[string]any   `json:"detail,omitempty"`
	Tracklist         []map[string]any `json:"tracklist,omitempty"`
	Entities          []DCEntity       `json:"entities"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
}

func DiscogsSearch(ctx context.Context, input map[string]any) (*DiscogsSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["release_id"] != nil:
			mode = "release"
		case input["master_id"] != nil:
			mode = "master_release"
		case input["artist_id"] != nil:
			mode = "artist"
		default:
			mode = "search"
		}
	}
	out := &DiscogsSearchOutput{Mode: mode, Source: "api.discogs.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0 +https://github.com/jroell/osint-agent")
		if token := os.Getenv("DISCOGS_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Discogs token="+token)
		}
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("discogs: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("discogs HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
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
		params.Set("per_page", "20")
		if t, ok := input["type"].(string); ok && t != "" {
			params.Set("type", t)
		}
		body, err := get("https://api.discogs.com/database/search?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			Pagination struct {
				Items int `json:"items"`
			} `json:"pagination"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("discogs decode: %w", err)
		}
		out.Total = resp.Pagination.Items
		for _, r := range resp.Results {
			out.Results = append(out.Results, parseDCRelease(r))
		}
	case "release":
		id, _ := input["release_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.release_id required")
		}
		out.Query = id
		body, err := get("https://api.discogs.com/releases/" + url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("discogs decode: %w", err)
		}
		out.Detail = rec
		if tl, ok := rec["tracklist"].([]any); ok {
			for _, t := range tl {
				if m, ok := t.(map[string]any); ok {
					out.Tracklist = append(out.Tracklist, m)
				}
			}
		}
	case "master_release":
		id, _ := input["master_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.master_id required")
		}
		out.Query = id
		body, err := get("https://api.discogs.com/masters/" + url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("discogs decode: %w", err)
		}
		out.Detail = rec
	case "artist":
		id, _ := input["artist_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.artist_id required")
		}
		out.Query = id
		body, err := get("https://api.discogs.com/artists/" + url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("discogs decode: %w", err)
		}
		out.Detail = rec
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Results)
	if out.Detail != nil {
		out.Returned++
	}
	out.Entities = discogsBuildEntities(out)
	out.HighlightFindings = discogsBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseDCRelease(m map[string]any) DCRelease {
	r := DCRelease{
		ID:      int(gtFloat(m, "id")),
		Title:   gtString(m, "title"),
		Country: gtString(m, "country"),
		Type:    gtString(m, "type"),
	}
	if y, ok := m["year"].(float64); ok {
		r.Year = int(y)
	} else if ys := gtString(m, "year"); ys != "" {
		_, _ = fmt.Sscanf(ys, "%d", &r.Year)
	}
	for _, k := range []string{"format", "genre", "style", "label"} {
		if v, ok := m[k].([]any); ok {
			for _, x := range v {
				if s, ok := x.(string); ok {
					switch k {
					case "format":
						r.Format = append(r.Format, s)
					case "genre":
						r.Genre = append(r.Genre, s)
					case "style":
						r.Style = append(r.Style, s)
					case "label":
						r.Label = append(r.Label, s)
					}
				}
			}
		}
	}
	r.URL = gtString(m, "uri")
	if !strings.HasPrefix(r.URL, "http") && r.URL != "" {
		r.URL = "https://www.discogs.com" + r.URL
	}
	return r
}

func discogsBuildEntities(o *DiscogsSearchOutput) []DCEntity {
	ents := []DCEntity{}
	for _, r := range o.Results {
		kind := "release"
		switch r.Type {
		case "master":
			kind = "master_release"
		case "artist":
			kind = "artist"
		case "label":
			kind = "label"
		}
		date := ""
		if r.Year > 0 {
			date = fmt.Sprintf("%d", r.Year)
		}
		ents = append(ents, DCEntity{
			Kind: kind, DiscogsID: r.ID, Title: r.Title, URL: r.URL, Date: date,
			Attributes: map[string]any{
				"format": r.Format, "genre": r.Genre, "style": r.Style,
				"label": r.Label, "country": r.Country,
			},
		})
	}
	if d := o.Detail; d != nil {
		// Best effort: extract identifying fields from any detail mode.
		ents = append(ents, DCEntity{
			Kind:      "release",
			DiscogsID: int(gtFloat(d, "id")),
			Title:     gtString(d, "title"),
			URL:       gtString(d, "uri"),
			Date:      gtString(d, "released"),
			Attributes: map[string]any{
				"tracklist_count": len(o.Tracklist),
			},
		})
	}
	return ents
}

func discogsBuildHighlights(o *DiscogsSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ discogs %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, r := range o.Results {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • [%s #%d] %s (%d) %s — %s",
			r.Type, r.ID, r.Title, r.Year, strings.Join(r.Genre, ","), r.URL))
	}
	if len(o.Tracklist) > 0 {
		hi = append(hi, fmt.Sprintf("  • tracklist: %d tracks", len(o.Tracklist)))
	}
	return hi
}
