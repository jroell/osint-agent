package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// BioLinkResolved represents one discovered link from the bio-link page.
type BioLinkResolved struct {
	Platform   string `json:"platform,omitempty"`     // INSTAGRAM, X, LINKEDIN, etc. (canonical for socialLinks); CLASSIC/CUSTOM otherwise
	Title      string `json:"title,omitempty"`        // Display title for custom links
	URL        string `json:"url"`
	Source     string `json:"source"` // socialLinks | links
	ResolvedHandle string `json:"resolved_handle,omitempty"` // e.g. "garyvee" for instagram.com/garyvee
}

// BioLinkResolveOutput is what we return.
type BioLinkResolveOutput struct {
	Username      string             `json:"username"`
	ResolvedURL   string             `json:"resolved_url"`
	Service       string             `json:"service"` // linktree | about_me | taplink
	DisplayName   string             `json:"display_name,omitempty"`
	Description   string             `json:"description,omitempty"`
	ProfileImage  string             `json:"profile_image,omitempty"`
	SocialHandles map[string]string  `json:"social_handles,omitempty"` // platform → handle (canonical, e.g. "instagram" → "garyvee")
	Links         []BioLinkResolved  `json:"links"`
	UniqueDomains []string           `json:"unique_domains,omitempty"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source        string             `json:"source"`
	TookMs        int64              `json:"tookMs"`
	Note          string             `json:"note,omitempty"`
}

// linktreeNextData is the minimal subset of __NEXT_DATA__ we need.
type linktreeNextData struct {
	Props struct {
		PageProps struct {
			Account struct {
				Username      string `json:"username"`
				PageTitle     string `json:"pageTitle"`
				ProfileTitle  string `json:"profileTitle"`
				Description   string `json:"description"`
				ProfilePicURL string `json:"profilePictureUrl"`
				IsActive      bool   `json:"isActive"`
				SocialLinks   []struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				} `json:"socialLinks"`
				Links []struct {
					Type  string `json:"type"`
					Title string `json:"title"`
					URL   string `json:"url"`
				} `json:"links"`
			} `json:"account"`
		} `json:"pageProps"`
	} `json:"props"`
}

var (
	nextDataRe = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.+?)</script>`)
	// Generic anchor matcher for about.me / taplink fallbacks
	anchorRe = regexp.MustCompile(`(?i)<a [^>]*href=["']([^"']+)["'][^>]*>([^<]{0,200})</a>`)
)

