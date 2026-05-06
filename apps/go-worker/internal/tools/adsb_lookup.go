package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ADSBLookup wraps two complementary ADS-B aircraft data sources:
//   - ADS-B Exchange RapidAPI tier (REQUIRES `RAPID_API_KEY`)
//   - The free public mirror at adsb.lol (no key)
//
// Modes:
//   - "by_icao24"     : aircraft by 24-bit ICAO hex
//   - "by_registration": aircraft by tail number (e.g. "N12345")
//   - "by_callsign"   : aircraft by callsign (e.g. "UAL123")
//   - "near_position" : list aircraft near a lat/lon within radius_nm
//
// Knowledge-graph: emits typed entities (kind: "aircraft") with stable
// ICAO24 hex IDs.

type ADSBAircraft struct {
	ICAO24       string  `json:"icao24"`
	Registration string  `json:"registration,omitempty"`
	Callsign     string  `json:"callsign,omitempty"`
	Type         string  `json:"aircraft_type,omitempty"`
	Operator     string  `json:"operator,omitempty"`
	Latitude     float64 `json:"latitude,omitempty"`
	Longitude    float64 `json:"longitude,omitempty"`
	AltitudeFt   int     `json:"altitude_ft,omitempty"`
	Heading      float64 `json:"heading,omitempty"`
	GroundSpeed  float64 `json:"ground_speed,omitempty"`
	OnGround     bool    `json:"on_ground,omitempty"`
	Squawk       string  `json:"squawk,omitempty"`
	Emergency    string  `json:"emergency,omitempty"`
	Source       string  `json:"data_source,omitempty"` // "adsb-lol" | "adsb-exchange"
	URL          string  `json:"track_url,omitempty"`
}

type ADSBEntity struct {
	Kind        string         `json:"kind"`
	ICAO24      string         `json:"icao24"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type ADSBLookupOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query"`
	Returned          int            `json:"returned"`
	Aircraft          []ADSBAircraft `json:"aircraft,omitempty"`
	Entities          []ADSBEntity   `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func ADSBLookup(ctx context.Context, input map[string]any) (*ADSBLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["icao24"] != nil:
			mode = "by_icao24"
		case input["registration"] != nil:
			mode = "by_registration"
		case input["callsign"] != nil:
			mode = "by_callsign"
		case input["latitude"] != nil:
			mode = "near_position"
		default:
			return nil, fmt.Errorf("required: icao24, registration, callsign, or lat+lng")
		}
	}
	out := &ADSBLookupOutput{Mode: mode, Source: "adsb.lol"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string, useRapid bool) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		if useRapid {
			if k := os.Getenv("RAPID_API_KEY"); k != "" {
				req.Header.Set("x-rapidapi-key", k)
				req.Header.Set("x-rapidapi-host", "adsbexchange-com1.p.rapidapi.com")
			} else {
				return nil, fmt.Errorf("adsb: RAPID_API_KEY not set for ADS-B Exchange")
			}
		}
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("adsb: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("adsb HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	parseAdsbLolFeed := func(body []byte, source string) []ADSBAircraft {
		var resp struct {
			AC []map[string]any `json:"ac"`
		}
		_ = json.Unmarshal(body, &resp)
		out := []ADSBAircraft{}
		for _, a := range resp.AC {
			ac := ADSBAircraft{
				ICAO24:       strings.ToLower(gtString(a, "hex")),
				Registration: gtString(a, "r"),
				Callsign:     strings.TrimSpace(gtString(a, "flight")),
				Type:         gtString(a, "t"),
				Latitude:     gtFloat(a, "lat"),
				Longitude:    gtFloat(a, "lon"),
				AltitudeFt:   int(gtFloat(a, "alt_baro")),
				Heading:      gtFloat(a, "track"),
				GroundSpeed:  gtFloat(a, "gs"),
				Squawk:       gtString(a, "squawk"),
				Emergency:    gtString(a, "emergency"),
				Source:       source,
			}
			if ac.ICAO24 != "" {
				ac.URL = "https://globe.adsbexchange.com/?icao=" + ac.ICAO24
			}
			out = append(out, ac)
		}
		return out
	}

	switch mode {
	case "by_icao24":
		hex, _ := input["icao24"].(string)
		hex = strings.ToLower(strings.TrimSpace(hex))
		if hex == "" {
			return nil, fmt.Errorf("input.icao24 required")
		}
		out.Query = hex
		body, err := get("https://api.adsb.lol/v2/icao/"+hex, false)
		if err != nil {
			return nil, err
		}
		out.Aircraft = parseAdsbLolFeed(body, "adsb-lol")
	case "by_registration":
		reg, _ := input["registration"].(string)
		reg = strings.ToUpper(strings.TrimSpace(reg))
		if reg == "" {
			return nil, fmt.Errorf("input.registration required")
		}
		out.Query = reg
		body, err := get("https://api.adsb.lol/v2/reg/"+reg, false)
		if err != nil {
			return nil, err
		}
		out.Aircraft = parseAdsbLolFeed(body, "adsb-lol")
	case "by_callsign":
		cs, _ := input["callsign"].(string)
		cs = strings.ToUpper(strings.TrimSpace(cs))
		if cs == "" {
			return nil, fmt.Errorf("input.callsign required")
		}
		out.Query = cs
		body, err := get("https://api.adsb.lol/v2/callsign/"+cs, false)
		if err != nil {
			return nil, err
		}
		out.Aircraft = parseAdsbLolFeed(body, "adsb-lol")
	case "near_position":
		lat, _ := input["latitude"].(float64)
		lon, _ := input["longitude"].(float64)
		radius := 25.0
		if r, ok := input["radius_nm"].(float64); ok && r > 0 {
			radius = r
		}
		out.Query = fmt.Sprintf("(%.4f,%.4f)±%.0fnm", lat, lon, radius)
		u := fmt.Sprintf("https://api.adsb.lol/v2/lat/%f/lon/%f/dist/%f", lat, lon, radius)
		body, err := get(u, false)
		if err != nil {
			return nil, err
		}
		out.Aircraft = parseAdsbLolFeed(body, "adsb-lol")
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Aircraft)
	out.Entities = adsbBuildEntities(out)
	out.HighlightFindings = adsbBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func adsbBuildEntities(o *ADSBLookupOutput) []ADSBEntity {
	ents := []ADSBEntity{}
	for _, a := range o.Aircraft {
		name := a.Registration
		if name == "" {
			name = a.Callsign
		}
		if name == "" {
			name = a.ICAO24
		}
		ents = append(ents, ADSBEntity{
			Kind: "aircraft", ICAO24: a.ICAO24, Name: name, URL: a.URL,
			Description: a.Type,
			Attributes: map[string]any{
				"registration": a.Registration,
				"callsign":     a.Callsign,
				"type":         a.Type,
				"operator":     a.Operator,
				"latitude":     a.Latitude,
				"longitude":    a.Longitude,
				"altitude_ft":  a.AltitudeFt,
				"heading":      a.Heading,
				"ground_speed": a.GroundSpeed,
				"squawk":       a.Squawk,
				"emergency":    a.Emergency,
				"source":       a.Source,
			},
		})
	}
	return ents
}

func adsbBuildHighlights(o *ADSBLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ adsb %s: %d aircraft", o.Mode, o.Returned)}
	for i, a := range o.Aircraft {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s] %s @ (%.4f,%.4f) FL%d hdg %.0f° %s",
			a.Registration, a.ICAO24, a.Callsign, a.Latitude, a.Longitude, a.AltitudeFt/100, a.Heading, a.URL))
	}
	return hi
}
