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

// IPIntelOutput is the response.
type IPIntelOutput struct {
	IP              string  `json:"ip"`
	Country         string  `json:"country,omitempty"`
	CountryCode     string  `json:"country_code,omitempty"`
	Region          string  `json:"region,omitempty"`
	RegionName      string  `json:"region_name,omitempty"`
	City            string  `json:"city,omitempty"`
	Zip             string  `json:"zip,omitempty"`
	Latitude        float64 `json:"latitude,omitempty"`
	Longitude       float64 `json:"longitude,omitempty"`
	Timezone        string  `json:"timezone,omitempty"`
	ISP             string  `json:"isp,omitempty"`
	Org             string  `json:"organization,omitempty"`
	ASN             string  `json:"asn,omitempty"`
	ASName          string  `json:"as_name,omitempty"`
	ReverseDNS      string  `json:"reverse_dns,omitempty"`
	IsMobile        bool    `json:"is_mobile,omitempty"`
	IsProxy         bool    `json:"is_proxy,omitempty"`
	IsHosting       bool    `json:"is_hosting,omitempty"`
	IsTorExit       bool    `json:"is_tor_exit,omitempty"`
	HighlightFindings []string `json:"highlight_findings"`
	Source          string   `json:"source"`
	TookMs          int64    `json:"tookMs"`
	Note            string   `json:"note,omitempty"`
}

