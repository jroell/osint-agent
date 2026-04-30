package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// CISAKEVLookup queries CISA's Known Exploited Vulnerabilities catalog —
// the federal-source list of CVEs that are known to be actively exploited
// in the wild. ~1500 entries, updated daily, free no-auth.
//
// Why this matters for ER / "connecting the dots":
//
//   - The catalog is the most authoritative public answer to "is CVE-X
//     actively exploited in the wild RIGHT NOW?". OSV/NVD tell you a
//     vulnerability exists; KEV tells you it's being weaponized.
//   - Each entry carries a `dueDate` — federal civilian agencies must
//     patch by that date under BOD 22-01. Anything past dueDate is
//     "officially overdue" — a uniquely time-sensitive signal.
//   - The `knownRansomwareCampaignUse` flag distinguishes nation-state /
//     APT exploitation from commodity ransomware. Surface separately.
//   - Pairs with osv_vuln_search (does this CVE exist?) and shodan
//     (is the affected service exposed?). Triangulates risk.
//
// Four modes:
//   - "lookup_cve"      : single CVE-ID → KEV record (or null + "not in KEV")
//   - "search_product"  : vendor or product → all KEV entries
//   - "recent"          : N most recent additions (default 14 days)
//   - "ransomware"      : KEV entries flagged for ransomware campaign use

type CISAKEVEntry struct {
	CVEID                       string   `json:"cve_id"`
	VendorProject               string   `json:"vendor_project"`
	Product                     string   `json:"product"`
	VulnerabilityName           string   `json:"vulnerability_name"`
	DateAdded                   string   `json:"date_added"`
	ShortDescription            string   `json:"short_description,omitempty"`
	RequiredAction              string   `json:"required_action,omitempty"`
	DueDate                     string   `json:"due_date,omitempty"`
	KnownRansomwareCampaignUse  string   `json:"known_ransomware_campaign_use,omitempty"` // "Known" | "Unknown"
	Notes                       string   `json:"notes,omitempty"`
	CWEs                        []string `json:"cwes,omitempty"`
	IsOverdue                   bool     `json:"is_overdue,omitempty"`        // dueDate has passed
	DaysOverdue                 int      `json:"days_overdue,omitempty"`
}

type CISAKEVLookupOutput struct {
	Mode               string         `json:"mode"`
	Query              string         `json:"query,omitempty"`
	CatalogVersion     string         `json:"catalog_version"`
	CatalogReleased    string         `json:"catalog_released"`
	TotalInCatalog     int            `json:"total_in_catalog"`
	MatchCount         int            `json:"match_count"`

	Entries            []CISAKEVEntry `json:"entries,omitempty"`
	NotInKEV           bool           `json:"not_in_kev,omitempty"` // for lookup_cve mode

	// Aggregations
	UniqueVendors      []string       `json:"unique_vendors,omitempty"`
	UniqueProducts     []string       `json:"unique_products,omitempty"`
	OverdueCount       int            `json:"overdue_count,omitempty"`
	RansomwareCount    int            `json:"ransomware_count,omitempty"`

	HighlightFindings  []string       `json:"highlight_findings"`
	Source             string         `json:"source"`
	TookMs             int64          `json:"tookMs"`
	Note               string         `json:"note,omitempty"`
}

// In-process cache (6h TTL) — catalog is small (~600KB JSON, 1500 rows)
// and changes once per business day on average.
var (
	cisaKevCache        []CISAKEVEntry
	cisaKevCacheVersion string
	cisaKevCacheRelease string
	cisaKevCacheLoaded  time.Time
	cisaKevCacheMu      sync.RWMutex
)

const cisaKevCacheTTL = 6 * time.Hour
const kevFeedURL = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"

