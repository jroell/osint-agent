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

// TVMazeLookup wraps the TVMaze TV-show metadata API. Free, no key.
// Useful as a fallback / cross-reference to TMDB; TVMaze has different
// coverage (stronger on US cable, weaker on international film).
//
// Modes:
//   - "search_shows" : keyword search → list of shows with TVMaze + IMDb IDs
//   - "show_details" : full show record by id
//   - "episodes_list" : all episodes for a show id
//   - "episode_by_number" : episode by show id + season + number
//   - "search_people" : person keyword search
//   - "person_details" : person record by id
//
// Knowledge-graph: every record emits a typed entity envelope (kind:
// tv_show | tv_episode | person) with stable IDs, suitable for direct
// ingest by panel_entity_resolution.

type TVMazeShow struct {
	ID           int      `json:"tvmaze_id"`
	Name         string   `json:"name"`
	Type         string   `json:"type,omitempty"`
	Language     string   `json:"language,omitempty"`
	Premiered    string   `json:"premiered,omitempty"`
	Ended        string   `json:"ended,omitempty"`
	Status       string   `json:"status,omitempty"`
	Rating       float64  `json:"rating,omitempty"`
	IMDb         string   `json:"imdb_id,omitempty"`
	TVRageID     int      `json:"tvrage_id,omitempty"`
	Runtime      int      `json:"runtime,omitempty"`
	Genres       []string `json:"genres,omitempty"`
	Network      string   `json:"network,omitempty"`
	Country      string   `json:"country,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	OfficialSite string   `json:"official_site,omitempty"`
}

type TVMazeEpisode struct {
	ID      int     `json:"tvmaze_id"`
	Name    string  `json:"name"`
	Season  int     `json:"season"`
	Number  int     `json:"number"`
	Type    string  `json:"type,omitempty"`
	Airdate string  `json:"airdate,omitempty"`
	Runtime int     `json:"runtime,omitempty"`
	Rating  float64 `json:"rating,omitempty"`
	Summary string  `json:"summary,omitempty"`
}

type TVMazePerson struct {
	ID       int    `json:"tvmaze_id"`
	Name     string `json:"name"`
	Country  string `json:"country,omitempty"`
	Birthday string `json:"birthday,omitempty"`
	Deathday string `json:"deathday,omitempty"`
	Gender   string `json:"gender,omitempty"`
}

type TVMazeEntity struct {
	Kind        string         `json:"kind"`
	TVMazeID    int            `json:"tvmaze_id"`
	IMDbID      string         `json:"imdb_id,omitempty"`
	Title       string         `json:"title,omitempty"`
	Name        string         `json:"name,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type TVMazeLookupOutput struct {
	Mode              string          `json:"mode"`
	Query             string          `json:"query,omitempty"`
	Returned          int             `json:"returned"`
	Shows             []TVMazeShow    `json:"shows,omitempty"`
	Episodes          []TVMazeEpisode `json:"episodes,omitempty"`
	Episode           *TVMazeEpisode  `json:"episode,omitempty"`
	People            []TVMazePerson  `json:"people,omitempty"`
	Detail            map[string]any  `json:"detail,omitempty"`
	Entities          []TVMazeEntity  `json:"entities"`
	HighlightFindings []string        `json:"highlight_findings"`
	Source            string          `json:"source"`
	TookMs            int64           `json:"tookMs"`
}

const tvMazeBase = "https://api.tvmaze.com"

