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

// CensusGeocoder normalizes US addresses against the Census TIGER/Line
// reference layer. Free, no auth. Two modes:
//
//   - "geocode" : free-text address → matched standardized address +
//                 lat/lon + parsed components (street, city, state, ZIP,
//                 suffix) + TIGER Line ID + Census tract / county /
//                 state hierarchy with FIPS GEOIDs.
//   - "reverse" : lat/lon → nearest matched street + Census geographies.
//
// Why this is an ER primitive: addresses returned by other tools (NPI
// provider addresses, OpenFDA recall recalling-firm addresses, CFPB
// complaint ZIPs, GovTrack member offices, LDA registrant addresses,
// SEC company business addresses) are typed inconsistently — some
// have "Apt 4B", some have "Suite 100", some abbreviate differently.
// Normalizing through Census Geocoder gives you a canonical form
// (uppercase, abbreviated suffix, full ZIP+4) plus a TIGER Line ID
// that can be used as a dedupe key. The Census tract GEOID is the
// cross-reference key into demographic + ACS data via Census APIs.

type CensusAddressMatch struct {
	MatchedAddress  string  `json:"matched_address,omitempty"`
	Latitude        float64 `json:"latitude,omitempty"`
	Longitude       float64 `json:"longitude,omitempty"`
	TigerLineID     string  `json:"tiger_line_id,omitempty"`
	TigerSide       string  `json:"tiger_side,omitempty"`

	// Parsed components
	FromAddress     string  `json:"from_address,omitempty"`
	ToAddress       string  `json:"to_address,omitempty"`
	StreetName      string  `json:"street_name,omitempty"`
	PreType         string  `json:"pre_type,omitempty"`
	SuffixType      string  `json:"suffix_type,omitempty"`
	PreDirection    string  `json:"pre_direction,omitempty"`
	SuffixDirection string  `json:"suffix_direction,omitempty"`
	City            string  `json:"city,omitempty"`
	State           string  `json:"state,omitempty"`
	Zip             string  `json:"zip,omitempty"`

	// Census geographies
	CensusState    string  `json:"census_state,omitempty"`
	CensusStateFIPS string `json:"census_state_fips,omitempty"`
	County         string  `json:"county,omitempty"`
	CountyFIPS     string  `json:"county_fips,omitempty"`
	Tract          string  `json:"census_tract,omitempty"`
	TractGEOID     string  `json:"census_tract_geoid,omitempty"`
	BlockGroup     string  `json:"block_group,omitempty"`
	BlockGEOID     string  `json:"block_geoid,omitempty"`
}

type CensusGeocoderOutput struct {
	Mode              string               `json:"mode"`
	Query             string               `json:"query,omitempty"`
	MatchCount        int                  `json:"match_count"`
	Matches           []CensusAddressMatch `json:"matches,omitempty"`

	HighlightFindings []string             `json:"highlight_findings"`
	Source            string               `json:"source"`
	TookMs            int64                `json:"tookMs"`
	Note              string               `json:"note,omitempty"`
}

