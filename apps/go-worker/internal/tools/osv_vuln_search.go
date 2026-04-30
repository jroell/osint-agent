package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// OSVReference is one external reference on a vulnerability.
type OSVReference struct {
	Type string `json:"type,omitempty"` // ADVISORY | WEB | PACKAGE | REPORT | FIX | etc.
	URL  string `json:"url"`
}

// OSVAffectedPackage is one affected package across the vulnerability.
type OSVAffectedPackage struct {
	Ecosystem string `json:"ecosystem,omitempty"` // npm | PyPI | Go | crates.io | Maven | RubyGems | etc.
	Name      string `json:"name,omitempty"`
	Purl      string `json:"purl,omitempty"`
}

// OSVSeverity is one CVSS-style score entry.
type OSVSeverity struct {
	Type  string `json:"type"`  // CVSS_V3 | CVSS_V2 | etc.
	Score string `json:"score"` // CVSS vector string
}

// OSVVulnerability is one vulnerability record.
type OSVVulnerability struct {
	ID                string               `json:"id"`
	Aliases           []string             `json:"aliases,omitempty"`
	Summary           string               `json:"summary,omitempty"`
	Details           string               `json:"details,omitempty"`
	Modified          string               `json:"modified,omitempty"`
	Published         string               `json:"published,omitempty"`
	Withdrawn         string               `json:"withdrawn,omitempty"`
	AffectedPackages  []OSVAffectedPackage `json:"affected_packages,omitempty"`
	Severity          []OSVSeverity        `json:"severity,omitempty"`
	References        []OSVReference       `json:"references,omitempty"`
	GitHubReviewed    bool                 `json:"github_reviewed,omitempty"`
	CWEIDs            []string             `json:"cwe_ids,omitempty"`
	SeverityLabel     string               `json:"severity_label,omitempty"` // LOW | MODERATE | HIGH | CRITICAL
}

// OSVEcosystemAggregate counts vulns per ecosystem.
type OSVEcosystemAggregate struct {
	Ecosystem string `json:"ecosystem"`
	Count     int    `json:"count"`
}

// OSVSeverityAggregate counts vulns per severity label.
type OSVSeverityAggregate struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// OSVVulnSearchOutput is the response.
type OSVVulnSearchOutput struct {
	Mode               string                 `json:"mode"`
	Query              string                 `json:"query"`
	TotalVulns         int                    `json:"total_vulns"`
	Vulnerabilities    []OSVVulnerability     `json:"vulnerabilities,omitempty"`
	TopEcosystems      []OSVEcosystemAggregate `json:"top_ecosystems,omitempty"`
	SeverityBreakdown  []OSVSeverityAggregate `json:"severity_breakdown,omitempty"`
	UniqueAliases      []string               `json:"unique_aliases,omitempty"`
	HighlightFindings  []string               `json:"highlight_findings"`
	Source             string                 `json:"source"`
	TookMs             int64                  `json:"tookMs"`
	Note               string                 `json:"note,omitempty"`
}

// raw structs
type osvRawVuln struct {
	ID         string `json:"id"`
	Aliases    []string `json:"aliases"`
	Summary    string `json:"summary"`
	Details    string `json:"details"`
	Modified   string `json:"modified"`
	Published  string `json:"published"`
	Withdrawn  string `json:"withdrawn"`
	Affected   []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
			Purl      string `json:"purl"`
		} `json:"package"`
	} `json:"affected"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	References []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"references"`
	DatabaseSpecific map[string]any `json:"database_specific"`
}

type osvQueryRespRaw struct {
	Vulns []osvRawVuln `json:"vulns"`
}

