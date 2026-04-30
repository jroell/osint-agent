package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// OSMFeature is one element returned from Overpass.
type OSMFeature struct {
	Type        string            `json:"type"`     // node | way | relation
	OSMID       int64             `json:"osm_id"`
	Lat         float64           `json:"lat,omitempty"`
	Lon         float64           `json:"lon,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Name        string            `json:"name,omitempty"`
	Street      string            `json:"addr_street,omitempty"`
	City        string            `json:"addr_city,omitempty"`
	State       string            `json:"addr_state,omitempty"`
	Country     string            `json:"addr_country,omitempty"`
	Postcode    string            `json:"addr_postcode,omitempty"`
	Operator    string            `json:"operator,omitempty"`
	Brand       string            `json:"brand,omitempty"`
	Website     string            `json:"website,omitempty"`
	Phone       string            `json:"phone,omitempty"`
	OSMURL      string            `json:"osm_url,omitempty"`
}

// OSMTagAggregate counts top tag values across the result set.
type OSMTagAggregate struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Count int    `json:"count"`
}

// OSMOverpassOutput is the response.
type OSMOverpassOutput struct {
	Mode             string             `json:"mode"`
	Query            string             `json:"query"`
	OverpassQL       string             `json:"overpass_ql,omitempty"`
	TotalReturned    int                `json:"total_returned"`
	Features         []OSMFeature       `json:"features"`
	TopAmenities     []OSMTagAggregate  `json:"top_amenities,omitempty"`
	TopCountries     []OSMTagAggregate  `json:"top_countries,omitempty"`
	TopBrands        []OSMTagAggregate  `json:"top_brands,omitempty"`
	BoundingBox      []float64          `json:"bounding_box,omitempty"` // [minLat, minLon, maxLat, maxLon]
	HighlightFindings []string          `json:"highlight_findings"`
	Source           string             `json:"source"`
	TookMs           int64              `json:"tookMs"`
	Note             string             `json:"note,omitempty"`
}

type overpassRawElement struct {
	Type   string            `json:"type"`
	ID     int64             `json:"id"`
	Lat    float64           `json:"lat"`
	Lon    float64           `json:"lon"`
	Center *struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"center"`
	Tags map[string]string `json:"tags"`
}

type overpassRawResp struct {
	Elements []overpassRawElement `json:"elements"`
}