func CensusGeocoder(ctx context.Context, input map[string]any) (*CensusGeocoderOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["lat"]; ok {
			mode = "reverse"
		} else if _, ok := input["latitude"]; ok {
			mode = "reverse"
		} else {
			mode = "geocode"
		}
	}

	out := &CensusGeocoderOutput{
		Mode:   mode,
		Source: "geocoding.geo.census.gov",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "geocode":
		addr, _ := input["address"].(string)
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return nil, fmt.Errorf("input.address required for geocode mode")
		}
		out.Query = addr
		// Use 'geographies' endpoint to get tract/county/state in one call
		params := url.Values{}
		params.Set("address", addr)
		params.Set("benchmark", "Public_AR_Current")
		params.Set("vintage", "Current_Current")
		params.Set("format", "json")
		urlStr := "https://geocoding.geo.census.gov/geocoder/geographies/onelineaddress?" + params.Encode()
		body, err := censusGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		if err := decodeCensusGeocode(body, out); err != nil {
			return nil, err
		}

	case "reverse":
		lat := getCensusFloat(input, "latitude", "lat")
		lon := getCensusFloat(input, "longitude", "lon", "lng")
		if lat == 0 && lon == 0 {
			return nil, fmt.Errorf("input.latitude (or lat) and input.longitude (or lon) required for reverse mode")
		}
		out.Query = fmt.Sprintf("%.6f,%.6f", lat, lon)
		params := url.Values{}
		params.Set("x", fmt.Sprintf("%f", lon))
		params.Set("y", fmt.Sprintf("%f", lat))
		params.Set("benchmark", "Public_AR_Current")
		params.Set("vintage", "Current_Current")
		params.Set("format", "json")
		urlStr := "https://geocoding.geo.census.gov/geocoder/geographies/coordinates?" + params.Encode()
		body, err := censusGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		// Reverse geocoding returns geographies but no addressMatches array
		// — extract Census tract/county/state directly from the geographies
		// block.
		var raw struct {
			Result struct {
				Geographies map[string][]map[string]any `json:"geographies"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("census reverse decode: %w", err)
		}
		match := CensusAddressMatch{
			Latitude:  lat,
			Longitude: lon,
		}
		if states, ok := raw.Result.Geographies["States"]; ok && len(states) > 0 {
			match.CensusState = gtString(states[0], "NAME")
			match.CensusStateFIPS = gtString(states[0], "STATE")
		}
		if counties, ok := raw.Result.Geographies["Counties"]; ok && len(counties) > 0 {
			match.County = gtString(counties[0], "NAME")
			match.CountyFIPS = gtString(counties[0], "GEOID")
		}
		if tracts, ok := raw.Result.Geographies["Census Tracts"]; ok && len(tracts) > 0 {
			match.Tract = gtString(tracts[0], "BASENAME")
			match.TractGEOID = gtString(tracts[0], "GEOID")
		}
		if blocks, ok := raw.Result.Geographies["Census Blocks"]; ok && len(blocks) > 0 {
			match.BlockGEOID = gtString(blocks[0], "GEOID")
		}
		if blockGroups, ok := raw.Result.Geographies["Block Groups"]; ok && len(blockGroups) > 0 {
			match.BlockGroup = gtString(blockGroups[0], "BASENAME")
		}
		// If we got at least one geography, return it
		if match.CensusState != "" || match.County != "" || match.TractGEOID != "" {
			out.Matches = []CensusAddressMatch{match}
			out.MatchCount = 1
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: geocode, reverse", mode)
	}

	out.HighlightFindings = buildCensusHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func decodeCensusGeocode(body []byte, out *CensusGeocoderOutput) error {
	var raw struct {
		Result struct {
			AddressMatches []struct {
				MatchedAddress string `json:"matchedAddress"`
				Coordinates    struct {
					X float64 `json:"x"`
					Y float64 `json:"y"`
				} `json:"coordinates"`
				TigerLine struct {
					Side        string `json:"side"`
					TigerLineID string `json:"tigerLineId"`
				} `json:"tigerLine"`
				AddressComponents struct {
					FromAddress     string `json:"fromAddress"`
					ToAddress       string `json:"toAddress"`
					StreetName      string `json:"streetName"`
					PreType         string `json:"preType"`
					SuffixType      string `json:"suffixType"`
					PreDirection    string `json:"preDirection"`
					SuffixDirection string `json:"suffixDirection"`
					City            string `json:"city"`
					State           string `json:"state"`
					Zip             string `json:"zip"`
				} `json:"addressComponents"`
				Geographies map[string][]map[string]any `json:"geographies"`
			} `json:"addressMatches"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("census decode: %w", err)
	}
	out.MatchCount = len(raw.Result.AddressMatches)
	for _, m := range raw.Result.AddressMatches {
		match := CensusAddressMatch{
			MatchedAddress:  m.MatchedAddress,
			Latitude:        m.Coordinates.Y,
			Longitude:       m.Coordinates.X,
			TigerLineID:     m.TigerLine.TigerLineID,
			TigerSide:       m.TigerLine.Side,
			FromAddress:     m.AddressComponents.FromAddress,
			ToAddress:       m.AddressComponents.ToAddress,
			StreetName:      m.AddressComponents.StreetName,
			PreType:         m.AddressComponents.PreType,
			SuffixType:      m.AddressComponents.SuffixType,
			PreDirection:    m.AddressComponents.PreDirection,
			SuffixDirection: m.AddressComponents.SuffixDirection,
			City:            m.AddressComponents.City,
			State:           m.AddressComponents.State,
			Zip:             m.AddressComponents.Zip,
		}
		if states, ok := m.Geographies["States"]; ok && len(states) > 0 {
			match.CensusState = gtString(states[0], "NAME")
			match.CensusStateFIPS = gtString(states[0], "STATE")
		}
		if counties, ok := m.Geographies["Counties"]; ok && len(counties) > 0 {
			match.County = gtString(counties[0], "NAME")
			match.CountyFIPS = gtString(counties[0], "GEOID")
		}
		if tracts, ok := m.Geographies["Census Tracts"]; ok && len(tracts) > 0 {
			match.Tract = gtString(tracts[0], "BASENAME")
			match.TractGEOID = gtString(tracts[0], "GEOID")
		}
		if blockGroups, ok := m.Geographies["Block Groups"]; ok && len(blockGroups) > 0 {
			match.BlockGroup = gtString(blockGroups[0], "BASENAME")
		}
		if blocks, ok := m.Geographies["Census Blocks"]; ok && len(blocks) > 0 {
			match.BlockGEOID = gtString(blocks[0], "GEOID")
		}
		out.Matches = append(out.Matches, match)
	}
	return nil
}