// OSVVulnSearch queries api.osv.dev for open-source vulnerability data.
// Free, no auth. Three modes:
//
//   - "lookup"  : direct ID lookup (CVE/GHSA/PYSEC/RUSTSEC/MAL/GO/etc.)
//   - "package" : vulnerabilities affecting a specific package — requires
//                 input.ecosystem (e.g. "npm", "PyPI", "Go", "crates.io",
//                 "Maven", "RubyGems", "Packagist", "Pub", "NuGet",
//                 "Hex", "Hackage")
//   - "commit"  : vulnerabilities tracked at a specific git commit hash
//
// Why this matters for ER:
//   - Distinct from cve_intel_chain (which is NVD/EPSS/KEV) — OSV is the
//     canonical PACKAGE-ECOSYSTEM vulnerability database. CVE-2021-44228
//     (Log4Shell) appears in both, but `npm install xss-validator@1.0.0`
//     vulnerabilities only show up in OSV's npm namespace.
//   - Aggregates GitHub Advisory Database (GHSA) + Linux distros + every
//     major package manager into one query.
//   - For software-supply-chain ER, OSV is the lookup pattern most security
//     scanning tools (Snyk, Trivy, etc.) actually use under the hood.
//   - The commit-hash mode is unique: given a git commit, lists every CVE
//     that was either introduced or fixed at that commit.
func OSVVulnSearch(ctx context.Context, input map[string]any) (*OSVVulnSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "lookup"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (vuln ID for lookup, package name for package mode, commit SHA for commit mode)")
	}

	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &OSVVulnSearchOutput{
		Mode:   mode,
		Query:  query,
		Source: "api.osv.dev",
	}
	start := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "lookup":
		v, err := osvLookupOne(ctx, client, query)
		if err != nil {
			return nil, err
		}
		if v == nil {
			out.Note = fmt.Sprintf("no OSV record for ID '%s'", query)
			out.HighlightFindings = []string{out.Note}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		out.Vulnerabilities = []OSVVulnerability{*v}
		out.TotalVulns = 1
	case "package":
		ecosystem, _ := input["ecosystem"].(string)
		ecosystem = strings.TrimSpace(ecosystem)
		if ecosystem == "" {
			return nil, fmt.Errorf("input.ecosystem required for package mode (e.g. 'npm', 'PyPI', 'Go', 'crates.io', 'Maven', 'RubyGems')")
		}
		var version string
		if v, ok := input["version"].(string); ok {
			version = strings.TrimSpace(v)
		}
		body := map[string]any{
			"package": map[string]any{
				"name":      query,
				"ecosystem": ecosystem,
			},
		}
		if version != "" {
			body["version"] = version
		}
		vulns, err := osvQuery(ctx, client, body)
		if err != nil {
			return nil, err
		}
		// Cap to limit
		if len(vulns) > limit {
			vulns = vulns[:limit]
		}
		for i := range vulns {
			out.Vulnerabilities = append(out.Vulnerabilities, materializeOSVVuln(&vulns[i]))
		}
		out.TotalVulns = len(out.Vulnerabilities)
	case "commit":
		body := map[string]any{"commit": query}
		vulns, err := osvQuery(ctx, client, body)
		if err != nil {
			return nil, err
		}
		if len(vulns) > limit {
			vulns = vulns[:limit]
		}
		for i := range vulns {
			out.Vulnerabilities = append(out.Vulnerabilities, materializeOSVVuln(&vulns[i]))
		}
		out.TotalVulns = len(out.Vulnerabilities)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: lookup, package, commit", mode)
	}

	// Aggregations
	ecoAgg := map[string]int{}
	sevAgg := map[string]int{}
	aliasSet := map[string]struct{}{}
	for _, v := range out.Vulnerabilities {
		for _, p := range v.AffectedPackages {
			if p.Ecosystem != "" {
				ecoAgg[p.Ecosystem]++
			}
		}
		if v.SeverityLabel != "" {
			sevAgg[v.SeverityLabel]++
		}
		for _, a := range v.Aliases {
			aliasSet[a] = struct{}{}
		}
	}
	for e, c := range ecoAgg {
		out.TopEcosystems = append(out.TopEcosystems, OSVEcosystemAggregate{Ecosystem: e, Count: c})
	}
	sort.SliceStable(out.TopEcosystems, func(i, j int) bool { return out.TopEcosystems[i].Count > out.TopEcosystems[j].Count })

	for s, c := range sevAgg {
		out.SeverityBreakdown = append(out.SeverityBreakdown, OSVSeverityAggregate{Label: s, Count: c})
	}
	sort.SliceStable(out.SeverityBreakdown, func(i, j int) bool {
		// CRITICAL > HIGH > MODERATE > LOW
		order := func(s string) int {
			switch strings.ToUpper(s) {
			case "CRITICAL":
				return 4
			case "HIGH":
				return 3
			case "MODERATE":
				return 2
			case "LOW":
				return 1
			}
			return 0
		}
		return order(out.SeverityBreakdown[i].Label) > order(out.SeverityBreakdown[j].Label)
	})

	for a := range aliasSet {
		out.UniqueAliases = append(out.UniqueAliases, a)
	}
	sort.Strings(out.UniqueAliases)

	out.HighlightFindings = buildOSVHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func osvLookupOne(ctx context.Context, client *http.Client, id string) (*OSVVulnerability, error) {
	endpoint := "https://api.osv.dev/v1/vulns/" + id
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("osv lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("osv %d: %s", resp.StatusCode, string(body))
	}
	var raw osvRawVuln
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.ID == "" {
		return nil, nil
	}
	v := materializeOSVVuln(&raw)
	return &v, nil
}

func osvQuery(ctx context.Context, client *http.Client, body map[string]any) ([]osvRawVuln, error) {
	bodyBytes, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.osv.dev/v1/query", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("osv query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("osv %d: %s", resp.StatusCode, string(body))
	}
	var raw osvQueryRespRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw.Vulns, nil
}

