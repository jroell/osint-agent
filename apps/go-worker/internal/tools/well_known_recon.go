package tools

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type WellKnownProbe struct {
	Path          string `json:"path"`
	URL           string `json:"url"`
	HTTPStatus    int    `json:"http_status"`
	ContentType   string `json:"content_type,omitempty"`
	BodyBytes     int    `json:"body_bytes"`
	Found         bool   `json:"found"`
	BodyExcerpt   string `json:"body_excerpt,omitempty"`
}

type RobotsParsed struct {
	UserAgents      []string `json:"user_agents,omitempty"`
	DisallowPaths   []string `json:"disallow_paths,omitempty"`
	AllowPaths      []string `json:"allow_paths,omitempty"`
	SitemapURLs     []string `json:"sitemap_urls,omitempty"`
	CrawlDelays     []string `json:"crawl_delays,omitempty"`
}

type SitemapParsed struct {
	URLs           []string `json:"urls,omitempty"`
	URLCount       int      `json:"url_count"`
	IsIndex        bool     `json:"is_index"`
	NestedSitemaps []string `json:"nested_sitemaps,omitempty"`
}

type OIDCConfig struct {
	Issuer                  string   `json:"issuer,omitempty"`
	AuthorizationEndpoint   string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint           string   `json:"token_endpoint,omitempty"`
	UserInfoEndpoint        string   `json:"userinfo_endpoint,omitempty"`
	JWKSURI                 string   `json:"jwks_uri,omitempty"`
	RegistrationEndpoint    string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint      string   `json:"revocation_endpoint,omitempty"`
	IntrospectionEndpoint   string   `json:"introspection_endpoint,omitempty"`
	EndSessionEndpoint      string   `json:"end_session_endpoint,omitempty"`
	ScopesSupported         []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported  []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported     []string `json:"grant_types_supported,omitempty"`
}

type AppleAASA struct {
	AppIDs     []string `json:"app_ids,omitempty"`     // bundle ID + team ID
	Teams      []string `json:"teams,omitempty"`        // team IDs
	WebcredAppIDs []string `json:"webcredentials_app_ids,omitempty"`
	UniversalLinkPaths []string `json:"universal_link_paths,omitempty"`
}

type AssetLinks struct {
	Statements []map[string]any `json:"statements"`
	PackageNames []string `json:"android_package_names,omitempty"`
	CertSHA256  []string `json:"sha256_cert_fingerprints,omitempty"`
}

type SecurityTxt struct {
	Contacts          []string `json:"contacts,omitempty"`
	Encryption        []string `json:"encryption,omitempty"`
	Acknowledgments   []string `json:"acknowledgments,omitempty"`
	PreferredLanguages []string `json:"preferred_languages,omitempty"`
	Canonical         []string `json:"canonical,omitempty"`
	Policy            []string `json:"policy,omitempty"`
	Hiring            []string `json:"hiring,omitempty"`
	Expires           string   `json:"expires,omitempty"`
}

type WellKnownReconOutput struct {
	Target           string             `json:"target"`
	Probes           []WellKnownProbe   `json:"probes"`
	FoundCount       int                `json:"found_count"`
	Robots           *RobotsParsed      `json:"robots,omitempty"`
	Sitemap          *SitemapParsed     `json:"sitemap,omitempty"`
	OIDC             *OIDCConfig        `json:"oidc_config,omitempty"`
	OAuthAuthServer  *OIDCConfig        `json:"oauth_auth_server,omitempty"`
	AppleAASA        *AppleAASA         `json:"apple_app_site_association,omitempty"`
	AssetLinks       *AssetLinks        `json:"android_asset_links,omitempty"`
	SecurityTxt      *SecurityTxt       `json:"security_txt,omitempty"`
	HumansTxt        string             `json:"humans_txt,omitempty"`
	HighValueFindings []string          `json:"high_value_findings"` // human-readable summary
	Source           string             `json:"source"`
	TookMs           int64              `json:"tookMs"`
}

// wellKnownPaths to probe in parallel.
var wellKnownPaths = []string{
	"/robots.txt",
	"/sitemap.xml",
	"/sitemap_index.xml",
	"/humans.txt",
	"/.well-known/security.txt",
	"/security.txt", // legacy
	"/.well-known/openid-configuration",
	"/.well-known/oauth-authorization-server",
	"/.well-known/host-meta",
	"/.well-known/host-meta.json",
	"/.well-known/openpgpkey/hu/",
	"/.well-known/change-password",
	"/.well-known/dnt-policy.txt",
	"/.well-known/apple-app-site-association",
	"/apple-app-site-association",
	"/.well-known/assetlinks.json",
	"/.well-known/discord",
	"/.well-known/matrix/server",
	"/.well-known/matrix/client",
	"/.well-known/nodeinfo",
	"/.well-known/webfinger",
	"/.well-known/openid-credential-issuer",
	"/.well-known/oauth-protected-resource",
}

