package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EPSSScore queries first.org's Exploit Prediction Scoring System API.
//
// EPSS is the actuarial leading indicator that complements CISA KEV:
//
//   - **CISA KEV** says: this CVE is being EXPLOITED RIGHT NOW.
//   - **EPSS** says:    probability this CVE will be exploited in the
//                       next 30 days, on a 0.0–1.0 scale, plus its
//                       percentile rank against the entire CVE corpus.
//
// EPSS is updated daily by FIRST.org based on machine-learning over
// real-world exploitation telemetry (PoCs published, social-media
// signal, threat-feed mentions, etc.). It's the only public dataset
// that gives a *probability* rather than a binary flag.
//
// Free, no auth.
//
// Three modes:
//
//   - "lookup_cves"  : 1–100 CVE IDs → EPSS records (cve, epss, percentile, date)
//   - "top"          : N highest-EPSS CVEs across the global corpus
//                      (the "what's everyone exploiting right now" view)
//   - "time_series"  : historical EPSS values for one CVE (track velocity)
//
// Cross-referencing with KEV gives the full prioritization picture:
//
//     KEV=true  + EPSS>0.9  →  CRITICAL: actively exploited and probability ↑
//     KEV=true  + EPSS<0.5  →  HIGH:     exploited but probability shrinking
//     KEV=false + EPSS>0.9  →  HIGH:     LEADING INDICATOR — patch before KEV
//     KEV=false + EPSS<0.1  →  LOW:      defer

type EPSSEntry struct {
	CVE        string  `json:"cve"`
	EPSS       float64 `json:"epss"`
	Percentile float64 `json:"percentile"`
	Date       string  `json:"date,omitempty"`

	// Derived bucket
	Severity string `json:"severity,omitempty"` // critical|high|medium|low
}

type EPSSScoreOutput struct {
	Mode              string      `json:"mode"`
	Query             string      `json:"query,omitempty"`

	Entries           []EPSSEntry `json:"entries,omitempty"`
	NotFound          []string    `json:"not_found,omitempty"`

	// Aggregate counts
	TotalReturned     int         `json:"total_returned"`
	TotalAvailable    int         `json:"total_available,omitempty"`
	CriticalCount     int         `json:"critical_count,omitempty"` // epss > 0.9
	HighCount         int         `json:"high_count,omitempty"`     // 0.5 < epss <= 0.9
	MediumCount       int         `json:"medium_count,omitempty"`   // 0.1 < epss <= 0.5
	LowCount          int         `json:"low_count,omitempty"`      // epss <= 0.1

	// Highest-priority entry summary (top by EPSS in the result set)
	TopByEPSS         *EPSSEntry  `json:"top_by_epss,omitempty"`

	HighlightFindings []string    `json:"highlight_findings"`
	Source            string      `json:"source"`
	TookMs            int64       `json:"tookMs"`
	Note              string      `json:"note,omitempty"`
}

func epssBucket(score float64) string {
	switch {
	case score > 0.9:
		return "critical"
	case score > 0.5:
		return "high"
	case score > 0.1:
		return "medium"
	default:
		return "low"
	}
}

