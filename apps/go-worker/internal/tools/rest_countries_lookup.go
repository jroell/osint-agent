package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// RestCountriesLookup wraps the REST Countries v3.1 free no-auth API.
// 250+ country records with rich reference data — capitals, currencies,
// languages, neighboring-country borders, area, population, calling
// codes, timezones, demonyms, FIFA codes, driving side, continents,
// Google Maps + OSM URLs.
//
// Why this is useful for international ER:
//   - Corp HQ jurisdiction lookup: pair with `gleif_lei_lookup` (returns
//     ISO country code in the LEI record) → REST Countries → full country
//     profile.
//   - Sanctions cross-reference: pair with `opensanctions` (returns
//     country codes) → REST Countries → context.
//   - Federal Register / treaty / international-agreement context:
//     resolve country names to ISO codes for downstream queries.
//
// Three modes:
//
//   - "by_name"   : fuzzy or exact match by name (full=true for exact)
//   - "by_code"   : ISO 3166-1 alpha-2 (cca2), alpha-3 (cca3), or IOC
//                   code (cioc) lookup
//   - "by_region" : list all countries in a region (Europe / Asia /
//                   Americas / Africa / Oceania / Antarctic)

type RCCountry struct {
	CommonName       string            `json:"common_name"`
	OfficialName     string            `json:"official_name,omitempty"`
	NativeNames      map[string]string `json:"native_names,omitempty"` // language → common
	CCA2             string            `json:"cca2,omitempty"`         // ISO 3166-1 alpha-2
	CCA3             string            `json:"cca3,omitempty"`         // ISO 3166-1 alpha-3
	CIOC             string            `json:"cioc,omitempty"`         // International Olympic Committee code
	FIFA             string            `json:"fifa,omitempty"`         // FIFA football code
	Capital          []string          `json:"capital,omitempty"`
	Region           string            `json:"region,omitempty"`
	Subregion        string            `json:"subregion,omitempty"`
	Continents       []string          `json:"continents,omitempty"`
	Population       int64             `json:"population,omitempty"`
	AreaKm2          float64           `json:"area_km2,omitempty"`
	Latitude         float64           `json:"latitude,omitempty"`
	Longitude        float64           `json:"longitude,omitempty"`
	Currencies       []RCCurrency      `json:"currencies,omitempty"`
	Languages        []string          `json:"languages,omitempty"`
	Borders          []string          `json:"border_country_codes,omitempty"` // CCA3 codes
	Timezones        []string          `json:"timezones,omitempty"`
	CallingCodeRoot  string            `json:"calling_code_root,omitempty"`     // e.g. "+1", "+4"
	CallingCodes     []string          `json:"calling_codes,omitempty"`         // root+suffix
	GiniIndex        float64           `json:"gini_index,omitempty"`
	GiniYear         string            `json:"gini_year,omitempty"`
	DemonymEnglish   string            `json:"demonym_english,omitempty"`
	CarDrivingSide   string            `json:"car_driving_side,omitempty"`
	Independent      *bool             `json:"independent,omitempty"`
	UNMember         *bool             `json:"un_member,omitempty"`
	GoogleMapsURL    string            `json:"google_maps_url,omitempty"`
	OpenStreetMapURL string            `json:"osm_url,omitempty"`
	FlagURL          string            `json:"flag_png_url,omitempty"`
}

type RCCurrency struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Symbol string `json:"symbol,omitempty"`
}

type RestCountriesLookupOutput struct {
	Mode              string      `json:"mode"`
	Query             string      `json:"query,omitempty"`
	Returned          int         `json:"returned"`
	Countries         []RCCountry `json:"countries,omitempty"`

	HighlightFindings []string    `json:"highlight_findings"`
	Source            string      `json:"source"`
	TookMs            int64       `json:"tookMs"`
	Note              string      `json:"note,omitempty"`
}

const rcFields = "name,cca2,cca3,cioc,fifa,capital,region,subregion,continents,population,area,latlng,currencies,languages,borders,timezones,idd,gini,demonyms,car,independent,unMember,maps,flags"