// WellKnownRecon probes ~20 standard `.well-known/` paths + robots.txt +
// sitemap.xml + humans.txt in parallel, parses each according to its known
// format, and extracts structured findings.
//
// Use cases:
//   - Discover OAuth/OIDC endpoint surface (the openid-configuration alone
//     reveals 8-12 endpoints; client_id patterns reveal naming conventions)
//   - Find org's iOS Bundle/Team IDs via apple-app-site-association
//   - Find Android package names + cert SHA-256 via assetlinks.json
//   - Get security disclosure contact via security.txt
//   - Enumerate intentionally-hidden URLs via robots.txt Disallow paths
//   - Pull all crawler-known URLs via sitemap.xml (often includes admin/draft pages)
func WellKnownRecon(ctx context.Context, input map[string]any) (*WellKnownReconOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil, errors.New("input.target required (apex domain or full URL)")
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	target = strings.TrimRight(target, "/")

	start := time.Now()
	out := &WellKnownReconOutput{Target: target, Source: "well_known_recon"}

	// Optional custom path list.
	paths := wellKnownPaths
	if v, ok := input["additional_paths"].([]any); ok && len(v) > 0 {
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				if !strings.HasPrefix(s, "/") {
					s = "/" + s
				}
				paths = append(paths, s)
			}
		}
	}

	// Probe all paths in parallel.
	results := make([]WellKnownProbe, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 12)
	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = wkProbe(ctx, target, path)
		}(i, p)
	}
	wg.Wait()

	// Filter / dedupe overlapping paths.
	for _, r := range results {
		if r.Found {
			out.FoundCount++
		}
		out.Probes = append(out.Probes, r)
	}

	highlights := []string{}

	// Parse known formats.
	for _, r := range results {
		if !r.Found {
			continue
		}
		switch r.Path {
		case "/robots.txt":
			out.Robots = parseRobots(r.BodyExcerpt)
			if out.Robots != nil && len(out.Robots.DisallowPaths) > 0 {
				highlights = append(highlights, fmt.Sprintf("robots.txt has %d Disallow paths (intentionally-hidden URLs):", len(out.Robots.DisallowPaths)))
				for i, p := range out.Robots.DisallowPaths {
					if i >= 5 {
						highlights = append(highlights, fmt.Sprintf("  ...and %d more", len(out.Robots.DisallowPaths)-5))
						break
					}
					highlights = append(highlights, "  "+p)
				}
			}
		case "/sitemap.xml", "/sitemap_index.xml":
			if out.Sitemap == nil {
				out.Sitemap = parseSitemap(r.BodyExcerpt)
				if out.Sitemap != nil && out.Sitemap.URLCount > 0 {
					highlights = append(highlights, fmt.Sprintf("sitemap exposes %d URLs", out.Sitemap.URLCount))
				}
			}
		case "/humans.txt":
			out.HumansTxt = truncate(strings.TrimSpace(r.BodyExcerpt), 1500)
			if out.HumansTxt != "" {
				highlights = append(highlights, "humans.txt present (often lists team members)")
			}
		case "/.well-known/security.txt", "/security.txt":
			if out.SecurityTxt == nil {
				out.SecurityTxt = parseSecurityTxt(r.BodyExcerpt)
				if out.SecurityTxt != nil && len(out.SecurityTxt.Contacts) > 0 {
					highlights = append(highlights, fmt.Sprintf("security.txt contact(s): %s", strings.Join(out.SecurityTxt.Contacts, ", ")))
				}
			}
		case "/.well-known/openid-configuration":
			out.OIDC = parseOIDC(r.BodyExcerpt)
			if out.OIDC != nil && out.OIDC.Issuer != "" {
				highlights = append(highlights, fmt.Sprintf("OIDC issuer: %s", out.OIDC.Issuer))
				highlights = append(highlights, fmt.Sprintf("OIDC endpoints: auth=%s | token=%s | userinfo=%s",
					out.OIDC.AuthorizationEndpoint, out.OIDC.TokenEndpoint, out.OIDC.UserInfoEndpoint))
			}
		case "/.well-known/oauth-authorization-server":
			out.OAuthAuthServer = parseOIDC(r.BodyExcerpt)
			if out.OAuthAuthServer != nil && out.OAuthAuthServer.Issuer != "" {
				highlights = append(highlights, fmt.Sprintf("OAuth auth server issuer: %s", out.OAuthAuthServer.Issuer))
			}
		case "/.well-known/apple-app-site-association", "/apple-app-site-association":
			if out.AppleAASA == nil {
				out.AppleAASA = parseAASA(r.BodyExcerpt)
				if out.AppleAASA != nil && len(out.AppleAASA.AppIDs) > 0 {
					highlights = append(highlights, fmt.Sprintf("iOS app bundle/team IDs: %s", strings.Join(out.AppleAASA.AppIDs, ", ")))
				}
			}
		case "/.well-known/assetlinks.json":
			out.AssetLinks = parseAssetLinks(r.BodyExcerpt)
			if out.AssetLinks != nil && len(out.AssetLinks.PackageNames) > 0 {
				highlights = append(highlights, fmt.Sprintf("Android packages: %s", strings.Join(out.AssetLinks.PackageNames, ", ")))
			}
		}
	}

	out.HighValueFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func wkProbe(ctx context.Context, base, path string) WellKnownProbe {
	url := base + path
	rec := WellKnownProbe{Path: path, URL: url}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return rec
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (compatible; osint-agent/well-known-recon)")
	req.Header.Set("Accept", "*/*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rec
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	rec.HTTPStatus = resp.StatusCode
	rec.ContentType = resp.Header.Get("Content-Type")
	rec.BodyBytes = len(body)
	if resp.StatusCode == 200 && len(body) > 0 {
		rec.Found = true
		rec.BodyExcerpt = string(body)
	}
	return rec
}

func parseRobots(body string) *RobotsParsed {
	out := &RobotsParsed{}
	uaSet := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		low := strings.ToLower(line)
		switch {
		case strings.HasPrefix(low, "user-agent:"):
			ua := strings.TrimSpace(line[len("user-agent:"):])
			if ua != "" {
				uaSet[ua] = true
			}
		case strings.HasPrefix(low, "disallow:"):
			p := strings.TrimSpace(line[len("disallow:"):])
			if p != "" && p != "/" {
				out.DisallowPaths = append(out.DisallowPaths, p)
			}
		case strings.HasPrefix(low, "allow:"):
			p := strings.TrimSpace(line[len("allow:"):])
			if p != "" {
				out.AllowPaths = append(out.AllowPaths, p)
			}
		case strings.HasPrefix(low, "sitemap:"):
			s := strings.TrimSpace(line[len("sitemap:"):])
			if s != "" {
				out.SitemapURLs = append(out.SitemapURLs, s)
			}
		case strings.HasPrefix(low, "crawl-delay:"):
			d := strings.TrimSpace(line[len("crawl-delay:"):])
			if d != "" {
				out.CrawlDelays = append(out.CrawlDelays, d)
			}
		}
	}
	for ua := range uaSet {
		out.UserAgents = append(out.UserAgents, ua)
	}
	sort.Strings(out.UserAgents)
	// Dedupe disallow.
	out.DisallowPaths = dedupeStrings(out.DisallowPaths)
	out.AllowPaths = dedupeStrings(out.AllowPaths)
	return out
}

