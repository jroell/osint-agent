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

// OpenMeteoSearch wraps Open-Meteo's free no-auth weather + air-quality
// API (Swiss nonprofit, independent infrastructure). Historical weather
// archive goes back to 1940. Pairs with `usgs_earthquake_search` for
// temporal-spatial forensic OSINT, and with `nominatim_geocode` /
// `census_geocoder` for address-to-coordinate lookups.
//
// Three modes:
//
//   - "current"     : live conditions at lat/lon — temperature, humidity,
//                      precipitation, wind speed/direction, cloud cover,
//                      weather code (WMO standard codes).
//   - "historical"  : date range (since 1940) → daily aggregates with
//                      max/min temperature, precipitation total, wind max,
//                      weather code. Useful for alibi verification ("was
//                      it raining at X on date Y?"), accident analysis,
//                      social-media post corroboration.
//   - "air_quality" : current pollutant levels (PM2.5, PM10, NO2, O3,
//                      SO2, CO) + US AQI + European AQI.

type OpenMeteoCurrentWeather struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	Elevation      float64 `json:"elevation_m"`
	Timezone       string  `json:"timezone,omitempty"`
	Time           string  `json:"time,omitempty"`
	TempF          float64 `json:"temperature_f"`
	HumidityPct    float64 `json:"relative_humidity_pct"`
	PrecipInches   float64 `json:"precipitation_in"`
	WindSpeedMph   float64 `json:"wind_speed_mph"`
	WindDir        float64 `json:"wind_direction_deg"`
	CloudCoverPct  float64 `json:"cloud_cover_pct"`
	WeatherCode    int     `json:"weather_code"`
	WeatherDesc    string  `json:"weather_description,omitempty"`
}

type OpenMeteoDailyEntry struct {
	Date         string  `json:"date"`
	TempMaxF     float64 `json:"temperature_max_f"`
	TempMinF     float64 `json:"temperature_min_f"`
	PrecipIn     float64 `json:"precipitation_total_in"`
	WindMaxMph   float64 `json:"wind_speed_max_mph"`
	WeatherCode  int     `json:"weather_code"`
	WeatherDesc  string  `json:"weather_description,omitempty"`
}

type OpenMeteoAirQuality struct {
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Time            string  `json:"time,omitempty"`
	PM10            float64 `json:"pm10_ug_m3"`
	PM25            float64 `json:"pm2_5_ug_m3"`
	CO              float64 `json:"carbon_monoxide_ug_m3"`
	NO2             float64 `json:"nitrogen_dioxide_ug_m3"`
	Ozone           float64 `json:"ozone_ug_m3"`
	SO2             float64 `json:"sulphur_dioxide_ug_m3"`
	USAqi           float64 `json:"us_aqi"`
	EuropeanAqi     float64 `json:"european_aqi"`
	USAqiCategory   string  `json:"us_aqi_category,omitempty"`
}

type OpenMeteoSearchOutput struct {
	Mode              string                 `json:"mode"`
	Query             string                 `json:"query,omitempty"`
	Current           *OpenMeteoCurrentWeather `json:"current,omitempty"`
	Historical        []OpenMeteoDailyEntry  `json:"historical,omitempty"`
	AirQuality        *OpenMeteoAirQuality   `json:"air_quality,omitempty"`

	HighlightFindings []string               `json:"highlight_findings"`
	Source            string                 `json:"source"`
	TookMs            int64                  `json:"tookMs"`
	Note              string                 `json:"note,omitempty"`
}

// WMO weather code → human-readable description
// https://open-meteo.com/en/docs (search "weather_code" in the docs)
var weatherCodes = map[int]string{
	0:  "Clear sky",
	1:  "Mainly clear",
	2:  "Partly cloudy",
	3:  "Overcast",
	45: "Fog",
	48: "Depositing rime fog",
	51: "Light drizzle",
	53: "Moderate drizzle",
	55: "Dense drizzle",
	56: "Light freezing drizzle",
	57: "Dense freezing drizzle",
	61: "Slight rain",
	63: "Moderate rain",
	65: "Heavy rain",
	66: "Light freezing rain",
	67: "Heavy freezing rain",
	71: "Slight snow",
	73: "Moderate snow",
	75: "Heavy snow",
	77: "Snow grains",
	80: "Slight rain showers",
	81: "Moderate rain showers",
	82: "Violent rain showers",
	85: "Slight snow showers",
	86: "Heavy snow showers",
	95: "Thunderstorm",
	96: "Thunderstorm with slight hail",
	99: "Thunderstorm with heavy hail",
}

func weatherCodeDesc(c int) string {
	if d, ok := weatherCodes[c]; ok {
		return d
	}
	return fmt.Sprintf("Code %d", c)
}

