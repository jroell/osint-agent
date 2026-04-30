package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// MusicBrainzSearch wraps the MusicBrainz public API — the open music
// metadata database (operated by MetaBrainz Foundation). Free, no auth,
// rate-limited to 1 req/sec for anonymous clients.
//
// 2M+ artists, 30M+ recordings, 5M+ releases, with extensive cross-
// platform linking (AllMusic, Bandcamp, Spotify, Apple Music, Discogs,
// Last.fm, Wikipedia, official social media, etc.) and authoritative
// identifiers (ISNI, IPI, IPN — international name registries).
//
// Why this matters for music ER: MusicBrainz IDs (MBIDs) are the canonical
// cross-reference key for music — the "Wikidata of music". An MBID gives
// you a single anchor that's referenced by Spotify, Apple Music,
// MusicBrainz Picard tags, and many academic music corpora.
//
// Three modes:
//   - "search_artists"   : fuzzy artist name → artists with MBID +
//                           country + type + life-span + ISNI/IPI codes
//   - "artist_detail"    : by MBID → full record with aliases (search
//                           hints, transliterations) + URL relations
//                           (cross-platform pivots: AllMusic, Bandcamp,
//                           Spotify, Apple Music, Discogs, Last.fm,
//                           Wikipedia, official site, blog, etc.)
//   - "search_recordings": title + optional artist filter → recordings
//                           with ISRCs and release placements

type MBArtist struct {
	ID            string   `json:"id"`        // MBID
	Name          string   `json:"name"`
	SortName      string   `json:"sort_name,omitempty"`
	Type          string   `json:"type,omitempty"` // Person | Group | Orchestra | Choir | Character | Other
	Gender        string   `json:"gender,omitempty"`
	Country       string   `json:"country,omitempty"`
	Area          string   `json:"area,omitempty"`
	BeginArea     string   `json:"begin_area,omitempty"`
	BeginDate     string   `json:"begin_date,omitempty"`
	EndDate       string   `json:"end_date,omitempty"`
	Ended         bool     `json:"ended,omitempty"`
	ISNIs         []string `json:"isnis,omitempty"`         // International Standard Name Identifier
	IPIs          []string `json:"ipis,omitempty"`          // Interested Parties Information
	Disambig      string   `json:"disambiguation,omitempty"`
	Score         int      `json:"score,omitempty"`
	URL           string   `json:"url,omitempty"`
}