func parseSitemap(body string) *SitemapParsed {
	out := &SitemapParsed{}
	// Try sitemap-index format first
	type sitemapIndex struct {
		XMLName xml.Name `xml:"sitemapindex"`
		Sitemap []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	var idx sitemapIndex
	if err := xml.Unmarshal([]byte(body), &idx); err == nil && len(idx.Sitemap) > 0 {
		out.IsIndex = true
		for _, s := range idx.Sitemap {
			if s.Loc != "" {
				out.NestedSitemaps = append(out.NestedSitemaps, s.Loc)
			}
		}
		return out
	}
	// URL set format
	type urlset struct {
		XMLName xml.Name `xml:"urlset"`
		URL     []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	var us urlset
	if err := xml.Unmarshal([]byte(body), &us); err == nil {
		for _, u := range us.URL {
			if u.Loc != "" {
				out.URLs = append(out.URLs, u.Loc)
			}
		}
	}
	// Cap returned URLs.
	out.URLCount = len(out.URLs)
	if out.URLCount > 200 {
		out.URLs = out.URLs[:200]
	}
	return out
}

var securityTxtKeyRE = regexp.MustCompile(`(?i)^([a-z][a-z\-]+):\s*(.+?)\s*$`)

func parseSecurityTxt(body string) *SecurityTxt {
	out := &SecurityTxt{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := securityTxtKeyRE.FindStringSubmatch(line)
		if len(m) < 3 {
			continue
		}
		key := strings.ToLower(m[1])
		val := strings.TrimSpace(m[2])
		switch key {
		case "contact":
			out.Contacts = append(out.Contacts, val)
		case "encryption":
			out.Encryption = append(out.Encryption, val)
		case "acknowledgments", "acknowledgements":
			out.Acknowledgments = append(out.Acknowledgments, val)
		case "preferred-languages":
			out.PreferredLanguages = append(out.PreferredLanguages, val)
		case "canonical":
			out.Canonical = append(out.Canonical, val)
		case "policy":
			out.Policy = append(out.Policy, val)
		case "hiring":
			out.Hiring = append(out.Hiring, val)
		case "expires":
			out.Expires = val
		}
	}
	return out
}

func parseOIDC(body string) *OIDCConfig {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil
	}
	get := func(k string) string {
		if v, ok := raw[k]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				return s
			}
		}
		return ""
	}
	getArr := func(k string) []string {
		if v, ok := raw[k]; ok {
			var a []string
			if err := json.Unmarshal(v, &a); err == nil {
				return a
			}
		}
		return nil
	}
	return &OIDCConfig{
		Issuer:                 get("issuer"),
		AuthorizationEndpoint:  get("authorization_endpoint"),
		TokenEndpoint:          get("token_endpoint"),
		UserInfoEndpoint:       get("userinfo_endpoint"),
		JWKSURI:                get("jwks_uri"),
		RegistrationEndpoint:   get("registration_endpoint"),
		RevocationEndpoint:     get("revocation_endpoint"),
		IntrospectionEndpoint:  get("introspection_endpoint"),
		EndSessionEndpoint:     get("end_session_endpoint"),
		ScopesSupported:        getArr("scopes_supported"),
		ResponseTypesSupported: getArr("response_types_supported"),
		GrantTypesSupported:    getArr("grant_types_supported"),
	}
}

