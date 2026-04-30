package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type NominatimResult struct {
	OSMType      string  `json:"osm_type,omitempty"`
	OSMID        int64   `json:"osm_id,omitempty"`
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	DisplayName  string  `json:"display_name"`
	Class        string  `json:"class,omitempty"`
	Type         string  `json:"type,omitempty"`
	AddressType  string  `json:"address_type,omitempty"`
	Importance   float64 `json:"importance,omitempty"`
	BoundingBox  []float64 `json:"bounding_box,omitempty"` // [minLat, maxLat, minLon, maxLon]
	// Address components (only if include_address=true)
	HouseNumber  string  `json:"house_number,omitempty"`
	Road         string  `json:"road,omitempty"`
	Neighbourhood string `json:"neighbourhood,omitempty"`
	Suburb       string  `json:"suburb,omitempty"`
	City         string  `json:"city,omitempty"`
	County       string  `json:"county,omitempty"`
	State        string  `json:"state,omitempty"`
	Postcode     string  `json:"postcode,omitempty"`
	Country      string  `json:"country,omitempty"`
	CountryCode  string  `json:"country_code,omitempty"`
}

type NominatimOutput struct {
	Mode             string            `json:"mode"`
	Query            string            `json:"query"`
	TotalReturned    int               `json:"total_returned"`
	Results          []NominatimResult `json:"results"`
	ReverseAddress   *NominatimResult  `json:"reverse_address,omitempty"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source           string            `json:"source"`
	TookMs           int64             `json:"tookMs"`
	Note             string            `json:"note,omitempty"`
}

// NominatimGeocode queries OpenStreetMap's Nominatim API for geocoding
// (address → lat/lng) and reverse geocoding (lat/lng → address).
//
// Modes:
//   - "geocode" (default): query by address/place name, get lat/lng + structured address
//   - "reverse": query by lat/lng, get address
//
// CRITICAL: Nominatim's free public instance has a strict rate limit of
// 1 req/sec per source IP. Heavy use should self-host or use a paid mirror
// (LocationIQ, Mapbox, Google Maps).
//
// Use cases:
//   - Geographic ER: connect address records (from `mobile_app_lookup`,
//     `gleif_lei_lookup`, `whois`) to actual coordinates
//   - Proximity analysis: are two entities geographically close?
//   - Reverse: given coordinates from EXIF/`exif_extract_geolocate`, find
//     the precise address
//
// Free, no key. Polite User-Agent required.
func NominatimGeocode(ctx context.Context, input map[string]any) (*NominatimOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "geocode"
	}

	limit := 5
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 50 {
		limit = int(v)
	}
	includeAddress := true
	if v, ok := input["include_address"].(bool); ok {
		includeAddress = v
	}

	start := time.Now()
	out := &NominatimOutput{Mode: mode, Source: "nominatim.openstreetmap.org"}

	switch mode {
	case "geocode":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, errors.New("input.query required for geocode mode (address or place name)")
		}
		out.Query = q
		params := url.Values{}
		params.Set("q", q)
		params.Set("format", "json")
		params.Set("limit", fmt.Sprintf("%d", limit))
		if includeAddress {
			params.Set("addressdetails", "1")
		}
		body, err := nominatimFetch(ctx, "https://nominatim.openstreetmap.org/search?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw []nominatimRaw
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("nominatim parse: %w", err)
		}
		for _, r := range raw {
			out.Results = append(out.Results, nominatimRawToResult(r))
		}
		out.TotalReturned = len(out.Results)

	case "reverse":
		lat, _ := input["lat"].(float64)
		lon, _ := input["lon"].(float64)
		if lat == 0 && lon == 0 {
			return nil, errors.New("input.lat and input.lon required for reverse mode")
		}
		out.Query = fmt.Sprintf("%.6f,%.6f", lat, lon)
		params := url.Values{}
		params.Set("lat", fmt.Sprintf("%.6f", lat))
		params.Set("lon", fmt.Sprintf("%.6f", lon))
		params.Set("format", "json")
		params.Set("addressdetails", "1")
		body, err := nominatimFetch(ctx, "https://nominatim.openstreetmap.org/reverse?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw nominatimRaw
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("nominatim reverse parse: %w", err)
		}
		r := nominatimRawToResult(raw)
		out.ReverseAddress = &r
		out.Results = []NominatimResult{r}
		out.TotalReturned = 1

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use geocode or reverse", mode)
	}

	// Highlights
	highlights := []string{
		fmt.Sprintf("%d results for '%s' (mode=%s)", out.TotalReturned, out.Query, mode),
	}
	if len(out.Results) > 0 {
		top := out.Results[0]
		highlights = append(highlights, fmt.Sprintf("top: %.6f,%.6f — %s", top.Lat, top.Lon, top.DisplayName))
	}
	out.HighlightFindings = highlights
	if out.TotalReturned == 0 {
		out.Note = "No results — address may be too new/obscure for OSM, or formatted incorrectly. Try with city + state + country for better results."
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

type nominatimRaw struct {
	OSMType     string                 `json:"osm_type"`
	OSMID       int64                  `json:"osm_id"`
	Lat         string                 `json:"lat"`
	Lon         string                 `json:"lon"`
	DisplayName string                 `json:"display_name"`
	Class       string                 `json:"class"`
	Type        string                 `json:"type"`
	AddressType string                 `json:"addresstype"`
	Importance  float64                `json:"importance"`
	BoundingBox []string               `json:"boundingbox"`
	Address     map[string]interface{} `json:"address"`
}

func nominatimRawToResult(r nominatimRaw) NominatimResult {
	out := NominatimResult{
		OSMType: r.OSMType, OSMID: r.OSMID,
		DisplayName: r.DisplayName, Class: r.Class, Type: r.Type,
		AddressType: r.AddressType, Importance: r.Importance,
	}
	fmt.Sscanf(r.Lat, "%f", &out.Lat)
	fmt.Sscanf(r.Lon, "%f", &out.Lon)
	for _, b := range r.BoundingBox {
		var f float64
		fmt.Sscanf(b, "%f", &f)
		out.BoundingBox = append(out.BoundingBox, f)
	}
	if r.Address != nil {
		if v, ok := r.Address["house_number"].(string); ok {
			out.HouseNumber = v
		}
		if v, ok := r.Address["road"].(string); ok {
			out.Road = v
		}
		if v, ok := r.Address["neighbourhood"].(string); ok {
			out.Neighbourhood = v
		}
		if v, ok := r.Address["suburb"].(string); ok {
			out.Suburb = v
		}
		// City may be under "city", "town", or "village"
		for _, k := range []string{"city", "town", "village", "hamlet"} {
			if v, ok := r.Address[k].(string); ok && v != "" {
				out.City = v
				break
			}
		}
		if v, ok := r.Address["county"].(string); ok {
			out.County = v
		}
		if v, ok := r.Address["state"].(string); ok {
			out.State = v
		}
		if v, ok := r.Address["postcode"].(string); ok {
			out.Postcode = v
		}
		if v, ok := r.Address["country"].(string); ok {
			out.Country = v
		}
		if v, ok := r.Address["country_code"].(string); ok {
			out.CountryCode = strings.ToUpper(v)
		}
	}
	return out
}

func nominatimFetch(ctx context.Context, endpoint string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/nominatim (https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nominatim fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("nominatim status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}