type MBAlias struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Locale    string `json:"locale,omitempty"`
	Primary   bool   `json:"primary,omitempty"`
	BeginDate string `json:"begin_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

type MBRelation struct {
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	Target string `json:"target,omitempty"`
}

type MBArtistDetail struct {
	MBArtist
	Aliases   []MBAlias    `json:"aliases,omitempty"`
	Relations []MBRelation `json:"url_relations,omitempty"`
}

type MBRecording struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	LengthMs int      `json:"length_ms,omitempty"`
	ISRCs    []string `json:"isrcs,omitempty"`
	Disambig string   `json:"disambiguation,omitempty"`
	Score    int      `json:"score,omitempty"`
	Artists  []string `json:"artists,omitempty"`
	Releases []string `json:"releases,omitempty"`
	URL      string   `json:"url,omitempty"`
}

type MusicBrainzSearchOutput struct {
	Mode              string           `json:"mode"`
	Query             string           `json:"query,omitempty"`
	TotalCount        int              `json:"total_count,omitempty"`
	Returned          int              `json:"returned"`
	Artists           []MBArtist       `json:"artists,omitempty"`
	ArtistDetail      *MBArtistDetail  `json:"artist_detail,omitempty"`
	Recordings        []MBRecording    `json:"recordings,omitempty"`

	// Aggregations / surfaced cross-platform links
	CrossPlatformURLs map[string]string `json:"cross_platform_urls,omitempty"`

	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
	Note              string           `json:"note,omitempty"`
}

func MusicBrainzSearch(ctx context.Context, input map[string]any) (*MusicBrainzSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["mbid"]; ok {
			mode = "artist_detail"
		} else if _, ok := input["recording"]; ok {
			mode = "search_recordings"
		} else {
			mode = "search_artists"
		}
	}

	out := &MusicBrainzSearchOutput{
		Mode:   mode,
		Source: "musicbrainz.org/ws/2",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "search_artists":
		artist, _ := input["artist"].(string)
		artist = strings.TrimSpace(artist)
		if artist == "" {
			artist, _ = input["query"].(string)
			artist = strings.TrimSpace(artist)
		}
		if artist == "" {
			return nil, fmt.Errorf("input.artist or input.query required")
		}
		out.Query = artist
		params := url.Values{}
		params.Set("query", "artist:"+artist)
		limit := 5
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 25 {
			limit = int(l)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("fmt", "json")
		body, err := mbGet(ctx, cli, "https://musicbrainz.org/ws/2/artist?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Count   int                `json:"count"`
			Artists []map[string]any   `json:"artists"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("musicbrainz decode: %w", err)
		}
		out.TotalCount = raw.Count
		for _, a := range raw.Artists {
			ar := convertMBArtist(a)
			out.Artists = append(out.Artists, ar)
		}
		out.Returned = len(out.Artists)

	case "artist_detail":
		mbid, _ := input["mbid"].(string)
		mbid = strings.TrimSpace(mbid)
		if mbid == "" {
			return nil, fmt.Errorf("input.mbid required for artist_detail")
		}
		out.Query = mbid
		incs := "aliases+url-rels"
		params := url.Values{}
		params.Set("inc", incs)
		params.Set("fmt", "json")
		body, err := mbGet(ctx, cli, "https://musicbrainz.org/ws/2/artist/"+url.PathEscape(mbid)+"?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("artist detail decode: %w", err)
		}
		ar := convertMBArtist(raw)
		detail := &MBArtistDetail{MBArtist: ar}
		// Aliases
		if aliases, ok := raw["aliases"].([]any); ok {
			for _, ai := range aliases {
				am, ok := ai.(map[string]any)
				if !ok {
					continue
				}
				detail.Aliases = append(detail.Aliases, MBAlias{
					Name:      gtString(am, "name"),
					Type:      gtString(am, "type"),
					Locale:    gtString(am, "locale"),
					Primary:   gtBool(am, "primary"),
					BeginDate: gtString(am, "begin"),
					EndDate:   gtString(am, "end"),
				})
			}
		}
		// URL relations + aggregate cross-platform map
		out.CrossPlatformURLs = map[string]string{}
		if rels, ok := raw["relations"].([]any); ok {
			for _, ri := range rels {
				rm, ok := ri.(map[string]any)
				if !ok {
					continue
				}
				typ := gtString(rm, "type")
				var u string
				if urlObj, ok := rm["url"].(map[string]any); ok {
					u = gtString(urlObj, "resource")
				}
				detail.Relations = append(detail.Relations, MBRelation{
					Type: typ,
					URL:  u,
				})
				// Surface key cross-platform URLs (one per type — use first)
				if u != "" && typ != "" {
					if _, exists := out.CrossPlatformURLs[typ]; !exists {
						out.CrossPlatformURLs[typ] = u
					}
				}
			}
		}
		out.ArtistDetail = detail
		out.Returned = 1

	case "search_recordings":
		recording, _ := input["recording"].(string)
		artist, _ := input["artist"].(string)
		recording = strings.TrimSpace(recording)
		if recording == "" {
			return nil, fmt.Errorf("input.recording required")
		}
		out.Query = recording
		var q string
		if artist != "" {
			q = fmt.Sprintf("recording:%q AND artist:%q", recording, artist)
			out.Query = recording + " by " + artist
		} else {
			q = fmt.Sprintf("recording:%q", recording)
		}
		params := url.Values{}
		params.Set("query", q)
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 25 {
			limit = int(l)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("fmt", "json")
		body, err := mbGet(ctx, cli, "https://musicbrainz.org/ws/2/recording?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Count      int              `json:"count"`
			Recordings []map[string]any `json:"recordings"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("recording decode: %w", err)
		}
		out.TotalCount = raw.Count
		for _, r := range raw.Recordings {
			rec := MBRecording{
				ID:       gtString(r, "id"),
				Title:    gtString(r, "title"),
				LengthMs: gtInt(r, "length"),
				Disambig: gtString(r, "disambiguation"),
				Score:    gtInt(r, "score"),
			}
			if isrcs, ok := r["isrcs"].([]any); ok {
				for _, i := range isrcs {
					if s, ok := i.(string); ok {
						rec.ISRCs = append(rec.ISRCs, s)
					}
				}
			}
			if artists, ok := r["artist-credit"].([]any); ok {
				for _, a := range artists {
					if am, ok := a.(map[string]any); ok {
						if name := gtString(am, "name"); name != "" {
							rec.Artists = append(rec.Artists, name)
						}
					}
				}
			}
			if releases, ok := r["releases"].([]any); ok {
				for i, rel := range releases {
					if i >= 3 {
						break
					}
					if rm, ok := rel.(map[string]any); ok {
						if t := gtString(rm, "title"); t != "" {
							rec.Releases = append(rec.Releases, t)
						}
					}
				}
			}
			if rec.ID != "" {
				rec.URL = "https://musicbrainz.org/recording/" + rec.ID
			}
			out.Recordings = append(out.Recordings, rec)
		}
		out.Returned = len(out.Recordings)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search_artists, artist_detail, search_recordings", mode)
	}

	out.HighlightFindings = buildMBHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func convertMBArtist(a map[string]any) MBArtist {
	ar := MBArtist{
		ID:       gtString(a, "id"),
		Name:     gtString(a, "name"),
		SortName: gtString(a, "sort-name"),
		Type:     gtString(a, "type"),
		Gender:   gtString(a, "gender"),
		Country:  gtString(a, "country"),
		Disambig: gtString(a, "disambiguation"),
		Score:    gtInt(a, "score"),
	}
	if area, ok := a["area"].(map[string]any); ok {
		ar.Area = gtString(area, "name")
	}
	if ba, ok := a["begin-area"].(map[string]any); ok {
		ar.BeginArea = gtString(ba, "name")
	}
	if ls, ok := a["life-span"].(map[string]any); ok {
		ar.BeginDate = gtString(ls, "begin")
		ar.EndDate = gtString(ls, "end")
		ar.Ended = gtBool(ls, "ended")
	}
	if isnis, ok := a["isnis"].([]any); ok {
		for _, i := range isnis {
			if s, ok := i.(string); ok {
				ar.ISNIs = append(ar.ISNIs, s)
			}
		}
	}
	if ipis, ok := a["ipis"].([]any); ok {
		for _, i := range ipis {
			if s, ok := i.(string); ok {
				ar.IPIs = append(ar.IPIs, s)
			}
		}
	}
	if ar.ID != "" {
		ar.URL = "https://musicbrainz.org/artist/" + ar.ID
	}
	return ar
}

func mbGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	// MusicBrainz requires a meaningful User-Agent
	req.Header.Set("User-Agent", "osint-agent/1.0 (https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("musicbrainz HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildMBHighlights(o *MusicBrainzSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search_artists":
		hi = append(hi, fmt.Sprintf("✓ %d artists match '%s' (returning %d)", o.TotalCount, o.Query, o.Returned))
		for i, a := range o.Artists {
			if i >= 5 {
				break
			}
			lifespan := ""
			if a.BeginDate != "" || a.EndDate != "" {
				lifespan = fmt.Sprintf(" (%s – %s)", a.BeginDate, a.EndDate)
			}
			loc := ""
			if a.Country != "" {
				loc = " [" + a.Country + "]"
			}
			isni := ""
			if len(a.ISNIs) > 0 {
				isni = " · ISNI " + a.ISNIs[0]
			}
			hi = append(hi, fmt.Sprintf("  • %s%s [%s]%s%s · MBID %s",
				a.Name, lifespan, a.Type, loc, isni, a.ID))
			if a.Disambig != "" {
				hi = append(hi, "    "+hfTruncate(a.Disambig, 100))
			}
		}

	case "artist_detail":
		if o.ArtistDetail == nil {
			hi = append(hi, "✗ no artist detail")
			break
		}
		d := o.ArtistDetail
		lifespan := ""
		if d.BeginDate != "" || d.EndDate != "" {
			lifespan = fmt.Sprintf(" (%s – %s)", d.BeginDate, d.EndDate)
		}
		hi = append(hi, fmt.Sprintf("✓ %s%s [%s]", d.Name, lifespan, d.Type))
		hi = append(hi, fmt.Sprintf("  area: %s · country: %s", d.Area, d.Country))
		if len(d.ISNIs) > 0 {
			hi = append(hi, "  ISNI: "+strings.Join(d.ISNIs, ", "))
		}
		if len(d.Aliases) > 0 {
			altNames := make([]string, 0, len(d.Aliases))
			for _, a := range d.Aliases {
				if a.Name != "" {
					altNames = append(altNames, a.Name)
				}
				if len(altNames) >= 6 {
					break
				}
			}
			hi = append(hi, "  aliases: "+strings.Join(altNames, " · "))
		}
		if len(o.CrossPlatformURLs) > 0 {
			hi = append(hi, fmt.Sprintf("  🔗 cross-platform IDs (%d):", len(o.CrossPlatformURLs)))
			keys := make([]string, 0, len(o.CrossPlatformURLs))
			for k := range o.CrossPlatformURLs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i >= 12 {
					break
				}
				hi = append(hi, fmt.Sprintf("    %s: %s", k, o.CrossPlatformURLs[k]))
			}
		}

	case "search_recordings":
		hi = append(hi, fmt.Sprintf("✓ %d recordings match '%s' (returning %d)", o.TotalCount, o.Query, o.Returned))
		for i, r := range o.Recordings {
			if i >= 5 {
				break
			}
			artists := strings.Join(r.Artists, ", ")
			isrc := ""
			if len(r.ISRCs) > 0 {
				isrc = " · ISRC " + r.ISRCs[0]
			}
			length := ""
			if r.LengthMs > 0 {
				length = fmt.Sprintf(" · %d:%02d", r.LengthMs/60000, (r.LengthMs/1000)%60)
			}
			hi = append(hi, fmt.Sprintf("  • %s by %s%s%s · MBID %s",
				r.Title, artists, length, isrc, r.ID))
			if len(r.Releases) > 0 {
				hi = append(hi, "    on: "+strings.Join(r.Releases, ", "))
			}
		}
	}
	return hi
}
