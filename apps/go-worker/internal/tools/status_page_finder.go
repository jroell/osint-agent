package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type StatusPageProbe struct {
	URL       string `json:"url"`
	Found     bool   `json:"found"`
	Vendor    string `json:"vendor,omitempty"` // statuspage.io | instatus | better-stack | atlassian | etc.
	HTTPStatus int    `json:"http_status,omitempty"`
}

type StatusPageComponent struct {
	Name        string `json:"name"`
	Status      string `json:"status,omitempty"` // operational | degraded | outage | maintenance
	Description string `json:"description,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
}

type StatusPageIncident struct {
	Title     string `json:"title"`
	Status    string `json:"status"` // resolved | investigating | identified | monitoring
	Impact    string `json:"impact,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	URL       string `json:"url,omitempty"`
}

type StatusPageFinderOutput struct {
	Target            string                `json:"target"`
	Probes            []StatusPageProbe     `json:"probes"`
	StatusPageURL     string                `json:"status_page_url,omitempty"`
	Vendor            string                `json:"vendor,omitempty"`
	OverallStatus     string                `json:"overall_status,omitempty"`
	Components        []StatusPageComponent `json:"components,omitempty"`
	ActiveIncidents   []StatusPageIncident  `json:"active_incidents,omitempty"`
	RecentIncidents   []StatusPageIncident  `json:"recent_incidents,omitempty"`
	HighValueFindings []string              `json:"high_value_findings,omitempty"`
	Source            string                `json:"source"`
	TookMs            int64                 `json:"tookMs"`
	Note              string                `json:"note,omitempty"`
}

// commonStatusSubdomains — patterns we probe.
var commonStatusSubdomains = []string{
	"status",
	"healthcheck",
	"health",
	"uptime",
}

// commonStatusVendors — known external status-page-as-a-service hosts.
// Order matters; first match wins.
var commonStatusVendors = []struct {
	Pattern string
	Name    string
}{
	{".statuspage.io", "Atlassian Statuspage"},
	{".instatus.com", "Instatus"},
	{".betterstack.com", "Better Stack"},
	{".cachet.io", "Cachet"},
	{".uptimerobot.com", "UptimeRobot"},
	{"statushq.com", "StatusHQ"},
}

