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

// TMDBLookup wraps The Movie Database (TMDB) v3 API. Free tier with API key.
//
// TMDB is the canonical source for movie/TV episode-level metadata: titles,
// directors, writers, air dates, cast, crew, runtime, episode order, season
// structure. Critical for any film/TV-credit OSINT chain.
//
// Modes:
//   - "search_movie" / "search_tv" / "search_person" : keyword search
//   - "movie_details" / "tv_details" : full record by TMDB ID
//   - "tv_season_details" : full season with episode list
//   - "tv_episode_details" : episode with credits (director/writer/guests)
//   - "person_details" : person record (bio, birthplace, dates)
//   - "movie_credits" / "tv_credits" : full cast + crew for a title
//   - "person_credits" : filmography for a person
//
// Knowledge-graph integration: every record emits a typed entity envelope
// (kind: "movie" | "tv_show" | "tv_season" | "tv_episode" | "person") with
// stable identifiers, suitable for direct ingest by panel_entity_resolution
// and entity_link_finder.

type TMDBSearchMovie struct {
	ID            int     `json:"tmdb_id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title,omitempty"`
	ReleaseDate   string  `json:"release_date,omitempty"`
	Overview      string  `json:"overview,omitempty"`
	VoteAverage   float64 `json:"vote_average,omitempty"`
	VoteCount     int     `json:"vote_count,omitempty"`
	Popularity    float64 `json:"popularity,omitempty"`
	OriginalLang  string  `json:"original_language,omitempty"`
	IMDbID        string  `json:"imdb_id,omitempty"`
}