// platformPatternMap maps known social URL patterns → canonical platform name and a handle-extraction regex.
var platformPatterns = []struct {
	platform   string
	urlPattern *regexp.Regexp
}{
	{"instagram", regexp.MustCompile(`(?i)(?:www\.)?instagram\.com/([\w._]{1,30})`)},
	{"twitter", regexp.MustCompile(`(?i)(?:www\.)?(?:twitter|x)\.com/([\w_]{1,15})`)},
	{"facebook", regexp.MustCompile(`(?i)(?:www\.)?facebook\.com/([\w.\-]{1,50})`)},
	{"tiktok", regexp.MustCompile(`(?i)(?:www\.)?tiktok\.com/@?([\w._]{1,30})`)},
	{"youtube", regexp.MustCompile(`(?i)(?:www\.)?youtube\.com/(?:c/|user/|@)?([\w\-]{1,40})`)},
	{"linkedin", regexp.MustCompile(`(?i)(?:www\.)?linkedin\.com/in/([\w\-]{1,50})`)},
	{"github", regexp.MustCompile(`(?i)(?:www\.)?github\.com/([\w\-]{1,40})`)},
	{"twitch", regexp.MustCompile(`(?i)(?:www\.)?twitch\.tv/([\w_]{1,25})`)},
	{"reddit", regexp.MustCompile(`(?i)(?:www\.)?reddit\.com/user/([\w_\-]{1,30})`)},
	{"snapchat", regexp.MustCompile(`(?i)(?:www\.)?snapchat\.com/add/([\w._\-]{1,30})`)},
	{"pinterest", regexp.MustCompile(`(?i)(?:www\.)?pinterest\.com/([\w_\-]{1,30})`)},
	{"discord", regexp.MustCompile(`(?i)discord\.(?:gg|com/invite)/([\w]{4,30})`)},
	{"telegram", regexp.MustCompile(`(?i)(?:t\.me|telegram\.me)/([\w_]{4,40})`)},
	{"whatsapp", regexp.MustCompile(`(?i)wa\.me/(?:message/)?([\w]{6,30})`)},
	{"spotify", regexp.MustCompile(`(?i)open\.spotify\.com/(?:user|artist)/([\w]{1,40})`)},
	{"soundcloud", regexp.MustCompile(`(?i)soundcloud\.com/([\w_\-]{1,40})`)},
	{"medium", regexp.MustCompile(`(?i)medium\.com/@?([\w._\-]{1,40})`)},
	{"substack", regexp.MustCompile(`(?i)([\w\-]+)\.substack\.com`)},
	{"patreon", regexp.MustCompile(`(?i)patreon\.com/([\w_\-]{1,40})`)},
	{"onlyfans", regexp.MustCompile(`(?i)onlyfans\.com/([\w_\-]{1,40})`)},
	{"venmo", regexp.MustCompile(`(?i)venmo\.com/(?:u/)?([\w_\-]{1,40})`)},
	{"cashapp", regexp.MustCompile(`(?i)cash\.app/\$?([\w]{1,30})`)},
	{"paypal", regexp.MustCompile(`(?i)paypal\.(?:me|com/paypalme)/([\w_\-]{1,30})`)},
	{"buymeacoffee", regexp.MustCompile(`(?i)buymeacoffee\.com/([\w_\-]{1,40})`)},
	{"kofi", regexp.MustCompile(`(?i)ko-fi\.com/([\w_\-]{1,40})`)},
	{"bluesky", regexp.MustCompile(`(?i)bsky\.app/profile/([\w._\-]{1,40})`)},
	{"mastodon", regexp.MustCompile(`(?i)([\w._]+)@([\w.\-]+\.\w+)`)},
}

