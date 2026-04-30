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

// VINDecoder wraps NHTSA's free no-auth public APIs for vehicle OSINT.
// VINs appear in social media photos, accident reports, insurance
// fraud cases, used-car listings — uniquely useful identifier.
//
// Three modes:
//
//   - "decode_vin"           : 17-char VIN → make/model/year/engine/plant
//                               + safety equipment (ABS, ESC, blind-spot
//                               monitoring, adaptive cruise, etc.)
//   - "recalls"              : Make/Model/Year → all NHTSA recall
//                               campaigns with consequence text + remedy +
//                               park-it / park-outside / OTA-update flags
//   - "models_for_make_year" : browse all models a manufacturer produced
//                               in a given model year (useful when fuzzy:
//                               "Ford 2024 truck" → see Maverick / Ranger
//                               / F-150 / F-250 etc.)

type VINDecodedVehicle struct {
	VIN                  string `json:"vin"`
	Make                 string `json:"make,omitempty"`
	Model                string `json:"model,omitempty"`
	ModelYear            string `json:"model_year,omitempty"`
	ManufacturerName     string `json:"manufacturer_name,omitempty"`
	VehicleType          string `json:"vehicle_type,omitempty"`
	BodyClass            string `json:"body_class,omitempty"`
	Doors                string `json:"doors,omitempty"`
	Trim                 string `json:"trim,omitempty"`
	Series               string `json:"series,omitempty"`

	// Engine
	EngineDisplacementL  string `json:"engine_displacement_l,omitempty"`
	EngineCylinders      string `json:"engine_cylinders,omitempty"`
	EngineHP             string `json:"engine_hp,omitempty"`
	FuelType             string `json:"fuel_type,omitempty"`
	FuelTypeSecondary    string `json:"fuel_type_secondary,omitempty"`

	// Transmission
	TransmissionStyle    string `json:"transmission_style,omitempty"`
	DriveType            string `json:"drive_type,omitempty"`

	// Plant info
	PlantCity            string `json:"plant_city,omitempty"`
	PlantState           string `json:"plant_state,omitempty"`
	PlantCountry         string `json:"plant_country,omitempty"`
	PlantCompanyName     string `json:"plant_company,omitempty"`

	// Weight / size
	GVWR                 string `json:"gvwr,omitempty"`

	// Safety equipment
	ABS                  string `json:"abs,omitempty"`
	TPMS                 string `json:"tpms,omitempty"`
	ESC                  string `json:"electronic_stability_control,omitempty"`
	BlindSpotMon         string `json:"blind_spot_monitoring,omitempty"`
	AdaptiveCruise       string `json:"adaptive_cruise_control,omitempty"`
	LaneDeparture        string `json:"lane_departure_warning,omitempty"`
	ForwardCollision     string `json:"forward_collision_warning,omitempty"`
	ParkAssist           string `json:"park_assist,omitempty"`

	// Errors / status
	ErrorCode            string `json:"error_code,omitempty"`
	ErrorText            string `json:"error_text,omitempty"`
}

type NHTSARecall struct {
	CampaignNumber       string `json:"campaign_number"`
	Manufacturer         string `json:"manufacturer,omitempty"`
	Component            string `json:"component,omitempty"`
	Summary              string `json:"summary,omitempty"`
	Consequence          string `json:"consequence,omitempty"`
	Remedy               string `json:"remedy,omitempty"`
	ReportReceivedDate   string `json:"report_received_date,omitempty"`
	Make                 string `json:"make,omitempty"`
	Model                string `json:"model,omitempty"`
	ModelYear            string `json:"model_year,omitempty"`
	ParkIt               bool   `json:"park_it,omitempty"`
	ParkOutside          bool   `json:"park_outside,omitempty"`
	OverTheAirUpdate     bool   `json:"over_the_air_update,omitempty"`
}

type ModelEntry struct {
	Make  string `json:"make"`
	Model string `json:"model"`
}

