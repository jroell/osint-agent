package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type HTReconOutput struct {
	Target            string              `json:"target"`
	OperationsRun     []string            `json:"operations_run"`
	Subdomains        []string            `json:"subdomains,omitempty"`             // hostsearch
	IPMappings        []HTIPMapping       `json:"ip_mappings,omitempty"`             // hostsearch (subdomain → IP)
	ReverseIPDomains  []string            `json:"reverse_ip_domains,omitempty"`      // reverseiplookup (other domains on same IP)
	DNSRecords        []HTDNSRecord       `json:"dns_records,omitempty"`             // dnshost (A/AAAA/MX/NS/TXT)
	ASInfo            *HTASInfo           `json:"as_info,omitempty"`                 // aslookup
	WhoisRaw          string              `json:"whois_raw,omitempty"`                // whois
	UniqueIPs         []string            `json:"unique_ips,omitempty"`
	TotalSubdomains   int                 `json:"total_subdomains,omitempty"`
	TotalCohostedDomains int              `json:"total_cohosted_domains,omitempty"`
	Source            string              `json:"source"`
	TookMs            int64               `json:"tookMs"`
	Errors            map[string]string   `json:"errors,omitempty"`
	Note              string              `json:"note,omitempty"`
}

type HTIPMapping struct {
	Subdomain string `json:"subdomain"`
	IP        string `json:"ip"`
}

type HTDNSRecord struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type HTASInfo struct {
	IP    string `json:"ip"`
	ASN   string `json:"asn"`
	Range string `json:"range"`
	Org   string `json:"org"`
}

// HackerTargetRecon multi-endpoint OSINT recon via hackertarget.com's free
// no-auth API. Fans out parallel calls to 5 endpoints based on `ops` array
// (or runs all by default). Free tier: 100 req/day per source IP.
//
// Endpoints exposed:
//   - hostsearch        → enumerates subdomains + their A records (one of the
//                         best free passive subdomain sources)
//   - dnshost           → all DNS record types for a hostname
//   - reverseiplookup   → all OTHER domains hosted on the same IP (shared
//                         hosting / co-tenancy discovery; THE classic ER pivot)
//   - aslookup          → AS number, org name, IP range
//   - whois             → registrar metadata
//
// Composability: pairs with shodan_internetdb (per-IP fingerprint) and
// alienvault_otx_passive_dns (passive DNS shadow) for a complete
// infrastructure-recon stack.
func HackerTargetRecon(ctx context.Context, input map[string]any) (*HTReconOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil, errors.New("input.target required (domain or IP)")
	}

	// Default ops or user-specified.
	allOps := []string{"hostsearch", "dnshost", "reverseiplookup", "aslookup", "whois"}
	ops := allOps
	if v, ok := input["ops"].([]any); ok && len(v) > 0 {
		custom := []string{}
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				custom = append(custom, s)
			}
		}
		if len(custom) > 0 {
			ops = custom
		}
	}

	start := time.Now()
	out := &HTReconOutput{Target: target, OperationsRun: ops, Source: "hackertarget.com", Errors: map[string]string{}}

	type opResult struct {
		op   string
		body string
		err  error
	}
	resultsCh := make(chan opResult, len(ops))
	var wg sync.WaitGroup
	for _, op := range ops {
		wg.Add(1)
		go func(o string) {
			defer wg.Done()
			body, err := htFetch(ctx, o, target)
			resultsCh <- opResult{op: o, body: body, err: err}
		}(op)
	}
	wg.Wait()
	close(resultsCh)

	ipSet := map[string]bool{}

	for r := range resultsCh {
		if r.err != nil {
			out.Errors[r.op] = r.err.Error()
			continue
		}
		// hackertarget often returns "API count exceeded - getting rate limited" or similar plain-text errors with 200 status.
		if strings.Contains(r.body, "API count exceeded") || strings.Contains(r.body, "rate limited") || strings.Contains(strings.ToLower(r.body), "error check your search parameter") {
			out.Errors[r.op] = strings.TrimSpace(r.body)
			continue
		}
		switch r.op {
		case "hostsearch":
			// CSV: subdomain,ip per line
			for _, line := range strings.Split(r.body, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, ",", 2)
				if len(parts) != 2 {
					continue
				}
				sd, ip := strings.ToLower(parts[0]), parts[1]
				out.Subdomains = append(out.Subdomains, sd)
				out.IPMappings = append(out.IPMappings, HTIPMapping{Subdomain: sd, IP: ip})
				ipSet[ip] = true
			}
			sort.Strings(out.Subdomains)
			out.TotalSubdomains = len(out.Subdomains)
		case "dnshost":
			// CSV: type,value per line
			for _, line := range strings.Split(r.body, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, ",", 2)
				if len(parts) != 2 {
					continue
				}
				out.DNSRecords = append(out.DNSRecords, HTDNSRecord{Type: strings.TrimSpace(parts[0]), Value: strings.TrimSpace(parts[1])})
			}
		case "reverseiplookup":
			// One domain per line
			seen := map[string]bool{}
			for _, line := range strings.Split(r.body, "\n") {
				d := strings.ToLower(strings.TrimSpace(line))
				if d == "" || d == target || seen[d] {
					continue
				}
				seen[d] = true
				out.ReverseIPDomains = append(out.ReverseIPDomains, d)
			}
			sort.Strings(out.ReverseIPDomains)
			out.TotalCohostedDomains = len(out.ReverseIPDomains)
		case "aslookup":
			// Format: "IP","ASN","RANGE","Org Name"
			line := strings.TrimSpace(r.body)
			if line != "" {
				asInfo := parseHTAslookup(line)
				if asInfo != nil {
					out.ASInfo = asInfo
				}
			}
		case "whois":
			out.WhoisRaw = strings.TrimSpace(r.body)
			if len(out.WhoisRaw) > 8000 {
				out.WhoisRaw = out.WhoisRaw[:8000] + "\n[truncated]"
			}
		}
	}

	if len(ipSet) > 0 {
		for ip := range ipSet {
			out.UniqueIPs = append(out.UniqueIPs, ip)
		}
		sort.Strings(out.UniqueIPs)
	}

	out.TookMs = time.Since(start).Milliseconds()

	if len(out.Errors) > 0 && len(out.Subdomains)+len(out.DNSRecords)+len(out.ReverseIPDomains) == 0 && out.ASInfo == nil && out.WhoisRaw == "" {
		out.Note = "All operations failed — likely rate-limited (free tier: 100/day per source IP). Wait or use a different source IP."
	} else if len(out.Errors) > 0 {
		out.Note = fmt.Sprintf("%d of %d operations failed (likely partial rate-limit). See `errors` field.", len(out.Errors), len(ops))
	}
	return out, nil
}

func htFetch(ctx context.Context, op, target string) (string, error) {
	endpoint := fmt.Sprintf("https://api.hackertarget.com/%s/?q=%s", op, url.QueryEscape(target))
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/hackertarget-recon")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	return string(body), nil
}

func parseHTAslookup(line string) *HTASInfo {
	// Format like: "1.2.3.4","13335","1.2.3.0/24","CLOUDFLARENET, US"
	fields := []string{}
	current := strings.Builder{}
	inQuotes := false
	for _, ch := range line {
		switch {
		case ch == '"':
			inQuotes = !inQuotes
		case ch == ',' && !inQuotes:
			fields = append(fields, current.String())
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	if len(fields) < 4 {
		return nil
	}
	return &HTASInfo{
		IP:    fields[0],
		ASN:   fields[1],
		Range: fields[2],
		Org:   fields[3],
	}
}
