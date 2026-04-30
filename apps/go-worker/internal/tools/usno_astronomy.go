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

// USNOAstronomy wraps the US Naval Observatory's free no-auth astronomy
// API. Authoritative ephemeris data — sun + moon rise/set/transit,
// twilight times, moon phase, fraction illuminated.
//
// Why this is unique forensic OSINT:
//   - Photo timestamp/location verification: a "sunset photo" claimed
//     at 6 PM in winter at lat 60°N is implausible (sunset was at 4 PM).
//   - Moon phase verification: a "full moon" claim on a date when the
//     moon was actually waxing crescent is provably false.
//   - Astronomical twilight calculation: was it actually dark enough to
//     stargaze at the claimed time?
//   - Solar position: was the sun at the claimed azimuth/altitude?
//
// Pairs with `openmeteo_search` (cloud cover at the same time — was the
// sun/moon visible?) and `usgs_earthquake_search` for full temporal-
// spatial forensic OSINT.
//
// Two modes:
//
//   - "day_data" : date + lat/lon → sun events (Begin Civil Twilight,
//                   Rise, Upper Transit (solar noon), Set, End Civil
//                   Twilight) + moon events (Rise, Set, Upper Transit)
//                   + current moon phase + fraction illuminated +
//                   closest phase milestone with date/time.
//   - "phases"   : year → all 4 moon-phase dates for that year (New /
//                   First Quarter / Full / Last Quarter, ~48 milestones).

type USNOEvent struct {
	Phenomenon string `json:"phenomenon"` // "Rise", "Set", "Upper Transit", "Begin Civil Twilight", etc.
	Time       string `json:"time"`        // HH:MM in local timezone
}

type USNOPhase struct {
	Phase string `json:"phase"` // "New Moon" | "First Quarter" | "Full Moon" | "Last Quarter"
	Year  int    `json:"year"`
	Month int    `json:"month"`
	Day   int    `json:"day"`
	Time  string `json:"time,omitempty"`
}

type USNODayData struct {
	Date         string      `json:"date"`
	Latitude     float64     `json:"latitude"`
	Longitude    float64     `json:"longitude"`
	TZOffsetH    int         `json:"timezone_offset_hours"`
	DayOfWeek    string      `json:"day_of_week,omitempty"`
	SunEvents    []USNOEvent `json:"sun_events,omitempty"`
	MoonEvents   []USNOEvent `json:"moon_events,omitempty"`
	CurrentPhase string      `json:"current_phase,omitempty"`         // e.g. "Waxing Gibbous", "First Quarter"
	FracIllum    string      `json:"fraction_illuminated,omitempty"`  // e.g. "50%"
	ClosestPhase *USNOPhase  `json:"closest_phase,omitempty"`
}

type USNOAstronomyOutput struct {
	Mode              string       `json:"mode"`
	Query             string       `json:"query,omitempty"`
	DayData           *USNODayData `json:"day_data,omitempty"`
	Phases            []USNOPhase  `json:"phases,omitempty"`

	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
	Note              string       `json:"note,omitempty"`
}

