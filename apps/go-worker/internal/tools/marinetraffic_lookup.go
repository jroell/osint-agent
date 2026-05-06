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

// MarineTrafficLookup wraps the MarineTraffic Service API. Paid;
// REQUIRES `MARINETRAFFIC_API_KEY`. MarineTraffic is the dominant
// commercial provider of global AIS vessel data (denser coverage than
// AISHub's community feed).
//
// Modes:
//   - "vessel_position"    : current position by MMSI/IMO/IMO/SHIPID
//   - "vessel_master_data" : static metadata (name, type, builder, owner)
//   - "vessels_in_area"    : bounding box query (LATMIN/LATMAX/LONMIN/LONMAX)
//   - "voyage_forecast"    : next-hour ETA + destination
//   - "port_calls"         : recent port calls for a vessel
//
// Knowledge-graph: emits typed entities (kind: "vessel" | "port_call")
// with stable MMSI/IMO IDs. Pairs with `aishub_lookup` (free fallback).

type MTVessel struct {
	MMSI        int     `json:"mmsi,omitempty"`
	IMO         int     `json:"imo,omitempty"`
	ShipID      int     `json:"ship_id,omitempty"`
	Name        string  `json:"name,omitempty"`
	Callsign    string  `json:"callsign,omitempty"`
	ShipType    int     `json:"ship_type,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
	Speed       float64 `json:"speed_knots,omitempty"`
	Course      float64 `json:"course,omitempty"`
	Heading     int     `json:"heading,omitempty"`
	Status      string  `json:"nav_status,omitempty"`
	Destination string  `json:"destination,omitempty"`
	ETA         string  `json:"eta,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
	Length      float64 `json:"length_m,omitempty"`
	Width       float64 `json:"width_m,omitempty"`
	Draught     float64 `json:"draught_m,omitempty"`
	YearBuilt   int     `json:"year_built,omitempty"`
	Flag        string  `json:"flag,omitempty"`
	URL         string  `json:"marinetraffic_url"`
}

type MTPortCall struct {
	MMSI     int    `json:"mmsi"`
	PortName string `json:"port_name"`
	PortID   int    `json:"port_id,omitempty"`
	Country  string `json:"country,omitempty"`
	Arrived  string `json:"arrival,omitempty"`
	Departed string `json:"departure,omitempty"`
}

