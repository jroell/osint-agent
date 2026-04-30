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

// OpenFDASearch wraps the FDA's openFDA public APIs (free, no auth) for
// pharma + medical-device + food regulatory OSINT. Closes the medical
// chain alongside NPI Registry (provider lookup) and clinicaltrials_search
// (active trials).
//
// Four modes:
//
//   - "drug_recalls"   : drug enforcement actions filterable by classification
//                         (Class I = most severe; reasonable probability of
//                         death or serious adverse health consequences),
//                         status, recalling firm, date, state. Each entry
//                         has product description, reason for recall,
//                         distribution pattern.
//   - "drug_label"     : drug label lookup by brand/generic/manufacturer.
//                         Returns indications, **boxed warnings** (FDA's
//                         highest-severity warning, e.g. "RISK OF THYROID
//                         C-CELL TUMORS" for Ozempic), warnings, adverse
//                         reactions, dosage, contraindications.
//   - "device_events"  : MAUDE database — every adverse event, malfunction,
//                         injury, or death involving a medical device.
//                         Filterable by brand_name / manufacturer /
//                         event_type / date.
//   - "food_recalls"   : food enforcement actions — same shape as drug
//                         recalls but for food products.

type OpenFDARecall struct {
	RecallNumber       string `json:"recall_number"`
	Classification     string `json:"classification,omitempty"`
	Status             string `json:"status,omitempty"`
	RecallInitiation   string `json:"recall_initiation_date,omitempty"`
	ReportDate         string `json:"report_date,omitempty"`
	RecallingFirm      string `json:"recalling_firm,omitempty"`
	State              string `json:"state,omitempty"`
	Country            string `json:"country,omitempty"`
	ProductDescription string `json:"product_description,omitempty"`
	ReasonForRecall    string `json:"reason_for_recall,omitempty"`
	ProductQuantity    string `json:"product_quantity,omitempty"`
	DistributionPattern string `json:"distribution_pattern,omitempty"`
	VoluntaryMandated  string `json:"voluntary_mandated,omitempty"`
	OpenFDA            map[string][]string `json:"openfda,omitempty"`
}

type OpenFDADrugLabel struct {
	ID                string              `json:"id"`
	BrandName         []string            `json:"brand_name,omitempty"`
	GenericName       []string            `json:"generic_name,omitempty"`
	Manufacturer      []string            `json:"manufacturer,omitempty"`
	Route             []string            `json:"route,omitempty"`
	ProductType       []string            `json:"product_type,omitempty"`
	Indications       string              `json:"indications,omitempty"`
	BoxedWarning      string              `json:"boxed_warning,omitempty"`
	Warnings          string              `json:"warnings,omitempty"`
	AdverseReactions  string              `json:"adverse_reactions,omitempty"`
	Contraindications string              `json:"contraindications,omitempty"`
	DosageAndAdministration string        `json:"dosage_and_administration,omitempty"`
	EffectiveTime     string              `json:"effective_time,omitempty"`
	OpenFDA           map[string][]string `json:"openfda,omitempty"`
}

type OpenFDADeviceEvent struct {
	MDRReportKey       string `json:"mdr_report_key"`
	EventType          string `json:"event_type,omitempty"`
	DateReceived       string `json:"date_received,omitempty"`
	DateOfEvent        string `json:"date_of_event,omitempty"`
	BrandName          string `json:"device_brand_name,omitempty"`
	GenericName        string `json:"device_generic_name,omitempty"`
	Manufacturer       string `json:"manufacturer_name,omitempty"`
	ProductProblems    []string `json:"product_problems,omitempty"`
	PatientProblems    []string `json:"patient_problems,omitempty"`
	NarrativeExcerpt   string `json:"narrative_excerpt,omitempty"`
	NumDevicesInEvent  string `json:"number_of_devices_in_event,omitempty"`
	ReportSource       string `json:"report_source_code,omitempty"`
}