func USNOAstronomy(ctx context.Context, input map[string]any) (*USNOAstronomyOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["year"]; ok {
			mode = "phases"
		} else {
			mode = "day_data"
		}
	}

	out := &USNOAstronomyOutput{
		Mode:   mode,
		Source: "aa.usno.navy.mil/api",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "day_data":
		date, _ := input["date"].(string)
		date = strings.TrimSpace(date)
		if date == "" {
			date = time.Now().UTC().Format("2006-01-02")
		}
		lat := getCensusFloat(input, "latitude", "lat")
		lon := getCensusFloat(input, "longitude", "lon", "lng")
		if lat == 0 && lon == 0 {
			return nil, fmt.Errorf("input.latitude (or lat) and input.longitude (or lon) required for day_data mode")
		}
		tzOffset := 0
		if v, ok := input["timezone_offset_hours"].(float64); ok {
			tzOffset = int(v)
		}
		out.Query = fmt.Sprintf("%s at %.4f,%.4f (UTC%+d)", date, lat, lon, tzOffset)

		params := url.Values{}
		params.Set("date", date)
		params.Set("coords", fmt.Sprintf("%g,%g", lat, lon))
		params.Set("tz", fmt.Sprintf("%d", tzOffset))
		body, err := usnoGet(ctx, cli, "https://aa.usno.navy.mil/api/rstt/oneday?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Properties struct {
				Data struct {
					Day        int    `json:"day"`
					Month      int    `json:"month"`
					Year       int    `json:"year"`
					DayOfWeek  string `json:"day_of_week"`
					CurPhase   string `json:"curphase"`
					FracIllum  string `json:"fracillum"`
					Sundata    []struct {
						Phen string `json:"phen"`
						Time string `json:"time"`
					} `json:"sundata"`
					Moondata []struct {
						Phen string `json:"phen"`
						Time string `json:"time"`
					} `json:"moondata"`
					ClosestPhase struct {
						Phase string `json:"phase"`
						Year  int    `json:"year"`
						Month int    `json:"month"`
						Day   int    `json:"day"`
						Time  string `json:"time"`
					} `json:"closestphase"`
				} `json:"data"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("usno day decode: %w", err)
		}
		d := raw.Properties.Data
		dd := &USNODayData{
			Date:         date,
			Latitude:     lat,
			Longitude:    lon,
			TZOffsetH:    tzOffset,
			DayOfWeek:    d.DayOfWeek,
			CurrentPhase: d.CurPhase,
			FracIllum:    d.FracIllum,
		}
		for _, s := range d.Sundata {
			dd.SunEvents = append(dd.SunEvents, USNOEvent{Phenomenon: s.Phen, Time: s.Time})
		}
		for _, m := range d.Moondata {
			dd.MoonEvents = append(dd.MoonEvents, USNOEvent{Phenomenon: m.Phen, Time: m.Time})
		}
		if d.ClosestPhase.Phase != "" {
			dd.ClosestPhase = &USNOPhase{
				Phase: d.ClosestPhase.Phase,
				Year:  d.ClosestPhase.Year,
				Month: d.ClosestPhase.Month,
				Day:   d.ClosestPhase.Day,
				Time:  d.ClosestPhase.Time,
			}
		}
		out.DayData = dd

	case "phases":
		yearAny := input["year"]
		yearStr := fmt.Sprintf("%v", yearAny)
		if yearStr == "" || yearStr == "<nil>" {
			yearStr = fmt.Sprintf("%d", time.Now().UTC().Year())
		}
		out.Query = "moon phases " + yearStr
		body, err := usnoGet(ctx, cli, "https://aa.usno.navy.mil/api/moon/phases/year?year="+yearStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			PhaseData []struct {
				Phase string `json:"phase"`
				Year  int    `json:"year"`
				Month int    `json:"month"`
				Day   int    `json:"day"`
				Time  string `json:"time"`
			} `json:"phasedata"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("usno phases decode: %w", err)
		}
		for _, p := range raw.PhaseData {
			out.Phases = append(out.Phases, USNOPhase{
				Phase: p.Phase,
				Year:  p.Year,
				Month: p.Month,
				Day:   p.Day,
				Time:  p.Time,
			})
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: day_data, phases", mode)
	}

	out.HighlightFindings = buildUSNOHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func usnoGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usno: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("usno HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildUSNOHighlights(o *USNOAstronomyOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "day_data":
		if o.DayData == nil {
			break
		}
		d := o.DayData
		hi = append(hi, fmt.Sprintf("✓ %s (%s) at %.4f,%.4f UTC%+d", d.Date, d.DayOfWeek, d.Latitude, d.Longitude, d.TZOffsetH))
		if d.CurrentPhase != "" {
			frac := ""
			if d.FracIllum != "" {
				frac = " (" + d.FracIllum + " illuminated)"
			}
			hi = append(hi, fmt.Sprintf("  🌙 moon phase: %s%s", d.CurrentPhase, frac))
		}
		if d.ClosestPhase != nil {
			hi = append(hi, fmt.Sprintf("  closest phase: %s on %d-%02d-%02d %s",
				d.ClosestPhase.Phase, d.ClosestPhase.Year, d.ClosestPhase.Month, d.ClosestPhase.Day, d.ClosestPhase.Time))
		}
		if len(d.SunEvents) > 0 {
			parts := make([]string, 0, len(d.SunEvents))
			for _, e := range d.SunEvents {
				parts = append(parts, fmt.Sprintf("%s %s", e.Phenomenon, e.Time))
			}
			hi = append(hi, "  ☀️  sun: "+strings.Join(parts, " · "))
		}
		if len(d.MoonEvents) > 0 {
			parts := make([]string, 0, len(d.MoonEvents))
			for _, e := range d.MoonEvents {
				parts = append(parts, fmt.Sprintf("%s %s", e.Phenomenon, e.Time))
			}
			hi = append(hi, "  🌙 moon: "+strings.Join(parts, " · "))
		}

	case "phases":
		hi = append(hi, fmt.Sprintf("✓ %d moon phases for %s", len(o.Phases), o.Query))
		display := o.Phases
		if len(display) > 16 {
			display = display[:16]
		}
		for _, p := range display {
			marker := ""
			switch p.Phase {
			case "New Moon":
				marker = "🌑"
			case "First Quarter":
				marker = "🌓"
			case "Full Moon":
				marker = "🌕"
			case "Last Quarter":
				marker = "🌗"
			}
			hi = append(hi, fmt.Sprintf("  %s %s — %d-%02d-%02d %s", marker, p.Phase, p.Year, p.Month, p.Day, p.Time))
		}
		if len(o.Phases) > 16 {
			hi = append(hi, fmt.Sprintf("  …and %d more", len(o.Phases)-16))
		}
	}
	return hi
}