func kevLoadCatalog(ctx context.Context) error {
	cisaKevCacheMu.RLock()
	if cisaKevCache != nil && time.Since(cisaKevCacheLoaded) < cisaKevCacheTTL {
		cisaKevCacheMu.RUnlock()
		return nil
	}
	cisaKevCacheMu.RUnlock()

	cisaKevCacheMu.Lock()
	defer cisaKevCacheMu.Unlock()
	// Re-check under write lock
	if cisaKevCache != nil && time.Since(cisaKevCacheLoaded) < cisaKevCacheTTL {
		return nil
	}

	cli := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", kevFeedURL, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("KEV fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("KEV fetch HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))

	var raw struct {
		CatalogVersion  string `json:"catalogVersion"`
		DateReleased    string `json:"dateReleased"`
		Vulnerabilities []struct {
			CVEID                      string   `json:"cveID"`
			VendorProject              string   `json:"vendorProject"`
			Product                    string   `json:"product"`
			VulnerabilityName          string   `json:"vulnerabilityName"`
			DateAdded                  string   `json:"dateAdded"`
			ShortDescription           string   `json:"shortDescription"`
			RequiredAction             string   `json:"requiredAction"`
			DueDate                    string   `json:"dueDate"`
			KnownRansomwareCampaignUse string   `json:"knownRansomwareCampaignUse"`
			Notes                      string   `json:"notes"`
			CWEs                       []string `json:"cwes"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("KEV decode: %w", err)
	}

	now := time.Now()
	entries := make([]CISAKEVEntry, 0, len(raw.Vulnerabilities))
	for _, v := range raw.Vulnerabilities {
		e := CISAKEVEntry{
			CVEID:                      v.CVEID,
			VendorProject:              strings.TrimSpace(v.VendorProject),
			Product:                    strings.TrimSpace(v.Product),
			VulnerabilityName:          v.VulnerabilityName,
			DateAdded:                  v.DateAdded,
			ShortDescription:           v.ShortDescription,
			RequiredAction:             v.RequiredAction,
			DueDate:                    v.DueDate,
			KnownRansomwareCampaignUse: v.KnownRansomwareCampaignUse,
			Notes:                      v.Notes,
			CWEs:                       v.CWEs,
		}
		// Compute overdue status
		if e.DueDate != "" {
			if t, err := time.Parse("2006-01-02", e.DueDate); err == nil {
				if now.After(t) {
					e.IsOverdue = true
					e.DaysOverdue = int(now.Sub(t).Hours() / 24)
				}
			}
		}
		entries = append(entries, e)
	}

	cisaKevCache = entries
	cisaKevCacheVersion = raw.CatalogVersion
	cisaKevCacheRelease = raw.DateReleased
	cisaKevCacheLoaded = now
	return nil
}

func CISAKEVLookup(ctx context.Context, input map[string]any) (*CISAKEVLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect: cve_id present → lookup_cve, query present → search_product, else → recent
		if _, ok := input["cve_id"]; ok {
			mode = "lookup_cve"
		} else if _, ok := input["query"]; ok {
			mode = "search_product"
		} else {
			mode = "recent"
		}
	}

	if err := kevLoadCatalog(ctx); err != nil {
		return nil, err
	}

	out := &CISAKEVLookupOutput{
		Mode:            mode,
		Source:          "cisa.gov/known-exploited-vulnerabilities",
		CatalogVersion:  cisaKevCacheVersion,
		CatalogReleased: cisaKevCacheRelease,
		TotalInCatalog:  len(cisaKevCache),
	}
	start := time.Now()

	cisaKevCacheMu.RLock()
	cat := cisaKevCache
	cisaKevCacheMu.RUnlock()

	switch mode {
	case "lookup_cve":
		cve, _ := input["cve_id"].(string)
		cve = strings.ToUpper(strings.TrimSpace(cve))
		if cve == "" {
			return nil, fmt.Errorf("input.cve_id required for lookup_cve")
		}
		out.Query = cve
		for _, e := range cat {
			if strings.EqualFold(e.CVEID, cve) {
				out.Entries = []CISAKEVEntry{e}
				out.MatchCount = 1
				break
			}
		}
		if out.MatchCount == 0 {
			out.NotInKEV = true
		}

	case "search_product":
		q, _ := input["query"].(string)
		q = strings.ToLower(strings.TrimSpace(q))
		if q == "" {
			return nil, fmt.Errorf("input.query required for search_product")
		}
		out.Query = q
		matches := []CISAKEVEntry{}
		for _, e := range cat {
			vendor := strings.ToLower(e.VendorProject)
			product := strings.ToLower(e.Product)
			if strings.Contains(vendor, q) || strings.Contains(product, q) {
				matches = append(matches, e)
			}
		}
		// Most-recent first
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].DateAdded > matches[j].DateAdded })
		limit := 50
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 500 {
			limit = int(l)
		}
		if len(matches) > limit {
			out.Note = fmt.Sprintf("returning %d of %d matches", limit, len(matches))
			matches = matches[:limit]
		}
		out.Entries = matches
		out.MatchCount = len(matches)

	case "recent":
		days := 14
		if d, ok := input["days"].(float64); ok && d > 0 && d <= 365 {
			days = int(d)
		}
		cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
		out.Query = fmt.Sprintf("dateAdded >= %s (last %d days)", cutoff, days)
		matches := []CISAKEVEntry{}
		for _, e := range cat {
			if e.DateAdded >= cutoff {
				matches = append(matches, e)
			}
		}
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].DateAdded > matches[j].DateAdded })
		out.Entries = matches
		out.MatchCount = len(matches)

	case "ransomware":
		matches := []CISAKEVEntry{}
		for _, e := range cat {
			if strings.EqualFold(e.KnownRansomwareCampaignUse, "Known") {
				matches = append(matches, e)
			}
		}
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].DateAdded > matches[j].DateAdded })
		limit := 100
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 1000 {
			limit = int(l)
		}
		if len(matches) > limit {
			out.Note = fmt.Sprintf("returning %d of %d ransomware-flagged entries", limit, len(matches))
			matches = matches[:limit]
		}
		out.Entries = matches
		out.MatchCount = len(matches)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: lookup_cve, search_product, recent, ransomware", mode)
	}

	// Aggregations
	vendorSet := map[string]struct{}{}
	productSet := map[string]struct{}{}
	for _, e := range out.Entries {
		if e.VendorProject != "" {
			vendorSet[e.VendorProject] = struct{}{}
		}
		if e.Product != "" {
			productSet[e.Product] = struct{}{}
		}
		if e.IsOverdue {
			out.OverdueCount++
		}
		if strings.EqualFold(e.KnownRansomwareCampaignUse, "Known") {
			out.RansomwareCount++
		}
	}
	for v := range vendorSet {
		out.UniqueVendors = append(out.UniqueVendors, v)
	}
	sort.Strings(out.UniqueVendors)
	for p := range productSet {
		out.UniqueProducts = append(out.UniqueProducts, p)
	}
	sort.Strings(out.UniqueProducts)

	out.HighlightFindings = buildKEVHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildKEVHighlights(o *CISAKEVLookupOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "lookup_cve":
		if o.NotInKEV {
			hi = append(hi, fmt.Sprintf("✓ %s is NOT in CISA KEV (not flagged as actively exploited)", o.Query))
			break
		}
		e := o.Entries[0]
		ransomMarker := ""
		if strings.EqualFold(e.KnownRansomwareCampaignUse, "Known") {
			ransomMarker = " 💀 [RANSOMWARE-USE]"
		}
		hi = append(hi, fmt.Sprintf("⚠️  %s — IN KEV%s", e.CVEID, ransomMarker))
		hi = append(hi, fmt.Sprintf("  vendor/product: %s / %s", e.VendorProject, e.Product))
		hi = append(hi, fmt.Sprintf("  vulnerability: %s", e.VulnerabilityName))
		hi = append(hi, fmt.Sprintf("  added to KEV: %s", e.DateAdded))
		if e.DueDate != "" {
			marker := ""
			if e.IsOverdue {
				marker = fmt.Sprintf(" ⏰ [%d DAYS OVERDUE]", e.DaysOverdue)
			}
			hi = append(hi, fmt.Sprintf("  federal patch deadline: %s%s", e.DueDate, marker))
		}
		if len(e.CWEs) > 0 {
			hi = append(hi, fmt.Sprintf("  CWE: %s", strings.Join(e.CWEs, ", ")))
		}
		hi = append(hi, fmt.Sprintf("  required action: %s", hfTruncate(e.RequiredAction, 200)))
		hi = append(hi, fmt.Sprintf("  description: %s", hfTruncate(e.ShortDescription, 240)))

	case "search_product":
		hi = append(hi, fmt.Sprintf("✓ %d KEV entries match '%s'", o.MatchCount, o.Query))
		if o.RansomwareCount > 0 {
			hi = append(hi, fmt.Sprintf("  💀 %d flagged for ransomware campaign use", o.RansomwareCount))
		}
		if o.OverdueCount > 0 {
			hi = append(hi, fmt.Sprintf("  ⏰ %d past federal patch deadline", o.OverdueCount))
		}
		for i, e := range o.Entries {
			if i >= 8 {
				break
			}
			marker := ""
			if strings.EqualFold(e.KnownRansomwareCampaignUse, "Known") {
				marker += " 💀"
			}
			if e.IsOverdue {
				marker += " ⏰"
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s — %s%s", e.DateAdded, e.CVEID, hfTruncate(e.VulnerabilityName, 80), marker))
		}

	case "recent":
		hi = append(hi, fmt.Sprintf("✓ %d KEV entries added in %s", o.MatchCount, o.Query))
		if o.RansomwareCount > 0 {
			hi = append(hi, fmt.Sprintf("  💀 %d flagged for ransomware campaign use", o.RansomwareCount))
		}
		for i, e := range o.Entries {
			if i >= 10 {
				break
			}
			marker := ""
			if strings.EqualFold(e.KnownRansomwareCampaignUse, "Known") {
				marker = " 💀"
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s — %s/%s — %s%s", e.DateAdded, e.CVEID, e.VendorProject, e.Product, hfTruncate(e.VulnerabilityName, 60), marker))
		}

	case "ransomware":
		hi = append(hi, fmt.Sprintf("✓ %d KEV entries flagged for known ransomware use (%d-entry catalog from %s)", o.MatchCount, o.TotalInCatalog, o.CatalogVersion))
		// Top vendors with ransomware-flagged CVEs
		vendorCount := map[string]int{}
		for _, e := range o.Entries {
			vendorCount[e.VendorProject]++
		}
		type vc struct {
			v string
			c int
		}
		topV := []vc{}
		for v, c := range vendorCount {
			topV = append(topV, vc{v, c})
		}
		sort.SliceStable(topV, func(i, j int) bool { return topV[i].c > topV[j].c })
		hi = append(hi, "  vendors most-targeted by ransomware (top 10):")
		for i, e := range topV {
			if i >= 10 {
				break
			}
			hi = append(hi, fmt.Sprintf("    %s: %d CVEs", e.v, e.c))
		}
		hi = append(hi, "  most recent ransomware-flagged additions:")
		for i, e := range o.Entries {
			if i >= 5 {
				break
			}
			hi = append(hi, fmt.Sprintf("    [%s] %s — %s/%s", e.DateAdded, e.CVEID, e.VendorProject, e.Product))
		}
	}
	hi = append(hi, fmt.Sprintf("  catalog version %s, released %s", o.CatalogVersion, o.CatalogReleased))
	return hi
}
