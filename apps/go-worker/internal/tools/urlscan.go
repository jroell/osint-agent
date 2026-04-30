package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type URLScanHit struct {
	TaskID     string `json:"task_id"`
	TaskTime   string `json:"task_time,omitempty"`
	TaskURL    string `json:"task_url,omitempty"`
	PageURL    string `json:"page_url,omitempty"`
	Domain     string `json:"domain,omitempty"`
	IP         string `json:"ip,omitempty"`
	ASN        string `json:"asn,omitempty"`
	ASNName    string `json:"asn_name,omitempty"`
	Country    string `json:"country,omitempty"`
	Server     string `json:"server,omitempty"`
	Title      string `json:"title,omitempty"`
	UniqIPs    int    `json:"uniq_ips,omitempty"`
	UniqHosts  int    `json:"uniq_hosts,omitempty"`
	Screenshot string `json:"screenshot,omitempty"`
	ResultURL  string `json:"result_url"` // urlscan.io/result/<uuid>/
}

type URLScanSearchOutput struct {
	Query        string       `json:"query"`
	Total        int          `json:"total"`
	Returned     int          `json:"returned"`
	HasMore      bool         `json:"has_more"`
	Results      []URLScanHit `json:"results"`
	UniqueIPs    int          `json:"unique_ips"`
	UniqueDomains int         `json:"unique_domains"`
	UniqueASNs   int          `json:"unique_asns"`
	Source       string       `json:"source"`
	TookMs       int64        `json:"tookMs"`
	Note         string       `json:"note,omitempty"`
}

// URLScanSearch queries urlscan.io's 800M+ historical scans. The query
// syntax is the most powerful entity-resolution primitive in this catalog —
// every scan is indexed by IP, ASN, cert hash, favicon hash, page content,
// hostname, technology, geo. Free public-only path needs no key (rate-limited);
// URLSCAN_API_KEY raises to 1000 searches/month.
//
// Pivot examples:
//   domain:example.com                  — every scan of this domain
//   ip:1.2.3.4                          — every site seen at this IP
//   asn:AS13335                         — every site in Cloudflare's ASN
//   hash:<TLS-cert-sha256>              — every site sharing a TLS cert
//   favicon_hash:<mmh3>                 — every site with the same favicon
//                                          (high-precision shared-infra signal)
//   page.title:"Vurvey"                 — every site whose title matches
//   page.server:"cloudflare"            — every CF-fronted site we've seen
//   technology:"Sanity"                 — every site running a given tech
//   filename:"id_rsa"                   — every site that 200'd this filename
//
// Combine with AND/OR/NOT:
//   ip:1.2.3.4 AND NOT domain:example.com
func URLScanSearch(ctx context.Context, input map[string]any) (*URLScanSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (urlscan search syntax — see tool description)")
	}
	size := 50
	if v, ok := input["size"].(float64); ok && v > 0 {
		size = int(v)
		if size > 1000 {
			size = 1000
		}
	}

	start := time.Now()
	endpoint := fmt.Sprintf("https://urlscan.io/api/v1/search/?q=%s&size=%d",
		url.QueryEscape(q), size)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/json")
	if apiKey := os.Getenv("URLSCAN_API_KEY"); apiKey != "" {
		req.Header.Set("API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("urlscan: %w", err)
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if len(body) > 16<<20 {
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("urlscan %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Results []struct {
			ID   string `json:"_id"`
			Sort []any  `json:"sort"`
			Task struct {
				UUID   string `json:"uuid"`
				Time   string `json:"time"`
				URL    string `json:"url"`
				Domain string `json:"domain"`
			} `json:"task"`
			Page struct {
				URL     string `json:"url"`
				Domain  string `json:"domain"`
				IP      string `json:"ip"`
				ASN     string `json:"asn"`
				ASNName string `json:"asnname"`
				Country string `json:"country"`
				Server  string `json:"server"`
				Title   string `json:"title"`
			} `json:"page"`
			Stats struct {
				UniqIPs   int `json:"uniqIPs"`
				UniqHosts int `json:"uniqHosts"`
			} `json:"stats"`
			Screenshot string `json:"screenshot"`
			Result     string `json:"result"`
		} `json:"results"`
		Total   int  `json:"total"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("urlscan parse: %w", err)
	}

	out := &URLScanSearchOutput{
		Query: q, Total: parsed.Total, Returned: len(parsed.Results),
		HasMore: parsed.HasMore, Source: "urlscan.io",
		TookMs: time.Since(start).Milliseconds(),
	}
	uniqIP := map[string]struct{}{}
	uniqDom := map[string]struct{}{}
	uniqASN := map[string]struct{}{}
	for _, r := range parsed.Results {
		hit := URLScanHit{
			TaskID: r.Task.UUID, TaskTime: r.Task.Time, TaskURL: r.Task.URL,
			PageURL: r.Page.URL, Domain: r.Page.Domain, IP: r.Page.IP,
			ASN: r.Page.ASN, ASNName: r.Page.ASNName, Country: r.Page.Country,
			Server: r.Page.Server, Title: r.Page.Title,
			UniqIPs: r.Stats.UniqIPs, UniqHosts: r.Stats.UniqHosts,
			Screenshot: r.Screenshot, ResultURL: r.Result,
		}
		if hit.ResultURL == "" && hit.TaskID != "" {
			hit.ResultURL = "https://urlscan.io/result/" + hit.TaskID + "/"
		}
		out.Results = append(out.Results, hit)
		if r.Page.IP != "" {
			uniqIP[r.Page.IP] = struct{}{}
		}
		if r.Page.Domain != "" {
			uniqDom[strings.ToLower(r.Page.Domain)] = struct{}{}
		}
		if r.Page.ASN != "" {
			uniqASN[r.Page.ASN] = struct{}{}
		}
	}
	out.UniqueIPs = len(uniqIP)
	out.UniqueDomains = len(uniqDom)
	out.UniqueASNs = len(uniqASN)
	if os.Getenv("URLSCAN_API_KEY") == "" {
		out.Note = "unauthenticated (low rate limit). Set URLSCAN_API_KEY (free signup at https://urlscan.io/user/signup) for 1000 searches/month."
	}
	return out, nil
}
