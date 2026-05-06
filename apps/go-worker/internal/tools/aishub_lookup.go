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

// AISHubLookup wraps the AISHub vessel-AIS feed (data.aishub.net).
// Free with an account API username (set `AISHUB_USERNAME`); aishub
// is community-fed and supplies AIS positions worldwide.
//
// Modes:
//   - "by_mmsi"       : vessel by MMSI (9-digit Maritime Mobile Service ID)
//   - "by_imo"        : vessel by IMO number
//   - "by_callsign"   : vessel by radio callsign
//   - "near_position" : vessels within radius of a lat/lon
//
// Knowledge-graph: emits typed entities (kind: "vessel") with stable
// MMSI as the primary identifier (IMO when present).

type AISVessel struct {
	MMSI        int     `json:"mmsi"`
	IMO         int     `json:"imo,omitempty"`
	Name        string  `json:"name,omitempty"`
	Callsign    string  `json:"callsign,omitempty"`
	ShipType    int     `json:"ship_type,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
	Speed       float64 `json:"speed_knots,omitempty"`
	Course      float64 `json:"course,omitempty"`
	Heading     int     `json:"heading,omitempty"`
	Status      int     `json:"nav_status,omitempty"`
	Destination string  `json:"destination,omitempty"`
	ETA         string  `json:"eta,omitempty"`
	Time        string  `json:"position_time,omitempty"`
	Source      string  `json:"data_source,omitempty"`
	URL         string  `json:"track_url,omitempty"`
}

type AISEntity struct {
	Kind        string         `json:"kind"`
	MMSI        int            `json:"mmsi"`
	IMO         int            `json:"imo,omitempty"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type AISHubLookupOutput struct {
	Mode              string      `json:"mode"`
	Query             string      `json:"query"`
	Returned          int         `json:"returned"`
	Vessels           []AISVessel `json:"vessels,omitempty"`
	Entities          []AISEntity `json:"entities"`
	HighlightFindings []string    `json:"highlight_findings"`
	Source            string      `json:"source"`
	TookMs            int64       `json:"tookMs"`
}

func AISHubLookup(ctx context.Context, input map[string]any) (*AISHubLookupOutput, error) {
	username := os.Getenv("AISHUB_USERNAME")
	if username == "" {
		return nil, fmt.Errorf("AISHUB_USERNAME not set; register at aishub.net")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["mmsi"] != nil:
			mode = "by_mmsi"
		case input["imo"] != nil:
			mode = "by_imo"
		case input["callsign"] != nil:
			mode = "by_callsign"
		case input["latitude"] != nil:
			mode = "near_position"
		default:
			return nil, fmt.Errorf("required: mmsi, imo, callsign, or lat+lng")
		}
	}
	out := &AISHubLookupOutput{Mode: mode, Source: "data.aishub.net"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(params url.Values) ([]byte, error) {
		params.Set("username", username)
		params.Set("format", "1") // JSON
		params.Set("output", "json")
		params.Set("compress", "0")
		u := "https://data.aishub.net/ws.php?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("aishub: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("aishub HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	parseAISFeed := func(body []byte) ([]AISVessel, error) {
		// AISHub returns: [{ERROR: false, USERNAME, FORMAT, LATITUDE_MIN, ...}, [{vessel}, ...]]
		var raw []json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("aishub decode: %w", err)
		}
		if len(raw) < 2 {
			// Could be {ERROR:true, ERROR_MESSAGE:"..."}
			var errResp map[string]any
			if json.Unmarshal(body, &errResp) == nil {
				if msg, ok := errResp["ERROR_MESSAGE"].(string); ok {
					return nil, fmt.Errorf("aishub error: %s", msg)
				}
			}
			return nil, fmt.Errorf("aishub: empty response")
		}
		var arr []map[string]any
		if err := json.Unmarshal(raw[1], &arr); err != nil {
			return nil, fmt.Errorf("aishub vessel array: %w", err)
		}
		vessels := make([]AISVessel, 0, len(arr))
		for _, v := range arr {
			mmsi := int(gtFloat(v, "MMSI"))
			imo := int(gtFloat(v, "IMO"))
			ves := AISVessel{
				MMSI:        mmsi,
				IMO:         imo,
				Name:        strings.TrimSpace(gtString(v, "NAME")),
				Callsign:    strings.TrimSpace(gtString(v, "CALLSIGN")),
				ShipType:    int(gtFloat(v, "TYPE")),
				Latitude:    gtFloat(v, "LATITUDE"),
				Longitude:   gtFloat(v, "LONGITUDE"),
				Speed:       gtFloat(v, "SOG"),
				Course:      gtFloat(v, "COG"),
				Heading:     int(gtFloat(v, "HEADING")),
				Status:      int(gtFloat(v, "NAVSTAT")),
				Destination: strings.TrimSpace(gtString(v, "DEST")),
				ETA:         strings.TrimSpace(gtString(v, "ETA")),
				Time:        strings.TrimSpace(gtString(v, "TIME")),
				Source:      "aishub",
			}
			if mmsi != 0 {
				ves.URL = fmt.Sprintf("https://www.marinetraffic.com/en/ais/details/ships/mmsi:%d", mmsi)
			}
			vessels = append(vessels, ves)
		}
		return vessels, nil
	}

	switch mode {
	case "by_mmsi":
		mmsi := tmdbIntID(input, "mmsi")
		if mmsi == 0 {
			return nil, fmt.Errorf("input.mmsi required (9-digit numeric)")
		}
		out.Query = fmt.Sprintf("%d", mmsi)
		body, err := get(url.Values{"mmsi": []string{out.Query}})
		if err != nil {
			return nil, err
		}
		v, err := parseAISFeed(body)
		if err != nil {
			return nil, err
		}
		out.Vessels = v
	case "by_imo":
		imo := tmdbIntID(input, "imo")
		if imo == 0 {
			return nil, fmt.Errorf("input.imo required")
		}
		out.Query = fmt.Sprintf("%d", imo)
		body, err := get(url.Values{"imo": []string{out.Query}})
		if err != nil {
			return nil, err
		}
		v, err := parseAISFeed(body)
		if err != nil {
			return nil, err
		}
		out.Vessels = v
	case "by_callsign":
		cs, _ := input["callsign"].(string)
		cs = strings.ToUpper(strings.TrimSpace(cs))
		if cs == "" {
			return nil, fmt.Errorf("input.callsign required")
		}
		out.Query = cs
		body, err := get(url.Values{"callsign": []string{cs}})
		if err != nil {
			return nil, err
		}
		v, err := parseAISFeed(body)
		if err != nil {
			return nil, err
		}
		out.Vessels = v
	case "near_position":
		lat, _ := input["latitude"].(float64)
		lon, _ := input["longitude"].(float64)
		radius := 30.0
		if r, ok := input["radius_nm"].(float64); ok && r > 0 {
			radius = r
		}
		// AISHub uses a bounding-box request; convert radius (nm) to ~ degree window
		dlat := radius / 60.0
		dlon := radius / 60.0 // small-region approximation
		params := url.Values{
			"latmin": []string{fmt.Sprintf("%f", lat-dlat)},
			"latmax": []string{fmt.Sprintf("%f", lat+dlat)},
			"lonmin": []string{fmt.Sprintf("%f", lon-dlon)},
			"lonmax": []string{fmt.Sprintf("%f", lon+dlon)},
		}
		out.Query = fmt.Sprintf("(%.4f,%.4f)±%.0fnm", lat, lon, radius)
		body, err := get(params)
		if err != nil {
			return nil, err
		}
		v, err := parseAISFeed(body)
		if err != nil {
			return nil, err
		}
		out.Vessels = v
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Vessels)
	out.Entities = aishubBuildEntities(out)
	out.HighlightFindings = aishubBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func aishubBuildEntities(o *AISHubLookupOutput) []AISEntity {
	ents := []AISEntity{}
	for _, v := range o.Vessels {
		name := v.Name
		if name == "" {
			name = fmt.Sprintf("MMSI %d", v.MMSI)
		}
		ents = append(ents, AISEntity{
			Kind: "vessel", MMSI: v.MMSI, IMO: v.IMO, Name: name, URL: v.URL,
			Description: v.Destination,
			Attributes: map[string]any{
				"callsign":      v.Callsign,
				"ship_type":     v.ShipType,
				"latitude":      v.Latitude,
				"longitude":     v.Longitude,
				"speed_knots":   v.Speed,
				"course":        v.Course,
				"heading":       v.Heading,
				"nav_status":    v.Status,
				"destination":   v.Destination,
				"eta":           v.ETA,
				"position_time": v.Time,
				"source":        v.Source,
			},
		})
	}
	return ents
}

func aishubBuildHighlights(o *AISHubLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ aishub %s: %d vessels", o.Mode, o.Returned)}
	for i, v := range o.Vessels {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [MMSI:%d IMO:%d cs:%s] @ (%.4f,%.4f) %.1fkn → %s",
			v.Name, v.MMSI, v.IMO, v.Callsign, v.Latitude, v.Longitude, v.Speed, v.Destination))
	}
	return hi
}