func RestCountriesLookup(ctx context.Context, input map[string]any) (*RestCountriesLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["code"]; ok {
			mode = "by_code"
		} else if _, ok := input["region"]; ok {
			mode = "by_region"
		} else {
			mode = "by_name"
		}
	}

	out := &RestCountriesLookupOutput{
		Mode:   mode,
		Source: "restcountries.com/v3.1",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "by_name":
		name, _ := input["name"].(string)
		if name == "" {
			name, _ = input["query"].(string)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("input.name (or query) required for by_name mode")
		}
		out.Query = name
		fullText := false
		if v, ok := input["full_text"].(bool); ok {
			fullText = v
		}
		params := url.Values{}
		params.Set("fields", rcFields)
		if fullText {
			params.Set("fullText", "true")
		}
		urlStr := fmt.Sprintf("https://restcountries.com/v3.1/name/%s?%s", url.PathEscape(name), params.Encode())
		body, err := rcGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		countries, err := parseRCCountries(body)
		if err != nil {
			return nil, err
		}
		out.Countries = countries

	case "by_code":
		code, _ := input["code"].(string)
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" {
			return nil, fmt.Errorf("input.code required for by_code mode (alpha-2 like 'US', alpha-3 like 'USA', or IOC like 'UNK')")
		}
		out.Query = code
		params := url.Values{}
		params.Set("fields", rcFields)
		urlStr := fmt.Sprintf("https://restcountries.com/v3.1/alpha/%s?%s", url.PathEscape(code), params.Encode())
		body, err := rcGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		// /alpha/ may return a single object or an array depending on code length
		// Try array first
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err == nil && len(arr) > 0 {
			countries := make([]RCCountry, 0, len(arr))
			for _, c := range arr {
				countries = append(countries, convertRCCountry(c))
			}
			out.Countries = countries
		} else {
			var single map[string]any
			if err := json.Unmarshal(body, &single); err == nil && single != nil {
				out.Countries = []RCCountry{convertRCCountry(single)}
			}
		}

	case "by_region":
		region, _ := input["region"].(string)
		region = strings.TrimSpace(region)
		if region == "" {
			return nil, fmt.Errorf("input.region required (e.g. 'Europe', 'Asia', 'Americas', 'Africa', 'Oceania', 'Antarctic')")
		}
		out.Query = region
		params := url.Values{}
		params.Set("fields", rcFields)
		urlStr := fmt.Sprintf("https://restcountries.com/v3.1/region/%s?%s", url.PathEscape(region), params.Encode())
		body, err := rcGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		countries, err := parseRCCountries(body)
		if err != nil {
			return nil, err
		}
		// Sort by population descending so big countries come first
		sort.SliceStable(countries, func(i, j int) bool {
			return countries[i].Population > countries[j].Population
		})
		// Cap at 30 by default (regions can have 50+)
		limit := 30
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
			limit = int(l)
		}
		if len(countries) > limit {
			out.Note = fmt.Sprintf("region has %d countries; returning top %d by population", len(countries), limit)
			countries = countries[:limit]
		}
		out.Countries = countries

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: by_name, by_code, by_region", mode)
	}

	out.Returned = len(out.Countries)
	out.HighlightFindings = buildRCHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseRCCountries(body []byte) ([]RCCountry, error) {
	var arr []map[string]any
	if err := json.Unmarshal(body, &arr); err != nil {
		return nil, fmt.Errorf("rest countries decode: %w", err)
	}
	countries := make([]RCCountry, 0, len(arr))
	for _, c := range arr {
		countries = append(countries, convertRCCountry(c))
	}
	return countries, nil
}