// BioLinkResolve fetches a user's "link in bio" page (Linktree, about.me,
// or taplink) and extracts every cross-platform identity they have
// self-declared. Three lookup modes:
//
//   - Pass `username` only → tries linktr.ee/<username>, then about.me/<username>,
//     then taplink.cc/<username> and uses the first one that succeeds.
//   - Pass `service` to lock to a specific platform (linktree | about_me | taplink).
//   - Pass `url` directly for full URL of the bio page.
//
// Why this matters for ER:
//   - Linktree/about.me/taplink contents are SELF-PUBLISHED by the user as
//     their official cross-platform identity graph — the highest-trust ER
//     signal short of a verified identity provider.
//   - Most influencers/creators/musicians/podcasters have one — closes the
//     loop from "we found this Instagram handle" to "here's every other
//     handle they own."
//   - Resolves Instagram/X/LinkedIn/YouTube/Twitch/TikTok/GitHub handles +
//     payment handles (Venmo, Cash App, PayPal) all in one call.
func BioLinkResolve(ctx context.Context, input map[string]any) (*BioLinkResolveOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")

	directURL, _ := input["url"].(string)
	directURL = strings.TrimSpace(directURL)

	service, _ := input["service"].(string)
	service = strings.ToLower(strings.TrimSpace(service))

	if username == "" && directURL == "" {
		return nil, fmt.Errorf("input.username or input.url required")
	}

	start := time.Now()
	out := &BioLinkResolveOutput{
		Username:      username,
		SocialHandles: map[string]string{},
		Source:        "bio-link platforms (linktr.ee, about.me, taplink.cc)",
	}

	candidates := []struct {
		service string
		url     string
	}{}
	if directURL != "" {
		// detect service from URL
		svc := "unknown"
		switch {
		case strings.Contains(directURL, "linktr.ee/") || strings.Contains(directURL, "linktree.com/"):
			svc = "linktree"
		case strings.Contains(directURL, "about.me/"):
			svc = "about_me"
		case strings.Contains(directURL, "taplink.cc/"):
			svc = "taplink"
		}
		candidates = append(candidates, struct{ service, url string }{svc, directURL})
	} else if service != "" {
		// service-locked mode
		switch service {
		case "linktree":
			candidates = append(candidates, struct{ service, url string }{"linktree", "https://linktr.ee/" + url.PathEscape(username)})
		case "about_me", "aboutme":
			candidates = append(candidates, struct{ service, url string }{"about_me", "https://about.me/" + url.PathEscape(username)})
		case "taplink":
			candidates = append(candidates, struct{ service, url string }{"taplink", "https://taplink.cc/" + url.PathEscape(username)})
		default:
			return nil, fmt.Errorf("unknown service '%s' — supported: linktree, about_me, taplink", service)
		}
	} else {
		// auto-discovery
		candidates = []struct{ service, url string }{
			{"linktree", "https://linktr.ee/" + url.PathEscape(username)},
			{"about_me", "https://about.me/" + url.PathEscape(username)},
			{"taplink", "https://taplink.cc/" + url.PathEscape(username)},
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr string

	for _, c := range candidates {
		req, err := http.NewRequestWithContext(ctx, "GET", c.url, nil)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; osint-agent/0.1)")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4_000_000))
		resp.Body.Close()
		if err != nil {
			lastErr = err.Error()
			continue
		}

		if resp.StatusCode == 404 {
			lastErr = fmt.Sprintf("%s/%s not found (404)", c.service, username)
			continue
		}
		if resp.StatusCode >= 400 {
			lastErr = fmt.Sprintf("%s returned %d", c.service, resp.StatusCode)
			continue
		}

		out.ResolvedURL = c.url
		out.Service = c.service
		switch c.service {
		case "linktree":
			if !parseLinktree(string(body), out) {
				lastErr = "linktree returned page but no parseable __NEXT_DATA__"
				continue
			}
		case "about_me":
			parseAboutMe(string(body), out)
		case "taplink":
			parseTaplink(string(body), out)
		default:
			parseAboutMe(string(body), out) // generic anchor scrape
		}
		// Normalize handles + dedupe
		finalize(out)
		out.TookMs = time.Since(start).Milliseconds()
		buildHighlights(out)
		return out, nil
	}

	out.TookMs = time.Since(start).Milliseconds()
	out.Note = fmt.Sprintf("no bio-link page resolved for username '%s'; last error: %s", username, lastErr)
	return out, nil
}

func parseLinktree(html string, out *BioLinkResolveOutput) bool {
	m := nextDataRe.FindStringSubmatch(html)
	if len(m) < 2 {
		return false
	}
	var nd linktreeNextData
	if err := json.Unmarshal([]byte(m[1]), &nd); err != nil {
		return false
	}
	acc := nd.Props.PageProps.Account
	if acc.Username == "" {
		return false
	}
	if out.Username == "" {
		out.Username = acc.Username
	}
	out.DisplayName = strings.TrimSpace(acc.ProfileTitle)
	if out.DisplayName == "" {
		out.DisplayName = acc.PageTitle
	}
	out.Description = strings.TrimSpace(acc.Description)
	out.ProfileImage = acc.ProfilePicURL

	for _, s := range acc.SocialLinks {
		out.Links = append(out.Links, BioLinkResolved{
			Platform: strings.ToLower(s.Type),
			URL:      s.URL,
			Source:   "socialLinks",
		})
	}
	for _, l := range acc.Links {
		out.Links = append(out.Links, BioLinkResolved{
			Platform: strings.ToLower(l.Type),
			Title:    l.Title,
			URL:      l.URL,
			Source:   "links",
		})
	}
	return true
}

func parseAboutMe(html string, out *BioLinkResolveOutput) {
	// Extract <title> as display name
	if t := regexp.MustCompile(`(?is)<title[^>]*>([^<]+)</title>`).FindStringSubmatch(html); len(t) > 1 {
		out.DisplayName = strings.TrimSpace(t[1])
	}
	// Extract og:description
	if d := regexp.MustCompile(`(?is)<meta\s+property=["']og:description["']\s+content=["']([^"']+)`).FindStringSubmatch(html); len(d) > 1 {
		out.Description = strings.TrimSpace(d[1])
	}
	if i := regexp.MustCompile(`(?is)<meta\s+property=["']og:image["']\s+content=["']([^"']+)`).FindStringSubmatch(html); len(i) > 1 {
		out.ProfileImage = i[1]
	}
	// Extract anchor URLs
	seen := map[string]bool{}
	for _, m := range anchorRe.FindAllStringSubmatch(html, -1) {
		raw := strings.TrimSpace(m[1])
		if !strings.HasPrefix(raw, "http") {
			continue
		}
		// skip about.me-internal links + tracking
		if strings.Contains(raw, "about.me") || strings.Contains(raw, "javascript:") {
			continue
		}
		if seen[raw] {
			continue
		}
		seen[raw] = true
		out.Links = append(out.Links, BioLinkResolved{
			Title:  strings.TrimSpace(m[2]),
			URL:    raw,
			Source: "anchor",
		})
	}
}

func parseTaplink(html string, out *BioLinkResolveOutput) {
	parseAboutMe(html, out) // same anchor-scrape strategy works for taplink
}

func finalize(out *BioLinkResolveOutput) {
	domainSet := map[string]bool{}
	for i := range out.Links {
		l := &out.Links[i]
		// detect platform + handle from URL
		if l.URL == "" {
			continue
		}
		u, err := url.Parse(l.URL)
		if err == nil && u.Host != "" {
			host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
			domainSet[host] = true
		}
		for _, p := range platformPatterns {
			m := p.urlPattern.FindStringSubmatch(l.URL)
			if len(m) > 1 {
				if l.Platform == "" || l.Platform == "classic" || l.Platform == "custom" {
					l.Platform = p.platform
				}
				l.ResolvedHandle = m[1]
				if _, exists := out.SocialHandles[p.platform]; !exists {
					out.SocialHandles[p.platform] = m[1]
				}
				break
			}
		}
	}
	for d := range domainSet {
		out.UniqueDomains = append(out.UniqueDomains, d)
	}
	sort.Strings(out.UniqueDomains)
}

func buildHighlights(out *BioLinkResolveOutput) {
	hi := []string{}
	if out.Service != "" && out.ResolvedURL != "" {
		hi = append(hi, fmt.Sprintf("resolved via %s: %s", out.Service, out.ResolvedURL))
	}
	if len(out.SocialHandles) > 0 {
		hi = append(hi, fmt.Sprintf("✓ %d social platforms with resolved handles — strong cross-platform ER signal (self-published)", len(out.SocialHandles)))
		// Pretty print platform → handle
		platforms := []string{}
		for p := range out.SocialHandles {
			platforms = append(platforms, p)
		}
		sort.Strings(platforms)
		parts := []string{}
		for _, p := range platforms {
			parts = append(parts, fmt.Sprintf("%s=%s", p, out.SocialHandles[p]))
		}
		hi = append(hi, "handles: "+strings.Join(parts, ", "))
	}
	if len(out.Links) > 0 {
		hi = append(hi, fmt.Sprintf("%d total links discovered (%d unique domains)", len(out.Links), len(out.UniqueDomains)))
	}
	// Flag financial/payment handles — high-value
	paymentSet := map[string]bool{"venmo": true, "cashapp": true, "paypal": true, "buymeacoffee": true, "kofi": true}
	pay := []string{}
	for p := range out.SocialHandles {
		if paymentSet[p] {
			pay = append(pay, p+"="+out.SocialHandles[p])
		}
	}
	if len(pay) > 0 {
		sort.Strings(pay)
		hi = append(hi, "💰 payment handles disclosed: "+strings.Join(pay, ", "))
	}
	if len(out.SocialHandles) == 0 && len(out.Links) == 0 {
		hi = append(hi, "no links found — page may exist but be empty, or anti-bot is blocking")
	}
	out.HighlightFindings = hi
}
