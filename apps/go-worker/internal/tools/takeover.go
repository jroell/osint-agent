package tools

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type TakeoverFinding struct {
	Service      string `json:"service"`
	CNAME        string `json:"cname"`
	Vulnerable   bool   `json:"vulnerable"`
	Confidence   string `json:"confidence"` // "high" | "medium" | "low"
	Evidence     string `json:"evidence,omitempty"`
}

type TakeoverOutput struct {
	Domain    string            `json:"domain"`
	CNAME     string            `json:"cname,omitempty"`
	Findings  []TakeoverFinding `json:"findings,omitempty"`
	TookMs    int64             `json:"tookMs"`
	Note      string            `json:"note,omitempty"`
}

// takeoverFingerprints — built-in list of common subdomain-takeover signatures.
// Each entry: a domain suffix that a CNAME would point to + a body fingerprint
// that indicates the underlying service is unclaimed. Sources: EdOverflow's
// can-i-take-over-xyz, plus public bug-bounty writeups (Detectify, HackerOne).
var takeoverFingerprints = []struct {
	Service     string
	CNAMESuffix string
	Body        string // case-insensitive substring
	Confidence  string
}{
	{"github-pages", "github.io", "There isn't a GitHub Pages site here", "high"},
	{"heroku", "herokuapp.com", "No such app", "high"},
	{"heroku", "herokudns.com", "No such app", "high"},
	{"aws-s3", "s3.amazonaws.com", "NoSuchBucket", "high"},
	{"aws-cloudfront", "cloudfront.net", "Bad Request: ERROR: The request could not be satisfied", "medium"},
	{"azure", "azurewebsites.net", "Error 404 - Web app not found", "high"},
	{"azure", "cloudapp.net", "Not Found", "low"},
	{"azure-traffic-mgr", "trafficmanager.net", "Page Not Found", "medium"},
	{"shopify", "myshopify.com", "Sorry, this shop is currently unavailable", "high"},
	{"ghost", "ghost.io", "The thing you were looking for is no longer here", "high"},
	{"tumblr", "tumblr.com", "There's nothing here", "medium"},
	{"zendesk", "zendesk.com", "Help Center Closed", "high"},
	{"statuspage", "statuspage.io", "You are being redirected", "low"},
	{"fastly", "fastly.net", "Fastly error: unknown domain", "high"},
	{"surge", "surge.sh", "project not found", "high"},
	{"unbounce", "unbouncepages.com", "The requested URL was not found on this server", "medium"},
	{"bigcartel", "bigcartel.com", "Oops! We couldn&#8217;t find that page", "medium"},
	{"helpjuice", "helpjuice.com", "We could not find what you're looking for", "medium"},
	{"helpscout", "helpscoutdocs.com", "No settings were found for this company", "high"},
	{"intercom", "custom.intercom.help", "This page is reserved for artistic golfers", "high"},
	{"netlify", "netlify.app", "Not Found - Request ID:", "medium"},
	{"pantheon", "pantheonsite.io", "The gods are wise", "medium"},
	{"readme", "readme.io", "Project doesnt exist...", "high"},
	{"webflow", "proxy.webflow.com", "The page you are looking for doesn't exist or has been moved", "medium"},
	{"wpengine", "wpengine.com", "The site you were looking for couldn't be found", "high"},
}

// TakeoverCheck resolves the input domain's CNAME, matches it against a built-in
// fingerprint list, and (when matched) fetches the resource over HTTP/HTTPS to
// look for the unclaimed-service body marker. Designed for reconnaissance, not
// active exploitation — finding a takeover here means the target may have a
// dangling CNAME, NOT that the service has been or should be claimed.
func TakeoverCheck(ctx context.Context, input map[string]any) (*TakeoverOutput, error) {
	domain, _ := input["domain"].(string)
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return nil, errors.New("input.domain required")
	}

	start := time.Now()
	out := &TakeoverOutput{
		Domain: domain,
		Note:   "matches indicate a *candidate* takeover; always manually verify and only act with explicit authorization",
		TookMs: time.Since(start).Milliseconds(),
	}

	r := &net.Resolver{PreferGo: true}
	cname, err := r.LookupCNAME(ctx, domain)
	if err != nil {
		// No CNAME → no takeover candidate via this method.
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	cname = strings.TrimSuffix(strings.ToLower(cname), ".")
	out.CNAME = cname

	for _, fp := range takeoverFingerprints {
		if !strings.HasSuffix(cname, "."+fp.CNAMESuffix) && cname != fp.CNAMESuffix {
			continue
		}
		// Found a service match. Fetch the target over HTTP/HTTPS to look for the
		// service-specific "unclaimed" body marker.
		evidence, vulnerable := probeFingerprint(ctx, domain, fp.Body)
		out.Findings = append(out.Findings, TakeoverFinding{
			Service:    fp.Service,
			CNAME:      cname,
			Vulnerable: vulnerable,
			Confidence: fp.Confidence,
			Evidence:   evidence,
		})
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func probeFingerprint(ctx context.Context, domain, fingerprint string) (string, bool) {
	for _, scheme := range []string{"https", "http"} {
		body, ok := fetchProbeBody(ctx, scheme+"://"+domain)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(body), strings.ToLower(fingerprint)) {
			return truncate(body, 240), true
		}
		// First successful fetch — no fingerprint match means this looks claimed.
		return truncate(body, 120), false
	}
	return "", false
}

func fetchProbeBody(ctx context.Context, url string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	client := &http.Client{
		Timeout: 8 * time.Second,
		// Do NOT follow redirects — many "claimed" services 302 into a UI; we want
		// the raw landing page bytes from the fingerprinted host.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10)) // 256 KiB enough for "not-found" pages
	if err != nil {
		return "", false
	}
	return string(body), true
}