func convertRCCountry(c map[string]any) RCCountry {
	rc := RCCountry{
		CCA2: gtString(c, "cca2"),
		CCA3: gtString(c, "cca3"),
		CIOC: gtString(c, "cioc"),
		FIFA: gtString(c, "fifa"),
	}
	// Name (nested)
	if name, ok := c["name"].(map[string]any); ok {
		rc.CommonName = gtString(name, "common")
		rc.OfficialName = gtString(name, "official")
		if nn, ok := name["nativeName"].(map[string]any); ok {
			rc.NativeNames = map[string]string{}
			for lang, val := range nn {
				if vm, ok := val.(map[string]any); ok {
					if cn := gtString(vm, "common"); cn != "" {
						rc.NativeNames[lang] = cn
					}
				}
			}
		}
	}
	// Capital
	if caps, ok := c["capital"].([]any); ok {
		for _, x := range caps {
			if s, ok := x.(string); ok {
				rc.Capital = append(rc.Capital, s)
			}
		}
	}
	rc.Region = gtString(c, "region")
	rc.Subregion = gtString(c, "subregion")
	if conts, ok := c["continents"].([]any); ok {
		for _, x := range conts {
			if s, ok := x.(string); ok {
				rc.Continents = append(rc.Continents, s)
			}
		}
	}
	rc.Population = int64(gtFloat(c, "population"))
	rc.AreaKm2 = gtFloat(c, "area")
	if ll, ok := c["latlng"].([]any); ok && len(ll) >= 2 {
		rc.Latitude = gtFloatAt(ll, 0)
		rc.Longitude = gtFloatAt(ll, 1)
	}
	// Currencies
	if curs, ok := c["currencies"].(map[string]any); ok {
		for code, val := range curs {
			cur := RCCurrency{Code: code}
			if vm, ok := val.(map[string]any); ok {
				cur.Name = gtString(vm, "name")
				cur.Symbol = gtString(vm, "symbol")
			}
			rc.Currencies = append(rc.Currencies, cur)
		}
		sort.SliceStable(rc.Currencies, func(i, j int) bool {
			return rc.Currencies[i].Code < rc.Currencies[j].Code
		})
	}
	// Languages (map of code → language name)
	if langs, ok := c["languages"].(map[string]any); ok {
		for _, val := range langs {
			if s, ok := val.(string); ok {
				rc.Languages = append(rc.Languages, s)
			}
		}
		sort.Strings(rc.Languages)
	}
	// Borders
	if borders, ok := c["borders"].([]any); ok {
		for _, x := range borders {
			if s, ok := x.(string); ok {
				rc.Borders = append(rc.Borders, s)
			}
		}
	}
	// Timezones
	if tzs, ok := c["timezones"].([]any); ok {
		for _, x := range tzs {
			if s, ok := x.(string); ok {
				rc.Timezones = append(rc.Timezones, s)
			}
		}
	}
	// Calling codes
	if idd, ok := c["idd"].(map[string]any); ok {
		root := gtString(idd, "root")
		rc.CallingCodeRoot = root
		if suffixes, ok := idd["suffixes"].([]any); ok {
			for i, x := range suffixes {
				if i >= 5 { // cap suffixes (USA has 268)
					break
				}
				if s, ok := x.(string); ok {
					rc.CallingCodes = append(rc.CallingCodes, root+s)
				}
			}
			if len(suffixes) > 5 {
				rc.CallingCodes = append(rc.CallingCodes, fmt.Sprintf("(+%d more)", len(suffixes)-5))
			}
		}
	}
	// Gini
	if gini, ok := c["gini"].(map[string]any); ok {
		for year, val := range gini {
			rc.GiniIndex = gtFloat(map[string]any{"v": val}, "v")
			rc.GiniYear = year
			break // typically a single year
		}
	}
	// Demonym (English)
	if demos, ok := c["demonyms"].(map[string]any); ok {
		if eng, ok := demos["eng"].(map[string]any); ok {
			rc.DemonymEnglish = gtString(eng, "m")
		}
	}
	// Car
	if car, ok := c["car"].(map[string]any); ok {
		rc.CarDrivingSide = gtString(car, "side")
	}
	// Independent + UN member
	if v, ok := c["independent"].(bool); ok {
		rc.Independent = &v
	}
	if v, ok := c["unMember"].(bool); ok {
		rc.UNMember = &v
	}
	// Maps
	if maps, ok := c["maps"].(map[string]any); ok {
		rc.GoogleMapsURL = gtString(maps, "googleMaps")
		rc.OpenStreetMapURL = gtString(maps, "openStreetMaps")
	}
	// Flag
	if flags, ok := c["flags"].(map[string]any); ok {
		rc.FlagURL = gtString(flags, "png")
	}
	return rc
}

func gtFloatAt(arr []any, i int) float64 {
	if i < 0 || i >= len(arr) {
		return 0
	}
	switch v := arr[i].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

func rcGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rest countries: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("rest countries: not found (404)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rest countries HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildRCHighlights(o *RestCountriesLookupOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d countries match '%s'", o.Returned, o.Query))
	displayLimit := 5
	if o.Mode == "by_name" || o.Mode == "by_code" {
		displayLimit = 3
	}
	for i, c := range o.Countries {
		if i >= displayLimit {
			break
		}
		details := []string{}
		if c.CCA2 != "" {
			details = append(details, c.CCA2+"/"+c.CCA3)
		}
		if len(c.Capital) > 0 {
			details = append(details, "capital: "+strings.Join(c.Capital, ", "))
		}
		if c.Population > 0 {
			details = append(details, fmt.Sprintf("pop %s", fmtThousands(int(c.Population))))
		}
		if c.Region != "" {
			details = append(details, c.Region+"/"+c.Subregion)
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s", c.CommonName, strings.Join(details, " · ")))

		extras := []string{}
		if len(c.Currencies) > 0 {
			currs := []string{}
			for _, cur := range c.Currencies {
				symbol := ""
				if cur.Symbol != "" {
					symbol = " " + cur.Symbol
				}
				currs = append(currs, cur.Code+symbol)
			}
			extras = append(extras, "currencies: "+strings.Join(currs, ", "))
		}
		if len(c.Languages) > 0 {
			extras = append(extras, "languages: "+strings.Join(c.Languages, ", "))
		}
		if len(c.Borders) > 0 {
			borders := c.Borders
			if len(borders) > 8 {
				borders = borders[:8]
			}
			extras = append(extras, fmt.Sprintf("borders %d (%s)", len(c.Borders), strings.Join(borders, ", ")))
		}
		if c.CallingCodeRoot != "" {
			callstr := c.CallingCodeRoot
			if len(c.CallingCodes) > 0 && len(c.CallingCodes) <= 3 {
				callstr = strings.Join(c.CallingCodes, ", ")
			}
			extras = append(extras, "tel "+callstr)
		}
		if c.AreaKm2 > 0 {
			extras = append(extras, fmt.Sprintf("area %s km²", fmtThousands(int(c.AreaKm2))))
		}
		if c.GiniIndex > 0 {
			extras = append(extras, fmt.Sprintf("Gini %.1f (%s)", c.GiniIndex, c.GiniYear))
		}
		for _, e := range extras {
			hi = append(hi, "    "+e)
		}
	}
	return hi
}
