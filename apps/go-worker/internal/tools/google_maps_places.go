package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// GMapsPlace is one place result.
type GMapsPlace struct {
	PlaceID         string   `json:"place_id"`
	Name            string   `json:"name"`
	FormattedAddress string  `json:"formatted_address,omitempty"`
	Latitude        float64  `json:"latitude,omitempty"`
	Longitude       float64  `json:"longitude,omitempty"`
	Rating          float64  `json:"rating,omitempty"`
	UserRatingsTotal int     `json:"user_ratings_total,omitempty"`
	Types           []string `json:"types,omitempty"`
	BusinessStatus  string   `json:"business_status,omitempty"`
	PriceLevel      int      `json:"price_level,omitempty"`
	GoogleMapsURL   string   `json:"google_maps_url,omitempty"`
}

// GMapsReview is one user review.
type GMapsReview struct {
	AuthorName  string  `json:"author_name"`
	AuthorURL   string  `json:"author_url,omitempty"`
	Rating      float64 `json:"rating"`
	Text        string  `json:"text,omitempty"`
	RelativeTime string `json:"relative_time,omitempty"`
	Time        int64   `json:"time,omitempty"`
	Language    string  `json:"language,omitempty"`
}

// GMapsPlaceDetail is the full place record.
type GMapsPlaceDetail struct {
	GMapsPlace
	FormattedPhone     string        `json:"formatted_phone,omitempty"`
	InternationalPhone string        `json:"international_phone,omitempty"`
	Website            string        `json:"website,omitempty"`
	WeekdayHours       []string      `json:"weekday_hours,omitempty"`
	IsOpenNow          bool          `json:"is_open_now,omitempty"`
	Reviews            []GMapsReview `json:"reviews,omitempty"`
	PhotoRefs          []string      `json:"photo_refs,omitempty"` // photo_reference IDs
	PhotoURLs          []string      `json:"photo_urls,omitempty"` // direct image URLs (signed)
	UTCOffset          int           `json:"utc_offset_minutes,omitempty"`
}

// GoogleMapsPlacesOutput is the response.
type GoogleMapsPlacesOutput struct {
	Mode              string             `json:"mode"`
	Query             string             `json:"query,omitempty"`
	PlaceID           string             `json:"place_id,omitempty"`
	Lat               float64            `json:"lat,omitempty"`
	Lon               float64            `json:"lon,omitempty"`
	RadiusM           int                `json:"radius_m,omitempty"`
	Type              string             `json:"type,omitempty"`
	Places            []GMapsPlace       `json:"places,omitempty"`
	Detail            *GMapsPlaceDetail  `json:"detail,omitempty"`
	UniqueCategories  []string           `json:"unique_categories,omitempty"`
	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
	Note              string             `json:"note,omitempty"`
}

// raw response shapes
type gmapsTextSearchRaw struct {
	Status  string `json:"status"`
	Results []struct {
		PlaceID          string   `json:"place_id"`
		Name             string   `json:"name"`
		FormattedAddress string   `json:"formatted_address"`
		Geometry         struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
		} `json:"geometry"`
		Rating           float64  `json:"rating"`
		UserRatingsTotal int      `json:"user_ratings_total"`
		Types            []string `json:"types"`
		BusinessStatus   string   `json:"business_status"`
		PriceLevel       int      `json:"price_level"`
	} `json:"results"`
	ErrorMessage string `json:"error_message"`
}

type gmapsDetailsRaw struct {
	Status string `json:"status"`
	Result struct {
		PlaceID                  string   `json:"place_id"`
		Name                     string   `json:"name"`
		FormattedAddress         string   `json:"formatted_address"`
		FormattedPhoneNumber     string   `json:"formatted_phone_number"`
		InternationalPhoneNumber string   `json:"international_phone_number"`
		Website                  string   `json:"website"`
		Rating                   float64  `json:"rating"`
		UserRatingsTotal         int      `json:"user_ratings_total"`
		Types                    []string `json:"types"`
		URL                      string   `json:"url"`
		UTCOffset                int      `json:"utc_offset"`
		BusinessStatus           string   `json:"business_status"`
		Geometry struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
		} `json:"geometry"`
		OpeningHours struct {
			OpenNow     bool     `json:"open_now"`
			WeekdayText []string `json:"weekday_text"`
		} `json:"opening_hours"`
		Reviews []struct {
			AuthorName               string  `json:"author_name"`
			AuthorURL                string  `json:"author_url"`
			Rating                   float64 `json:"rating"`
			Text                     string  `json:"text"`
			Time                     int64   `json:"time"`
			RelativeTimeDescription  string  `json:"relative_time_description"`
			Language                 string  `json:"language"`
		} `json:"reviews"`
		Photos []struct {
			PhotoReference   string `json:"photo_reference"`
			Width            int    `json:"width"`
			Height           int    `json:"height"`
			HTMLAttributions []string `json:"html_attributions"`
		} `json:"photos"`
	} `json:"result"`
	ErrorMessage string `json:"error_message"`
}