type MTEntity struct {
	Kind        string         `json:"kind"`
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type MarineTrafficLookupOutput struct {
	Mode              string       `json:"mode"`
	Query             string       `json:"query,omitempty"`
	Returned          int          `json:"returned"`
	Vessels           []MTVessel   `json:"vessels,omitempty"`
	PortCalls         []MTPortCall `json:"port_calls,omitempty"`
	Entities          []MTEntity   `json:"entities"`
	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
}

func MarineTrafficLookup(ctx context.Context, input map[string]any) (*MarineTrafficLookupOutput, error) {
	apiKey := os.Getenv("MARINETRAFFIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("MARINETRAFFIC_API_KEY not set; subscribe at marinetraffic.com")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["bbox"] != nil || (input["lat_min"] != nil && input["lat_max"] != nil):
			mode = "vessels_in_area"
		case input["mmsi"] != nil && input["port_calls"] == true:
			mode = "port_calls"
		case input["mmsi"] != nil:
			mode = "vessel_position"
		default:
			return nil, fmt.Errorf("required: mmsi, imo, or bbox")
		}
	}
	out := &MarineTrafficLookupOutput{Mode: mode, Source: "services.marinetraffic.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(endpoint string, params url.Values) ([]byte, error) {
		u := "https://services.marinetraffic.com/api/" + endpoint + "/" + apiKey
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("marinetraffic: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("marinetraffic HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "vessel_position":
		mmsi := tmdbIntID(input, "mmsi")
		if mmsi == 0 {
			return nil, fmt.Errorf("input.mmsi required")
		}
		out.Query = fmt.Sprintf("%d", mmsi)
		params := url.Values{
			"v":        []string{"4"},
			"mmsi":     []string{out.Query},
			"protocol": []string{"jsono"},
			"msgtype":  []string{"extended"},
		}
		body, err := get("exportvessel", params)
		if err != nil {
			return nil, err
		}
		out.Vessels = parseMTVesselJSON(body)
	case "vessel_master_data":
		mmsi := tmdbIntID(input, "mmsi")
		if mmsi == 0 {
			return nil, fmt.Errorf("input.mmsi required")
		}
		out.Query = fmt.Sprintf("%d", mmsi)
		params := url.Values{
			"v":        []string{"3"},
			"mmsi":     []string{out.Query},
			"protocol": []string{"jsono"},
		}
		body, err := get("vesselmasterdata", params)
		if err != nil {
			return nil, err
		}
		out.Vessels = parseMTVesselJSON(body)
	case "vessels_in_area":
		params := url.Values{
			"v":        []string{"4"},
			"protocol": []string{"jsono"},
			"msgtype":  []string{"simple"},
		}
		if v, ok := input["lat_min"].(float64); ok {
			params.Set("minlat", fmt.Sprintf("%f", v))
		}
		if v, ok := input["lat_max"].(float64); ok {
			params.Set("maxlat", fmt.Sprintf("%f", v))
		}
		if v, ok := input["lon_min"].(float64); ok {
			params.Set("minlon", fmt.Sprintf("%f", v))
		}
		if v, ok := input["lon_max"].(float64); ok {
			params.Set("maxlon", fmt.Sprintf("%f", v))
		}
		out.Query = params.Encode()
		body, err := get("exportvessels", params)
		if err != nil {
			return nil, err
		}
		out.Vessels = parseMTVesselJSON(body)
	case "voyage_forecast":
		mmsi := tmdbIntID(input, "mmsi")
		if mmsi == 0 {
			return nil, fmt.Errorf("input.mmsi required")
		}
		out.Query = fmt.Sprintf("%d", mmsi)
		params := url.Values{
			"v":        []string{"2"},
			"mmsi":     []string{out.Query},
			"protocol": []string{"jsono"},
		}
		body, err := get("voyageforecast", params)
		if err != nil {
			return nil, err
		}
		out.Vessels = parseMTVesselJSON(body)
	case "port_calls":
		mmsi := tmdbIntID(input, "mmsi")
		if mmsi == 0 {
			return nil, fmt.Errorf("input.mmsi required")
		}
		out.Query = fmt.Sprintf("%d", mmsi)
		params := url.Values{
			"v":        []string{"5"},
			"mmsi":     []string{out.Query},
			"protocol": []string{"jsono"},
		}
		body, err := get("portcalls", params)
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		_ = json.Unmarshal(body, &arr)
		for _, m := range arr {
			out.PortCalls = append(out.PortCalls, MTPortCall{
				MMSI:     mmsi,
				PortName: gtString(m, "PORT_NAME"),
				PortID:   int(gtFloat(m, "PORT_ID")),
				Country:  gtString(m, "COUNTRY"),
				Arrived:  gtString(m, "ARRIVAL"),
				Departed: gtString(m, "DEPARTURE"),
			})
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Vessels) + len(out.PortCalls)
	out.Entities = mtBuildEntities(out)
	out.HighlightFindings = mtBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseMTVesselJSON(body []byte) []MTVessel {
	var arr []map[string]any
	_ = json.Unmarshal(body, &arr)
	out := []MTVessel{}
	for _, m := range arr {
		mmsi := int(gtFloat(m, "MMSI"))
		v := MTVessel{
			MMSI:        mmsi,
			IMO:         int(gtFloat(m, "IMO")),
			ShipID:      int(gtFloat(m, "SHIP_ID")),
			Name:        strings.TrimSpace(gtString(m, "SHIPNAME")),
			Callsign:    strings.TrimSpace(gtString(m, "CALLSIGN")),
			ShipType:    int(gtFloat(m, "SHIPTYPE")),
			Latitude:    gtFloat(m, "LAT"),
			Longitude:   gtFloat(m, "LON"),
			Speed:       gtFloat(m, "SPEED") / 10.0, // MarineTraffic encodes speed × 10
			Course:      gtFloat(m, "COURSE"),
			Heading:     int(gtFloat(m, "HEADING")),
			Status:      gtString(m, "STATUS"),
			Destination: strings.TrimSpace(gtString(m, "DESTINATION")),
			ETA:         strings.TrimSpace(gtString(m, "ETA")),
			Timestamp:   strings.TrimSpace(gtString(m, "TIMESTAMP")),
			Length:      gtFloat(m, "LENGTH"),
			Width:       gtFloat(m, "WIDTH"),
			Draught:     gtFloat(m, "DRAUGHT"),
			YearBuilt:   int(gtFloat(m, "YEAR_BUILT")),
			Flag:        gtString(m, "FLAG"),
		}
		if mmsi != 0 {
			v.URL = fmt.Sprintf("https://www.marinetraffic.com/en/ais/details/ships/mmsi:%d", mmsi)
		}
		out = append(out, v)
	}
	return out
}

func mtBuildEntities(o *MarineTrafficLookupOutput) []MTEntity {
	ents := []MTEntity{}
	for _, v := range o.Vessels {
		name := v.Name
		if name == "" {
			name = fmt.Sprintf("MMSI %d", v.MMSI)
		}
		ents = append(ents, MTEntity{
			Kind: "vessel", ID: fmt.Sprintf("%d", v.MMSI), Name: name, URL: v.URL,
			Date: v.Timestamp, Description: v.Destination,
			Attributes: map[string]any{
				"mmsi": v.MMSI, "imo": v.IMO, "ship_id": v.ShipID,
				"callsign": v.Callsign, "ship_type": v.ShipType,
				"latitude": v.Latitude, "longitude": v.Longitude,
				"speed_knots": v.Speed, "course": v.Course, "heading": v.Heading,
				"status": v.Status, "destination": v.Destination, "eta": v.ETA,
				"length_m": v.Length, "width_m": v.Width, "draught_m": v.Draught,
				"year_built": v.YearBuilt, "flag": v.Flag,
			},
		})
	}
	for _, p := range o.PortCalls {
		ents = append(ents, MTEntity{
			Kind: "port_call", Name: p.PortName, Date: p.Arrived,
			Description: fmt.Sprintf("MMSI %d arrived %s, departed %s", p.MMSI, p.Arrived, p.Departed),
			Attributes:  map[string]any{"mmsi": p.MMSI, "port_id": p.PortID, "country": p.Country},
		})
	}
	return ents
}

func mtBuildHighlights(o *MarineTrafficLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ marinetraffic %s: %d records", o.Mode, o.Returned)}
	for i, v := range o.Vessels {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [MMSI:%d IMO:%d] @ (%.4f,%.4f) %.1fkn → %s (eta %s)",
			v.Name, v.MMSI, v.IMO, v.Latitude, v.Longitude, v.Speed, v.Destination, v.ETA))
	}
	for i, p := range o.PortCalls {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("    port: %s [%s] %s → %s", p.PortName, p.Country, p.Arrived, p.Departed))
	}
	return hi
}