func getCensusFloat(input map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := input[k]; ok {
			switch x := v.(type) {
			case float64:
				return x
			case int:
				return float64(x)
			case string:
				var f float64
				if _, err := fmt.Sscanf(x, "%f", &f); err == nil {
					return f
				}
			}
		}
	}
	return 0
}

func censusGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("census: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("census HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildCensusHighlights(o *CensusGeocoderOutput) []string {
	hi := []string{}
	if o.MatchCount == 0 {
		hi = append(hi, fmt.Sprintf("✗ no Census Geocoder matches for '%s'", o.Query))
		return hi
	}
	switch o.Mode {
	case "geocode":
		hi = append(hi, fmt.Sprintf("✓ %d match(es) for '%s'", o.MatchCount, o.Query))
		for i, m := range o.Matches {
			if i >= 3 {
				break
			}
			hi = append(hi, fmt.Sprintf("  • %s", m.MatchedAddress))
			hi = append(hi, fmt.Sprintf("    coords: %.6f, %.6f · TIGER %s (%s)", m.Latitude, m.Longitude, m.TigerLineID, m.TigerSide))
			if m.County != "" {
				hi = append(hi, fmt.Sprintf("    county: %s, %s · FIPS %s", m.County, m.CensusState, m.CountyFIPS))
			}
			if m.TractGEOID != "" {
				hi = append(hi, fmt.Sprintf("    Census tract %s · GEOID %s", m.Tract, m.TractGEOID))
			}
			if m.BlockGEOID != "" {
				hi = append(hi, fmt.Sprintf("    block GEOID: %s · block group %s", m.BlockGEOID, m.BlockGroup))
			}
		}
	case "reverse":
		hi = append(hi, fmt.Sprintf("✓ Census geographies for %s", o.Query))
		m := o.Matches[0]
		if m.CensusState != "" {
			hi = append(hi, fmt.Sprintf("  state: %s · FIPS %s", m.CensusState, m.CensusStateFIPS))
		}
		if m.County != "" {
			hi = append(hi, fmt.Sprintf("  county: %s · FIPS %s", m.County, m.CountyFIPS))
		}
		if m.TractGEOID != "" {
			hi = append(hi, fmt.Sprintf("  Census tract %s · GEOID %s", m.Tract, m.TractGEOID))
		}
		if m.BlockGEOID != "" {
			hi = append(hi, fmt.Sprintf("  block GEOID: %s · block group %s", m.BlockGEOID, m.BlockGroup))
		}
	}
	return hi
}
