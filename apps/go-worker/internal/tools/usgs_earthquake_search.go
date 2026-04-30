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

// USGSEarthquakeSearch wraps USGS's free no-auth earthquake catalog API.
// Real-time + historical seismic events globally, updated every 5 min.
//
// Why this is useful for ER / forensic OSINT:
//   - Alibi corroboration: "User claimed they were rattled by an
//     earthquake at X on date Y" → verify a real M3+ event happened
//     within the geographic + temporal window.
//   - Social media verification: tweets/posts about earthquake felt at
//     specific time → confirm against authoritative seismic data.
//   - Time-correlation: building damage reports / insurance claims /
//     news reports aligned to actual seismic timing.
//   - Insurance fraud: claims for earthquake damage when no real event
//     occurred at the claimed location/time.
//
// Pairs with `nominatim_geocode` + `census_geocoder` for converting
// addresses to lat/lon to bbox-search the catalog.
//
// Two modes:
//
//   - "recent"  : preset feeds for fast lookup. Format: "{mag}_{period}"
//                 where mag = significant | 4.5 | 2.5 | 1.0 | all
//                 and period = day | week | month | hour.
//                 Common choices:
//                   - "2.5_week"        : felt-strength events past week
//                   - "4.5_week"        : meaningful events past week
//                   - "significant_month": damage-causing events past month
//                   - "all_day"         : every detected quake past 24h
//   - "query"   : full custom search by date range + magnitude min/max
//                 + bbox + depth filters via FDSN web service.

type USGSQuake struct {
	ID                string  `json:"id"`
	Magnitude         float64 `json:"magnitude"`
	MagnitudeType     string  `json:"magnitude_type,omitempty"`
	Place             string  `json:"place,omitempty"`
	Time              string  `json:"time,omitempty"`             // ISO8601 UTC
	TimeUnixMs        int64   `json:"time_unix_ms,omitempty"`
	UpdatedTime       string  `json:"updated_time,omitempty"`
	Latitude          float64 `json:"latitude"`
	Longitude         float64 `json:"longitude"`
	DepthKm           float64 `json:"depth_km"`
	Status            string  `json:"status,omitempty"`           // automatic | reviewed
	Type              string  `json:"type,omitempty"`             // earthquake | quarry blast | explosion | other
	Alert             string  `json:"alert,omitempty"`            // green | yellow | orange | red — PAGER alert
	Tsunami           bool    `json:"tsunami,omitempty"`
	Significance      int     `json:"significance,omitempty"`     // 0..1000+
	NumStations       int     `json:"num_stations,omitempty"`
	URL               string  `json:"url,omitempty"`
	DistanceFromQuery string  `json:"distance_from_query,omitempty"`
}

type USGSEarthquakeSearchOutput struct {
	Mode              string      `json:"mode"`
	Query             string      `json:"query,omitempty"`
	TotalCount        int         `json:"total_count"`
	Returned          int         `json:"returned"`
	Earthquakes       []USGSQuake `json:"earthquakes,omitempty"`

	MagnitudeMin      float64     `json:"magnitude_min,omitempty"`
	MagnitudeMax      float64     `json:"magnitude_max,omitempty"`
	TsunamiCount      int         `json:"tsunami_count,omitempty"`
	AlertedCount      int         `json:"alerted_count,omitempty"` // non-green PAGER
	UniqueRegions     []string    `json:"unique_regions,omitempty"`

	HighlightFindings []string    `json:"highlight_findings"`
	Source            string      `json:"source"`
	TookMs            int64       `json:"tookMs"`
	Note              string      `json:"note,omitempty"`
}

