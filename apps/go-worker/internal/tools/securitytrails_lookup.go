package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// SecurityTrailsLookup wraps the SecurityTrails API. Paid; REQUIRES
// `SECURITYTRAILS_API_KEY`.
//
// SecurityTrails is the most commonly used historical-WHOIS + DNS-history
// source. Closes the "find every domain ever associated with this email"
// and "what was this domain's nameservers in 2018" gaps.
//
// Modes:
//   - "domain"          : current DNS + WHOIS for a domain
//   - "domain_history"  : historical DNS records (a, ns, mx, soa, txt, aaaa)
//   - "subdomains"      : list known subdomains
//   - "associated"      : domains associated with the same registrar/email
//   - "ip_neighbors"    : domains hosted on the same IP
//   - "whois_history"   : historical WHOIS for a domain
//
// Knowledge-graph: emits typed entities (kind: "domain" | "ip_address" |
// "dns_record") with stable identifiers.

type STDomain struct {
	Hostname    string         `json:"hostname"`
	CurrentDNS  map[string]any `json:"current_dns,omitempty"`
	Subdomains  []string       `json:"subdomains,omitempty"`
	WHOISRecord map[string]any `json:"whois_record,omitempty"`
}

type STHistoryRecord struct {
	Type         string `json:"type"`
	FirstSeen    string `json:"first_seen,omitempty"`
	LastSeen     string `json:"last_seen,omitempty"`
	Value        string `json:"value,omitempty"`
	Organization string `json:"organization,omitempty"`
}

type STEntity struct {
	Kind        string         `json:"kind"`
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	URL         string         `json:"url,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type SecurityTrailsLookupOutput struct {
	Mode              string            `json:"mode"`
	Query             string            `json:"query,omitempty"`
	Returned          int               `json:"returned"`
	Domain            *STDomain         `json:"domain,omitempty"`
	HistoryRecords    []STHistoryRecord `json:"history_records,omitempty"`
	Detail            map[string]any    `json:"detail,omitempty"`
	Entities          []STEntity        `json:"entities"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source            string            `json:"source"`
	TookMs            int64             `json:"tookMs"`
}

