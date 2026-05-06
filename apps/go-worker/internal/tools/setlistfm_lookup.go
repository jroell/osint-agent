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

// SetlistFMLookup wraps the Setlist.fm REST API. REQUIRES SETLISTFM_API_KEY.
//
// The canonical source for concert-by-concert setlists. Critical for
// any "what songs were played at concert X on date Y" question.
//
// Modes:
//   - "search_setlists" : keyword/artistName/year/cityName search
//   - "setlist_by_id"   : fetch a setlist by id
//   - "search_artists"  : artist by name
//
// Knowledge-graph: emits typed entities (kind: "concert" | "artist") with
// stable Setlist.fm URLs.

type SFSetlist struct {
	ID           string   `json:"setlist_id"`
	EventDate    string   `json:"event_date,omitempty"`
	Artist       string   `json:"artist,omitempty"`
	ArtistMBID   string   `json:"artist_mbid,omitempty"`
	VenueName    string   `json:"venue_name,omitempty"`
	VenueCity    string   `json:"venue_city,omitempty"`
	VenueCountry string   `json:"venue_country,omitempty"`
	Tour         string   `json:"tour,omitempty"`
	URL          string   `json:"setlistfm_url"`
	Songs        []string `json:"songs,omitempty"`
}

type SFEntity struct {
	Kind        string         `json:"kind"`
	SetlistFMID string         `json:"setlistfm_id"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type SetlistFMLookupOutput struct {
	Mode              string           `json:"mode"`
	Query             string           `json:"query,omitempty"`
	Returned          int              `json:"returned"`
	Total             int              `json:"total,omitempty"`
	Setlists          []SFSetlist      `json:"setlists,omitempty"`
	Artists           []map[string]any `json:"artists,omitempty"`
	Entities          []SFEntity       `json:"entities"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
}

func SetlistFMLookup(ctx context.Context, input map[string]any) (*SetlistFMLookupOutput, error) {
	apiKey := os.Getenv("SETLISTFM_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("SETLISTFM_API_KEY not set; register at api.setlist.fm and set env var")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["setlist_id"] != nil:
			mode = "setlist_by_id"
		case input["artist_name"] != nil && input["search_artists"] == true:
			mode = "search_artists"
		default:
			mode = "search_setlists"
		}
	}
	out := &SetlistFMLookupOutput{Mode: mode, Source: "api.setlist.fm"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(path string, params url.Values) ([]byte, error) {
		u := "https://api.setlist.fm" + path
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("setlistfm: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("setlistfm: unauthorized (check SETLISTFM_API_KEY)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("setlistfm HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search_setlists":
		params := url.Values{}
		if v, ok := input["artist_name"].(string); ok && v != "" {
			params.Set("artistName", v)
		}
		if v, ok := input["year"].(string); ok && v != "" {
			params.Set("year", v)
		}
		if v, ok := input["city_name"].(string); ok && v != "" {
			params.Set("cityName", v)
		}
		if v, ok := input["venue_name"].(string); ok && v != "" {
			params.Set("venueName", v)
		}
		out.Query = params.Encode()
		body, err := get("/rest/1.0/search/setlists", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Total   int              `json:"total"`
			Setlist []map[string]any `json:"setlist"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("setlistfm decode: %w", err)
		}
		out.Total = resp.Total
		for _, s := range resp.Setlist {
			out.Setlists = append(out.Setlists, parseSFSetlist(s))
		}
	case "setlist_by_id":
		id, _ := input["setlist_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.setlist_id required")
		}
		out.Query = id
		body, err := get("/rest/1.0/setlist/"+url.PathEscape(id), url.Values{})
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("setlistfm decode: %w", err)
		}
		out.Setlists = []SFSetlist{parseSFSetlist(rec)}
	case "search_artists":
		name, _ := input["artist_name"].(string)
		if name == "" {
			return nil, fmt.Errorf("input.artist_name required")
		}
		out.Query = name
		params := url.Values{}
		params.Set("artistName", name)
		body, err := get("/rest/1.0/search/artists", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Total  int              `json:"total"`
			Artist []map[string]any `json:"artist"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("setlistfm decode: %w", err)
		}
		out.Total = resp.Total
		out.Artists = resp.Artist
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Setlists) + len(out.Artists)
	out.Entities = sfBuildEntities(out)
	out.HighlightFindings = sfBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseSFSetlist(m map[string]any) SFSetlist {
	s := SFSetlist{
		ID:        gtString(m, "id"),
		EventDate: gtString(m, "eventDate"),
		Tour:      "",
		URL:       gtString(m, "url"),
	}
	if a, ok := m["artist"].(map[string]any); ok {
		s.Artist = gtString(a, "name")
		s.ArtistMBID = gtString(a, "mbid")
	}
	if v, ok := m["venue"].(map[string]any); ok {
		s.VenueName = gtString(v, "name")
		if c, ok := v["city"].(map[string]any); ok {
			s.VenueCity = gtString(c, "name")
			if cc, ok := c["country"].(map[string]any); ok {
				s.VenueCountry = gtString(cc, "name")
			}
		}
	}
	if t, ok := m["tour"].(map[string]any); ok {
		s.Tour = gtString(t, "name")
	}
	if sets, ok := m["sets"].(map[string]any); ok {
		if setArr, ok := sets["set"].([]any); ok {
			for _, set := range setArr {
				if sm, ok := set.(map[string]any); ok {
					if songs, ok := sm["song"].([]any); ok {
						for _, song := range songs {
							if smap, ok := song.(map[string]any); ok {
								if name := gtString(smap, "name"); name != "" {
									s.Songs = append(s.Songs, name)
								}
							}
						}
					}
				}
			}
		}
	}
	return s
}

func sfBuildEntities(o *SetlistFMLookupOutput) []SFEntity {
	ents := []SFEntity{}
	for _, s := range o.Setlists {
		title := fmt.Sprintf("%s @ %s, %s", s.Artist, s.VenueName, s.VenueCity)
		ents = append(ents, SFEntity{
			Kind: "concert", SetlistFMID: s.ID, Title: title, URL: s.URL, Date: s.EventDate,
			Description: fmt.Sprintf("%d songs", len(s.Songs)),
			Attributes: map[string]any{
				"artist":      s.Artist,
				"artist_mbid": s.ArtistMBID,
				"venue":       s.VenueName,
				"city":        s.VenueCity,
				"country":     s.VenueCountry,
				"tour":        s.Tour,
				"songs":       s.Songs,
			},
		})
	}
	for _, a := range o.Artists {
		ents = append(ents, SFEntity{
			Kind: "artist", SetlistFMID: gtString(a, "mbid"), Title: gtString(a, "name"),
			URL: gtString(a, "url"),
		})
	}
	return ents
}

func sfBuildHighlights(o *SetlistFMLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ setlistfm %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, s := range o.Setlists {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s @ %s, %s, %s — %d songs",
			s.Artist, s.VenueName, s.VenueCity, s.EventDate, len(s.Songs)))
	}
	return hi
}