// GoogleMapsPlaces queries Google Maps Places API for geographic OSINT.
// Three modes:
//   - "text_search"   : keyword query + optional location bias → places
//   - "place_details" : full record by place_id (phone/website/reviews/photos)
//   - "nearby_search" : find POIs near a lat/lng with optional type filter
//
// Why this matters for ER:
//   - Identifies businesses by name+city: full address + phone + website
//     + rating + reviews + photos.
//   - Reviews are SOURCE-OF-TRUTH for negative-press / dispute signals
//     (e.g. a ★1 review accusing a company of non-payment is a real
//     OSINT artifact).
//   - Phone numbers from this surface tie naturally into OSINT chains —
//     verified business phone is rare in other indices.
//   - place_id is a stable Google identifier — re-queryable forever.
//   - Pairs with osm_overpass_query (free, broader OSM data) and
//     nominatim_geocode (free address → coords).
//
// REQUIRES GOOGLE_MAPS_API_KEY.
func GoogleMapsPlaces(ctx context.Context, input map[string]any) (*GoogleMapsPlacesOutput, error) {
	apiKey := os.Getenv("GOOGLE_MAPS_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_MAPS_API_KEY env var required")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "text_search"
	}

	out := &GoogleMapsPlacesOutput{
		Mode:   mode,
		Source: "maps.googleapis.com/maps/api/place",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	switch mode {
	case "text_search":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.query required for text_search")
		}
		out.Query = q
		params := url.Values{}
		params.Set("query", q)
		params.Set("key", apiKey)
		// Optional location bias
		if lat, ok := input["lat"].(float64); ok {
			if lon, ok2 := input["lon"].(float64); ok2 {
				params.Set("location", fmt.Sprintf("%f,%f", lat, lon))
				out.Lat, out.Lon = lat, lon
				if r, ok := input["radius_m"].(float64); ok && r > 0 {
					params.Set("radius", fmt.Sprintf("%.0f", r))
					out.RadiusM = int(r)
				}
			}
		}
		raw, err := gmapsFetchTextSearch(ctx, cli, params)
		if err != nil {
			return nil, err
		}
		for _, r := range raw.Results {
			out.Places = append(out.Places, GMapsPlace{
				PlaceID:          r.PlaceID,
				Name:             r.Name,
				FormattedAddress: r.FormattedAddress,
				Latitude:         r.Geometry.Location.Lat,
				Longitude:        r.Geometry.Location.Lng,
				Rating:           r.Rating,
				UserRatingsTotal: r.UserRatingsTotal,
				Types:            r.Types,
				BusinessStatus:   r.BusinessStatus,
				PriceLevel:       r.PriceLevel,
				GoogleMapsURL:    "https://maps.google.com/?cid=" + r.PlaceID,
			})
		}

	case "place_details":
		pid, _ := input["place_id"].(string)
		pid = strings.TrimSpace(pid)
		if pid == "" {
			return nil, fmt.Errorf("input.place_id required for place_details")
		}
		out.PlaceID = pid
		params := url.Values{}
		params.Set("place_id", pid)
		params.Set("fields", "name,formatted_address,formatted_phone_number,international_phone_number,website,rating,user_ratings_total,opening_hours,types,url,reviews,photos,geometry,business_status,utc_offset")
		params.Set("key", apiKey)
		raw, err := gmapsFetchDetails(ctx, cli, params)
		if err != nil {
			return nil, err
		}
		r := raw.Result
		detail := &GMapsPlaceDetail{
			GMapsPlace: GMapsPlace{
				PlaceID:          r.PlaceID,
				Name:             r.Name,
				FormattedAddress: r.FormattedAddress,
				Latitude:         r.Geometry.Location.Lat,
				Longitude:        r.Geometry.Location.Lng,
				Rating:           r.Rating,
				UserRatingsTotal: r.UserRatingsTotal,
				Types:            r.Types,
				BusinessStatus:   r.BusinessStatus,
				GoogleMapsURL:    r.URL,
			},
			FormattedPhone:     r.FormattedPhoneNumber,
			InternationalPhone: r.InternationalPhoneNumber,
			Website:            r.Website,
			WeekdayHours:       r.OpeningHours.WeekdayText,
			IsOpenNow:          r.OpeningHours.OpenNow,
			UTCOffset:          r.UTCOffset,
		}
		for _, rv := range r.Reviews {
			detail.Reviews = append(detail.Reviews, GMapsReview{
				AuthorName:   rv.AuthorName,
				AuthorURL:    rv.AuthorURL,
				Rating:       rv.Rating,
				Text:         hfTruncate(rv.Text, 600),
				RelativeTime: rv.RelativeTimeDescription,
				Time:         rv.Time,
				Language:     rv.Language,
			})
		}
		for _, p := range r.Photos {
			detail.PhotoRefs = append(detail.PhotoRefs, p.PhotoReference)
			// Construct direct photo URL (max 800px width)
			photoURL := fmt.Sprintf("https://maps.googleapis.com/maps/api/place/photo?maxwidth=800&photo_reference=%s&key=%s", p.PhotoReference, apiKey)
			detail.PhotoURLs = append(detail.PhotoURLs, photoURL)
		}
		out.Detail = detail

	case "nearby_search":
		lat, _ := input["lat"].(float64)
		lon, _ := input["lon"].(float64)
		if lat == 0 && lon == 0 {
			return nil, fmt.Errorf("input.lat and input.lon required for nearby_search")
		}
		out.Lat, out.Lon = lat, lon
		radius := 1000.0
		if r, ok := input["radius_m"].(float64); ok && r > 0 {
			radius = r
		}
		out.RadiusM = int(radius)
		placeType, _ := input["type"].(string)
		placeType = strings.TrimSpace(placeType)
		out.Type = placeType

		params := url.Values{}
		params.Set("location", fmt.Sprintf("%f,%f", lat, lon))
		params.Set("radius", fmt.Sprintf("%.0f", radius))
		if placeType != "" {
			params.Set("type", placeType)
		}
		if kw, ok := input["keyword"].(string); ok {
			kw = strings.TrimSpace(kw)
			if kw != "" {
				params.Set("keyword", kw)
				out.Query = kw
			}
		}
		params.Set("key", apiKey)
		// nearbysearch endpoint uses different URL
		endpoint := "https://maps.googleapis.com/maps/api/place/nearbysearch/json?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/0.1")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gmaps nearby: %w", err)
		}
		defer resp.Body.Close()
		var raw gmapsTextSearchRaw
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, err
		}
		if raw.Status != "OK" && raw.Status != "ZERO_RESULTS" {
			return nil, fmt.Errorf("gmaps: %s — %s", raw.Status, raw.ErrorMessage)
		}
		for _, r := range raw.Results {
			out.Places = append(out.Places, GMapsPlace{
				PlaceID:          r.PlaceID,
				Name:             r.Name,
				FormattedAddress: r.FormattedAddress,
				Latitude:         r.Geometry.Location.Lat,
				Longitude:        r.Geometry.Location.Lng,
				Rating:           r.Rating,
				UserRatingsTotal: r.UserRatingsTotal,
				Types:            r.Types,
				BusinessStatus:   r.BusinessStatus,
				PriceLevel:       r.PriceLevel,
			})
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: text_search, place_details, nearby_search", mode)
	}

	// Aggregations
	catSet := map[string]struct{}{}
	for _, p := range out.Places {
		for _, t := range p.Types {
			catSet[t] = struct{}{}
		}
	}
	if out.Detail != nil {
		for _, t := range out.Detail.Types {
			catSet[t] = struct{}{}
		}
	}
	for c := range catSet {
		out.UniqueCategories = append(out.UniqueCategories, c)
	}
	sort.Strings(out.UniqueCategories)

	out.HighlightFindings = buildGMapsHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func gmapsFetchTextSearch(ctx context.Context, cli *http.Client, params url.Values) (*gmapsTextSearchRaw, error) {
	endpoint := "https://maps.googleapis.com/maps/api/place/textsearch/json?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmaps textsearch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var raw gmapsTextSearchRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if raw.Status != "OK" && raw.Status != "ZERO_RESULTS" {
		return nil, fmt.Errorf("gmaps: %s — %s", raw.Status, raw.ErrorMessage)
	}
	return &raw, nil
}