func TVMazeLookup(ctx context.Context, input map[string]any) (*TVMazeLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["show_id"] != nil && input["season"] != nil && input["number"] != nil:
			mode = "episode_by_number"
		case input["show_id"] != nil:
			mode = "show_details"
		case input["person_id"] != nil:
			mode = "person_details"
		case input["query"] != nil && input["who"] == "person":
			mode = "search_people"
		default:
			mode = "search_shows"
		}
	}
	out := &TVMazeLookupOutput{Mode: mode, Source: "api.tvmaze.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(path string, params url.Values) ([]byte, error) {
		u := tvMazeBase + path
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("tvmaze: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("tvmaze: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("tvmaze HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search_shows":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		body, err := get("/search/shows", url.Values{"q": []string{q}})
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("tvmaze decode: %w", err)
		}
		for _, item := range arr {
			if show, ok := item["show"].(map[string]any); ok {
				out.Shows = append(out.Shows, parseTVMazeShow(show))
			}
		}
	case "show_details":
		id := tmdbIntID(input, "show_id")
		if id == 0 {
			return nil, fmt.Errorf("input.show_id required")
		}
		body, err := get(fmt.Sprintf("/shows/%d", id), url.Values{})
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("tvmaze decode: %w", err)
		}
		out.Detail = rec
		out.Shows = []TVMazeShow{parseTVMazeShow(rec)}
	case "episodes_list":
		id := tmdbIntID(input, "show_id")
		if id == 0 {
			return nil, fmt.Errorf("input.show_id required")
		}
		body, err := get(fmt.Sprintf("/shows/%d/episodes", id), url.Values{})
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("tvmaze decode: %w", err)
		}
		for _, rec := range arr {
			out.Episodes = append(out.Episodes, parseTVMazeEpisode(rec))
		}
	case "episode_by_number":
		id := tmdbIntID(input, "show_id")
		season := tmdbIntID(input, "season")
		number := tmdbIntID(input, "number")
		if id == 0 {
			return nil, fmt.Errorf("input.show_id required")
		}
		body, err := get(fmt.Sprintf("/shows/%d/episodebynumber", id),
			url.Values{"season": []string{fmt.Sprintf("%d", season)}, "number": []string{fmt.Sprintf("%d", number)}})
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("tvmaze decode: %w", err)
		}
		ep := parseTVMazeEpisode(rec)
		out.Episode = &ep
	case "search_people":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		body, err := get("/search/people", url.Values{"q": []string{q}})
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("tvmaze decode: %w", err)
		}
		for _, item := range arr {
			if p, ok := item["person"].(map[string]any); ok {
				out.People = append(out.People, parseTVMazePerson(p))
			}
		}
	case "person_details":
		id := tmdbIntID(input, "person_id")
		if id == 0 {
			return nil, fmt.Errorf("input.person_id required")
		}
		body, err := get(fmt.Sprintf("/people/%d", id), url.Values{})
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("tvmaze decode: %w", err)
		}
		out.Detail = rec
		out.People = []TVMazePerson{parseTVMazePerson(rec)}
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search_shows, show_details, episodes_list, episode_by_number, search_people, person_details", mode)
	}

	out.Returned = len(out.Shows) + len(out.Episodes) + len(out.People)
	if out.Episode != nil {
		out.Returned++
	}
	out.Entities = tvmazeBuildEntities(out)
	out.HighlightFindings = tvmazeBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseTVMazeShow(m map[string]any) TVMazeShow {
	s := TVMazeShow{
		ID:           int(gtFloat(m, "id")),
		Name:         gtString(m, "name"),
		Type:         gtString(m, "type"),
		Language:     gtString(m, "language"),
		Premiered:    gtString(m, "premiered"),
		Ended:        gtString(m, "ended"),
		Status:       gtString(m, "status"),
		Runtime:      int(gtFloat(m, "runtime")),
		Summary:      stripHTMLBare(gtString(m, "summary")),
		OfficialSite: gtString(m, "officialSite"),
	}
	if r, ok := m["rating"].(map[string]any); ok {
		s.Rating = gtFloat(r, "average")
	}
	if e, ok := m["externals"].(map[string]any); ok {
		s.IMDb = gtString(e, "imdb")
		s.TVRageID = int(gtFloat(e, "tvrage"))
	}
	if g, ok := m["genres"].([]any); ok {
		for _, x := range g {
			if str, ok := x.(string); ok {
				s.Genres = append(s.Genres, str)
			}
		}
	}
	if n, ok := m["network"].(map[string]any); ok {
		s.Network = gtString(n, "name")
		if c, ok := n["country"].(map[string]any); ok {
			s.Country = gtString(c, "name")
		}
	}
	if w, ok := m["webChannel"].(map[string]any); ok && s.Network == "" {
		s.Network = gtString(w, "name")
	}
	return s
}