// US EPA AQI categories
func usAqiCategory(aqi float64) string {
	switch {
	case aqi <= 50:
		return "Good"
	case aqi <= 100:
		return "Moderate"
	case aqi <= 150:
		return "Unhealthy for Sensitive Groups"
	case aqi <= 200:
		return "Unhealthy"
	case aqi <= 300:
		return "Very Unhealthy"
	default:
		return "Hazardous"
	}
}

func OpenMeteoSearch(ctx context.Context, input map[string]any) (*OpenMeteoSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["start_date"]; ok {
			mode = "historical"
		} else {
			mode = "current"
		}
	}

	lat := getCensusFloat(input, "latitude", "lat")
	lon := getCensusFloat(input, "longitude", "lon", "lng")
	if lat == 0 && lon == 0 {
		return nil, fmt.Errorf("input.latitude (or lat) and input.longitude (or lon) required")
	}

	out := &OpenMeteoSearchOutput{
		Mode:   mode,
		Source: "open-meteo.com",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "current":
		params := url.Values{}
		params.Set("latitude", fmt.Sprintf("%g", lat))
		params.Set("longitude", fmt.Sprintf("%g", lon))
		params.Set("current", "temperature_2m,relative_humidity_2m,precipitation,wind_speed_10m,wind_direction_10m,cloud_cover,weather_code")
		params.Set("temperature_unit", "fahrenheit")
		params.Set("wind_speed_unit", "mph")
		params.Set("precipitation_unit", "inch")
		out.Query = fmt.Sprintf("current at %.4f,%.4f", lat, lon)

		body, err := openMeteoGet(ctx, cli, "https://api.open-meteo.com/v1/forecast?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Elevation float64 `json:"elevation"`
			Timezone  string  `json:"timezone"`
			Current   map[string]any `json:"current"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("openmeteo current decode: %w", err)
		}
		c := &OpenMeteoCurrentWeather{
			Latitude:      raw.Latitude,
			Longitude:     raw.Longitude,
			Elevation:     raw.Elevation,
			Timezone:      raw.Timezone,
			Time:          gtString(raw.Current, "time"),
			TempF:         gtFloat(raw.Current, "temperature_2m"),
			HumidityPct:   gtFloat(raw.Current, "relative_humidity_2m"),
			PrecipInches:  gtFloat(raw.Current, "precipitation"),
			WindSpeedMph:  gtFloat(raw.Current, "wind_speed_10m"),
			WindDir:       gtFloat(raw.Current, "wind_direction_10m"),
			CloudCoverPct: gtFloat(raw.Current, "cloud_cover"),
			WeatherCode:   gtInt(raw.Current, "weather_code"),
		}
		c.WeatherDesc = weatherCodeDesc(c.WeatherCode)
		out.Current = c

	case "historical":
		startDate, _ := input["start_date"].(string)
		endDate, _ := input["end_date"].(string)
		startDate = strings.TrimSpace(startDate)
		endDate = strings.TrimSpace(endDate)
		if startDate == "" {
			return nil, fmt.Errorf("input.start_date required for historical mode (YYYY-MM-DD)")
		}
		if endDate == "" {
			endDate = startDate
		}
		out.Query = fmt.Sprintf("historical at %.4f,%.4f from %s to %s", lat, lon, startDate, endDate)

		params := url.Values{}
		params.Set("latitude", fmt.Sprintf("%g", lat))
		params.Set("longitude", fmt.Sprintf("%g", lon))
		params.Set("start_date", startDate)
		params.Set("end_date", endDate)
		params.Set("daily", "temperature_2m_max,temperature_2m_min,precipitation_sum,wind_speed_10m_max,weather_code")
		params.Set("temperature_unit", "fahrenheit")
		params.Set("wind_speed_unit", "mph")
		params.Set("precipitation_unit", "inch")

		body, err := openMeteoGet(ctx, cli, "https://archive-api.open-meteo.com/v1/archive?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Daily struct {
				Time              []string  `json:"time"`
				TemperatureMax    []float64 `json:"temperature_2m_max"`
				TemperatureMin    []float64 `json:"temperature_2m_min"`
				PrecipitationSum  []float64 `json:"precipitation_sum"`
				WindSpeedMax      []float64 `json:"wind_speed_10m_max"`
				WeatherCode       []int     `json:"weather_code"`
			} `json:"daily"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("openmeteo historical decode: %w", err)
		}
		for i, date := range raw.Daily.Time {
			e := OpenMeteoDailyEntry{Date: date}
			if i < len(raw.Daily.TemperatureMax) {
				e.TempMaxF = raw.Daily.TemperatureMax[i]
			}
			if i < len(raw.Daily.TemperatureMin) {
				e.TempMinF = raw.Daily.TemperatureMin[i]
			}
			if i < len(raw.Daily.PrecipitationSum) {
				e.PrecipIn = raw.Daily.PrecipitationSum[i]
			}
			if i < len(raw.Daily.WindSpeedMax) {
				e.WindMaxMph = raw.Daily.WindSpeedMax[i]
			}
			if i < len(raw.Daily.WeatherCode) {
				e.WeatherCode = raw.Daily.WeatherCode[i]
				e.WeatherDesc = weatherCodeDesc(e.WeatherCode)
			}
			out.Historical = append(out.Historical, e)
		}

	case "air_quality":
		params := url.Values{}
		params.Set("latitude", fmt.Sprintf("%g", lat))
		params.Set("longitude", fmt.Sprintf("%g", lon))
		params.Set("current", "pm10,pm2_5,carbon_monoxide,nitrogen_dioxide,ozone,sulphur_dioxide,us_aqi,european_aqi")
		out.Query = fmt.Sprintf("air quality at %.4f,%.4f", lat, lon)

		body, err := openMeteoGet(ctx, cli, "https://air-quality-api.open-meteo.com/v1/air-quality?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Latitude  float64        `json:"latitude"`
			Longitude float64        `json:"longitude"`
			Current   map[string]any `json:"current"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("openmeteo air decode: %w", err)
		}
		aq := &OpenMeteoAirQuality{
			Latitude:    raw.Latitude,
			Longitude:   raw.Longitude,
			Time:        gtString(raw.Current, "time"),
			PM10:        gtFloat(raw.Current, "pm10"),
			PM25:        gtFloat(raw.Current, "pm2_5"),
			CO:          gtFloat(raw.Current, "carbon_monoxide"),
			NO2:         gtFloat(raw.Current, "nitrogen_dioxide"),
			Ozone:       gtFloat(raw.Current, "ozone"),
			SO2:         gtFloat(raw.Current, "sulphur_dioxide"),
			USAqi:       gtFloat(raw.Current, "us_aqi"),
			EuropeanAqi: gtFloat(raw.Current, "european_aqi"),
		}
		aq.USAqiCategory = usAqiCategory(aq.USAqi)
		out.AirQuality = aq

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: current, historical, air_quality", mode)
	}

	out.HighlightFindings = buildOpenMeteoHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func openMeteoGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openmeteo: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openmeteo HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildOpenMeteoHighlights(o *OpenMeteoSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "current":
		if o.Current == nil {
			break
		}
		c := o.Current
		hi = append(hi, fmt.Sprintf("✓ Current weather at %.4f,%.4f (elev %.0fm)", c.Latitude, c.Longitude, c.Elevation))
		hi = append(hi, fmt.Sprintf("  %.1f°F · %s · %.0f%% humidity · cloud %.0f%%", c.TempF, c.WeatherDesc, c.HumidityPct, c.CloudCoverPct))
		hi = append(hi, fmt.Sprintf("  wind %.1f mph @ %.0f° · precip %.2f in", c.WindSpeedMph, c.WindDir, c.PrecipInches))
		hi = append(hi, fmt.Sprintf("  observation time: %s (timezone %s)", c.Time, c.Timezone))

	case "historical":
		hi = append(hi, fmt.Sprintf("✓ %d daily records: %s", len(o.Historical), o.Query))
		display := o.Historical
		if len(display) > 8 {
			display = display[:8]
		}
		for _, e := range display {
			hi = append(hi, fmt.Sprintf("  • %s — high %.1f°F / low %.1f°F · %.2f in precip · wind %.1f mph max · %s",
				e.Date, e.TempMaxF, e.TempMinF, e.PrecipIn, e.WindMaxMph, e.WeatherDesc))
		}
		if len(o.Historical) > 8 {
			hi = append(hi, fmt.Sprintf("  …and %d more days", len(o.Historical)-8))
		}

	case "air_quality":
		if o.AirQuality == nil {
			break
		}
		a := o.AirQuality
		hi = append(hi, fmt.Sprintf("✓ Air quality at %.4f,%.4f", a.Latitude, a.Longitude))
		hi = append(hi, fmt.Sprintf("  US AQI %.0f (%s) · European AQI %.0f", a.USAqi, a.USAqiCategory, a.EuropeanAqi))
		hi = append(hi, fmt.Sprintf("  pollutants (μg/m³): PM2.5 %.1f · PM10 %.1f · NO₂ %.1f · O₃ %.1f · SO₂ %.1f · CO %.0f",
			a.PM25, a.PM10, a.NO2, a.Ozone, a.SO2, a.CO))
		hi = append(hi, fmt.Sprintf("  observation time: %s", a.Time))
	}
	return hi
}