// StatusPageFinder probes for and parses an organization's public status page.
// Status pages are OSINT goldmines because they expose:
//   - Service component names (microservice topology)
//   - Active incidents (current outages reveal infrastructure pressure points)
//   - Historical incidents (post-mortems often included)
//   - Subscriber-count and update cadence (operational maturity signal)
//
// Strategy:
//  1. Probe status.<domain>, healthcheck.<domain>, etc.
//  2. Probe common SaaS vendor patterns (<org>.statuspage.io, etc.)
//  3. For each found page, attempt to parse:
//     - Statuspage.io style: append /api/v2/components.json + /api/v2/incidents.json
//     - HTML scraping of common patterns
func StatusPageFinder(ctx context.Context, input map[string]any) (*StatusPageFinderOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil, errors.New("input.target required (apex domain)")
	}
	stem := extractBrandStem(target)
	apex := target
	if !strings.Contains(target, ".") {
		// just brand name → can't probe subdomains
		apex = ""
	}

	start := time.Now()
	out := &StatusPageFinderOutput{Target: target, Source: "status_page_finder"}

	// Build candidate URLs.
	var candidates []string
	if apex != "" {
		for _, sub := range commonStatusSubdomains {
			candidates = append(candidates, fmt.Sprintf("https://%s.%s", sub, apex))
		}
	}
	// Known vendor patterns
	candidates = append(candidates, fmt.Sprintf("https://%s.statuspage.io", stem))
	candidates = append(candidates, fmt.Sprintf("https://%s.instatus.com", stem))
	candidates = append(candidates, fmt.Sprintf("https://%s.betterstack.com", stem))

	// Allow caller to supply custom candidates.
	if v, ok := input["additional_urls"].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				if !strings.HasPrefix(s, "http") {
					s = "https://" + s
				}
				candidates = append(candidates, s)
			}
		}
	}

	// Probe in parallel
	probes := make([]StatusPageProbe, len(candidates))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, c := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, u string) {
			defer wg.Done()
			defer func() { <-sem }()
			probes[idx] = statusProbe(ctx, u)
		}(i, c)
	}
	wg.Wait()
	out.Probes = probes

	// Find best status page (any found).
	for _, p := range probes {
		if p.Found {
			out.StatusPageURL = p.URL
			out.Vendor = p.Vendor
			break
		}
	}

	if out.StatusPageURL == "" {
		out.Note = "No status page found at common subdomains or vendor patterns. Try additional_urls=[...] if known."
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// If statuspage.io family, parse the JSON API.
	if strings.Contains(out.Vendor, "Statuspage") {
		statusparseStatuspageIO(ctx, out)
	} else {
		// HTML scraping fallback
		statusparseHTML(ctx, out)
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

var statusComponentRE = regexp.MustCompile(`(?i)<a[^>]*class="component[^"]*"[^>]*>([^<]{2,80})</a>`)

func statusProbe(ctx context.Context, url string) StatusPageProbe {
	rec := StatusPageProbe{URL: url}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "osint-agent/status-page-finder")
	req.Header.Set("Accept", "text/html,application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rec
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	rec.HTTPStatus = resp.StatusCode

	finalURL := resp.Request.URL.String()
	finalURLLow := strings.ToLower(finalURL)

	// Detect vendor from final URL or body content
	for _, v := range commonStatusVendors {
		if strings.Contains(finalURLLow, v.Pattern) {
			rec.Vendor = v.Name
			break
		}
	}
	// Body-based vendor detection
	bodyLow := strings.ToLower(string(body))
	if rec.Vendor == "" {
		if strings.Contains(bodyLow, "statuspage.io") {
			rec.Vendor = "Atlassian Statuspage"
		} else if strings.Contains(bodyLow, "instatus") {
			rec.Vendor = "Instatus"
		} else if strings.Contains(bodyLow, "betterstack") || strings.Contains(bodyLow, "better stack") {
			rec.Vendor = "Better Stack"
		}
	}

	// Heuristic: 200 + body contains common status-page words
	if resp.StatusCode == 200 && (strings.Contains(bodyLow, "operational") ||
		strings.Contains(bodyLow, "all systems") ||
		strings.Contains(bodyLow, "incident") ||
		strings.Contains(bodyLow, "uptime") ||
		strings.Contains(bodyLow, "subscribed") ||
		strings.Contains(bodyLow, "service status") ||
		rec.Vendor != "") {
		rec.Found = true
	}
	return rec
}

func statusparseStatuspageIO(ctx context.Context, out *StatusPageFinderOutput) {
	base := strings.TrimRight(out.StatusPageURL, "/")
	// /api/v2/summary.json gives one-shot view
	endpoint := base + "/api/v2/summary.json"
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/status-page-finder")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return
	}
	var parsed struct {
		Status struct {
			Indicator   string `json:"indicator"`
			Description string `json:"description"`
		} `json:"status"`
		Components []struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			Description string `json:"description"`
			Group       struct {
				Name string `json:"name"`
			} `json:"-"`
			GroupID string `json:"group_id"`
		} `json:"components"`
		Incidents []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			Impact    string `json:"impact"`
			StartedAt string `json:"started_at"`
			ShortlinkURL string `json:"shortlink"`
		} `json:"incidents"`
		ScheduledMaintenances []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			Impact    string `json:"impact"`
			StartedAt string `json:"scheduled_for"`
		} `json:"scheduled_maintenances"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}
	out.OverallStatus = parsed.Status.Description
	for _, c := range parsed.Components {
		out.Components = append(out.Components, StatusPageComponent{
			Name: c.Name, Status: c.Status, Description: c.Description,
		})
	}
	for _, inc := range parsed.Incidents {
		out.ActiveIncidents = append(out.ActiveIncidents, StatusPageIncident{
			Title: inc.Name, Status: inc.Status, Impact: inc.Impact, StartedAt: inc.StartedAt, URL: inc.ShortlinkURL,
		})
	}
	for _, m := range parsed.ScheduledMaintenances {
		out.ActiveIncidents = append(out.ActiveIncidents, StatusPageIncident{
			Title: "[Maintenance] " + m.Name, Status: m.Status, Impact: m.Impact, StartedAt: m.StartedAt,
		})
	}

	// Recent incidents (past resolved) via /api/v2/incidents.json
	endpoint2 := base + "/api/v2/incidents.json"
	cctx2, cancel2 := context.WithTimeout(ctx, 12*time.Second)
	defer cancel2()
	req2, _ := http.NewRequestWithContext(cctx2, http.MethodGet, endpoint2, nil)
	req2.Header.Set("User-Agent", "osint-agent/status-page-finder")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(io.LimitReader(resp2.Body, 8<<20))
	if resp2.StatusCode != 200 {
		return
	}
	var hist struct {
		Incidents []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			Impact    string `json:"impact"`
			StartedAt string `json:"started_at"`
			Shortlink string `json:"shortlink"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal(body2, &hist); err == nil {
		for i, inc := range hist.Incidents {
			if i >= 10 {
				break
			}
			out.RecentIncidents = append(out.RecentIncidents, StatusPageIncident{
				Title: inc.Name, Status: inc.Status, Impact: inc.Impact, StartedAt: inc.StartedAt, URL: inc.Shortlink,
			})
		}
	}

	// Highlight signals
	if len(out.Components) > 0 {
		out.HighValueFindings = append(out.HighValueFindings,
			fmt.Sprintf("%d service components catalogued (microservice topology)", len(out.Components)))
	}
	if len(out.ActiveIncidents) > 0 {
		out.HighValueFindings = append(out.HighValueFindings,
			fmt.Sprintf("⚠️  %d ACTIVE incidents — current ops pressure point", len(out.ActiveIncidents)))
	}
}

func statusparseHTML(ctx context.Context, out *StatusPageFinderOutput) {
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, out.StatusPageURL, nil)
	req.Header.Set("User-Agent", "osint-agent/status-page-finder")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	html := string(body)

	// Naive component extraction
	for _, m := range statusComponentRE.FindAllStringSubmatch(html, -1) {
		if len(m) >= 2 {
			name := strings.TrimSpace(m[1])
			if name != "" && !strings.Contains(name, "<") {
				out.Components = append(out.Components, StatusPageComponent{Name: name})
			}
		}
	}
	if strings.Contains(strings.ToLower(html), "all systems operational") {
		out.OverallStatus = "All Systems Operational"
	}
}

// keep url/json imports satisfied
var _ = url.QueryEscape
var _ = json.Marshal