func parseTVMazeEpisode(m map[string]any) TVMazeEpisode {
	e := TVMazeEpisode{
		ID:      int(gtFloat(m, "id")),
		Name:    gtString(m, "name"),
		Season:  int(gtFloat(m, "season")),
		Number:  int(gtFloat(m, "number")),
		Type:    gtString(m, "type"),
		Airdate: gtString(m, "airdate"),
		Runtime: int(gtFloat(m, "runtime")),
		Summary: stripHTMLBare(gtString(m, "summary")),
	}
	if r, ok := m["rating"].(map[string]any); ok {
		e.Rating = gtFloat(r, "average")
	}
	return e
}

func parseTVMazePerson(m map[string]any) TVMazePerson {
	p := TVMazePerson{
		ID:       int(gtFloat(m, "id")),
		Name:     gtString(m, "name"),
		Birthday: gtString(m, "birthday"),
		Deathday: gtString(m, "deathday"),
		Gender:   gtString(m, "gender"),
	}
	if c, ok := m["country"].(map[string]any); ok {
		p.Country = gtString(c, "name")
	}
	return p
}

func tvmazeBuildEntities(o *TVMazeLookupOutput) []TVMazeEntity {
	ents := []TVMazeEntity{}
	for _, s := range o.Shows {
		ents = append(ents, TVMazeEntity{
			Kind: "tv_show", TVMazeID: s.ID, IMDbID: s.IMDb, Title: s.Name,
			Date: s.Premiered, Description: s.Summary,
			Attributes: map[string]any{"network": s.Network, "country": s.Country, "genres": s.Genres, "language": s.Language, "status": s.Status},
		})
	}
	for _, e := range o.Episodes {
		ents = append(ents, TVMazeEntity{
			Kind: "tv_episode", TVMazeID: e.ID, Title: e.Name, Date: e.Airdate, Description: e.Summary,
			Attributes: map[string]any{"season": e.Season, "episode": e.Number, "runtime": e.Runtime},
		})
	}
	if e := o.Episode; e != nil {
		ents = append(ents, TVMazeEntity{
			Kind: "tv_episode", TVMazeID: e.ID, Title: e.Name, Date: e.Airdate, Description: e.Summary,
			Attributes: map[string]any{"season": e.Season, "episode": e.Number, "runtime": e.Runtime},
		})
	}
	for _, p := range o.People {
		ents = append(ents, TVMazeEntity{
			Kind: "person", TVMazeID: p.ID, Name: p.Name, Date: p.Birthday,
			Attributes: map[string]any{"country": p.Country, "deathday": p.Deathday, "gender": p.Gender},
		})
	}
	return ents
}

func tvmazeBuildHighlights(o *TVMazeLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ tvmaze %s: %d records", o.Mode, o.Returned)}
	for i, s := range o.Shows {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • show #%d %s (%s) — %s [%s]", s.ID, s.Name, s.Premiered, s.Network, s.IMDb))
	}
	for i, e := range o.Episodes {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • S%dE%d \"%s\" %s", e.Season, e.Number, e.Name, e.Airdate))
	}
	if e := o.Episode; e != nil {
		hi = append(hi, fmt.Sprintf("  • S%dE%d \"%s\" — %s", e.Season, e.Number, e.Name, e.Airdate))
	}
	for i, p := range o.People {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • person #%d %s (%s) born %s", p.ID, p.Name, p.Country, p.Birthday))
	}
	return hi
}

// (uses tracker_pivot.go's stripHTMLBare)
