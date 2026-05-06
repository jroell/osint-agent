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

// FlightAwareLookup wraps the FlightAware AeroAPI v4
// (aeroapi.flightaware.com). Paid; REQUIRES `FLIGHTAWARE_API_KEY`.
//
// The dominant commercial flight tracking + ATC data source. Replaces
// the now-blocked free adsb.lol public mirror.
//
// Modes:
//   - "flight"             : flights for an ident (callsign/registration)
//   - "operator"           : flights for an airline/operator
//   - "airport"            : recent arrivals/departures at an airport
//   - "registration"       : flights by tail number (e.g. N12345)
//   - "track"              : track points for a specific flight
//
// Knowledge-graph: emits typed entities (kind: "flight" | "aircraft" |
// "airport") with stable AeroAPI fa_flight_id IDs.

type FAFlight struct {
	FAFlightID   string  `json:"fa_flight_id"`
	Ident        string  `json:"ident,omitempty"`
	Operator     string  `json:"operator,omitempty"`
	Origin       string  `json:"origin_code,omitempty"`
	Destination  string  `json:"destination_code,omitempty"`
	Registration string  `json:"registration,omitempty"`
	AircraftType string  `json:"aircraft_type,omitempty"`
	ScheduledOff string  `json:"scheduled_out,omitempty"`
	ActualOff    string  `json:"actual_out,omitempty"`
	ScheduledOn  string  `json:"scheduled_in,omitempty"`
	ActualOn     string  `json:"actual_in,omitempty"`
	Status       string  `json:"status,omitempty"`
	Latitude     float64 `json:"latitude,omitempty"`
	Longitude    float64 `json:"longitude,omitempty"`
	AltitudeFt   int     `json:"altitude_ft,omitempty"`
	GroundSpeed  float64 `json:"ground_speed_kt,omitempty"`
	URL          string  `json:"flightaware_url"`
}