type VINDecoderOutput struct {
	Mode              string              `json:"mode"`
	Query             string              `json:"query,omitempty"`
	Vehicle           *VINDecodedVehicle  `json:"vehicle,omitempty"`
	Recalls           []NHTSARecall       `json:"recalls,omitempty"`
	Models            []ModelEntry        `json:"models,omitempty"`
	TotalCount        int                 `json:"total_count,omitempty"`

	// Aggregations for recalls mode
	OTAUpdateCount    int                 `json:"ota_update_count,omitempty"`
	ParkItCount       int                 `json:"park_it_count,omitempty"`
	UniqueComponents  []string            `json:"unique_components,omitempty"`

	HighlightFindings []string            `json:"highlight_findings"`
	Source            string              `json:"source"`
	TookMs            int64               `json:"tookMs"`
	Note              string              `json:"note,omitempty"`
}

func VINDecoder(ctx context.Context, input map[string]any) (*VINDecoderOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if _, ok := input["vin"]; ok {
			mode = "decode_vin"
		} else if _, ok := input["model"]; ok {
			mode = "recalls"
		} else if _, ok := input["make"]; ok {
			mode = "models_for_make_year"
		} else {
			return nil, fmt.Errorf("no mode and no input.vin / input.make to auto-detect from")
		}
	}

	out := &VINDecoderOutput{
		Mode:   mode,
		Source: "vpic.nhtsa.dot.gov + api.nhtsa.gov",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "decode_vin":
		vin, _ := input["vin"].(string)
		vin = strings.ToUpper(strings.TrimSpace(vin))
		if vin == "" {
			return nil, fmt.Errorf("input.vin required for decode_vin mode")
		}
		out.Query = vin
		// /decodevinvalues/ returns flatter single-object data
		urlStr := fmt.Sprintf("https://vpic.nhtsa.dot.gov/api/vehicles/decodevinvalues/%s?format=json", url.PathEscape(vin))
		body, err := nhtsaGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Results []map[string]any `json:"Results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("VIN decode: %w", err)
		}
		if len(raw.Results) == 0 {
			return nil, fmt.Errorf("no decode result for VIN %s", vin)
		}
		r := raw.Results[0]
		v := &VINDecodedVehicle{
			VIN:                 vin,
			Make:                gtString(r, "Make"),
			Model:               gtString(r, "Model"),
			ModelYear:           gtString(r, "ModelYear"),
			ManufacturerName:    gtString(r, "ManufacturerName"),
			VehicleType:         gtString(r, "VehicleType"),
			BodyClass:           gtString(r, "BodyClass"),
			Doors:               gtString(r, "Doors"),
			Trim:                gtString(r, "Trim"),
			Series:              gtString(r, "Series"),
			EngineDisplacementL: gtString(r, "DisplacementL"),
			EngineCylinders:    gtString(r, "EngineCylinders"),
			EngineHP:           gtString(r, "EngineHP"),
			FuelType:           gtString(r, "FuelTypePrimary"),
			FuelTypeSecondary:  gtString(r, "FuelTypeSecondary"),
			TransmissionStyle:  gtString(r, "TransmissionStyle"),
			DriveType:          gtString(r, "DriveType"),
			PlantCity:          gtString(r, "PlantCity"),
			PlantState:         gtString(r, "PlantState"),
			PlantCountry:       gtString(r, "PlantCountry"),
			PlantCompanyName:   gtString(r, "PlantCompanyName"),
			GVWR:               gtString(r, "GVWR"),
			ABS:                gtString(r, "ABS"),
			TPMS:               gtString(r, "TPMS"),
			ESC:                gtString(r, "ESC"),
			BlindSpotMon:       gtString(r, "BlindSpotMon"),
			AdaptiveCruise:     gtString(r, "AdaptiveCruiseControl"),
			LaneDeparture:      gtString(r, "LaneDepartureWarning"),
			ForwardCollision:   gtString(r, "ForwardCollisionWarning"),
			ParkAssist:         gtString(r, "ParkAssist"),
			ErrorCode:          gtString(r, "ErrorCode"),
			ErrorText:          gtString(r, "ErrorText"),
		}
		out.Vehicle = v

	case "recalls":
		make, _ := input["make"].(string)
		model, _ := input["model"].(string)
		yearAny := input["model_year"]
		make = strings.TrimSpace(make)
		model = strings.TrimSpace(model)
		if make == "" || model == "" {
			return nil, fmt.Errorf("input.make and input.model required for recalls mode")
		}
		yearStr := fmt.Sprintf("%v", yearAny)
		yearStr = strings.TrimSpace(yearStr)
		if yearStr == "" || yearStr == "<nil>" {
			return nil, fmt.Errorf("input.model_year required for recalls mode")
		}
		out.Query = fmt.Sprintf("%s %s %s", make, model, yearStr)
		params := url.Values{}
		params.Set("make", make)
		params.Set("model", model)
		params.Set("modelYear", yearStr)
		urlStr := "https://api.nhtsa.gov/recalls/recallsByVehicle?" + params.Encode()
		body, err := nhtsaGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Count   int              `json:"Count"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("recalls decode: %w", err)
		}
		out.TotalCount = raw.Count
		componentSet := map[string]struct{}{}
		for _, r := range raw.Results {
			rec := NHTSARecall{
				CampaignNumber:     gtString(r, "NHTSACampaignNumber"),
				Manufacturer:       gtString(r, "Manufacturer"),
				Component:          gtString(r, "Component"),
				Summary:            hfTruncate(gtString(r, "Summary"), 400),
				Consequence:        hfTruncate(gtString(r, "Consequence"), 200),
				Remedy:             hfTruncate(gtString(r, "Remedy"), 300),
				ReportReceivedDate: gtString(r, "ReportReceivedDate"),
				Make:               gtString(r, "Make"),
				Model:              gtString(r, "Model"),
				ModelYear:          gtString(r, "ModelYear"),
				ParkIt:             gtBool(r, "parkIt"),
				ParkOutside:        gtBool(r, "parkOutSide"),
				OverTheAirUpdate:   gtBool(r, "overTheAirUpdate"),
			}
			out.Recalls = append(out.Recalls, rec)
			if rec.OverTheAirUpdate {
				out.OTAUpdateCount++
			}
			if rec.ParkIt {
				out.ParkItCount++
			}
			if rec.Component != "" {
				componentSet[rec.Component] = struct{}{}
			}
		}
		for c := range componentSet {
			out.UniqueComponents = append(out.UniqueComponents, c)
		}
		sort.Strings(out.UniqueComponents)

	case "models_for_make_year":
		make, _ := input["make"].(string)
		yearAny := input["model_year"]
		make = strings.TrimSpace(make)
		if make == "" {
			return nil, fmt.Errorf("input.make required for models_for_make_year mode")
		}
		yearStr := fmt.Sprintf("%v", yearAny)
		yearStr = strings.TrimSpace(yearStr)
		if yearStr == "" || yearStr == "<nil>" {
			return nil, fmt.Errorf("input.model_year required for models_for_make_year mode")
		}
		out.Query = fmt.Sprintf("%s %s", make, yearStr)
		urlStr := fmt.Sprintf("https://vpic.nhtsa.dot.gov/api/vehicles/getmodelsformakeyear/make/%s/modelyear/%s?format=json",
			url.PathEscape(make), url.PathEscape(yearStr))
		body, err := nhtsaGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Count   int `json:"Count"`
			Results []struct {
				MakeName  string `json:"Make_Name"`
				ModelName string `json:"Model_Name"`
			} `json:"Results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("models decode: %w", err)
		}
		out.TotalCount = raw.Count
		for _, m := range raw.Results {
			out.Models = append(out.Models, ModelEntry{Make: m.MakeName, Model: m.ModelName})
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: decode_vin, recalls, models_for_make_year", mode)
	}

	out.HighlightFindings = buildVINHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func nhtsaGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nhtsa: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("nhtsa HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildVINHighlights(o *VINDecoderOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "decode_vin":
		if o.Vehicle == nil {
			break
		}
		v := o.Vehicle
		title := strings.TrimSpace(strings.Join([]string{v.ModelYear, v.Make, v.Model, v.Trim}, " "))
		if title == "" {
			title = v.VIN
		}
		hi = append(hi, fmt.Sprintf("✓ VIN %s → %s", v.VIN, title))
		mfg := []string{}
		if v.ManufacturerName != "" {
			mfg = append(mfg, v.ManufacturerName)
		}
		plant := []string{}
		if v.PlantCity != "" {
			p := v.PlantCity
			if v.PlantState != "" {
				p += ", " + v.PlantState
			}
			if v.PlantCountry != "" {
				p += ", " + v.PlantCountry
			}
			plant = append(plant, p)
		}
		if len(mfg) > 0 || len(plant) > 0 {
			hi = append(hi, fmt.Sprintf("  manufacturer: %s · plant: %s", strings.Join(mfg, ", "), strings.Join(plant, ", ")))
		}
		spec := []string{}
		if v.VehicleType != "" {
			spec = append(spec, "type: "+v.VehicleType)
		}
		if v.BodyClass != "" {
			spec = append(spec, "body: "+v.BodyClass)
		}
		if v.Doors != "" {
			spec = append(spec, v.Doors+" doors")
		}
		if v.DriveType != "" {
			spec = append(spec, v.DriveType)
		}
		if len(spec) > 0 {
			hi = append(hi, "  "+strings.Join(spec, " · "))
		}
		eng := []string{}
		if v.FuelType != "" {
			eng = append(eng, v.FuelType)
		}
		if v.EngineDisplacementL != "" {
			eng = append(eng, v.EngineDisplacementL+"L")
		}
		if v.EngineCylinders != "" {
			eng = append(eng, "I"+v.EngineCylinders)
		}
		if v.EngineHP != "" {
			eng = append(eng, v.EngineHP+" HP")
		}
		if v.TransmissionStyle != "" {
			eng = append(eng, v.TransmissionStyle)
		}
		if len(eng) > 0 {
			hi = append(hi, "  engine: "+strings.Join(eng, " · "))
		}
		safety := []string{}
		if v.ABS == "Standard" {
			safety = append(safety, "ABS")
		}
		if v.ESC == "Standard" {
			safety = append(safety, "ESC")
		}
		if v.BlindSpotMon == "Standard" {
			safety = append(safety, "BlindSpot")
		}
		if v.AdaptiveCruise == "Standard" {
			safety = append(safety, "AdaptiveCruise")
		}
		if v.LaneDeparture == "Standard" {
			safety = append(safety, "LaneDeparture")
		}
		if v.ForwardCollision == "Standard" {
			safety = append(safety, "ForwardCollision")
		}
		if len(safety) > 0 {
			hi = append(hi, "  safety: "+strings.Join(safety, ", "))
		}
		if v.ErrorText != "" && v.ErrorText != "0 - VIN decoded clean. Check Digit (9th position) is correct" {
			hi = append(hi, "  ⚠ "+v.ErrorText)
		}

	case "recalls":
		hi = append(hi, fmt.Sprintf("✓ %d NHTSA recalls for %s", o.TotalCount, o.Query))
		flags := []string{}
		if o.OTAUpdateCount > 0 {
			flags = append(flags, fmt.Sprintf("%d OTA-fixed", o.OTAUpdateCount))
		}
		if o.ParkItCount > 0 {
			flags = append(flags, fmt.Sprintf("⚠️ %d park-it (do not drive)", o.ParkItCount))
		}
		if len(flags) > 0 {
			hi = append(hi, "  "+strings.Join(flags, " · "))
		}
		if len(o.UniqueComponents) > 0 && len(o.UniqueComponents) <= 5 {
			hi = append(hi, "  components: "+strings.Join(o.UniqueComponents, ", "))
		}
		for i, r := range o.Recalls {
			if i >= 5 {
				break
			}
			marker := ""
			if r.ParkIt {
				marker += " ⛔PARK"
			}
			if r.OverTheAirUpdate {
				marker += " 📡OTA"
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s — %s%s",
				r.ReportReceivedDate, r.CampaignNumber, hfTruncate(r.Component, 50), marker))
			hi = append(hi, fmt.Sprintf("    %s", hfTruncate(r.Consequence, 120)))
		}

	case "models_for_make_year":
		hi = append(hi, fmt.Sprintf("✓ %d models for %s", o.TotalCount, o.Query))
		display := o.Models
		if len(display) > 12 {
			display = display[:12]
		}
		modelNames := make([]string, 0, len(display))
		for _, m := range display {
			modelNames = append(modelNames, m.Model)
		}
		hi = append(hi, "  models: "+strings.Join(modelNames, ", "))
		if len(o.Models) > 12 {
			hi = append(hi, fmt.Sprintf("  …and %d more", len(o.Models)-12))
		}
	}
	return hi
}
