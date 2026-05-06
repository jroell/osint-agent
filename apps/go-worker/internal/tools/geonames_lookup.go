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

// GeoNamesLookup wraps the GeoNames public web services.
// Free with a registered username (set GEONAMES_USERNAME env var).
//
// GeoNames covers ~12M place names with rich attributes (admin codes,
// elevation, population, alternate names, time zones, neighbours).
// Stronger than Nominatim alone for "place by name → coordinates +
// admin context" and "what places are near these coordinates."
//
// Modes:
//   - "search"     : place-name search (with country/feature filters)
//   - "find_nearby" : nearest named places to a lat/lon (within radius_km)
//   - "country_info": country-level reference data
//
// Knowledge-graph: emits typed entities (kind: "place") with stable
// GeoName IDs.

type GNPlace struct {
	GeoNameID    int     `json:"geonameid"`
	Name         string  `json:"name"`
	Country      string  `json:"country_name,omitempty"`
	CountryCode  string  `json:"country_code,omitempty"`
	Admin1       string  `json:"admin1,omitempty"`
	FeatureClass string  `json:"feature_class,omitempty"`
	FeatureCode  string  `json:"feature_code,omitempty"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	Population   int64   `json:"population,omitempty"`
	URL          string  `json:"geonames_url"`
}

type GNEntity struct {
	Kind        string         `json:"kind"`
	GeoNameID   int            `json:"geonameid"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type GeoNamesLookupOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	Returned          int        `json:"returned"`
	Total             int        `json:"total,omitempty"`
	Places            []GNPlace  `json:"places,omitempty"`
	Entities          []GNEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func GeoNamesLookup(ctx context.Context, input map[string]any) (*GeoNamesLookupOutput, error) {
	username := os.Getenv("GEONAMES_USERNAME")
	if username == "" {
		username = "demo" // last-resort; rate-limited
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["latitude"] != nil && input["longitude"] != nil:
			mode = "find_nearby"
		case input["country_code"] != nil:
			mode = "country_info"
		default:
			mode = "search"
		}
	}
	out := &GeoNamesLookupOutput{Mode: mode, Source: "api.geonames.org"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("geonames: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("geonames HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		// GeoNames returns 200 even on quota/auth errors with {"status": {...}}
		return body, nil
	}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		params := url.Values{}
		params.Set("q", q)
		params.Set("username", username)
		params.Set("maxRows", "20")
		params.Set("type", "json")
		if cc, ok := input["country"].(string); ok && cc != "" {
			params.Set("country", cc)
		}
		if fc, ok := input["feature_class"].(string); ok && fc != "" {
			params.Set("featureClass", fc)
		}
		body, err := get("http://api.geonames.org/searchJSON?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			TotalResultsCount int              `json:"totalResultsCount"`
			Geonames          []map[string]any `json:"geonames"`
			Status            struct {
				Message string `json:"message"`
				Value   int    `json:"value"`
			} `json:"status"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("geonames decode: %w", err)
		}
		if resp.Status.Message != "" {
			return nil, fmt.Errorf("geonames: %s (status %d)", resp.Status.Message, resp.Status.Value)
		}
		out.Total = resp.TotalResultsCount
		for _, g := range resp.Geonames {
			out.Places = append(out.Places, parseGNPlace(g))
		}

	case "find_nearby":
		lat, _ := input["latitude"].(float64)
		lon, _ := input["longitude"].(float64)
		if lat == 0 && lon == 0 {
			return nil, fmt.Errorf("input.latitude and input.longitude required")
		}
		radius := 20.0
		if r, ok := input["radius_km"].(float64); ok && r > 0 {
			radius = r
		}
		params := url.Values{}
		params.Set("lat", fmt.Sprintf("%f", lat))
		params.Set("lng", fmt.Sprintf("%f", lon))
		params.Set("radius", fmt.Sprintf("%.1f", radius))
		params.Set("username", username)
		params.Set("maxRows", "20")
		body, err := get("http://api.geonames.org/findNearbyJSON?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			Geonames []map[string]any `json:"geonames"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("geonames decode: %w", err)
		}
		for _, g := range resp.Geonames {
			out.Places = append(out.Places, parseGNPlace(g))
		}

	case "country_info":
		cc, _ := input["country_code"].(string)
		if cc == "" {
			return nil, fmt.Errorf("input.country_code required")
		}
		out.Query = cc
		params := url.Values{}
		params.Set("country", cc)
		params.Set("username", username)
		body, err := get("http://api.geonames.org/countryInfoJSON?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			Geonames []map[string]any `json:"geonames"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("geonames decode: %w", err)
		}
		for _, g := range resp.Geonames {
			out.Places = append(out.Places, parseGNPlace(g))
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Places)
	out.Entities = gnBuildEntities(out)
	out.HighlightFindings = gnBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseGNPlace(m map[string]any) GNPlace {
	id := int(gtFloat(m, "geonameId"))
	name := gtString(m, "name")
	if name == "" {
		name = gtString(m, "toponymName")
	}
	if name == "" {
		name = gtString(m, "countryName")
	}
	pop := int64(gtFloat(m, "population"))
	return GNPlace{
		GeoNameID:    id,
		Name:         name,
		Country:      gtString(m, "countryName"),
		CountryCode:  gtString(m, "countryCode"),
		Admin1:       gtString(m, "adminName1"),
		FeatureClass: gtString(m, "fcl"),
		FeatureCode:  gtString(m, "fcode"),
		Latitude:     gtFloat(m, "lat"),
		Longitude:    gtFloat(m, "lng"),
		Population:   pop,
		URL:          fmt.Sprintf("https://www.geonames.org/%d", id),
	}
}

func gnBuildEntities(o *GeoNamesLookupOutput) []GNEntity {
	ents := []GNEntity{}
	for _, p := range o.Places {
		ents = append(ents, GNEntity{
			Kind: "place", GeoNameID: p.GeoNameID, Name: p.Name, URL: p.URL,
			Description: fmt.Sprintf("%s, %s — %s/%s", p.Admin1, p.Country, p.FeatureClass, p.FeatureCode),
			Attributes: map[string]any{
				"latitude":      p.Latitude,
				"longitude":     p.Longitude,
				"country":       p.Country,
				"country_code":  p.CountryCode,
				"admin1":        p.Admin1,
				"feature_class": p.FeatureClass,
				"feature_code":  p.FeatureCode,
				"population":    p.Population,
			},
		})
	}
	return ents
}

func gnBuildHighlights(o *GeoNamesLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ geonames %s: %d places (total %d)", o.Mode, o.Returned, o.Total)}
	for i, p := range o.Places {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%d] (%.4f,%.4f) %s — %s",
			p.Name, p.GeoNameID, p.Latitude, p.Longitude, p.CountryCode, p.FeatureCode))
	}
	return hi
}