// OSMOverpassQuery queries the OpenStreetMap Overpass API for geographic
// features. Free, no auth (subject to fair-use limits). 4 modes:
//
//   - "name_search"  : find OSM features named exactly "X" worldwide
//                      (brand-trace, e.g. every "Trump Tower" tagged in OSM)
//   - "tag_radius"   : find features matching a tag (amenity=school,
//                      shop=convenience, etc.) within a radius of a center
//                      point (geographic ER around a known location)
//   - "tag_in_bbox"  : same as tag_radius but in a lat/lon bounding box
//   - "free_form"    : raw Overpass QL query (advanced users)
//
// Why this matters for ER:
//   - Pairs with `nominatim_geocode`: address → lat/lon → query everything
//     within 500m. Useful for "is there a school next to this address?",
//     "what businesses are at this lat/lon?", etc.
//   - Brand-trace: every named "Starbucks", "Trump Tower", "Shell Station"
//     mapped worldwide via OSM contributors.
//   - Surveillance/infra OSINT: military_service, surveillance=camera,
//     defensive_works=bunker, etc. tags reveal hidden facilities.
//   - Operator/brand chain mapping: who operates a chain of locations
//     ("operator" tag = corporate parent).
func OSMOverpassQuery(ctx context.Context, input map[string]any) (*OSMOverpassOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "name_search"
	}

	limit := 50
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 200 {
		limit = int(v)
	}

	timeout := 25
	if v, ok := input["timeout_seconds"].(float64); ok && int(v) >= 5 && int(v) <= 90 {
		timeout = int(v)
	}

	var query string
	displayQuery := ""

	switch mode {
	case "name_search":
		nameVal, _ := input["name"].(string)
		nameVal = strings.TrimSpace(nameVal)
		if nameVal == "" {
			return nil, fmt.Errorf("input.name required for name_search mode")
		}
		// Escape quotes to prevent breaking out of the QL string
		nameVal = strings.ReplaceAll(nameVal, `"`, `\"`)
		query = fmt.Sprintf(`[out:json][timeout:%d];nwr["name"="%s"];out tags center %d;`, timeout, nameVal, limit)
		displayQuery = nameVal

	case "tag_radius":
		tagKey, _ := input["tag_key"].(string)
		tagKey = strings.TrimSpace(tagKey)
		tagVal, _ := input["tag_value"].(string)
		tagVal = strings.TrimSpace(tagVal)
		if tagKey == "" || tagVal == "" {
			return nil, fmt.Errorf("input.tag_key and input.tag_value required for tag_radius mode (e.g. tag_key='amenity', tag_value='school')")
		}
		lat, ok1 := input["lat"].(float64)
		lon, ok2 := input["lon"].(float64)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("input.lat and input.lon (numbers) required for tag_radius mode")
		}
		radius := 500.0
		if v, ok := input["radius_m"].(float64); ok && v > 0 && v <= 50000 {
			radius = v
		}
		tagKey = strings.ReplaceAll(tagKey, `"`, `\"`)
		tagVal = strings.ReplaceAll(tagVal, `"`, `\"`)
		query = fmt.Sprintf(`[out:json][timeout:%d];nwr["%s"="%s"](around:%.0f,%f,%f);out tags center %d;`,
			timeout, tagKey, tagVal, radius, lat, lon, limit)
		displayQuery = fmt.Sprintf("%s=%s within %.0fm of (%.4f,%.4f)", tagKey, tagVal, radius, lat, lon)

	case "tag_in_bbox":
		tagKey, _ := input["tag_key"].(string)
		tagVal, _ := input["tag_value"].(string)
		if tagKey == "" || tagVal == "" {
			return nil, fmt.Errorf("input.tag_key and input.tag_value required for tag_in_bbox mode")
		}
		minLat, ok1 := input["min_lat"].(float64)
		minLon, ok2 := input["min_lon"].(float64)
		maxLat, ok3 := input["max_lat"].(float64)
		maxLon, ok4 := input["max_lon"].(float64)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return nil, fmt.Errorf("input.min_lat, min_lon, max_lat, max_lon (numbers) required for tag_in_bbox mode")
		}
		tagKey = strings.ReplaceAll(tagKey, `"`, `\"`)
		tagVal = strings.ReplaceAll(tagVal, `"`, `\"`)
		query = fmt.Sprintf(`[out:json][timeout:%d];nwr["%s"="%s"](%f,%f,%f,%f);out tags center %d;`,
			timeout, tagKey, tagVal, minLat, minLon, maxLat, maxLon, limit)
		displayQuery = fmt.Sprintf("%s=%s in bbox", tagKey, tagVal)

	case "free_form":
		raw, _ := input["overpass_ql"].(string)
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("input.overpass_ql required for free_form mode")
		}
		// Sanity-cap query length to prevent abuse
		if len(raw) > 8000 {
			return nil, fmt.Errorf("overpass_ql query too long (>%d chars)", 8000)
		}
		query = raw
		displayQuery = "(custom QL)"

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: name_search, tag_radius, tag_in_bbox, free_form", mode)
	}

	start := time.Now()
	body := []byte("data=" + query)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://overpass-api.de/api/interpreter", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "osint-agent/0.1 (https://github.com/jroell/osint-agent)")

	client := &http.Client{Timeout: time.Duration(timeout+10) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("overpass request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("overpass %d: %s", resp.StatusCode, string(body))
	}

	var raw overpassRawResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("overpass decode: %w", err)
	}

	out := &OSMOverpassOutput{
		Mode:       mode,
		Query:      displayQuery,
		OverpassQL: query,
		Source:     "overpass-api.de",
	}

	amenityAgg := map[string]int{}
	countryAgg := map[string]int{}
	brandAgg := map[string]int{}
	minLat, minLon := 90.0, 180.0
	maxLat, maxLon := -90.0, -180.0
	hasBBox := false

	for _, el := range raw.Elements {
		f := OSMFeature{
			Type:  el.Type,
			OSMID: el.ID,
			Tags:  el.Tags,
			Lat:   el.Lat,
			Lon:   el.Lon,
		}
		// for ways/relations, lat/lon come from "center"
		if el.Center != nil {
			f.Lat = el.Center.Lat
			f.Lon = el.Center.Lon
		}
		// Hoist common tags
		if el.Tags != nil {
			f.Name = el.Tags["name"]
			f.Street = el.Tags["addr:street"]
			f.City = el.Tags["addr:city"]
			f.State = el.Tags["addr:state"]
			f.Country = el.Tags["addr:country"]
			f.Postcode = el.Tags["addr:postcode"]
			f.Operator = el.Tags["operator"]
			f.Brand = el.Tags["brand"]
			f.Website = el.Tags["website"]
			f.Phone = el.Tags["phone"]
			if amenity, ok := el.Tags["amenity"]; ok && amenity != "" {
				amenityAgg[amenity]++
			}
			if shop, ok := el.Tags["shop"]; ok && shop != "" {
				amenityAgg["shop="+shop]++
			}
			if c := el.Tags["addr:country"]; c != "" {
				countryAgg[c]++
			}
			if b := el.Tags["brand"]; b != "" {
				brandAgg[b]++
			}
		}
		f.OSMURL = fmt.Sprintf("https://www.openstreetmap.org/%s/%d", el.Type, el.ID)
		out.Features = append(out.Features, f)

		if f.Lat != 0 && f.Lon != 0 {
			hasBBox = true
			if f.Lat < minLat {
				minLat = f.Lat
			}
			if f.Lat > maxLat {
				maxLat = f.Lat
			}
			if f.Lon < minLon {
				minLon = f.Lon
			}
			if f.Lon > maxLon {
				maxLon = f.Lon
			}
		}
	}
	out.TotalReturned = len(out.Features)
	if hasBBox {
		out.BoundingBox = []float64{minLat, minLon, maxLat, maxLon}
	}

	out.TopAmenities = topNAggregate("amenity", amenityAgg, 10)
	out.TopCountries = topNAggregate("addr:country", countryAgg, 10)
	out.TopBrands = topNAggregate("brand", brandAgg, 10)

	// Highlights
	hi := []string{}
	hi = append(hi, fmt.Sprintf("%d OSM features matched", out.TotalReturned))
	if mode == "name_search" && len(out.Features) >= 2 {
		hi = append(hi, fmt.Sprintf("⚠️  %d distinct OSM features tagged with this exact name — likely separate locations of a brand or namesakes", len(out.Features)))
	}
	if len(out.TopCountries) > 0 {
		topC := []string{}
		for _, c := range out.TopCountries[:min2(5, len(out.TopCountries))] {
			topC = append(topC, fmt.Sprintf("%s=%d", c.Value, c.Count))
		}
		hi = append(hi, "by country: "+strings.Join(topC, ", "))
	}
	if len(out.TopAmenities) > 0 {
		topA := []string{}
		for _, a := range out.TopAmenities[:min2(5, len(out.TopAmenities))] {
			topA = append(topA, fmt.Sprintf("%s=%d", a.Value, a.Count))
		}
		hi = append(hi, "amenity/shop breakdown: "+strings.Join(topA, ", "))
	}
	if len(out.TopBrands) > 0 {
		topB := []string{}
		for _, b := range out.TopBrands[:min2(5, len(out.TopBrands))] {
			topB = append(topB, fmt.Sprintf("%s=%d", b.Value, b.Count))
		}
		hi = append(hi, "brand operators: "+strings.Join(topB, ", "))
	}
	if hasBBox {
		hi = append(hi, fmt.Sprintf("bounding box: lat[%.4f,%.4f] lon[%.4f,%.4f]", minLat, maxLat, minLon, maxLon))
	}
	if out.TotalReturned == 0 {
		hi = append(hi, "no features matched — try expanding radius or relaxing the tag value")
	}
	out.HighlightFindings = hi
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func topNAggregate(key string, counts map[string]int, n int) []OSMTagAggregate {
	res := make([]OSMTagAggregate, 0, len(counts))
	for v, c := range counts {
		res = append(res, OSMTagAggregate{Key: key, Value: v, Count: c})
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].Count > res[j].Count })
	if len(res) > n {
		res = res[:n]
	}
	return res
}