type FAEntity struct {
	Kind        string         `json:"kind"`
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type FlightAwareLookupOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Flights           []FAFlight     `json:"flights,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []FAEntity     `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func FlightAwareLookup(ctx context.Context, input map[string]any) (*FlightAwareLookupOutput, error) {
	apiKey := os.Getenv("FLIGHTAWARE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FLIGHTAWARE_API_KEY not set; subscribe at flightaware.com/aeroapi")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["fa_flight_id"] != nil:
			mode = "track"
		case input["registration"] != nil:
			mode = "registration"
		case input["operator"] != nil:
			mode = "operator"
		case input["airport"] != nil:
			mode = "airport"
		case input["ident"] != nil:
			mode = "flight"
		default:
			return nil, fmt.Errorf("required: ident, registration, operator, airport, or fa_flight_id")
		}
	}
	out := &FlightAwareLookupOutput{Mode: mode, Source: "aeroapi.flightaware.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(path string, params url.Values) (map[string]any, error) {
		u := "https://aeroapi.flightaware.com/aeroapi" + path
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("x-apikey", apiKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("flightaware: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 401 {
			return nil, fmt.Errorf("flightaware: unauthorized — check API key")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("flightaware HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("flightaware decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "flight":
		ident, _ := input["ident"].(string)
		ident = strings.ToUpper(strings.TrimSpace(ident))
		if ident == "" {
			return nil, fmt.Errorf("input.ident required (callsign or flight number)")
		}
		out.Query = ident
		m, err := get("/flights/"+url.PathEscape(ident), url.Values{})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Flights = parseFAFlights(m)
	case "registration":
		reg, _ := input["registration"].(string)
		reg = strings.ToUpper(strings.TrimSpace(reg))
		if reg == "" {
			return nil, fmt.Errorf("input.registration required")
		}
		out.Query = reg
		m, err := get("/flights/registration/"+url.PathEscape(reg), url.Values{})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Flights = parseFAFlights(m)
	case "operator":
		op, _ := input["operator"].(string)
		op = strings.ToUpper(strings.TrimSpace(op))
		if op == "" {
			return nil, fmt.Errorf("input.operator required (ICAO code, e.g. UAL, DAL)")
		}
		out.Query = op
		m, err := get("/operators/"+url.PathEscape(op)+"/flights", url.Values{})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Flights = parseFAFlights(m)
	case "airport":
		ap, _ := input["airport"].(string)
		ap = strings.ToUpper(strings.TrimSpace(ap))
		if ap == "" {
			return nil, fmt.Errorf("input.airport required (ICAO code, e.g. KSFO, EGLL)")
		}
		out.Query = ap
		m, err := get("/airports/"+url.PathEscape(ap)+"/flights", url.Values{})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		// Combine arrivals + departures + scheduled_arrivals + scheduled_departures
		for _, key := range []string{"arrivals", "departures", "scheduled_arrivals", "scheduled_departures"} {
			if arr, ok := m[key].([]any); ok {
				for _, x := range arr {
					if rec, ok := x.(map[string]any); ok {
						out.Flights = append(out.Flights, parseFAFlight(rec))
					}
				}
			}
		}
	case "track":
		id, _ := input["fa_flight_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.fa_flight_id required")
		}
		out.Query = id
		m, err := get("/flights/"+url.PathEscape(id)+"/track", url.Values{})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Flights)
	out.Entities = faBuildEntities(out)
	out.HighlightFindings = faBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseFAFlights(m map[string]any) []FAFlight {
	out := []FAFlight{}
	if flights, ok := m["flights"].([]any); ok {
		for _, x := range flights {
			if rec, ok := x.(map[string]any); ok {
				out = append(out, parseFAFlight(rec))
			}
		}
	}
	return out
}

func parseFAFlight(rec map[string]any) FAFlight {
	f := FAFlight{
		FAFlightID:   gtString(rec, "fa_flight_id"),
		Ident:        gtString(rec, "ident"),
		Operator:     gtString(rec, "operator"),
		Registration: gtString(rec, "registration"),
		AircraftType: gtString(rec, "aircraft_type"),
		ScheduledOff: gtString(rec, "scheduled_out"),
		ActualOff:    gtString(rec, "actual_out"),
		ScheduledOn:  gtString(rec, "scheduled_in"),
		ActualOn:     gtString(rec, "actual_in"),
		Status:       gtString(rec, "status"),
	}
	if origin, ok := rec["origin"].(map[string]any); ok {
		f.Origin = gtString(origin, "code")
	}
	if dest, ok := rec["destination"].(map[string]any); ok {
		f.Destination = gtString(dest, "code")
	}
	f.Latitude = gtFloat(rec, "last_position.latitude")
	if last, ok := rec["last_position"].(map[string]any); ok {
		f.Latitude = gtFloat(last, "latitude")
		f.Longitude = gtFloat(last, "longitude")
		f.AltitudeFt = int(gtFloat(last, "altitude")) * 100
		f.GroundSpeed = gtFloat(last, "groundspeed")
	}
	if f.FAFlightID != "" {
		f.URL = "https://flightaware.com/live/flight/id/" + f.FAFlightID
	}
	return f
}

func faBuildEntities(o *FlightAwareLookupOutput) []FAEntity {
	ents := []FAEntity{}
	for _, f := range o.Flights {
		name := f.Ident
		if name == "" {
			name = f.Registration
		}
		date := f.ActualOff
		if date == "" {
			date = f.ScheduledOff
		}
		ents = append(ents, FAEntity{
			Kind: "flight", ID: f.FAFlightID, Name: name, URL: f.URL, Date: date,
			Description: fmt.Sprintf("%s → %s [%s]", f.Origin, f.Destination, f.Status),
			Attributes: map[string]any{
				"ident": f.Ident, "operator": f.Operator, "registration": f.Registration,
				"aircraft_type": f.AircraftType,
				"origin":        f.Origin, "destination": f.Destination,
				"scheduled_out": f.ScheduledOff, "actual_out": f.ActualOff,
				"scheduled_in": f.ScheduledOn, "actual_in": f.ActualOn,
				"status":   f.Status,
				"latitude": f.Latitude, "longitude": f.Longitude,
				"altitude_ft": f.AltitudeFt, "ground_speed_kt": f.GroundSpeed,
			},
		})
	}
	return ents
}

func faBuildHighlights(o *FlightAwareLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ flightaware %s: %d flights", o.Mode, o.Returned)}
	for i, f := range o.Flights {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s] %s → %s (%s) — %s", f.Ident, f.Registration, f.Origin, f.Destination, f.Status, f.URL))
	}
	return hi
}