func USGSEarthquakeSearch(ctx context.Context, input map[string]any) (*USGSEarthquakeSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["start_date"]; ok {
			mode = "query"
		} else if _, ok := input["latitude"]; ok {
			mode = "query"
		} else {
			mode = "recent"
		}
	}

	out := &USGSEarthquakeSearchOutput{
		Mode:   mode,
		Source: "earthquake.usgs.gov",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "recent":
		feed, _ := input["feed"].(string)
		feed = strings.TrimSpace(feed)
		if feed == "" {
			feed = "2.5_week"
		}
		// Validate feed code: should be {mag}_{period}
		if !strings.Contains(feed, "_") {
			return nil, fmt.Errorf("input.feed must be {mag}_{period} (e.g. '2.5_week', 'significant_day', 'all_hour')")
		}
		out.Query = "feed=" + feed
		urlStr := "https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/" + feed + ".geojson"
		body, err := usgsGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		if err := decodeUSGSGeoJSON(body, out); err != nil {
			return nil, err
		}

	case "query":
		params := url.Values{}
		params.Set("format", "geojson")
		queryParts := []string{}

		if v, ok := input["start_date"].(string); ok && v != "" {
			params.Set("starttime", v)
			queryParts = append(queryParts, "from="+v)
		}
		if v, ok := input["end_date"].(string); ok && v != "" {
			params.Set("endtime", v)
			queryParts = append(queryParts, "to="+v)
		}
		if v, ok := input["min_magnitude"].(float64); ok {
			params.Set("minmagnitude", fmt.Sprintf("%g", v))
			queryParts = append(queryParts, fmt.Sprintf("min_mag=%g", v))
			out.MagnitudeMin = v
		}
		if v, ok := input["max_magnitude"].(float64); ok {
			params.Set("maxmagnitude", fmt.Sprintf("%g", v))
			out.MagnitudeMax = v
		}
		// Geographic filter — either bbox or center+radius
		bbox := false
		if v, ok := input["min_latitude"].(float64); ok {
			params.Set("minlatitude", fmt.Sprintf("%g", v))
			bbox = true
		}
		if v, ok := input["max_latitude"].(float64); ok {
			params.Set("maxlatitude", fmt.Sprintf("%g", v))
			bbox = true
		}
		if v, ok := input["min_longitude"].(float64); ok {
			params.Set("minlongitude", fmt.Sprintf("%g", v))
			bbox = true
		}
		if v, ok := input["max_longitude"].(float64); ok {
			params.Set("maxlongitude", fmt.Sprintf("%g", v))
			bbox = true
		}
		// Center+radius: lat/lon + maxradius_km
		lat, hasLat := input["latitude"].(float64)
		lon, hasLon := input["longitude"].(float64)
		if hasLat && hasLon && !bbox {
			params.Set("latitude", fmt.Sprintf("%g", lat))
			params.Set("longitude", fmt.Sprintf("%g", lon))
			rad := 100.0 // default 100km
			if r, ok := input["radius_km"].(float64); ok && r > 0 {
				rad = r
			}
			params.Set("maxradiuskm", fmt.Sprintf("%g", rad))
			queryParts = append(queryParts, fmt.Sprintf("near=(%g,%g) r=%gkm", lat, lon, rad))
		}
		if v, ok := input["min_depth_km"].(float64); ok {
			params.Set("mindepth", fmt.Sprintf("%g", v))
		}
		if v, ok := input["max_depth_km"].(float64); ok {
			params.Set("maxdepth", fmt.Sprintf("%g", v))
		}
		if v, ok := input["alert_level"].(string); ok && v != "" {
			params.Set("alertlevel", v) // green | yellow | orange | red
			queryParts = append(queryParts, "alert="+v)
		}
		// Limit
		limit := 50
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
			limit = int(l)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("orderby", "magnitude")

		if len(queryParts) == 0 {
			return nil, fmt.Errorf("at least one filter required: start_date, latitude+longitude, min_magnitude, etc.")
		}
		out.Query = strings.Join(queryParts, " · ")

		urlStr := "https://earthquake.usgs.gov/fdsnws/event/1/query?" + params.Encode()
		body, err := usgsGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		if err := decodeUSGSGeoJSON(body, out); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: recent, query", mode)
	}

	// Aggregations
	regionSet := map[string]struct{}{}
	for _, q := range out.Earthquakes {
		if q.Tsunami {
			out.TsunamiCount++
		}
		if q.Alert != "" && q.Alert != "green" {
			out.AlertedCount++
		}
		if q.Place != "" {
			// Extract region from end of place string ("X km Y of REGION_NAME")
			if idx := strings.LastIndex(q.Place, " of "); idx != -1 {
				region := strings.TrimSpace(q.Place[idx+4:])
				regionSet[region] = struct{}{}
			} else {
				regionSet[q.Place] = struct{}{}
			}
		}
	}
	for r := range regionSet {
		out.UniqueRegions = append(out.UniqueRegions, r)
	}
	sort.Strings(out.UniqueRegions)
	if len(out.UniqueRegions) > 12 {
		out.UniqueRegions = out.UniqueRegions[:12]
	}

	out.HighlightFindings = buildUSGSHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func decodeUSGSGeoJSON(body []byte, out *USGSEarthquakeSearchOutput) error {
	var raw struct {
		Metadata struct {
			Count int `json:"count"`
		} `json:"metadata"`
		Features []struct {
			ID         string         `json:"id"`
			Properties map[string]any `json:"properties"`
			Geometry   struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("usgs decode: %w", err)
	}
	out.TotalCount = raw.Metadata.Count
	for _, f := range raw.Features {
		p := f.Properties
		q := USGSQuake{
			ID:            f.ID,
			Magnitude:     gtFloat(p, "mag"),
			MagnitudeType: gtString(p, "magType"),
			Place:         gtString(p, "place"),
			TimeUnixMs:    int64(gtFloat(p, "time")),
			Status:        gtString(p, "status"),
			Type:          gtString(p, "type"),
			Alert:         gtString(p, "alert"),
			Tsunami:       gtFloat(p, "tsunami") > 0,
			Significance:  gtInt(p, "sig"),
			NumStations:   gtInt(p, "nst"),
			URL:           gtString(p, "url"),
		}
		if updMs := int64(gtFloat(p, "updated")); updMs > 0 {
			q.UpdatedTime = time.UnixMilli(updMs).UTC().Format(time.RFC3339)
		}
		if q.TimeUnixMs > 0 {
			q.Time = time.UnixMilli(q.TimeUnixMs).UTC().Format(time.RFC3339)
		}
		if len(f.Geometry.Coordinates) >= 3 {
			q.Longitude = f.Geometry.Coordinates[0]
			q.Latitude = f.Geometry.Coordinates[1]
			q.DepthKm = f.Geometry.Coordinates[2]
		}
		out.Earthquakes = append(out.Earthquakes, q)
	}
	out.Returned = len(out.Earthquakes)
	return nil
}

func usgsGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usgs: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("usgs HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildUSGSHighlights(o *USGSEarthquakeSearchOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d earthquakes returned for %s", o.Returned, o.Query))
	if o.TotalCount > o.Returned {
		hi = append(hi, fmt.Sprintf("  total in result set: %d", o.TotalCount))
	}
	if o.TsunamiCount > 0 {
		hi = append(hi, fmt.Sprintf("  🌊 %d tsunami-flagged events", o.TsunamiCount))
	}
	if o.AlertedCount > 0 {
		hi = append(hi, fmt.Sprintf("  🚨 %d events with PAGER alert (yellow/orange/red — damage expected)", o.AlertedCount))
	}
	// Highest-magnitude first
	sorted := make([]USGSQuake, len(o.Earthquakes))
	copy(sorted, o.Earthquakes)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Magnitude > sorted[j].Magnitude })
	for i, q := range sorted {
		if i >= 6 {
			break
		}
		marker := ""
		if q.Tsunami {
			marker += " 🌊"
		}
		if q.Alert != "" && q.Alert != "green" {
			marker += " 🚨" + q.Alert
		}
		hi = append(hi, fmt.Sprintf("  • M%.1f%s %s — %s · depth %.1fkm", q.Magnitude, marker, q.Time[:16], q.Place, q.DepthKm))
	}
	if len(o.UniqueRegions) > 0 && len(o.UniqueRegions) <= 8 {
		hi = append(hi, "  regions: "+strings.Join(o.UniqueRegions, ", "))
	}
	return hi
}