func parseAASA(body string) *AppleAASA {
	out := &AppleAASA{}
	var raw struct {
		Applinks struct {
			Apps    []string `json:"apps"`
			Details []struct {
				AppID  string   `json:"appID"`
				AppIDs []string `json:"appIDs"`
				Paths  []string `json:"paths"`
				Components []struct {
					Path string `json:"/"`
				} `json:"components"`
			} `json:"details"`
		} `json:"applinks"`
		WebCredentials struct {
			Apps []string `json:"apps"`
		} `json:"webcredentials"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil
	}
	teamSet := map[string]bool{}
	pathSet := map[string]bool{}
	for _, det := range raw.Applinks.Details {
		if det.AppID != "" {
			out.AppIDs = append(out.AppIDs, det.AppID)
			if i := strings.Index(det.AppID, "."); i > 0 {
				teamSet[det.AppID[:i]] = true
			}
		}
		for _, id := range det.AppIDs {
			out.AppIDs = append(out.AppIDs, id)
			if i := strings.Index(id, "."); i > 0 {
				teamSet[id[:i]] = true
			}
		}
		for _, p := range det.Paths {
			pathSet[p] = true
		}
		for _, c := range det.Components {
			if c.Path != "" {
				pathSet[c.Path] = true
			}
		}
	}
	out.AppIDs = dedupeStrings(out.AppIDs)
	for t := range teamSet {
		out.Teams = append(out.Teams, t)
	}
	sort.Strings(out.Teams)
	for p := range pathSet {
		out.UniversalLinkPaths = append(out.UniversalLinkPaths, p)
	}
	sort.Strings(out.UniversalLinkPaths)
	out.WebcredAppIDs = raw.WebCredentials.Apps
	return out
}

func parseAssetLinks(body string) *AssetLinks {
	var raw []map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil
	}
	out := &AssetLinks{Statements: raw}
	pkgSet := map[string]bool{}
	certSet := map[string]bool{}
	for _, st := range raw {
		target, _ := st["target"].(map[string]any)
		if target == nil {
			continue
		}
		if pkg, ok := target["package_name"].(string); ok && pkg != "" {
			pkgSet[pkg] = true
		}
		if certs, ok := target["sha256_cert_fingerprints"].([]any); ok {
			for _, c := range certs {
				if cs, ok := c.(string); ok {
					certSet[cs] = true
				}
			}
		}
	}
	for k := range pkgSet {
		out.PackageNames = append(out.PackageNames, k)
	}
	for k := range certSet {
		out.CertSHA256 = append(out.CertSHA256, k)
	}
	sort.Strings(out.PackageNames)
	sort.Strings(out.CertSHA256)
	return out
}

func dedupeStrings(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