type TMDBSearchTV struct {
	ID            int      `json:"tmdb_id"`
	Name          string   `json:"name"`
	OriginalName  string   `json:"original_name,omitempty"`
	FirstAirDate  string   `json:"first_air_date,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	VoteAverage   float64  `json:"vote_average,omitempty"`
	OriginCountry []string `json:"origin_country,omitempty"`
	OriginalLang  string   `json:"original_language,omitempty"`
}

type TMDBSearchPerson struct {
	ID           int     `json:"tmdb_id"`
	Name         string  `json:"name"`
	KnownForDept string  `json:"known_for_department,omitempty"`
	Popularity   float64 `json:"popularity,omitempty"`
	Gender       int     `json:"gender,omitempty"` // 0=unknown 1=female 2=male 3=nonbinary
	IMDbID       string  `json:"imdb_id,omitempty"`
}

type TMDBCrew struct {
	ID         int    `json:"tmdb_id"`
	Name       string `json:"name"`
	Job        string `json:"job,omitempty"`
	Department string `json:"department,omitempty"`
}

type TMDBCast struct {
	ID        int    `json:"tmdb_id"`
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	Order     int    `json:"order,omitempty"`
}

type TMDBEpisode struct {
	ID            int        `json:"tmdb_id"`
	Name          string     `json:"name"`
	SeasonNumber  int        `json:"season_number"`
	EpisodeNumber int        `json:"episode_number"`
	AirDate       string     `json:"air_date,omitempty"`
	Overview      string     `json:"overview,omitempty"`
	Runtime       int        `json:"runtime,omitempty"`
	VoteAverage   float64    `json:"vote_average,omitempty"`
	Crew          []TMDBCrew `json:"crew,omitempty"`
	GuestStars    []TMDBCast `json:"guest_stars,omitempty"`
}

// TMDBEntity is the knowledge-graph envelope used by ER.
type TMDBEntity struct {
	Kind        string         `json:"kind"` // movie | tv_show | tv_season | tv_episode | person
	TMDBID      int            `json:"tmdb_id"`
	IMDbID      string         `json:"imdb_id,omitempty"`
	Title       string         `json:"title,omitempty"` // movie/tv display title
	Name        string         `json:"name,omitempty"`  // person name
	Date        string         `json:"date,omitempty"`  // release/air/birth as relevant
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type TMDBLookupOutput struct {
	Mode              string             `json:"mode"`
	Query             string             `json:"query,omitempty"`
	Returned          int                `json:"returned"`
	Movies            []TMDBSearchMovie  `json:"movies,omitempty"`
	TVShows           []TMDBSearchTV     `json:"tv_shows,omitempty"`
	People            []TMDBSearchPerson `json:"people,omitempty"`
	Episode           *TMDBEpisode       `json:"episode,omitempty"`
	Episodes          []TMDBEpisode      `json:"episodes,omitempty"`
	Cast              []TMDBCast         `json:"cast,omitempty"`
	Crew              []TMDBCrew         `json:"crew,omitempty"`
	Detail            map[string]any     `json:"detail,omitempty"`
	Entities          []TMDBEntity       `json:"entities"` // ER envelope (always populated)
	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
}

const tmdbBase = "https://api.themoviedb.org/3"

func TMDBLookup(ctx context.Context, input map[string]any) (*TMDBLookupOutput, error) {
	apiKey := os.Getenv("TMDB_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("TMDB_API_KEY not set; set it in the environment to use tmdb_lookup")
	}

	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// auto-detect
		switch {
		case input["episode_number"] != nil && input["season_number"] != nil && input["tv_id"] != nil:
			mode = "tv_episode_details"
		case input["season_number"] != nil && input["tv_id"] != nil:
			mode = "tv_season_details"
		case input["movie_id"] != nil:
			mode = "movie_details"
		case input["tv_id"] != nil:
			mode = "tv_details"
		case input["person_id"] != nil:
			mode = "person_details"
		default:
			mode = "search_multi"
		}
	}

	out := &TMDBLookupOutput{
		Mode:   mode,
		Source: "api.themoviedb.org/3",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(path string, params url.Values) (map[string]any, error) {
		params.Set("api_key", apiKey)
		u := tmdbBase + path + "?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("tmdb: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("tmdb: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("tmdb HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 300))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("tmdb decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "search_movie", "search_tv", "search_person", "search_multi":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required for %s", mode)
		}
		out.Query = q
		params := url.Values{}
		params.Set("query", q)
		if year, ok := input["year"].(float64); ok && year > 0 {
			if mode == "search_movie" {
				params.Set("year", fmt.Sprintf("%d", int(year)))
			} else if mode == "search_tv" {
				params.Set("first_air_date_year", fmt.Sprintf("%d", int(year)))
			}
		}
		path := "/search/multi"
		if mode == "search_movie" {
			path = "/search/movie"
		} else if mode == "search_tv" {
			path = "/search/tv"
		} else if mode == "search_person" {
			path = "/search/person"
		}
		m, err := get(path, params)
		if err != nil {
			return nil, err
		}
		if results, ok := m["results"].([]any); ok {
			for _, r := range results {
				rec, _ := r.(map[string]any)
				if rec == nil {
					continue
				}
				switch gtString(rec, "media_type") {
				case "movie":
					out.Movies = append(out.Movies, parseTMDBMovie(rec))
				case "tv":
					out.TVShows = append(out.TVShows, parseTMDBTV(rec))
				case "person":
					out.People = append(out.People, parseTMDBPerson(rec))
				default:
					switch mode {
					case "search_movie":
						out.Movies = append(out.Movies, parseTMDBMovie(rec))
					case "search_tv":
						out.TVShows = append(out.TVShows, parseTMDBTV(rec))
					case "search_person":
						out.People = append(out.People, parseTMDBPerson(rec))
					}
				}
			}
		}

	case "movie_details":
		id := tmdbIntID(input, "movie_id")
		if id == 0 {
			return nil, fmt.Errorf("input.movie_id required")
		}
		params := url.Values{}
		params.Set("append_to_response", "credits,external_ids")
		m, err := get(fmt.Sprintf("/movie/%d", id), params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if credits, ok := m["credits"].(map[string]any); ok {
			if cast, ok := credits["cast"].([]any); ok {
				out.Cast = parseTMDBCast(cast)
			}
			if crew, ok := credits["crew"].([]any); ok {
				out.Crew = parseTMDBCrew(crew)
			}
		}

	case "tv_details":
		id := tmdbIntID(input, "tv_id")
		if id == 0 {
			return nil, fmt.Errorf("input.tv_id required")
		}
		params := url.Values{}
		params.Set("append_to_response", "external_ids")
		m, err := get(fmt.Sprintf("/tv/%d", id), params)
		if err != nil {
			return nil, err
		}
		out.Detail = m

	case "tv_season_details":
		tvID := tmdbIntID(input, "tv_id")
		seasonNum := tmdbIntID(input, "season_number")
		if tvID == 0 {
			return nil, fmt.Errorf("input.tv_id required")
		}
		params := url.Values{}
		m, err := get(fmt.Sprintf("/tv/%d/season/%d", tvID, seasonNum), params)
		if err != nil {
			return nil, err
		}
		if eps, ok := m["episodes"].([]any); ok {
			for _, e := range eps {
				rec, _ := e.(map[string]any)
				if rec == nil {
					continue
				}
				ep := parseTMDBEpisode(rec)
				out.Episodes = append(out.Episodes, ep)
			}
		}

	case "tv_episode_details":
		tvID := tmdbIntID(input, "tv_id")
		seasonNum := tmdbIntID(input, "season_number")
		epNum := tmdbIntID(input, "episode_number")
		if tvID == 0 {
			return nil, fmt.Errorf("input.tv_id required")
		}
		params := url.Values{}
		params.Set("append_to_response", "credits,external_ids")
		m, err := get(fmt.Sprintf("/tv/%d/season/%d/episode/%d", tvID, seasonNum, epNum), params)
		if err != nil {
			return nil, err
		}
		ep := parseTMDBEpisode(m)
		// credits subobject if requested
		if credits, ok := m["credits"].(map[string]any); ok {
			if crew, ok := credits["crew"].([]any); ok {
				ep.Crew = parseTMDBCrew(crew)
			}
			if guests, ok := credits["guest_stars"].([]any); ok {
				ep.GuestStars = parseTMDBCast(guests)
			}
		}
		out.Episode = &ep

	case "person_details":
		id := tmdbIntID(input, "person_id")
		if id == 0 {
			return nil, fmt.Errorf("input.person_id required")
		}
		params := url.Values{}
		params.Set("append_to_response", "external_ids")
		m, err := get(fmt.Sprintf("/person/%d", id), params)
		if err != nil {
			return nil, err
		}
		out.Detail = m

	case "movie_credits":
		id := tmdbIntID(input, "movie_id")
		if id == 0 {
			return nil, fmt.Errorf("input.movie_id required")
		}
		m, err := get(fmt.Sprintf("/movie/%d/credits", id), url.Values{})
		if err != nil {
			return nil, err
		}
		if cast, ok := m["cast"].([]any); ok {
			out.Cast = parseTMDBCast(cast)
		}
		if crew, ok := m["crew"].([]any); ok {
			out.Crew = parseTMDBCrew(crew)
		}

	case "tv_credits":
		id := tmdbIntID(input, "tv_id")
		if id == 0 {
			return nil, fmt.Errorf("input.tv_id required")
		}
		m, err := get(fmt.Sprintf("/tv/%d/aggregate_credits", id), url.Values{})
		if err != nil {
			return nil, err
		}
		if cast, ok := m["cast"].([]any); ok {
			out.Cast = parseTMDBCast(cast)
		}
		if crew, ok := m["crew"].([]any); ok {
			out.Crew = parseTMDBCrew(crew)
		}

	case "person_credits":
		id := tmdbIntID(input, "person_id")
		if id == 0 {
			return nil, fmt.Errorf("input.person_id required")
		}
		m, err := get(fmt.Sprintf("/person/%d/combined_credits", id), url.Values{})
		if err != nil {
			return nil, err
		}
		out.Detail = m

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search_movie, search_tv, search_person, search_multi, movie_details, tv_details, tv_season_details, tv_episode_details, person_details, movie_credits, tv_credits, person_credits", mode)
	}

	out.Returned = len(out.Movies) + len(out.TVShows) + len(out.People) + len(out.Episodes)
	if out.Episode != nil {
		out.Returned++
	}
	if out.Detail != nil {
		out.Returned++
	}
	out.Entities = tmdbBuildEntities(out)
	out.HighlightFindings = tmdbBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func tmdbIntID(input map[string]any, key string) int {
	v, ok := input[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		var n int
		_, _ = fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

func parseTMDBMovie(rec map[string]any) TMDBSearchMovie {
	m := TMDBSearchMovie{
		ID:            int(gtFloat(rec, "id")),
		Title:         gtString(rec, "title"),
		OriginalTitle: gtString(rec, "original_title"),
		ReleaseDate:   gtString(rec, "release_date"),
		Overview:      gtString(rec, "overview"),
		VoteAverage:   gtFloat(rec, "vote_average"),
		VoteCount:     int(gtFloat(rec, "vote_count")),
		Popularity:    gtFloat(rec, "popularity"),
		OriginalLang:  gtString(rec, "original_language"),
	}
	if ext, ok := rec["external_ids"].(map[string]any); ok {
		m.IMDbID = gtString(ext, "imdb_id")
	}
	return m
}

func parseTMDBTV(rec map[string]any) TMDBSearchTV {
	t := TMDBSearchTV{
		ID:           int(gtFloat(rec, "id")),
		Name:         gtString(rec, "name"),
		OriginalName: gtString(rec, "original_name"),
		FirstAirDate: gtString(rec, "first_air_date"),
		Overview:     gtString(rec, "overview"),
		VoteAverage:  gtFloat(rec, "vote_average"),
		OriginalLang: gtString(rec, "original_language"),
	}
	if oc, ok := rec["origin_country"].([]any); ok {
		for _, x := range oc {
			if s, ok := x.(string); ok {
				t.OriginCountry = append(t.OriginCountry, s)
			}
		}
	}
	return t
}

func parseTMDBPerson(rec map[string]any) TMDBSearchPerson {
	p := TMDBSearchPerson{
		ID:           int(gtFloat(rec, "id")),
		Name:         gtString(rec, "name"),
		KnownForDept: gtString(rec, "known_for_department"),
		Popularity:   gtFloat(rec, "popularity"),
		Gender:       int(gtFloat(rec, "gender")),
	}
	if ext, ok := rec["external_ids"].(map[string]any); ok {
		p.IMDbID = gtString(ext, "imdb_id")
	}
	return p
}

func parseTMDBCast(items []any) []TMDBCast {
	out := make([]TMDBCast, 0, len(items))
	for _, x := range items {
		rec, _ := x.(map[string]any)
		if rec == nil {
			continue
		}
		out = append(out, TMDBCast{
			ID:        int(gtFloat(rec, "id")),
			Name:      gtString(rec, "name"),
			Character: gtString(rec, "character"),
			Order:     int(gtFloat(rec, "order")),
		})
	}
	return out
}

func parseTMDBCrew(items []any) []TMDBCrew {
	out := make([]TMDBCrew, 0, len(items))
	for _, x := range items {
		rec, _ := x.(map[string]any)
		if rec == nil {
			continue
		}
		out = append(out, TMDBCrew{
			ID:         int(gtFloat(rec, "id")),
			Name:       gtString(rec, "name"),
			Job:        gtString(rec, "job"),
			Department: gtString(rec, "department"),
		})
	}
	return out
}

func parseTMDBEpisode(rec map[string]any) TMDBEpisode {
	return TMDBEpisode{
		ID:            int(gtFloat(rec, "id")),
		Name:          gtString(rec, "name"),
		SeasonNumber:  int(gtFloat(rec, "season_number")),
		EpisodeNumber: int(gtFloat(rec, "episode_number")),
		AirDate:       gtString(rec, "air_date"),
		Overview:      gtString(rec, "overview"),
		Runtime:       int(gtFloat(rec, "runtime")),
		VoteAverage:   gtFloat(rec, "vote_average"),
	}
}

// tmdbBuildEntities flattens output into the typed envelope used by the
// connecting-the-dots ER engine. Every emitted entity carries a stable
// (kind, tmdb_id) identifier; person IDs cross-reference IMDb when present.
func tmdbBuildEntities(o *TMDBLookupOutput) []TMDBEntity {
	ents := []TMDBEntity{}
	for _, m := range o.Movies {
		ents = append(ents, TMDBEntity{
			Kind: "movie", TMDBID: m.ID, IMDbID: m.IMDbID,
			Title: m.Title, Date: m.ReleaseDate, Description: m.Overview,
			Attributes: map[string]any{"original_title": m.OriginalTitle, "language": m.OriginalLang, "vote_avg": m.VoteAverage},
		})
	}
	for _, t := range o.TVShows {
		ents = append(ents, TMDBEntity{
			Kind: "tv_show", TMDBID: t.ID, Title: t.Name, Date: t.FirstAirDate, Description: t.Overview,
			Attributes: map[string]any{"original_name": t.OriginalName, "origin_country": t.OriginCountry, "language": t.OriginalLang},
		})
	}
	for _, p := range o.People {
		ents = append(ents, TMDBEntity{
			Kind: "person", TMDBID: p.ID, IMDbID: p.IMDbID, Name: p.Name,
			Attributes: map[string]any{"known_for": p.KnownForDept, "popularity": p.Popularity},
		})
	}
	for _, ep := range o.Episodes {
		ents = append(ents, TMDBEntity{
			Kind: "tv_episode", TMDBID: ep.ID, Title: ep.Name, Date: ep.AirDate, Description: ep.Overview,
			Attributes: map[string]any{"season": ep.SeasonNumber, "episode": ep.EpisodeNumber, "runtime": ep.Runtime},
		})
	}
	if o.Episode != nil {
		ep := *o.Episode
		ent := TMDBEntity{
			Kind: "tv_episode", TMDBID: ep.ID, Title: ep.Name, Date: ep.AirDate, Description: ep.Overview,
			Attributes: map[string]any{"season": ep.SeasonNumber, "episode": ep.EpisodeNumber, "runtime": ep.Runtime},
		}
		// embed crew/guest names so ER can link episode→person
		credits := []map[string]any{}
		for _, c := range ep.Crew {
			credits = append(credits, map[string]any{"role": "crew", "name": c.Name, "tmdb_id": c.ID, "job": c.Job, "department": c.Department})
		}
		for _, g := range ep.GuestStars {
			credits = append(credits, map[string]any{"role": "guest", "name": g.Name, "tmdb_id": g.ID, "character": g.Character})
		}
		if len(credits) > 0 {
			ent.Attributes["credits"] = credits
		}
		ents = append(ents, ent)
	}
	if d := o.Detail; d != nil {
		// movie/tv/person detail emits one canonical entity
		switch o.Mode {
		case "movie_details":
			ents = append(ents, TMDBEntity{
				Kind: "movie", TMDBID: int(gtFloat(d, "id")),
				IMDbID: gtString(d, "imdb_id"), Title: gtString(d, "title"),
				Date: gtString(d, "release_date"), Description: gtString(d, "overview"),
				Attributes: map[string]any{
					"runtime":       gtFloat(d, "runtime"),
					"original_lang": gtString(d, "original_language"),
					"budget":        gtFloat(d, "budget"),
					"revenue":       gtFloat(d, "revenue"),
				},
			})
		case "tv_details":
			ents = append(ents, TMDBEntity{
				Kind: "tv_show", TMDBID: int(gtFloat(d, "id")), Title: gtString(d, "name"),
				Date: gtString(d, "first_air_date"), Description: gtString(d, "overview"),
				Attributes: map[string]any{
					"number_of_seasons":  gtFloat(d, "number_of_seasons"),
					"number_of_episodes": gtFloat(d, "number_of_episodes"),
					"status":             gtString(d, "status"),
				},
			})
		case "person_details":
			ents = append(ents, TMDBEntity{
				Kind: "person", TMDBID: int(gtFloat(d, "id")), Name: gtString(d, "name"),
				Date: gtString(d, "birthday"),
				Attributes: map[string]any{
					"place_of_birth":    gtString(d, "place_of_birth"),
					"deathday":          gtString(d, "deathday"),
					"known_for_dept":    gtString(d, "known_for_department"),
					"biography_excerpt": tmdbExcerpt(gtString(d, "biography"), 400),
				},
			})
		}
	}
	for _, c := range o.Cast {
		ents = append(ents, TMDBEntity{
			Kind: "person", TMDBID: c.ID, Name: c.Name,
			Attributes: map[string]any{"role": "cast", "character": c.Character, "order": c.Order},
		})
	}
	for _, c := range o.Crew {
		ents = append(ents, TMDBEntity{
			Kind: "person", TMDBID: c.ID, Name: c.Name,
			Attributes: map[string]any{"role": "crew", "job": c.Job, "department": c.Department},
		})
	}
	// Dedup: when the same person appears in both Cast and Crew (e.g. an
	// actor-director on the same title), merge into a single entity so
	// downstream ER doesn't see two distinct people. See
	// TestDedupeTMDBEntities_QuantitativeImprovement.
	return dedupeTMDBEntitiesByKindID(ents)
}

// dedupeTMDBEntitiesByKindID collapses entries with the same
// (Kind, TMDBID|IMDbID) key. When duplicates collide, attributes are
// merged: scalar conflicts become a slice of distinct values (so a
// single merged person carries both role="cast" + role="crew"), and
// missing fields are filled in from the duplicate.
//
// Entities with no usable identifier (Kind=="" OR both TMDBID==0 AND
// IMDbID=="") are passed through unchanged — never deduped — so we
// don't accidentally collapse entities that just happen to lack IDs.
func dedupeTMDBEntitiesByKindID(ents []TMDBEntity) []TMDBEntity {
	seen := map[string]int{}
	out := make([]TMDBEntity, 0, len(ents))
	for _, e := range ents {
		if e.Kind == "" || (e.TMDBID == 0 && e.IMDbID == "") {
			out = append(out, e)
			continue
		}
		key := fmt.Sprintf("%s:%d:%s", e.Kind, e.TMDBID, e.IMDbID)
		if idx, ok := seen[key]; ok {
			out[idx] = mergeTMDBEntity(out[idx], e)
			continue
		}
		seen[key] = len(out)
		out = append(out, e)
	}
	return out
}

func mergeTMDBEntity(a, b TMDBEntity) TMDBEntity {
	if a.Title == "" {
		a.Title = b.Title
	}
	if a.Name == "" {
		a.Name = b.Name
	}
	if a.IMDbID == "" {
		a.IMDbID = b.IMDbID
	}
	if a.Date == "" {
		a.Date = b.Date
	}
	if a.Description == "" {
		a.Description = b.Description
	}
	if a.Attributes == nil {
		a.Attributes = map[string]any{}
	}
	for k, v := range b.Attributes {
		existing, ok := a.Attributes[k]
		if !ok {
			a.Attributes[k] = v
			continue
		}
		if attrEqual(existing, v) {
			continue
		}
		// scalar conflict → coerce to slice of distinct values
		if existingSlice, ok := existing.([]any); ok {
			present := false
			for _, x := range existingSlice {
				if attrEqual(x, v) {
					present = true
					break
				}
			}
			if !present {
				a.Attributes[k] = append(existingSlice, v)
			}
		} else {
			a.Attributes[k] = []any{existing, v}
		}
	}
	return a
}

func attrEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func tmdbBuildHighlights(o *TMDBLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ tmdb %s: %d records", o.Mode, o.Returned)}
	limit := 5
	for i, m := range o.Movies {
		if i >= limit {
			break
		}
		hi = append(hi, fmt.Sprintf("  • movie #%d %s (%s) — %s", m.ID, m.Title, m.ReleaseDate, m.OriginalLang))
	}
	for i, t := range o.TVShows {
		if i >= limit {
			break
		}
		hi = append(hi, fmt.Sprintf("  • tv #%d %s (%s) — %s", t.ID, t.Name, t.FirstAirDate, strings.Join(t.OriginCountry, ",")))
	}
	for i, p := range o.People {
		if i >= limit {
			break
		}
		hi = append(hi, fmt.Sprintf("  • person #%d %s — %s", p.ID, p.Name, p.KnownForDept))
	}
	for i, ep := range o.Episodes {
		if i >= limit {
			break
		}
		hi = append(hi, fmt.Sprintf("  • S%dE%d \"%s\" — %s", ep.SeasonNumber, ep.EpisodeNumber, ep.Name, ep.AirDate))
	}
	if ep := o.Episode; ep != nil {
		hi = append(hi, fmt.Sprintf("  • S%dE%d \"%s\" aired %s (runtime %dm)", ep.SeasonNumber, ep.EpisodeNumber, ep.Name, ep.AirDate, ep.Runtime))
		for i, c := range ep.Crew {
			if i >= 6 {
				break
			}
			hi = append(hi, fmt.Sprintf("      crew: %s — %s (%s)", c.Name, c.Job, c.Department))
		}
	}
	if d := o.Detail; d != nil {
		switch o.Mode {
		case "movie_details":
			hi = append(hi, fmt.Sprintf("  • %s (%s) runtime %.0fm",
				gtString(d, "title"), gtString(d, "release_date"), gtFloat(d, "runtime")))
		case "tv_details":
			hi = append(hi, fmt.Sprintf("  • %s (first air %s) — %g seasons / %g episodes",
				gtString(d, "name"), gtString(d, "first_air_date"),
				gtFloat(d, "number_of_seasons"), gtFloat(d, "number_of_episodes")))
		case "person_details":
			hi = append(hi, fmt.Sprintf("  • %s born %s in %s; dept %s",
				gtString(d, "name"), gtString(d, "birthday"),
				gtString(d, "place_of_birth"), gtString(d, "known_for_department")))
		}
	}
	return hi
}

func tmdbExcerpt(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