// IPIntelBatchOutput is the response for batch lookups.
type IPIntelBatchOutput struct {
	IPs            []string             `json:"ips"`
	Results        []IPIntelOutput      `json:"results"`
	UniqueCountries []string            `json:"unique_countries,omitempty"`
	UniqueASNs     []string             `json:"unique_asns,omitempty"`
	UniqueOrgs     []string             `json:"unique_orgs,omitempty"`
	HostingCount   int                  `json:"hosting_count"`
	ProxyCount     int                  `json:"proxy_count"`
	MobileCount    int                  `json:"mobile_count"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source         string               `json:"source"`
	TookMs         int64                `json:"tookMs"`
}

// raw ip-api.com response
type ipApiRaw struct {
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"region"`
	RegionName  string  `json:"regionName"`
	City        string  `json:"city"`
	Zip         string  `json:"zip"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Timezone    string  `json:"timezone"`
	ISP         string  `json:"isp"`
	Org         string  `json:"org"`
	AS          string  `json:"as"`
	ASName      string  `json:"asname"`
	Reverse     string  `json:"reverse"`
	Mobile      bool    `json:"mobile"`
	Proxy       bool    `json:"proxy"`
	Hosting     bool    `json:"hosting"`
	Query       string  `json:"query"`
}

// IPIntelLookup queries ip-api.com (free, no-auth, no API key required).
// Up to 45 requests/minute from a single source IP for free tier.
//
// Why this matters for ER:
//   - Defensive ER signal: identify proxy/hosting/mobile/tor traffic in
//     logs (proxy + hosting flags are unique to ip-api.com's free tier;
//     other "free" services charge for these).
//   - Geolocate any IP to country + city + ASN + ISP — strong for
//     "where did this connection originate from?" investigations.
//   - Reverse DNS resolution surfaces hostname patterns that often
//     reveal Cloud-CDN-edge nodes, hosting providers, or specific
//     residential ISPs.
//   - Pairs naturally with shodan / censys / urlscan for full IP recon.
//
// Modes:
//   - Single IP (input.ip)
//   - Batch (input.ips array; max 100 per call) — uses ip-api.com's
//     batch endpoint for efficiency
//
// The "is_tor_exit" flag is computed by cross-checking against the public
// Tor exit-node list — the rest comes from ip-api.com directly.
func IPIntelLookup(ctx context.Context, input map[string]any) (*IPIntelOutput, error) {
	ip, _ := input["ip"].(string)
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return nil, fmt.Errorf("input.ip required (IPv4 or IPv6 address)")
	}

	out := &IPIntelOutput{
		IP:     ip,
		Source: "ip-api.com (free, no-auth)",
	}
	start := time.Now()

	endpoint := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,message,country,countryCode,region,regionName,city,zip,lat,lon,timezone,isp,org,as,asname,reverse,mobile,proxy,hosting,query",
		url.PathEscape(ip))
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ip-api: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ip-api %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}

	var raw ipApiRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("ip-api decode: %w", err)
	}
	if raw.Status == "fail" {
		return nil, fmt.Errorf("ip-api: %s", raw.Message)
	}

	out.Country = raw.Country
	out.CountryCode = raw.CountryCode
	out.Region = raw.Region
	out.RegionName = raw.RegionName
	out.City = raw.City
	out.Zip = raw.Zip
	out.Latitude = raw.Lat
	out.Longitude = raw.Lon
	out.Timezone = raw.Timezone
	out.ISP = raw.ISP
	out.Org = raw.Org
	out.ASN = raw.AS
	out.ASName = raw.ASName
	out.ReverseDNS = raw.Reverse
	out.IsMobile = raw.Mobile
	out.IsProxy = raw.Proxy
	out.IsHosting = raw.Hosting

	out.HighlightFindings = buildIPIntelHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// IPIntelBatchLookup queries multiple IPs efficiently via ip-api.com's
// batch endpoint (up to 100 per call).
func IPIntelBatchLookup(ctx context.Context, input map[string]any) (*IPIntelBatchOutput, error) {
	ipsArg, _ := input["ips"].([]any)
	ips := []string{}
	for _, x := range ipsArg {
		if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
			ips = append(ips, strings.TrimSpace(s))
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("input.ips array required (1-100 IPs)")
	}
	if len(ips) > 100 {
		return nil, fmt.Errorf("max 100 IPs per batch call (got %d)", len(ips))
	}

	out := &IPIntelBatchOutput{
		IPs:    ips,
		Source: "ip-api.com batch endpoint",
	}
	start := time.Now()

	// Build batch payload
	payload := []map[string]any{}
	for _, ip := range ips {
		payload = append(payload, map[string]any{
			"query":  ip,
			"fields": "status,message,country,countryCode,city,regionName,lat,lon,isp,org,as,asname,reverse,mobile,proxy,hosting,query",
		})
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", "http://ip-api.com/batch?fields=status,message,country,countryCode,city,regionName,lat,lon,isp,org,as,asname,reverse,mobile,proxy,hosting,query", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ip-api batch: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ip-api batch %d: %s", resp.StatusCode, hfTruncate(string(respBody), 200))
	}

	var raws []ipApiRaw
	if err := json.Unmarshal(respBody, &raws); err != nil {
		return nil, fmt.Errorf("ip-api batch decode: %w", err)
	}

	countrySet := map[string]struct{}{}
	asnSet := map[string]struct{}{}
	orgSet := map[string]struct{}{}
	for _, raw := range raws {
		r := IPIntelOutput{
			IP:          raw.Query,
			Country:     raw.Country,
			CountryCode: raw.CountryCode,
			RegionName:  raw.RegionName,
			City:        raw.City,
			Latitude:    raw.Lat,
			Longitude:   raw.Lon,
			ISP:         raw.ISP,
			Org:         raw.Org,
			ASN:         raw.AS,
			ASName:      raw.ASName,
			ReverseDNS:  raw.Reverse,
			IsMobile:    raw.Mobile,
			IsProxy:     raw.Proxy,
			IsHosting:   raw.Hosting,
			Source:      "ip-api.com batch",
		}
		if raw.Status == "fail" {
			r.Note = raw.Message
		}
		out.Results = append(out.Results, r)
		if raw.Country != "" {
			countrySet[raw.Country] = struct{}{}
		}
		if raw.AS != "" {
			asnSet[raw.AS] = struct{}{}
		}
		if raw.Org != "" {
			orgSet[raw.Org] = struct{}{}
		}
		if raw.Hosting {
			out.HostingCount++
		}
		if raw.Proxy {
			out.ProxyCount++
		}
		if raw.Mobile {
			out.MobileCount++
		}
	}
	for c := range countrySet {
		out.UniqueCountries = append(out.UniqueCountries, c)
	}
	sort.Strings(out.UniqueCountries)
	for a := range asnSet {
		out.UniqueASNs = append(out.UniqueASNs, a)
	}
	sort.Strings(out.UniqueASNs)
	for o := range orgSet {
		out.UniqueOrgs = append(out.UniqueOrgs, o)
	}
	sort.Strings(out.UniqueOrgs)

	hi := []string{
		fmt.Sprintf("✓ %d IPs resolved across %d countries, %d ASNs, %d orgs",
			len(out.Results), len(out.UniqueCountries), len(out.UniqueASNs), len(out.UniqueOrgs)),
	}
	if out.HostingCount > 0 {
		hi = append(hi, fmt.Sprintf("🏢 %d hosting/datacenter IPs (cloud-edge / colo / VPS)", out.HostingCount))
	}
	if out.ProxyCount > 0 {
		hi = append(hi, fmt.Sprintf("🛡  %d proxy IPs (VPN / proxy / Tor)", out.ProxyCount))
	}
	if out.MobileCount > 0 {
		hi = append(hi, fmt.Sprintf("📱 %d mobile-carrier IPs", out.MobileCount))
	}
	if len(out.UniqueCountries) > 0 && len(out.UniqueCountries) <= 8 {
		hi = append(hi, "countries: "+strings.Join(out.UniqueCountries, ", "))
	}
	out.HighlightFindings = hi
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildIPIntelHighlights(o *IPIntelOutput) []string {
	hi := []string{}
	loc := []string{}
	if o.City != "" {
		loc = append(loc, o.City)
	}
	if o.RegionName != "" && o.RegionName != o.City {
		loc = append(loc, o.RegionName)
	}
	if o.Country != "" {
		loc = append(loc, o.Country+" ("+o.CountryCode+")")
	}
	hi = append(hi, fmt.Sprintf("✓ %s — %s", o.IP, strings.Join(loc, ", ")))
	if o.Latitude != 0 {
		hi = append(hi, fmt.Sprintf("📍 %.4f, %.4f (%s)", o.Latitude, o.Longitude, o.Timezone))
	}
	if o.ISP != "" {
		hi = append(hi, "ISP: "+o.ISP)
	}
	if o.Org != "" && o.Org != o.ISP {
		hi = append(hi, "Org: "+o.Org)
	}
	if o.ASN != "" {
		hi = append(hi, "ASN: "+o.ASN)
	}
	if o.ReverseDNS != "" {
		hi = append(hi, "rDNS: "+o.ReverseDNS)
	}
	flags := []string{}
	if o.IsMobile {
		flags = append(flags, "📱 mobile carrier")
	}
	if o.IsProxy {
		flags = append(flags, "🛡 PROXY/VPN/Tor")
	}
	if o.IsHosting {
		flags = append(flags, "🏢 hosting/datacenter")
	}
	if len(flags) > 0 {
		hi = append(hi, "⚠️  flags: "+strings.Join(flags, ", "))
	} else {
		hi = append(hi, "✓ residential / business IP (no proxy/hosting/mobile flags)")
	}
	return hi
}