func SecurityTrailsLookup(ctx context.Context, input map[string]any) (*SecurityTrailsLookupOutput, error) {
	apiKey := os.Getenv("SECURITYTRAILS_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("SECURITYTRAILS_API_KEY not set; subscribe at securitytrails.com")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	domain, _ := input["domain"].(string)
	domain = strings.ToLower(strings.TrimSpace(domain))

	if mode == "" {
		switch {
		case input["ip"] != nil:
			mode = "ip_neighbors"
		case domain != "":
			mode = "domain"
		default:
			return nil, fmt.Errorf("input.domain or input.ip required")
		}
	}
	out := &SecurityTrailsLookupOutput{Mode: mode, Source: "api.securitytrails.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(path string) (map[string]any, error) {
		u := "https://api.securitytrails.com/v1" + path
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("APIKEY", apiKey)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("securitytrails: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("securitytrails: unauthorized — check API key")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("securitytrails HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("securitytrails decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "domain":
		if domain == "" {
			return nil, fmt.Errorf("input.domain required")
		}
		out.Query = domain
		m, err := get("/domain/" + url.PathEscape(domain))
		if err != nil {
			return nil, err
		}
		out.Detail = m
		d := &STDomain{Hostname: domain}
		if cdns, ok := m["current_dns"].(map[string]any); ok {
			d.CurrentDNS = cdns
		}
		out.Domain = d
	case "domain_history":
		if domain == "" {
			return nil, fmt.Errorf("input.domain required")
		}
		out.Query = domain
		recordType, _ := input["record_type"].(string)
		if recordType == "" {
			recordType = "a"
		}
		m, err := get("/history/" + url.PathEscape(domain) + "/dns/" + recordType)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if records, ok := m["records"].([]any); ok {
			for _, r := range records {
				rec, _ := r.(map[string]any)
				if rec == nil {
					continue
				}
				values, _ := rec["values"].([]any)
				value := ""
				org := ""
				if len(values) > 0 {
					if v, ok := values[0].(map[string]any); ok {
						value = gtString(v, "ip")
						if value == "" {
							value = gtString(v, "nameserver")
						}
						if value == "" {
							value = gtString(v, "host")
						}
						org = gtString(v, "organization")
					}
				}
				out.HistoryRecords = append(out.HistoryRecords, STHistoryRecord{
					Type: recordType, FirstSeen: gtString(rec, "first_seen"),
					LastSeen: gtString(rec, "last_seen"), Value: value, Organization: org,
				})
			}
		}
	case "subdomains":
		if domain == "" {
			return nil, fmt.Errorf("input.domain required")
		}
		out.Query = domain
		m, err := get("/domain/" + url.PathEscape(domain) + "/subdomains?children_only=false&include_inactive=true")
		if err != nil {
			return nil, err
		}
		out.Detail = m
		d := &STDomain{Hostname: domain}
		if subs, ok := m["subdomains"].([]any); ok {
			for _, s := range subs {
				if str, ok := s.(string); ok {
					d.Subdomains = append(d.Subdomains, str)
				}
			}
		}
		out.Domain = d
	case "associated":
		if domain == "" {
			return nil, fmt.Errorf("input.domain required")
		}
		out.Query = domain
		m, err := get("/domain/" + url.PathEscape(domain) + "/associated")
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "ip_neighbors":
		ip, _ := input["ip"].(string)
		if ip == "" {
			return nil, fmt.Errorf("input.ip required")
		}
		out.Query = ip
		m, err := get("/ips/nearby/" + url.PathEscape(ip))
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "whois_history":
		if domain == "" {
			return nil, fmt.Errorf("input.domain required")
		}
		out.Query = domain
		m, err := get("/history/" + url.PathEscape(domain) + "/whois")
		if err != nil {
			return nil, err
		}
		out.Detail = m
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.HistoryRecords)
	if out.Domain != nil {
		out.Returned++
	}
	out.Entities = stBuildEntities(out)
	out.HighlightFindings = stBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func stBuildEntities(o *SecurityTrailsLookupOutput) []STEntity {
	ents := []STEntity{}
	if d := o.Domain; d != nil {
		ents = append(ents, STEntity{
			Kind: "domain", ID: d.Hostname, Name: d.Hostname,
			URL:         "https://securitytrails.com/domain/" + d.Hostname,
			Description: fmt.Sprintf("%d subdomains", len(d.Subdomains)),
			Attributes: map[string]any{
				"current_dns": d.CurrentDNS,
				"subdomains":  d.Subdomains,
			},
		})
	}
	for _, r := range o.HistoryRecords {
		kind := "dns_record"
		if r.Type == "a" || r.Type == "aaaa" {
			kind = "ip_address"
		}
		ents = append(ents, STEntity{
			Kind: kind, ID: r.Value, Name: r.Value,
			Description: fmt.Sprintf("%s (%s) %s → %s", r.Type, r.Organization, r.FirstSeen, r.LastSeen),
			Attributes: map[string]any{
				"record_type":  r.Type,
				"first_seen":   r.FirstSeen,
				"last_seen":    r.LastSeen,
				"organization": r.Organization,
			},
		})
	}
	return ents
}

func stBuildHighlights(o *SecurityTrailsLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ securitytrails %s: %d records", o.Mode, o.Returned)}
	if d := o.Domain; d != nil {
		hi = append(hi, fmt.Sprintf("  • domain %s — %d subdomains", d.Hostname, len(d.Subdomains)))
		for i, s := range d.Subdomains {
			if i >= 5 {
				break
			}
			hi = append(hi, "    "+s+"."+d.Hostname)
		}
	}
	for i, r := range o.HistoryRecords {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s record %s [%s → %s] (%s)", r.Type, r.Value, r.FirstSeen, r.LastSeen, r.Organization))
	}
	return hi
}