type OpenFDASearchOutput struct {
	Mode              string                `json:"mode"`
	Query             string                `json:"query,omitempty"`
	TotalCount        int                   `json:"total_count,omitempty"`
	Returned          int                   `json:"returned"`
	Recalls           []OpenFDARecall       `json:"recalls,omitempty"`
	DrugLabels        []OpenFDADrugLabel    `json:"drug_labels,omitempty"`
	DeviceEvents      []OpenFDADeviceEvent  `json:"device_events,omitempty"`

	// Aggregations
	ClassICount       int                   `json:"class_i_count,omitempty"`
	ClassIICount      int                   `json:"class_ii_count,omitempty"`
	ClassIIICount     int                   `json:"class_iii_count,omitempty"`
	OngoingCount      int                   `json:"ongoing_count,omitempty"`
	UniqueFirms       []string              `json:"unique_firms,omitempty"`
	UniqueStates      []string              `json:"unique_states,omitempty"`

	HighlightFindings []string              `json:"highlight_findings"`
	Source            string                `json:"source"`
	TookMs            int64                 `json:"tookMs"`
	Note              string                `json:"note,omitempty"`
}

func OpenFDASearch(ctx context.Context, input map[string]any) (*OpenFDASearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "drug_recalls"
	}

	out := &OpenFDASearchOutput{
		Mode:   mode,
		Source: "api.fda.gov",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	// All openFDA endpoints share the search/limit/skip pattern; the search
	// expression syntax is `field:value AND other:value`.
	limit := 10
	if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
		limit = int(l)
	}
	searchExpr := buildOpenFDASearchExpr(mode, input)
	if searchExpr == "" && mode != "drug_label" {
		// drug_label can be unfiltered (returns most recent labels)
		return nil, fmt.Errorf("at least one filter required (e.g. brand_name, classification, recalling_firm, manufacturer, state, date_received_min)")
	}

	switch mode {
	case "drug_recalls", "food_recalls":
		domain := "drug"
		if mode == "food_recalls" {
			domain = "food"
		}
		params := url.Values{}
		if searchExpr != "" {
			params.Set("search", searchExpr)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		out.Query = searchExpr

		body, err := openFDAGet(ctx, cli, fmt.Sprintf("https://api.fda.gov/%s/enforcement.json?%s", domain, params.Encode()))
		if err != nil {
			return nil, err
		}
		var raw struct {
			Meta struct {
				Results struct{ Total int `json:"total"` } `json:"results"`
			} `json:"meta"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("openfda decode: %w", err)
		}
		out.TotalCount = raw.Meta.Results.Total
		firmSet := map[string]struct{}{}
		stateSet := map[string]struct{}{}
		for _, r := range raw.Results {
			rec := OpenFDARecall{
				RecallNumber:        gtString(r, "recall_number"),
				Classification:      gtString(r, "classification"),
				Status:              gtString(r, "status"),
				RecallInitiation:    gtString(r, "recall_initiation_date"),
				ReportDate:          gtString(r, "report_date"),
				RecallingFirm:       gtString(r, "recalling_firm"),
				State:               gtString(r, "state"),
				Country:             gtString(r, "country"),
				ProductDescription:  hfTruncate(gtString(r, "product_description"), 240),
				ReasonForRecall:     hfTruncate(gtString(r, "reason_for_recall"), 400),
				ProductQuantity:     gtString(r, "product_quantity"),
				DistributionPattern: hfTruncate(gtString(r, "distribution_pattern"), 200),
				VoluntaryMandated:   gtString(r, "voluntary_mandated"),
			}
			if of, ok := r["openfda"].(map[string]any); ok {
				rec.OpenFDA = openFDAFlatStrSlice(of, []string{"brand_name", "generic_name", "manufacturer_name", "route"})
			}
			out.Recalls = append(out.Recalls, rec)
			switch rec.Classification {
			case "Class I":
				out.ClassICount++
			case "Class II":
				out.ClassIICount++
			case "Class III":
				out.ClassIIICount++
			}
			if strings.EqualFold(rec.Status, "Ongoing") {
				out.OngoingCount++
			}
			if rec.RecallingFirm != "" {
				firmSet[rec.RecallingFirm] = struct{}{}
			}
			if rec.State != "" {
				stateSet[rec.State] = struct{}{}
			}
		}
		for k := range firmSet {
			out.UniqueFirms = append(out.UniqueFirms, k)
		}
		sort.Strings(out.UniqueFirms)
		for k := range stateSet {
			out.UniqueStates = append(out.UniqueStates, k)
		}
		sort.Strings(out.UniqueStates)
		out.Returned = len(out.Recalls)

	case "drug_label":
		params := url.Values{}
		if searchExpr != "" {
			params.Set("search", searchExpr)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		out.Query = searchExpr

		body, err := openFDAGet(ctx, cli, "https://api.fda.gov/drug/label.json?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Meta struct {
				Results struct{ Total int `json:"total"` } `json:"results"`
			} `json:"meta"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("openfda decode: %w", err)
		}
		out.TotalCount = raw.Meta.Results.Total
		for _, r := range raw.Results {
			lbl := OpenFDADrugLabel{
				ID:            gtString(r, "id"),
				EffectiveTime: gtString(r, "effective_time"),
			}
			if of, ok := r["openfda"].(map[string]any); ok {
				lbl.OpenFDA = openFDAFlatStrSlice(of, []string{"brand_name", "generic_name", "manufacturer_name", "route", "product_type", "rxcui"})
				lbl.BrandName = lbl.OpenFDA["brand_name"]
				lbl.GenericName = lbl.OpenFDA["generic_name"]
				lbl.Manufacturer = lbl.OpenFDA["manufacturer_name"]
				lbl.Route = lbl.OpenFDA["route"]
				lbl.ProductType = lbl.OpenFDA["product_type"]
			}
			lbl.Indications = openFDAJoinFirst(r, "indications_and_usage", 500)
			lbl.BoxedWarning = openFDAJoinFirst(r, "boxed_warning", 500)
			lbl.Warnings = openFDAJoinFirst(r, "warnings", 400)
			lbl.AdverseReactions = openFDAJoinFirst(r, "adverse_reactions", 400)
			lbl.Contraindications = openFDAJoinFirst(r, "contraindications", 300)
			lbl.DosageAndAdministration = openFDAJoinFirst(r, "dosage_and_administration", 300)
			out.DrugLabels = append(out.DrugLabels, lbl)
		}
		out.Returned = len(out.DrugLabels)

	case "device_events":
		params := url.Values{}
		if searchExpr != "" {
			params.Set("search", searchExpr)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		out.Query = searchExpr

		body, err := openFDAGet(ctx, cli, "https://api.fda.gov/device/event.json?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Meta struct {
				Results struct{ Total int `json:"total"` } `json:"results"`
			} `json:"meta"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("openfda decode: %w", err)
		}
		out.TotalCount = raw.Meta.Results.Total
		for _, r := range raw.Results {
			ev := OpenFDADeviceEvent{
				MDRReportKey:       gtString(r, "mdr_report_key"),
				EventType:          gtString(r, "event_type"),
				DateReceived:       gtString(r, "date_received"),
				DateOfEvent:        gtString(r, "date_of_event"),
				NumDevicesInEvent:  gtString(r, "number_of_devices_in_event"),
				ReportSource:       gtString(r, "report_source_code"),
			}
			// device is an array of objects
			if devs, ok := r["device"].([]any); ok && len(devs) > 0 {
				if d0, ok := devs[0].(map[string]any); ok {
					ev.BrandName = gtString(d0, "brand_name")
					ev.GenericName = gtString(d0, "generic_name")
					ev.Manufacturer = gtString(d0, "manufacturer_d_name")
				}
			}
			if pp, ok := r["product_problems"].([]any); ok {
				for _, p := range pp {
					if s, ok := p.(string); ok {
						ev.ProductProblems = append(ev.ProductProblems, s)
					}
				}
			}
			// patient[].patient_problems is nested — iterate
			if patients, ok := r["patient"].([]any); ok {
				probSet := map[string]struct{}{}
				for _, p := range patients {
					if pm, ok := p.(map[string]any); ok {
						if pps, ok := pm["patient_problems"].([]any); ok {
							for _, pp := range pps {
								if s, ok := pp.(string); ok {
									probSet[s] = struct{}{}
								}
							}
						}
					}
				}
				for k := range probSet {
					ev.PatientProblems = append(ev.PatientProblems, k)
				}
				sort.Strings(ev.PatientProblems)
			}
			// mdr_text[].text — narrative
			if mdr, ok := r["mdr_text"].([]any); ok && len(mdr) > 0 {
				if t0, ok := mdr[0].(map[string]any); ok {
					ev.NarrativeExcerpt = hfTruncate(gtString(t0, "text"), 300)
				}
			}
			out.DeviceEvents = append(out.DeviceEvents, ev)
		}
		out.Returned = len(out.DeviceEvents)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: drug_recalls, drug_label, device_events, food_recalls", mode)
	}

	out.HighlightFindings = buildOpenFDAHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// buildOpenFDASearchExpr converts mode-specific filter inputs into an
// openFDA search expression of the form `field:"value"+AND+other:"value"`.
func buildOpenFDASearchExpr(mode string, input map[string]any) string {
	parts := []string{}
	add := func(s string) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	q := func(field, val string) string {
		if val == "" {
			return ""
		}
		val = strings.TrimSpace(val)
		// Quote if has spaces
		if strings.Contains(val, " ") {
			return field + `:"` + val + `"`
		}
		return field + ":" + val
	}

	switch mode {
	case "drug_recalls", "food_recalls":
		if v, ok := input["recalling_firm"].(string); ok {
			add(q("recalling_firm", v))
		}
		if v, ok := input["product"].(string); ok {
			add(q("product_description", v))
		}
		if v, ok := input["classification"].(string); ok {
			add(q("classification", v))
		}
		if v, ok := input["status"].(string); ok {
			add(q("status", v))
		}
		if v, ok := input["state"].(string); ok {
			add(q("state", strings.ToUpper(v)))
		}
		// Date range: report_date:[20260101 TO 20261231]
		startDate, _ := input["start_date"].(string)
		endDate, _ := input["end_date"].(string)
		if startDate != "" || endDate != "" {
			s := strings.ReplaceAll(startDate, "-", "")
			e := strings.ReplaceAll(endDate, "-", "")
			if s == "" {
				s = "20100101"
			}
			if e == "" {
				e = time.Now().Format("20060102")
			}
			add(fmt.Sprintf("report_date:[%s TO %s]", s, e))
		}
	case "drug_label":
		if v, ok := input["brand_name"].(string); ok {
			add(q("openfda.brand_name", v))
		}
		if v, ok := input["generic_name"].(string); ok {
			add(q("openfda.generic_name", v))
		}
		if v, ok := input["manufacturer"].(string); ok {
			add(q("openfda.manufacturer_name", v))
		}
		if v, ok := input["search_term"].(string); ok && v != "" {
			add(v) // free text
		}
	case "device_events":
		if v, ok := input["brand_name"].(string); ok {
			add(q("device.brand_name", v))
		}
		if v, ok := input["manufacturer"].(string); ok {
			add(q("device.manufacturer_d_name", v))
		}
		if v, ok := input["event_type"].(string); ok {
			add(q("event_type", v))
		}
		if v, ok := input["product_problem"].(string); ok {
			add(q("product_problems", v))
		}
		startDate, _ := input["start_date"].(string)
		endDate, _ := input["end_date"].(string)
		if startDate != "" || endDate != "" {
			s := strings.ReplaceAll(startDate, "-", "")
			e := strings.ReplaceAll(endDate, "-", "")
			if s == "" {
				s = "20100101"
			}
			if e == "" {
				e = time.Now().Format("20060102")
			}
			add(fmt.Sprintf("date_received:[%s TO %s]", s, e))
		}
	}
	// Join with literal " AND " — url.Values.Encode will turn the spaces
	// into "+" which openFDA decodes as spaces (correct openFDA syntax).
	// Using literal "+AND+" would double-encode to "%2BAND%2B" and the
	// API would receive literal plus-chars, returning 0 results.
	return strings.Join(parts, " AND ")
}

func openFDAFlatStrSlice(of map[string]any, fields []string) map[string][]string {
	out := map[string][]string{}
	for _, f := range fields {
		if v, ok := of[f].([]any); ok {
			ss := make([]string, 0, len(v))
			for _, x := range v {
				if s, ok := x.(string); ok && s != "" {
					ss = append(ss, s)
				}
			}
			if len(ss) > 0 {
				out[f] = ss
			}
		}
	}
	return out
}

func openFDAJoinFirst(r map[string]any, key string, maxChars int) string {
	if v, ok := r[key]; ok {
		switch x := v.(type) {
		case []any:
			if len(x) > 0 {
				if s, ok := x[0].(string); ok {
					return hfTruncate(s, maxChars)
				}
			}
		case string:
			return hfTruncate(x, maxChars)
		}
	}
	return ""
}

func openFDAGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openFDA: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		// 404 with NOT_FOUND error means zero results — handle gracefully
		if resp.StatusCode == 404 {
			// Return an empty results envelope so caller can decode it
			return []byte(`{"meta":{"results":{"total":0}},"results":[]}`), nil
		}
		return nil, fmt.Errorf("openFDA HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildOpenFDAHighlights(o *OpenFDASearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "drug_recalls", "food_recalls":
		hi = append(hi, fmt.Sprintf("✓ %d recalls match (returning %d)", o.TotalCount, o.Returned))
		classes := []string{}
		if o.ClassICount > 0 {
			classes = append(classes, fmt.Sprintf("🔥 %d Class I", o.ClassICount))
		}
		if o.ClassIICount > 0 {
			classes = append(classes, fmt.Sprintf("⚠️  %d Class II", o.ClassIICount))
		}
		if o.ClassIIICount > 0 {
			classes = append(classes, fmt.Sprintf("🟡 %d Class III", o.ClassIIICount))
		}
		if len(classes) > 0 {
			hi = append(hi, "  classifications: "+strings.Join(classes, " · "))
		}
		if o.OngoingCount > 0 {
			hi = append(hi, fmt.Sprintf("  ⏳ %d still ongoing", o.OngoingCount))
		}
		if len(o.UniqueFirms) > 0 && len(o.UniqueFirms) <= 6 {
			hi = append(hi, "  recalling firms: "+strings.Join(o.UniqueFirms, ", "))
		} else if len(o.UniqueFirms) > 6 {
			hi = append(hi, fmt.Sprintf("  %d unique recalling firms", len(o.UniqueFirms)))
		}
		for i, r := range o.Recalls {
			if i >= 5 {
				break
			}
			marker := ""
			switch r.Classification {
			case "Class I":
				marker = " 🔥"
			case "Class II":
				marker = " ⚠️"
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s — %s — %s%s",
				r.RecallInitiation, r.Classification, r.RecallingFirm, hfTruncate(r.ReasonForRecall, 100), marker))
		}

	case "drug_label":
		hi = append(hi, fmt.Sprintf("✓ %d drug labels match (returning %d)", o.TotalCount, o.Returned))
		for i, l := range o.DrugLabels {
			if i >= 4 {
				break
			}
			brand := strings.Join(l.BrandName, "/")
			generic := strings.Join(l.GenericName, "/")
			mfr := strings.Join(l.Manufacturer, "/")
			hi = append(hi, fmt.Sprintf("  • %s [%s] — %s", brand, generic, mfr))
			if l.BoxedWarning != "" {
				hi = append(hi, "    🚨 BOXED WARNING: "+hfTruncate(l.BoxedWarning, 150))
			}
			if l.Indications != "" {
				hi = append(hi, "    indications: "+hfTruncate(l.Indications, 150))
			}
		}

	case "device_events":
		hi = append(hi, fmt.Sprintf("✓ %d device events match (returning %d)", o.TotalCount, o.Returned))
		// Group by event type
		typeCount := map[string]int{}
		mfrCount := map[string]int{}
		for _, e := range o.DeviceEvents {
			typeCount[e.EventType]++
			mfrCount[e.Manufacturer]++
		}
		typeBreakdown := []string{}
		for t, c := range typeCount {
			typeBreakdown = append(typeBreakdown, fmt.Sprintf("%s×%d", t, c))
		}
		if len(typeBreakdown) > 0 {
			hi = append(hi, "  event types: "+strings.Join(typeBreakdown, " · "))
		}
		for i, e := range o.DeviceEvents {
			if i >= 4 {
				break
			}
			problems := strings.Join(e.PatientProblems, ", ")
			if problems == "" {
				problems = "(no patient problems coded)"
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s %s — %s — %s",
				e.DateReceived, e.EventType, e.BrandName, e.Manufacturer, hfTruncate(problems, 100)))
		}
	}
	return hi
}
