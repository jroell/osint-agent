package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type InternetDBRecord struct {
	IP        string   `json:"ip"`
	Ports     []int    `json:"ports"`
	CPEs      []string `json:"cpes"`
	Hostnames []string `json:"hostnames"`
	Tags      []string `json:"tags"`
	Vulns     []string `json:"vulns"`
	Note      string   `json:"note,omitempty"`
}

type ShodanInternetDBOutput struct {
	Targets       []string             `json:"targets"`
	ResolvedFrom  map[string][]string  `json:"resolved_from,omitempty"` // domain → IPs
	Records       []InternetDBRecord   `json:"records"`
	IPsQueried    int                  `json:"ips_queried"`
	IPsFound      int                  `json:"ips_with_data"`
	UniquePorts   []int                `json:"unique_ports"`
	UniqueVulns   []string             `json:"unique_vulns"`
	UniqueTags    []string             `json:"unique_tags"`
	UniqueCPEs    []string             `json:"unique_cpes"`
	Source        string               `json:"source"`
	TookMs        int64                `json:"tookMs"`
	Note          string               `json:"note,omitempty"`
}

// ShodanInternetDB queries Shodan's free, no-authentication InternetDB API
// (https://internetdb.shodan.io/<ip>) for fingerprint data on one or more IPs.
// Unlike the paid /shodan/host/ API, InternetDB requires no key and is rate-
// limited only by reasonable use. Returns: open ports, CPEs (Common Platform
// Enumeration software fingerprints), Shodan tags (cdn, vpn, self-signed,
// etc.), CVE IDs of known vulnerabilities, and reverse DNS hostnames.
//
// Pairs naturally with passive DNS / subfinder output — given a domain or
// list of subdomains, this tool resolves them and enriches each IP with
// fingerprint data. Critical for the "what's actually running here?"
// question without burning paid Shodan API credits.
//
// Accepts either:
//   - "ip" (single IPv4)
//   - "ips" ([]string of IPs)
//   - "domain" (a hostname; resolved A records, then each enriched)
func ShodanInternetDB(ctx context.Context, input map[string]any) (*ShodanInternetDBOutput, error) {
	start := time.Now()

	var targets []string
	resolvedFrom := map[string][]string{}

	if domain, _ := input["domain"].(string); strings.TrimSpace(domain) != "" {
		domain = strings.ToLower(strings.TrimSpace(domain))
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupIPAddr(rctx, domain)
		if err != nil {
			return nil, fmt.Errorf("DNS resolve failed for %s: %w", domain, err)
		}
		var ipList []string
		for _, ip := range ips {
			s := ip.IP.String()
			if !strings.Contains(s, ":") { // IPv4 only — InternetDB primarily indexes v4
				ipList = append(ipList, s)
			}
		}
		if len(ipList) == 0 {
			return nil, fmt.Errorf("domain %s has no IPv4 A records", domain)
		}
		targets = ipList
		resolvedFrom[domain] = ipList
	}

	if ip, _ := input["ip"].(string); strings.TrimSpace(ip) != "" {
		targets = append(targets, strings.TrimSpace(ip))
	}
	if arr, ok := input["ips"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				targets = append(targets, strings.TrimSpace(s))
			}
		}
	}

	// Dedup
	seen := map[string]bool{}
	deduped := make([]string, 0, len(targets))
	for _, t := range targets {
		if !seen[t] {
			seen[t] = true
			deduped = append(deduped, t)
		}
	}
	targets = deduped

	if len(targets) == 0 {
		return nil, errors.New("must provide one of: 'ip', 'ips' [], or 'domain'")
	}
	if len(targets) > 50 {
		targets = targets[:50] // safety cap
	}

	// Parallel fetch with concurrency cap.
	results := make([]InternetDBRecord, len(targets))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = fetchInternetDB(ctx, ip)
		}(i, t)
	}
	wg.Wait()

	// Aggregate.
	allPorts := map[int]bool{}
	allVulns := map[string]bool{}
	allTags := map[string]bool{}
	allCPEs := map[string]bool{}
	withData := 0
	records := make([]InternetDBRecord, 0, len(results))
	for _, r := range results {
		if len(r.Ports)+len(r.Vulns)+len(r.Tags)+len(r.CPEs) > 0 {
			withData++
			records = append(records, r)
		} else {
			records = append(records, r)
		}
		for _, p := range r.Ports {
			allPorts[p] = true
		}
		for _, v := range r.Vulns {
			allVulns[v] = true
		}
		for _, tg := range r.Tags {
			allTags[tg] = true
		}
		for _, c := range r.CPEs {
			allCPEs[c] = true
		}
	}

	uniquePorts := make([]int, 0, len(allPorts))
	for p := range allPorts {
		uniquePorts = append(uniquePorts, p)
	}
	sort.Ints(uniquePorts)
	uniqueVulns := keysSorted(allVulns)
	uniqueTags := keysSorted(allTags)
	uniqueCPEs := keysSorted(allCPEs)

	out := &ShodanInternetDBOutput{
		Targets:      targets,
		ResolvedFrom: resolvedFrom,
		Records:      records,
		IPsQueried:   len(targets),
		IPsFound:     withData,
		UniquePorts:  uniquePorts,
		UniqueVulns:  uniqueVulns,
		UniqueTags:   uniqueTags,
		UniqueCPEs:   uniqueCPEs,
		Source:       "internetdb.shodan.io",
		TookMs:       time.Since(start).Milliseconds(),
	}
	if withData == 0 {
		out.Note = "No InternetDB data — IPs may not have been recently scanned, or only have v6/CDN records"
	}
	return out, nil
}

func fetchInternetDB(ctx context.Context, ip string) InternetDBRecord {
	rec := InternetDBRecord{IP: ip}
	url := "https://internetdb.shodan.io/" + ip
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		rec.Note = "request build failed: " + err.Error()
		return rec
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		rec.Note = "fetch failed: " + err.Error()
		return rec
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 404 {
		rec.Note = "not in InternetDB (no recent Shodan scan data)"
		return rec
	}
	if resp.StatusCode != 200 {
		rec.Note = fmt.Sprintf("status %d", resp.StatusCode)
		return rec
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		rec.Note = "json parse failed: " + err.Error()
	}
	return rec
}

func keysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
