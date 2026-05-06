package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// SentinelHubImagery wraps the Sentinel Hub Process API
// (services.sentinel-hub.com). Free tier available.
//
// REQUIRES `SENTINEL_HUB_CLIENT_ID` and `SENTINEL_HUB_CLIENT_SECRET`
// (Copernicus / Sentinel Hub OAuth client credentials).
//
// Use cases: satellite imagery for OSINT geolocation verification,
// before/after comparison of damaged sites, vegetation/water analysis,
// nighttime lights for activity inference.
//
// Modes:
//   - "true_color"      : RGB true-color image of a bbox at a given date
//   - "ndvi"            : Normalized Difference Vegetation Index
//   - "ndwi"            : Normalized Difference Water Index
//   - "available_dates" : list available dates for a bbox (Sentinel-2 swath catalog)
//
// Knowledge-graph: emits typed entity (kind: "satellite_image") with
// bbox + date + product_type attributes; image returned as data:image/png;base64.

type SHEntity struct {
	Kind        string         `json:"kind"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type SentinelHubOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query"`
	BBox              []float64  `json:"bbox,omitempty"` // [minLon, minLat, maxLon, maxLat]
	Date              string     `json:"date,omitempty"`
	ImageDataURL      string     `json:"image_data_url,omitempty"` // data:image/png;base64,...
	AvailableDates    []string   `json:"available_dates,omitempty"`
	Entities          []SHEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func SentinelHubImagery(ctx context.Context, input map[string]any) (*SentinelHubOutput, error) {
	clientID := os.Getenv("SENTINEL_HUB_CLIENT_ID")
	clientSecret := os.Getenv("SENTINEL_HUB_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("SENTINEL_HUB_CLIENT_ID + SENTINEL_HUB_CLIENT_SECRET required (free tier at sentinel-hub.com)")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "true_color"
	}
	out := &SentinelHubOutput{Mode: mode, Source: "services.sentinel-hub.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 90 * time.Second}

	// Step 1: OAuth client-credentials grant
	tokenForm := url.Values{
		"grant_type":    []string{"client_credentials"},
		"client_id":     []string{clientID},
		"client_secret": []string{clientSecret},
	}
	tokReq, _ := http.NewRequestWithContext(ctx, "POST",
		"https://services.sentinel-hub.com/oauth/token",
		strings.NewReader(tokenForm.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokResp, err := cli.Do(tokReq)
	if err != nil {
		return nil, fmt.Errorf("sentinelhub oauth: %w", err)
	}
	tokBody, _ := io.ReadAll(io.LimitReader(tokResp.Body, 4<<20))
	tokResp.Body.Close()
	if tokResp.StatusCode != 200 {
		return nil, fmt.Errorf("sentinelhub oauth HTTP %d: %s", tokResp.StatusCode, hfTruncate(string(tokBody), 200))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(tokBody, &tok); err != nil {
		return nil, fmt.Errorf("sentinelhub oauth decode: %w", err)
	}

	// BBox
	var bbox []float64
	if b, ok := input["bbox"].([]any); ok && len(b) == 4 {
		for _, x := range b {
			if f, ok := x.(float64); ok {
				bbox = append(bbox, f)
			}
		}
	}
	if len(bbox) != 4 {
		return nil, fmt.Errorf("input.bbox required: [minLon, minLat, maxLon, maxLat]")
	}
	out.BBox = bbox
	dateFrom, _ := input["date_from"].(string)
	dateTo, _ := input["date_to"].(string)
	if dateFrom == "" {
		dateFrom = "2024-01-01T00:00:00Z"
	}
	if dateTo == "" {
		dateTo = time.Now().UTC().Format(time.RFC3339)
	}
	out.Date = dateFrom + "/" + dateTo
	out.Query = fmt.Sprintf("bbox=%v date=%s", bbox, out.Date)

	switch mode {
	case "true_color", "ndvi", "ndwi":
		evalscript := evalscriptFor(mode)
		body := map[string]any{
			"input": map[string]any{
				"bounds": map[string]any{
					"bbox": bbox,
					"properties": map[string]any{
						"crs": "http://www.opengis.net/def/crs/EPSG/0/4326",
					},
				},
				"data": []map[string]any{{
					"type": "S2L2A",
					"dataFilter": map[string]any{
						"timeRange": map[string]any{
							"from": dateFrom,
							"to":   dateTo,
						},
						"maxCloudCoverage": 30,
					},
				}},
			},
			"output": map[string]any{
				"width":  512,
				"height": 512,
				"responses": []map[string]any{{
					"identifier": "default",
					"format":     map[string]any{"type": "image/png"},
				}},
			},
			"evalscript": evalscript,
		}
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, "POST",
			"https://services.sentinel-hub.com/api/v1/process",
			bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "image/png")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sentinelhub process: %w", err)
		}
		defer resp.Body.Close()
		imgBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("sentinelhub process HTTP %d: %s", resp.StatusCode, hfTruncate(string(imgBody), 200))
		}
		out.ImageDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBody)
	case "available_dates":
		body := map[string]any{
			"datasetId": "S2L2A",
			"bbox":      bbox,
			"from":      dateFrom,
			"to":        dateTo,
		}
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, "POST",
			"https://services.sentinel-hub.com/api/v1/dataimport/dates",
			bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sentinelhub dates: %w", err)
		}
		defer resp.Body.Close()
		dateBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("sentinelhub dates HTTP %d", resp.StatusCode)
		}
		var arr []string
		_ = json.Unmarshal(dateBody, &arr)
		out.AvailableDates = arr
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Entities = []SHEntity{{
		Kind: "satellite_image",
		Name: fmt.Sprintf("%s @ bbox %v", mode, bbox),
		Date: out.Date,
		Attributes: map[string]any{
			"bbox":            bbox,
			"product":         mode,
			"available_dates": len(out.AvailableDates),
			"has_image":       out.ImageDataURL != "",
		},
	}}
	hi := []string{fmt.Sprintf("✓ sentinelhub %s: bbox=%v date=%s", mode, bbox, out.Date)}
	if out.ImageDataURL != "" {
		hi = append(hi, fmt.Sprintf("  • image returned (%d bytes base64)", len(out.ImageDataURL)))
	}
	if len(out.AvailableDates) > 0 {
		preview := out.AvailableDates
		if len(preview) > 6 {
			preview = preview[:6]
		}
		hi = append(hi, fmt.Sprintf("  • %d available dates: %s …", len(out.AvailableDates), strings.Join(preview, ", ")))
	}
	out.HighlightFindings = hi
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func evalscriptFor(mode string) string {
	switch mode {
	case "ndvi":
		return `//VERSION=3
function setup() { return { input: ["B04", "B08"], output: { bands: 3 } }; }
function evaluatePixel(s) {
  let v = (s.B08 - s.B04) / (s.B08 + s.B04);
  if (v < 0) return [0, 0, 1];
  if (v < 0.2) return [0.4, 0.2, 0];
  if (v < 0.5) return [0.7, 0.7, 0];
  return [0, 0.7, 0];
}`
	case "ndwi":
		return `//VERSION=3
function setup() { return { input: ["B03", "B08"], output: { bands: 3 } }; }
function evaluatePixel(s) {
  let v = (s.B03 - s.B08) / (s.B03 + s.B08);
  if (v > 0) return [0, 0, 0.8];
  return [0.3, 0.3, 0.3];
}`
	default:
		// true_color
		return `//VERSION=3
function setup() { return { input: ["B02", "B03", "B04"], output: { bands: 3 } }; }
function evaluatePixel(s) { return [s.B04 * 2.5, s.B03 * 2.5, s.B02 * 2.5]; }`
	}
}
