package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type OTXPassiveDNSEntry struct {
	Hostname   string `json:"hostname"`
	Address    string `json:"address"`
	First      string `json:"first"`
	Last       string `json:"last"`
	RecordType string `json:"record_type,omitempty"`
}

type OTXPassiveDNSOutput struct {
	Target          string               `json:"target"`
	TotalRecords    int                  `json:"total_records"`
	UniqueHostnames []string             `json:"unique_hostnames"`
	UniqueIPs       []string             `json:"unique_ips"`
	Subdomains      []string             `json:"subdomains_observed"`
	OldestRecord    string               `json:"oldest_record,omitempty"`
	NewestRecord    string               `json:"newest_record,omitempty"`
	RecentRecords   []OTXPassiveDNSEntry `json:"recent_records"`
	Source          string               `json:"source"`
	TookMs          int64                `json:"tookMs"`
	Note            string               `json:"note,omitempty"`
}

// AlienVaultOTXPassiveDNS queries AlienVault OTX (LevelBlue Labs) for passive
// DNS observations on a target domain. Passive DNS is recorded by sensors
// across the internet — these are real-world DNS lookups that actually
// resolved, not what's currently advertised in DNS. Frequently exposes:
//   - subdomains the org never listed publicly (internal tooling, staging)
//   - IPs from years ago that may still host legacy services
//   - C2 callbacks if a domain was ever compromised
//
// The 4th moat-feeding discovery channel (alongside js_endpoint_extract,
// swagger_openapi_finder, wayback_endpoint_extract). Different visibility
// model: passive DNS sees the SHADOW of traffic, not what the site advertises.
//
// Free tier: 10,000 req/hr with API key vs 1,000 anon. Sign up:
// https://otx.alienvault.com — no payment required.
func AlienVaultOTXPassiveDNS(ctx context.Context, input map[string]any) (*OTXPassiveDNSOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil, errors.New("input.target required (apex domain, e.g. 'vurvey.app')")
	}
	apiKey := os.Getenv("OTX_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OTX_API_KEY not set — sign up free at https://otx.alienvault.com (10,000 req/hr)")
	}
	start := time.Now()

	apiURL := fmt.Sprintf("https://otx.alienvault.com/api/v1/indicators/domain/%s/passive_dns", target)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-OTX-API-KEY", apiKey)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("otx request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("otx auth failed (status %d): check OTX_API_KEY", resp.StatusCode)
	}
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("otx rate-limited (429): free tier is 10,000 req/hr")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("otx returned status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		PassiveDNS []OTXPassiveDNSEntry `json:"passive_dns"`
		Count      int                  `json:"count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed parsing otx response: %w", err)
	}

	hostnames := map[string]bool{}
	ips := map[string]bool{}
	subdomains := map[string]bool{}
	oldest := ""
	newest := ""
	for _, e := range parsed.PassiveDNS {
		if e.Hostname != "" {
			hn := strings.ToLower(strings.TrimSuffix(e.Hostname, "."))
			hostnames[hn] = true
			if hn == target || strings.HasSuffix(hn, "."+target) {
				subdomains[hn] = true
			}
		}
		if e.Address != "" {
			ips[e.Address] = true
		}
		if e.First != "" && (oldest == "" || e.First < oldest) {
			oldest = e.First
		}
		if e.Last != "" && e.Last > newest {
			newest = e.Last
		}
	}

	hns := make([]string, 0, len(hostnames))
	for h := range hostnames {
		hns = append(hns, h)
	}
	sort.Strings(hns)
	ipl := make([]string, 0, len(ips))
	for i := range ips {
		ipl = append(ipl, i)
	}
	sort.Strings(ipl)
	subs := make([]string, 0, len(subdomains))
	for s := range subdomains {
		subs = append(subs, s)
	}
	sort.Strings(subs)

	rec := append([]OTXPassiveDNSEntry{}, parsed.PassiveDNS...)
	sort.Slice(rec, func(i, j int) bool { return rec[i].Last > rec[j].Last })
	if len(rec) > 50 {
		rec = rec[:50]
	}

	out := &OTXPassiveDNSOutput{
		Target:          target,
		TotalRecords:    len(parsed.PassiveDNS),
		UniqueHostnames: hns,
		UniqueIPs:       ipl,
		Subdomains:      subs,
		OldestRecord:    oldest,
		NewestRecord:    newest,
		RecentRecords:   rec,
		Source:          "alienvault.otx",
		TookMs:          time.Since(start).Milliseconds(),
	}
	if len(parsed.PassiveDNS) == 0 {
		out.Note = "No passive DNS observations — domain may be too new, too quiet, or not seen by OTX sensors"
	}
	return out, nil
}
