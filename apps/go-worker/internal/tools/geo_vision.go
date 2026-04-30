package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// =============================================================================
// ip_geolocate — IP → city/country/ASN/ISP/timezone
// Uses ip-api.com (free, no key, 45/min) by default; if IP_API_KEY is set,
// uses ipapi.com pro endpoint instead.
// =============================================================================

type IPGeoOutput struct {
	IP           string  `json:"ip"`
	Country      string  `json:"country,omitempty"`
	CountryCode  string  `json:"country_code,omitempty"`
	Region       string  `json:"region,omitempty"`
	City         string  `json:"city,omitempty"`
	ZIP          string  `json:"zip,omitempty"`
	Latitude     float64 `json:"latitude,omitempty"`
	Longitude    float64 `json:"longitude,omitempty"`
	Timezone     string  `json:"timezone,omitempty"`
	ISP          string  `json:"isp,omitempty"`
	Organization string  `json:"organization,omitempty"`
	ASN          string  `json:"asn,omitempty"`
	Mobile       bool    `json:"mobile,omitempty"`
	Proxy        bool    `json:"proxy,omitempty"`
	Hosting      bool    `json:"hosting,omitempty"`
	Source       string  `json:"source"`
	TookMs       int64   `json:"tookMs"`
}

func IPGeolocate(ctx context.Context, input map[string]any) (*IPGeoOutput, error) {
	ip, _ := input["ip"].(string)
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return nil, errors.New("input.ip required")
	}
	start := time.Now()

	// Free path: ip-api.com — generous rate limit, comprehensive fields, no key.
	if os.Getenv("IP_API_KEY") == "" {
		endpoint := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,message,country,countryCode,region,regionName,city,zip,lat,lon,timezone,isp,org,as,mobile,proxy,hosting,query", url.PathEscape(ip))
		body, err := httpGetJSON(ctx, endpoint, 10*time.Second)
		if err != nil {
			return nil, fmt.Errorf("ip-api.com: %w", err)
		}
		var p struct {
			Status      string  `json:"status"`
			Message     string  `json:"message"`
			Country     string  `json:"country"`
			CountryCode string  `json:"countryCode"`
			RegionName  string  `json:"regionName"`
			City        string  `json:"city"`
			ZIP         string  `json:"zip"`
			Lat         float64 `json:"lat"`
			Lon         float64 `json:"lon"`
			Timezone    string  `json:"timezone"`
			ISP         string  `json:"isp"`
			Org         string  `json:"org"`
			ASN         string  `json:"as"`
			Mobile      bool    `json:"mobile"`
			Proxy       bool    `json:"proxy"`
			Hosting     bool    `json:"hosting"`
			Query       string  `json:"query"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("ip-api parse: %w", err)
		}
		if p.Status != "success" {
			return nil, fmt.Errorf("ip-api: %s", p.Message)
		}
		return &IPGeoOutput{
			IP: p.Query, Country: p.Country, CountryCode: p.CountryCode,
			Region: p.RegionName, City: p.City, ZIP: p.ZIP,
			Latitude: p.Lat, Longitude: p.Lon, Timezone: p.Timezone,
			ISP: p.ISP, Organization: p.Org, ASN: p.ASN,
			Mobile: p.Mobile, Proxy: p.Proxy, Hosting: p.Hosting,
			Source: "ip-api.com (free)", TookMs: time.Since(start).Milliseconds(),
		}, nil
	}

	// Paid path: ipapi.com — better rate limit, security flags.
	endpoint := fmt.Sprintf("https://api.ipapi.com/api/%s?access_key=%s&security=1",
		url.PathEscape(ip), url.QueryEscape(os.Getenv("IP_API_KEY")))
	body, err := httpGetJSON(ctx, endpoint, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("ipapi.com: %w", err)
	}
	var p struct {
		IP           string  `json:"ip"`
		Country      string  `json:"country_name"`
		CountryCode  string  `json:"country_code"`
		Region       string  `json:"region_name"`
		City         string  `json:"city"`
		ZIP          string  `json:"zip"`
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		TimeZone     struct {
			ID string `json:"id"`
		} `json:"time_zone"`
		Connection struct {
			ASN int    `json:"asn"`
			ISP string `json:"isp"`
		} `json:"connection"`
		Security struct {
			IsProxy   bool `json:"is_proxy"`
			IsCrawler bool `json:"is_crawler"`
			IsTor     bool `json:"is_tor"`
		} `json:"security"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("ipapi.com parse: %w", err)
	}
	return &IPGeoOutput{
		IP: p.IP, Country: p.Country, CountryCode: p.CountryCode,
		Region: p.Region, City: p.City, ZIP: p.ZIP,
		Latitude: p.Latitude, Longitude: p.Longitude, Timezone: p.TimeZone.ID,
		ISP: p.Connection.ISP, ASN: fmt.Sprintf("AS%d", p.Connection.ASN),
		Proxy: p.Security.IsProxy || p.Security.IsTor,
		Source: "ipapi.com (paid)", TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// google_places_search — find places by name/text (REQUIRES GOOGLE_MAPS_API_KEY)
// =============================================================================

type PlaceResult struct {
	Name             string   `json:"name"`
	FormattedAddress string   `json:"formatted_address,omitempty"`
	PlaceID          string   `json:"place_id,omitempty"`
	Rating           float64  `json:"rating,omitempty"`
	UserRatingsTotal int      `json:"user_ratings_total,omitempty"`
	Types            []string `json:"types,omitempty"`
	BusinessStatus   string   `json:"business_status,omitempty"`
	Latitude         float64  `json:"latitude,omitempty"`
	Longitude        float64  `json:"longitude,omitempty"`
	Website          string   `json:"website,omitempty"`
	GoogleMapsURL    string   `json:"google_maps_url,omitempty"`
}

type PlacesOutput struct {
	Query   string        `json:"query"`
	Count   int           `json:"count"`
	Results []PlaceResult `json:"results"`
	Source  string        `json:"source"`
	TookMs  int64         `json:"tookMs"`
}

func GooglePlacesSearch(ctx context.Context, input map[string]any) (*PlacesOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	key := os.Getenv("GOOGLE_MAPS_API_KEY")
	if key == "" {
		key = os.Getenv("GOOGLE_API_KEY")
	}
	if key == "" {
		return nil, errors.New("GOOGLE_MAPS_API_KEY (or GOOGLE_API_KEY) env var required")
	}

	start := time.Now()
	endpoint := fmt.Sprintf("https://maps.googleapis.com/maps/api/place/textsearch/json?query=%s&key=%s",
		url.QueryEscape(q), url.QueryEscape(key))
	body, err := httpGetJSON(ctx, endpoint, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("google places: %w", err)
	}
	var resp struct {
		Status  string `json:"status"`
		Message string `json:"error_message"`
		Results []struct {
			Name             string   `json:"name"`
			FormattedAddress string   `json:"formatted_address"`
			PlaceID          string   `json:"place_id"`
			Rating           float64  `json:"rating"`
			UserRatingsTotal int      `json:"user_ratings_total"`
			Types            []string `json:"types"`
			BusinessStatus   string   `json:"business_status"`
			Geometry         struct {
				Location struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"location"`
			} `json:"geometry"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("google places parse: %w", err)
	}
	if resp.Status != "OK" && resp.Status != "ZERO_RESULTS" {
		return nil, fmt.Errorf("google places %s: %s", resp.Status, resp.Message)
	}
	out := &PlacesOutput{
		Query: q, Source: "maps.googleapis.com/place/textsearch",
		TookMs: time.Since(start).Milliseconds(),
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, PlaceResult{
			Name: r.Name, FormattedAddress: r.FormattedAddress, PlaceID: r.PlaceID,
			Rating: r.Rating, UserRatingsTotal: r.UserRatingsTotal,
			Types: r.Types, BusinessStatus: r.BusinessStatus,
			Latitude: r.Geometry.Location.Lat, Longitude: r.Geometry.Location.Lng,
			GoogleMapsURL: "https://www.google.com/maps/place/?q=place_id:" + r.PlaceID,
		})
	}
	out.Count = len(out.Results)
	return out, nil
}

// =============================================================================
// openai_vision_describe — describe an image using OpenAI Vision-LM
// (REQUIRES OPENAI_API_KEY)
// =============================================================================

type OpenAIVisionOutput struct {
	URL         string `json:"url"`
	Description string `json:"description"`
	Model       string `json:"model"`
	Source      string `json:"source"`
	TookMs      int64  `json:"tookMs"`
}

// OpenAIVisionDescribe sends an image (URL) to gpt-4o-mini (or chosen model)
// and returns a free-form description. Tunable system prompt for OSINT-specific
// extraction (text in image, identifiable landmarks, faces, etc.).
func OpenAIVisionDescribe(ctx context.Context, input map[string]any) (*OpenAIVisionOutput, error) {
	imageURL, _ := input["url"].(string)
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return nil, errors.New("input.url required (image URL)")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, errors.New("OPENAI_API_KEY env var required")
	}
	model := "gpt-4o-mini"
	if v, ok := input["model"].(string); ok && v != "" {
		model = v
	}
	prompt := "Describe this image in detail. Note any visible text (verbatim), identifiable landmarks or locations, distinguishing features, faces (gender/age/expression — DO NOT identify specific individuals), brands/logos, license plates (verbatim), timestamps. Be specific and factual."
	if v, ok := input["prompt"].(string); ok && v != "" {
		prompt = v
	}
	maxTokens := 800
	if v, ok := input["max_tokens"].(float64); ok && v > 0 {
		maxTokens = int(v)
	}

	start := time.Now()
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{{
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": map[string]string{"url": imageURL}},
			},
		}},
		"max_tokens": maxTokens,
	})
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai vision: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai vision %d: %s", resp.StatusCode, truncate(string(rb), 240))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, fmt.Errorf("openai vision parse: %w", err)
	}
	desc := ""
	if len(parsed.Choices) > 0 {
		desc = parsed.Choices[0].Message.Content
	}
	return &OpenAIVisionOutput{
		URL: imageURL, Description: desc, Model: parsed.Model,
		Source: "api.openai.com/v1/chat/completions",
		TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// google_vision_analyze — labels + safe-search + faces + text via Google Vision
// (REQUIRES GOOGLE_API_KEY)
// =============================================================================

type GoogleVisionOutput struct {
	URL          string                   `json:"url"`
	Labels       []map[string]interface{} `json:"labels,omitempty"`
	SafeSearch   map[string]string        `json:"safe_search,omitempty"`
	Faces        []map[string]interface{} `json:"faces,omitempty"`
	Text         string                   `json:"text,omitempty"`
	Landmarks    []map[string]interface{} `json:"landmarks,omitempty"`
	Logos        []map[string]interface{} `json:"logos,omitempty"`
	Source       string                   `json:"source"`
	TookMs       int64                    `json:"tookMs"`
}

func GoogleVisionAnalyze(ctx context.Context, input map[string]any) (*GoogleVisionOutput, error) {
	imageURL, _ := input["url"].(string)
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return nil, errors.New("input.url required (image URL)")
	}
	key := os.Getenv("GOOGLE_API_KEY")
	if key == "" {
		return nil, errors.New("GOOGLE_API_KEY env var required (Google Vision API)")
	}

	start := time.Now()
	requestBody, _ := json.Marshal(map[string]interface{}{
		"requests": []map[string]interface{}{{
			"image": map[string]interface{}{"source": map[string]string{"imageUri": imageURL}},
			"features": []map[string]interface{}{
				{"type": "LABEL_DETECTION", "maxResults": 15},
				{"type": "SAFE_SEARCH_DETECTION"},
				{"type": "FACE_DETECTION", "maxResults": 10},
				{"type": "TEXT_DETECTION"},
				{"type": "LANDMARK_DETECTION", "maxResults": 5},
				{"type": "LOGO_DETECTION", "maxResults": 5},
			},
		}},
	})
	endpoint := "https://vision.googleapis.com/v1/images:annotate?key=" + url.QueryEscape(key)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google vision: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("google vision %d: %s", resp.StatusCode, truncate(string(rb), 240))
	}
	var parsed struct {
		Responses []struct {
			LabelAnnotations    []map[string]interface{} `json:"labelAnnotations"`
			SafeSearch          map[string]string        `json:"safeSearchAnnotation"`
			FaceAnnotations     []map[string]interface{} `json:"faceAnnotations"`
			TextAnnotations     []struct {
				Description string `json:"description"`
			} `json:"textAnnotations"`
			LandmarkAnnotations []map[string]interface{} `json:"landmarkAnnotations"`
			LogoAnnotations     []map[string]interface{} `json:"logoAnnotations"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, fmt.Errorf("google vision parse: %w", err)
	}
	out := &GoogleVisionOutput{URL: imageURL, Source: "vision.googleapis.com",
		TookMs: time.Since(start).Milliseconds()}
	if len(parsed.Responses) > 0 {
		r := parsed.Responses[0]
		out.Labels = r.LabelAnnotations
		out.SafeSearch = r.SafeSearch
		out.Faces = r.FaceAnnotations
		out.Landmarks = r.LandmarkAnnotations
		out.Logos = r.LogoAnnotations
		if len(r.TextAnnotations) > 0 {
			out.Text = r.TextAnnotations[0].Description
		}
	}
	return out, nil
}