func EPSSScore(ctx context.Context, input map[string]any) (*EPSSScoreOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["cves"]; ok {
			mode = "lookup_cves"
		} else if _, ok := input["cve"]; ok {
			mode = "time_series"
		} else {
			mode = "top"
		}
	}

	out := &EPSSScoreOutput{
		Mode:   mode,
		Source: "first.org/data/v1/epss",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "lookup_cves":
		raw, _ := input["cves"]
		var cveList []string
		switch v := raw.(type) {
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok && s != "" {
					cveList = append(cveList, strings.ToUpper(strings.TrimSpace(s)))
				}
			}
		case string:
			for _, s := range strings.Split(v, ",") {
				s = strings.ToUpper(strings.TrimSpace(s))
				if s != "" {
					cveList = append(cveList, s)
				}
			}
		}
		if len(cveList) == 0 {
			return nil, fmt.Errorf("input.cves required (array of strings or comma-separated string)")
		}
		if len(cveList) > 100 {
			out.Note = fmt.Sprintf("input list capped to 100 CVEs (got %d)", len(cveList))
			cveList = cveList[:100]
		}
		out.Query = strings.Join(cveList, ",")
		// First.org accepts comma-separated cve= param up to ~100 CVEs at once
		params := url.Values{}
		params.Set("cve", strings.Join(cveList, ","))
		entries, total, err := epssQuery(ctx, cli, params)
		if err != nil {
			return nil, err
		}
		out.Entries = entries
		out.TotalAvailable = total
		// Compute not-found set
		found := map[string]struct{}{}
		for _, e := range entries {
			found[strings.ToUpper(e.CVE)] = struct{}{}
		}
		for _, c := range cveList {
			if _, ok := found[c]; !ok {
				out.NotFound = append(out.NotFound, c)
			}
		}

	case "top":
		limit := 20
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
			limit = int(l)
		}
		out.Query = fmt.Sprintf("top %d by EPSS", limit)
		params := url.Values{}
		params.Set("order", "!epss")
		params.Set("limit", strconv.Itoa(limit))
		// Optional EPSS threshold filter
		if min, ok := input["epss_min"].(float64); ok && min >= 0 && min <= 1 {
			params.Set("epss-gt", fmt.Sprintf("%.4f", min))
		}
		entries, total, err := epssQuery(ctx, cli, params)
		if err != nil {
			return nil, err
		}
		out.Entries = entries
		out.TotalAvailable = total

	case "time_series":
		cve, _ := input["cve"].(string)
		cve = strings.ToUpper(strings.TrimSpace(cve))
		if cve == "" {
			return nil, fmt.Errorf("input.cve required for time_series mode")
		}
		out.Query = cve
		params := url.Values{}
		params.Set("cve", cve)
		params.Set("scope", "time-series")
		entries, total, err := epssQuery(ctx, cli, params)
		if err != nil {
			return nil, err
		}
		// Sort chronological (oldest first)
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Date < entries[j].Date })
		out.Entries = entries
		out.TotalAvailable = total

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: lookup_cves, top, time_series", mode)
	}

	// Aggregations + bucket assignment
	for i := range out.Entries {
		out.Entries[i].Severity = epssBucket(out.Entries[i].EPSS)
		switch out.Entries[i].Severity {
		case "critical":
			out.CriticalCount++
		case "high":
			out.HighCount++
		case "medium":
			out.MediumCount++
		case "low":
			out.LowCount++
		}
	}
	out.TotalReturned = len(out.Entries)

	// Top by EPSS
	if len(out.Entries) > 0 {
		topIdx := 0
		for i, e := range out.Entries {
			if e.EPSS > out.Entries[topIdx].EPSS {
				topIdx = i
			}
		}
		t := out.Entries[topIdx]
		out.TopByEPSS = &t
	}

	out.HighlightFindings = buildEPSSHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func epssQuery(ctx context.Context, cli *http.Client, params url.Values) ([]EPSSEntry, int, error) {
	u := "https://api.first.org/data/v1/epss?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("epss: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, 0, fmt.Errorf("epss HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	// EPSS API returns numeric fields as STRINGS — parse manually.
	var raw struct {
		Status string `json:"status"`
		Total  int    `json:"total"`
		Data   []struct {
			CVE        string `json:"cve"`
			EPSS       string `json:"epss"`
			Percentile string `json:"percentile"`
			Date       string `json:"date"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, fmt.Errorf("epss decode: %w", err)
	}
	out := make([]EPSSEntry, 0, len(raw.Data))
	for _, d := range raw.Data {
		entry := EPSSEntry{
			CVE:  d.CVE,
			Date: d.Date,
		}
		if v, err := strconv.ParseFloat(d.EPSS, 64); err == nil {
			entry.EPSS = v
		}
		if v, err := strconv.ParseFloat(d.Percentile, 64); err == nil {
			entry.Percentile = v
		}
		out = append(out, entry)
	}
	return out, raw.Total, nil
}

func buildEPSSHighlights(o *EPSSScoreOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "lookup_cves":
		hi = append(hi, fmt.Sprintf("✓ EPSS scores for %d CVEs (out of %d available globally)", o.TotalReturned, o.TotalAvailable))
		if len(o.NotFound) > 0 {
			hi = append(hi, fmt.Sprintf("  ⚠️  %d CVE(s) have no EPSS score: %s", len(o.NotFound), strings.Join(o.NotFound, ", ")))
		}
		hi = append(hi, fmt.Sprintf("  severity buckets: 🔥 %d critical (>0.9) · ⚠️  %d high (0.5–0.9) · 🟡 %d medium (0.1–0.5) · ⚪ %d low (≤0.1)",
			o.CriticalCount, o.HighCount, o.MediumCount, o.LowCount))
		// Sort entries by EPSS desc for display
		sorted := make([]EPSSEntry, len(o.Entries))
		copy(sorted, o.Entries)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].EPSS > sorted[j].EPSS })
		for i, e := range sorted {
			if i >= 10 {
				break
			}
			marker := ""
			switch e.Severity {
			case "critical":
				marker = " 🔥"
			case "high":
				marker = " ⚠️"
			}
			hi = append(hi, fmt.Sprintf("  • %s — EPSS %.4f (%.2f%%ile)%s", e.CVE, e.EPSS, e.Percentile*100, marker))
		}

	case "top":
		hi = append(hi, fmt.Sprintf("✓ Top %d most-exploitation-probable CVEs globally (date %s)", o.TotalReturned, func() string {
			if len(o.Entries) > 0 {
				return o.Entries[0].Date
			}
			return ""
		}()))
		hi = append(hi, fmt.Sprintf("  severity buckets: 🔥 %d critical · ⚠️  %d high · 🟡 %d medium · ⚪ %d low",
			o.CriticalCount, o.HighCount, o.MediumCount, o.LowCount))
		for i, e := range o.Entries {
			if i >= 10 {
				break
			}
			marker := ""
			switch e.Severity {
			case "critical":
				marker = " 🔥"
			case "high":
				marker = " ⚠️"
			}
			hi = append(hi, fmt.Sprintf("  [%2d] %s — EPSS %.4f (%.2f%%ile)%s", i+1, e.CVE, e.EPSS, e.Percentile*100, marker))
		}

	case "time_series":
		hi = append(hi, fmt.Sprintf("✓ EPSS time-series for %s — %d data points", o.Query, o.TotalReturned))
		if len(o.Entries) > 0 {
			first := o.Entries[0]
			last := o.Entries[len(o.Entries)-1]
			delta := last.EPSS - first.EPSS
			arrow := "→"
			if delta > 0.1 {
				arrow = "↑"
			} else if delta < -0.1 {
				arrow = "↓"
			}
			hi = append(hi, fmt.Sprintf("  velocity: %.4f → %.4f (Δ %+.4f) %s", first.EPSS, last.EPSS, delta, arrow))
			hi = append(hi, fmt.Sprintf("  current bucket: %s", last.Severity))
			// Show 5 most recent points
			recent := o.Entries
			if len(recent) > 5 {
				recent = recent[len(recent)-5:]
			}
			for _, e := range recent {
				hi = append(hi, fmt.Sprintf("    [%s] EPSS %.4f (%.2f%%ile)", e.Date, e.EPSS, e.Percentile*100))
			}
		}
	}
	return hi
}
