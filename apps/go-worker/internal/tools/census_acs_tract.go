package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CensusACSTract queries the Census Bureau's American Community Survey
// (ACS) 5-year tabulation for a given Census tract GEOID. Free, no auth
// (rate-limited to 500 req/day without key — sufficient for ER use cases).
//
// Pairs natively with `census_geocoder`: address → GEOID → demographics.
//
// Returns a curated demographic profile with computed percentages:
//   - Total population
//   - Median household income (B19013)
//   - Median home value (B25077)
//   - Median gross rent (B25064)
//   - Unemployment rate (B23025_005 / B23025_002)
//   - Race breakdown (B02001) + Hispanic ethnicity (B03003)
//   - Housing tenure (B25003) — % owner vs. renter
//   - Educational attainment (B15003) — % bachelor's+
//   - Commute >60min (B08303) — % long commuters

type CensusACSDemographics struct {
	GEOID                 string  `json:"geoid"`
	TractName             string  `json:"tract_name,omitempty"`
	State                 string  `json:"state_fips,omitempty"`
	County                string  `json:"county_fips,omitempty"`
	Tract                 string  `json:"tract_code,omitempty"`
	DataYear              int     `json:"data_year"`

	// Population
	Population            int     `json:"total_population"`

	// Income / housing
	MedianIncome          int     `json:"median_household_income"`
	MedianHomeValue       int     `json:"median_home_value"`
	MedianGrossRent       int     `json:"median_gross_rent"`

	// Employment
	LaborForce            int     `json:"labor_force"`
	Unemployed            int     `json:"unemployed"`
	UnemploymentRatePct   float64 `json:"unemployment_rate_pct"`

	// Race + ethnicity
	WhiteAlone            int     `json:"race_white_alone"`
	BlackAlone            int     `json:"race_black_alone"`
	AsianAlone            int     `json:"race_asian_alone"`
	HispanicLatino        int     `json:"hispanic_latino"`
	WhitePct              float64 `json:"race_white_pct"`
	BlackPct              float64 `json:"race_black_pct"`
	AsianPct              float64 `json:"race_asian_pct"`
	HispanicPct           float64 `json:"hispanic_pct"`

	// Housing
	OwnerOccupied         int     `json:"owner_occupied"`
	RenterOccupied        int     `json:"renter_occupied"`
	OwnerPct              float64 `json:"owner_pct"`
	RenterPct             float64 `json:"renter_pct"`

	// Education (25+ pop)
	Pop25Plus             int     `json:"pop_25_plus"`
	BachelorsCount        int     `json:"bachelors_count"`
	GraduateOrHigherCount int     `json:"graduate_or_higher_count"`
	BachelorsPlusPct      float64 `json:"bachelors_plus_pct"`

	// Commute
	TotalCommuters        int     `json:"total_commuters"`
	LongCommuters         int     `json:"long_commuters"` // 60+ min
	LongCommuterPct       float64 `json:"long_commuter_pct"`
}

type CensusACSTractOutput struct {
	Mode              string                 `json:"mode"`
	Query             string                 `json:"query,omitempty"`
	Demographics      *CensusACSDemographics `json:"demographics,omitempty"`
	VariableLookup    map[string]string      `json:"variable_lookup,omitempty"`

	HighlightFindings []string               `json:"highlight_findings"`
	Source            string                 `json:"source"`
	TookMs            int64                  `json:"tookMs"`
	Note              string                 `json:"note,omitempty"`
}

// Curated ACS variable list — the columns we always pull
var acsCuratedVars = []string{
	"NAME",
	"B01001_001E", // total pop
	"B19013_001E", // median household income
	"B25077_001E", // median home value
	"B25064_001E", // median gross rent
	"B23025_002E", // labor force
	"B23025_005E", // unemployed
	"B02001_002E", // white alone
	"B02001_003E", // black alone
	"B02001_005E", // asian alone
	"B03003_003E", // hispanic latino
	"B25003_002E", // owner occupied
	"B25003_003E", // renter occupied
	"B15003_001E", // pop 25+
	"B15003_022E", // bachelor's
	"B15003_023E", // master's
	"B15003_024E", // professional school
	"B15003_025E", // doctorate
	"B08303_001E", // total commuters
	"B08303_013E", // 90+ min commute
}