func materializeOSVVuln(r *osvRawVuln) OSVVulnerability {
	v := OSVVulnerability{
		ID:        r.ID,
		Aliases:   r.Aliases,
		Summary:   r.Summary,
		Details:   hfTruncate(r.Details, 800),
		Modified:  r.Modified,
		Published: r.Published,
		Withdrawn: r.Withdrawn,
	}
	// affected packages — dedupe by name+ecosystem
	pkgSet := map[string]bool{}
	for _, a := range r.Affected {
		key := a.Package.Ecosystem + "|" + a.Package.Name
		if pkgSet[key] {
			continue
		}
		pkgSet[key] = true
		v.AffectedPackages = append(v.AffectedPackages, OSVAffectedPackage{
			Ecosystem: a.Package.Ecosystem,
			Name:      a.Package.Name,
			Purl:      a.Package.Purl,
		})
	}
	if len(v.AffectedPackages) > 20 {
		v.AffectedPackages = v.AffectedPackages[:20]
	}
	for _, s := range r.Severity {
		v.Severity = append(v.Severity, OSVSeverity{Type: s.Type, Score: s.Score})
	}
	for _, ref := range r.References {
		v.References = append(v.References, OSVReference{Type: ref.Type, URL: ref.URL})
	}
	if len(v.References) > 10 {
		v.References = v.References[:10]
	}
	if r.DatabaseSpecific != nil {
		if g, ok := r.DatabaseSpecific["github_reviewed"].(bool); ok {
			v.GitHubReviewed = g
		}
		if cwe, ok := r.DatabaseSpecific["cwe_ids"].([]any); ok {
			for _, c := range cwe {
				if s, ok := c.(string); ok {
					v.CWEIDs = append(v.CWEIDs, s)
				}
			}
		}
		if s, ok := r.DatabaseSpecific["severity"].(string); ok {
			v.SeverityLabel = s
		}
	}
	return v
}

func buildOSVHighlights(o *OSVVulnSearchOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("%d vulnerabilities returned for mode=%s query='%s'", o.TotalVulns, o.Mode, o.Query))
	if o.TotalVulns == 0 {
		return hi
	}
	if o.Mode == "lookup" && len(o.Vulnerabilities) > 0 {
		v := o.Vulnerabilities[0]
		hi = append(hi, fmt.Sprintf("✓ %s — %s", v.ID, v.Summary))
		if len(v.Aliases) > 0 {
			hi = append(hi, "aliases: "+strings.Join(v.Aliases, ", "))
		}
		if v.SeverityLabel != "" {
			hi = append(hi, "🚨 severity label: "+v.SeverityLabel)
		}
		if len(v.Severity) > 0 {
			parts := []string{}
			for _, s := range v.Severity {
				parts = append(parts, s.Type+"="+s.Score)
			}
			hi = append(hi, "📊 CVSS: "+strings.Join(parts, " | "))
		}
		if len(v.AffectedPackages) > 0 {
			ecoSet := map[string]bool{}
			for _, p := range v.AffectedPackages {
				ecoSet[p.Ecosystem] = true
			}
			ecos := []string{}
			for e := range ecoSet {
				if e != "" {
					ecos = append(ecos, e)
				}
			}
			sort.Strings(ecos)
			hi = append(hi, fmt.Sprintf("📦 affects %d package(s) across ecosystems: %s", len(v.AffectedPackages), strings.Join(ecos, ", ")))
		}
		if v.Withdrawn != "" {
			hi = append(hi, "⚠️  WITHDRAWN — vulnerability has been retracted: "+v.Withdrawn)
		}
		if len(v.CWEIDs) > 0 {
			hi = append(hi, "CWE: "+strings.Join(v.CWEIDs, ", "))
		}
	}
	if o.Mode == "package" && o.TotalVulns > 0 {
		critCount := 0
		highCount := 0
		for _, v := range o.Vulnerabilities {
			switch strings.ToUpper(v.SeverityLabel) {
			case "CRITICAL":
				critCount++
			case "HIGH":
				highCount++
			}
		}
		if critCount > 0 || highCount > 0 {
			hi = append(hi, fmt.Sprintf("🚨 %d CRITICAL + %d HIGH severity vulns affect this package", critCount, highCount))
		}
	}
	if o.Mode == "commit" && o.TotalVulns > 0 {
		hi = append(hi, fmt.Sprintf("⚠️  %d vulnerabilities tracked at this commit (introduced or fixed)", o.TotalVulns))
	}
	if len(o.SeverityBreakdown) > 0 && o.Mode != "lookup" {
		parts := []string{}
		for _, s := range o.SeverityBreakdown {
			parts = append(parts, fmt.Sprintf("%s=%d", s.Label, s.Count))
		}
		hi = append(hi, "severity breakdown: "+strings.Join(parts, ", "))
	}
	if len(o.TopEcosystems) > 1 && o.Mode != "lookup" {
		parts := []string{}
		for _, e := range o.TopEcosystems[:min2(5, len(o.TopEcosystems))] {
			parts = append(parts, fmt.Sprintf("%s(%d)", e.Ecosystem, e.Count))
		}
		hi = append(hi, "ecosystem breakdown: "+strings.Join(parts, ", "))
	}
	return hi
}