func gmapsFetchDetails(ctx context.Context, cli *http.Client, params url.Values) (*gmapsDetailsRaw, error) {
	endpoint := "https://maps.googleapis.com/maps/api/place/details/json?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmaps details: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var raw gmapsDetailsRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if raw.Status != "OK" {
		return nil, fmt.Errorf("gmaps: %s — %s", raw.Status, raw.ErrorMessage)
	}
	return &raw, nil
}

func buildGMapsHighlights(o *GoogleMapsPlacesOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "text_search":
		hi = append(hi, fmt.Sprintf("✓ %d places match '%s'", len(o.Places), o.Query))
		for i, p := range o.Places {
			if i >= 5 {
				break
			}
			ratingStr := ""
			if p.Rating > 0 {
				ratingStr = fmt.Sprintf(" ★%.1f (%d)", p.Rating, p.UserRatingsTotal)
			}
			hi = append(hi, fmt.Sprintf("  • %s%s — %s", p.Name, ratingStr, p.FormattedAddress))
		}
	case "place_details":
		if o.Detail == nil {
			break
		}
		d := o.Detail
		hi = append(hi, fmt.Sprintf("✓ %s — %s", d.Name, d.FormattedAddress))
		if d.FormattedPhone != "" {
			hi = append(hi, "📞 phone: "+d.FormattedPhone)
		}
		if d.Website != "" {
			hi = append(hi, "🌐 website: "+d.Website)
		}
		if d.Rating > 0 {
			hi = append(hi, fmt.Sprintf("⭐ rating: %.1f (%d reviews)", d.Rating, d.UserRatingsTotal))
		}
		if d.BusinessStatus != "" {
			hi = append(hi, "status: "+d.BusinessStatus)
		}
		if len(d.WeekdayHours) > 0 {
			hi = append(hi, "🕐 hours: "+strings.Join(d.WeekdayHours, " | "))
		}
		hi = append(hi, fmt.Sprintf("📷 %d photos available (signed URLs in photo_urls)", len(d.PhotoRefs)))
		if len(d.Reviews) > 0 {
			hi = append(hi, fmt.Sprintf("💬 %d sample reviews:", len(d.Reviews)))
			// Surface negative reviews specifically (★1 / ★2)
			negatives := 0
			for _, rv := range d.Reviews {
				if rv.Rating <= 2 {
					negatives++
				}
			}
			if negatives > 0 {
				hi = append(hi, fmt.Sprintf("  ⚠️  %d negative reviews (★1-2) — potential dispute/complaint signal", negatives))
			}
			for i, rv := range d.Reviews {
				if i >= 3 {
					break
				}
				hi = append(hi, fmt.Sprintf("  ★%.0f by %s: %s", rv.Rating, rv.AuthorName, hfTruncate(rv.Text, 100)))
			}
		}
	case "nearby_search":
		hi = append(hi, fmt.Sprintf("✓ %d places within %dm of (%.4f, %.4f)", len(o.Places), o.RadiusM, o.Lat, o.Lon))
		if o.Type != "" {
			hi = append(hi, "type filter: "+o.Type)
		}
		for i, p := range o.Places {
			if i >= 8 {
				break
			}
			ratingStr := ""
			if p.Rating > 0 {
				ratingStr = fmt.Sprintf(" ★%.1f", p.Rating)
			}
			hi = append(hi, fmt.Sprintf("  • %s%s — %s", p.Name, ratingStr, p.FormattedAddress))
		}
	}
	if len(o.UniqueCategories) > 0 && len(o.UniqueCategories) <= 12 {
		hi = append(hi, "categories: "+strings.Join(o.UniqueCategories, ", "))
	}
	return hi
}