func CensusACSTract(ctx context.Context, input map[string]any) (*CensusACSTractOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if _, ok := input["variable_codes"]; ok {
			mode = "variable_lookup"
		} else {
			mode = "tract_demographics"
		}
	}

	out := &CensusACSTractOutput{
		Mode:   mode,
		Source: "api.census.gov/data ACS5",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "tract_demographics":
		// Accept either GEOID (e.g. "24033802405") or separate state/county/tract
		geoid, _ := input["geoid"].(string)
		geoid = strings.TrimSpace(geoid)
		var stateFIPS, countyFIPS, tractCode string
		if geoid != "" {
			out.Query = "GEOID " + geoid
			// 11-char tract GEOID: state(2) + county(3) + tract(6)
			if len(geoid) != 11 {
				return nil, fmt.Errorf("GEOID must be 11 characters (state+county+tract); got %d", len(geoid))
			}
			stateFIPS = geoid[:2]
			countyFIPS = geoid[2:5]
			tractCode = geoid[5:]
		} else {
			stateFIPS, _ = input["state_fips"].(string)
			countyFIPS, _ = input["county_fips"].(string)
			tractCode, _ = input["tract_code"].(string)
			if stateFIPS == "" || countyFIPS == "" || tractCode == "" {
				return nil, fmt.Errorf("input.geoid (11-char) or all of state_fips + county_fips + tract_code required")
			}
			out.Query = fmt.Sprintf("state %s · county %s · tract %s", stateFIPS, countyFIPS, tractCode)
		}
		// Default year 2022 (latest released ACS5 typically lags 2 years)
		year := 2022
		if y, ok := input["year"].(float64); ok && y >= 2010 && y <= 2030 {
			year = int(y)
		}

		params := url.Values{}
		params.Set("get", strings.Join(acsCuratedVars, ","))
		params.Set("for", "tract:"+tractCode)
		params.Set("in", fmt.Sprintf("state:%s county:%s", stateFIPS, countyFIPS))
		urlStr := fmt.Sprintf("https://api.census.gov/data/%d/acs/acs5?%s", year, params.Encode())
		body, err := acsGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		// ACS returns a 2D array: header row + data rows
		var rows [][]any
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("ACS decode: %w", err)
		}
		if len(rows) < 2 {
			return nil, fmt.Errorf("no ACS data for tract %s%s%s in year %d", stateFIPS, countyFIPS, tractCode, year)
		}
		header := rows[0]
		data := rows[1]
		// Build a name → value map for easier extraction
		valMap := map[string]string{}
		for i := 0; i < len(header) && i < len(data); i++ {
			h := fmt.Sprintf("%v", header[i])
			v := fmt.Sprintf("%v", data[i])
			valMap[h] = v
		}

		d := &CensusACSDemographics{
			GEOID:    stateFIPS + countyFIPS + tractCode,
			DataYear: year,
			TractName: valMap["NAME"],
			State:    stateFIPS,
			County:   countyFIPS,
			Tract:    tractCode,
		}
		d.Population = acsInt(valMap["B01001_001E"])
		d.MedianIncome = acsInt(valMap["B19013_001E"])
		d.MedianHomeValue = acsInt(valMap["B25077_001E"])
		d.MedianGrossRent = acsInt(valMap["B25064_001E"])
		d.LaborForce = acsInt(valMap["B23025_002E"])
		d.Unemployed = acsInt(valMap["B23025_005E"])
		if d.LaborForce > 0 {
			d.UnemploymentRatePct = pct(d.Unemployed, d.LaborForce)
		}
		d.WhiteAlone = acsInt(valMap["B02001_002E"])
		d.BlackAlone = acsInt(valMap["B02001_003E"])
		d.AsianAlone = acsInt(valMap["B02001_005E"])
		d.HispanicLatino = acsInt(valMap["B03003_003E"])
		if d.Population > 0 {
			d.WhitePct = pct(d.WhiteAlone, d.Population)
			d.BlackPct = pct(d.BlackAlone, d.Population)
			d.AsianPct = pct(d.AsianAlone, d.Population)
			d.HispanicPct = pct(d.HispanicLatino, d.Population)
		}
		d.OwnerOccupied = acsInt(valMap["B25003_002E"])
		d.RenterOccupied = acsInt(valMap["B25003_003E"])
		totalHousing := d.OwnerOccupied + d.RenterOccupied
		if totalHousing > 0 {
			d.OwnerPct = pct(d.OwnerOccupied, totalHousing)
			d.RenterPct = pct(d.RenterOccupied, totalHousing)
		}
		d.Pop25Plus = acsInt(valMap["B15003_001E"])
		d.BachelorsCount = acsInt(valMap["B15003_022E"])
		grad := acsInt(valMap["B15003_023E"]) + acsInt(valMap["B15003_024E"]) + acsInt(valMap["B15003_025E"])
		d.GraduateOrHigherCount = grad
		if d.Pop25Plus > 0 {
			d.BachelorsPlusPct = pct(d.BachelorsCount+grad, d.Pop25Plus)
		}
		d.TotalCommuters = acsInt(valMap["B08303_001E"])
		d.LongCommuters = acsInt(valMap["B08303_013E"])
		if d.TotalCommuters > 0 {
			d.LongCommuterPct = pct(d.LongCommuters, d.TotalCommuters)
		}
		out.Demographics = d

	case "variable_lookup":
		raw, _ := input["variable_codes"]
		var codes []string
		switch v := raw.(type) {
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok && s != "" {
					codes = append(codes, strings.TrimSpace(s))
				}
			}
		case string:
			for _, s := range strings.Split(v, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					codes = append(codes, s)
				}
			}
		}
		if len(codes) == 0 {
			return nil, fmt.Errorf("input.variable_codes required (array or comma-separated string)")
		}
		year := 2022
		if y, ok := input["year"].(float64); ok && y >= 2010 && y <= 2030 {
			year = int(y)
		}
		out.Query = strings.Join(codes, ",")
		out.VariableLookup = map[string]string{}
		for _, c := range codes {
			urlStr := fmt.Sprintf("https://api.census.gov/data/%d/acs/acs5/variables/%s.json", year, c)
			body, err := acsGet(ctx, cli, urlStr)
			if err != nil {
				out.VariableLookup[c] = "ERROR: " + err.Error()
				continue
			}
			var v struct {
				Label   string `json:"label"`
				Concept string `json:"concept"`
			}
			if err := json.Unmarshal(body, &v); err != nil {
				out.VariableLookup[c] = "ERROR: decode: " + err.Error()
				continue
			}
			out.VariableLookup[c] = v.Label + " — concept: " + v.Concept
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: tract_demographics, variable_lookup", mode)
	}

	out.HighlightFindings = buildACSHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func acsInt(s string) int {
	if s == "" || s == "null" {
		return 0
	}
	// ACS returns numeric strings. Negative/sentinel values like "-666666666"
	// indicate "data not available" — treat as zero.
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func pct(num, denom int) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom) * 100.0
}

func acsGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ACS: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ACS HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildACSHighlights(o *CensusACSTractOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "tract_demographics":
		if o.Demographics == nil {
			hi = append(hi, fmt.Sprintf("✗ no ACS data for %s", o.Query))
			return hi
		}
		d := o.Demographics
		hi = append(hi, fmt.Sprintf("✓ %s (ACS5 %d)", d.TractName, d.DataYear))
		hi = append(hi, fmt.Sprintf("  pop: %s · median income: %s · home value: %s · gross rent: %s/mo",
			fmtThousands(d.Population), formatUSD(float64(d.MedianIncome)),
			formatUSD(float64(d.MedianHomeValue)), formatUSD(float64(d.MedianGrossRent))))
		if d.UnemploymentRatePct > 0 {
			hi = append(hi, fmt.Sprintf("  unemployment: %.1f%% (%d/%d in labor force)",
				d.UnemploymentRatePct, d.Unemployed, d.LaborForce))
		}
		racePts := []string{}
		if d.WhitePct > 5 {
			racePts = append(racePts, fmt.Sprintf("White %.0f%%", d.WhitePct))
		}
		if d.BlackPct > 5 {
			racePts = append(racePts, fmt.Sprintf("Black %.0f%%", d.BlackPct))
		}
		if d.AsianPct > 5 {
			racePts = append(racePts, fmt.Sprintf("Asian %.0f%%", d.AsianPct))
		}
		if d.HispanicPct > 5 {
			racePts = append(racePts, fmt.Sprintf("Hispanic %.0f%%", d.HispanicPct))
		}
		if len(racePts) > 0 {
			hi = append(hi, "  race/ethnicity: "+strings.Join(racePts, " · "))
		}
		hi = append(hi, fmt.Sprintf("  housing: %.0f%% owner / %.0f%% renter (%d / %d units)",
			d.OwnerPct, d.RenterPct, d.OwnerOccupied, d.RenterOccupied))
		hi = append(hi, fmt.Sprintf("  education (25+): %.0f%% bachelor's+ (%d bach + %d grad of %d)",
			d.BachelorsPlusPct, d.BachelorsCount, d.GraduateOrHigherCount, d.Pop25Plus))
		if d.LongCommuterPct > 0 {
			hi = append(hi, fmt.Sprintf("  commute: %.0f%% have 90+min commute (%d/%d)",
				d.LongCommuterPct, d.LongCommuters, d.TotalCommuters))
		}

	case "variable_lookup":
		hi = append(hi, fmt.Sprintf("✓ resolved %d Census variables", len(o.VariableLookup)))
		for k, v := range o.VariableLookup {
			hi = append(hi, fmt.Sprintf("  • %s: %s", k, hfTruncate(v, 200)))
		}
	}
	return hi
}

func fmtThousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	parts := []string{}
	for i := len(s); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{s[start:i]}, parts...)
	}
	return strings.Join(parts, ",")
}
