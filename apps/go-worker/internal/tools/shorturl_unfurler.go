package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RedirectHop struct {
	Number       int      `json:"number"`
	URL          string   `json:"url"`
	Method       string   `json:"method"`
	Status       int      `json:"status"`
	LocationHdr  string   `json:"location_header,omitempty"`
	Server       string   `json:"server,omitempty"`
	ContentType  string   `json:"content_type,omitempty"`
	SetCookies   []string `json:"set_cookies,omitempty"`
	RedirectVia  string   `json:"redirect_via,omitempty"` // 30x | meta-refresh | js-window-location
	Domain       string   `json:"domain"`
	NewDomain    bool     `json:"new_domain"` // changed from previous hop
}

type ShortURLUnfurlerOutput struct {
	OriginalURL    string        `json:"original_url"`
	FinalURL       string        `json:"final_url,omitempty"`
	HopCount       int           `json:"hop_count"`
	UniqueDomains  []string      `json:"unique_domains"`
	Hops           []RedirectHop `json:"hops"`
	HasMetaRefresh bool          `json:"has_meta_refresh_redirect"`
	HasJSRedirect  bool          `json:"has_js_redirect"`
	SuspicionScore int           `json:"suspicion_score"` // 0-100
	SuspicionReasons []string    `json:"suspicion_reasons,omitempty"`
	Source         string        `json:"source"`
	TookMs         int64         `json:"tookMs"`
	Note           string        `json:"note,omitempty"`
}

// Known shortener domains — these always indicate an obfuscated link.
var shortenerDomains = map[string]bool{
	"bit.ly": true, "tinyurl.com": true, "t.co": true, "ow.ly": true,
	"goo.gl": true, "is.gd": true, "buff.ly": true, "lnkd.in": true,
	"short.io": true, "rebrand.ly": true, "tiny.cc": true, "rb.gy": true,
	"tr.im": true, "snip.ly": true, "shorturl.at": true, "cutt.ly": true,
	"smarturl.it": true, "y2u.be": true, "youtu.be": true,
	"discord.gg": true, "fb.me": true, "linktr.ee": true,
	"shop.app": true, "0x.no": true, "me-qr.com": true,
}

// ShortURLUnfurler follows the redirect chain of a shortened URL up to a
// max-hops cap. Captures each hop's URL/status/headers/Set-Cookie/redirect
// mechanism. Detects 30x, meta-refresh, and javascript:window.location
// redirects (the latter two used by phishing operators to evade simple
// HTTP redirect followers).
//
// Use cases:
//   - Phishing analysis: bit.ly/abc → 3 hops → final landing page
//   - Marketing analytics: trace UTM-stripped redirects to source
//   - Social engineering forensics: capture every cookie set along the chain
//
// Suspicion score combines: hop count, # of unique domains, presence of
// meta-refresh/JS redirects (commonly evasive), known-shortener entries
// in the chain.
func ShortURLUnfurler(ctx context.Context, input map[string]any) (*ShortURLUnfurlerOutput, error) {
	urlIn, _ := input["url"].(string)
	urlIn = strings.TrimSpace(urlIn)
	if urlIn == "" {
		return nil, errors.New("input.url required")
	}
	if !strings.HasPrefix(urlIn, "http://") && !strings.HasPrefix(urlIn, "https://") {
		urlIn = "https://" + urlIn
	}
	maxHops := 15
	if v, ok := input["max_hops"].(float64); ok && int(v) > 0 && int(v) <= 50 {
		maxHops = int(v)
	}

	start := time.Now()
	out := &ShortURLUnfurlerOutput{OriginalURL: urlIn, Source: "shorturl_unfurler"}

	// Custom client that does NOT auto-follow redirects.
	client := &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // tell client to NOT follow
		},
	}

	currentURL := urlIn
	prevDomain := ""
	domainSet := map[string]bool{}

	for hopNum := 1; hopNum <= maxHops; hopNum++ {
		// Try GET first (some shorteners return 200 with meta-refresh)
		hop, nextURL, mechanism := unfurlOneHop(ctx, client, currentURL)
		hop.Number = hopNum
		hop.RedirectVia = mechanism

		// Compute domain
		if u, err := url.Parse(currentURL); err == nil {
			d := strings.ToLower(u.Host)
			if strings.HasPrefix(d, "www.") {
				d = d[4:]
			}
			hop.Domain = d
			hop.NewDomain = (d != prevDomain)
			domainSet[d] = true
			prevDomain = d
		}

		out.Hops = append(out.Hops, hop)

		if mechanism == "meta-refresh" {
			out.HasMetaRefresh = true
		}
		if mechanism == "js-window-location" {
			out.HasJSRedirect = true
		}

		if nextURL == "" {
			out.FinalURL = currentURL
			break
		}
		// Resolve relative URL
		if !strings.HasPrefix(nextURL, "http://") && !strings.HasPrefix(nextURL, "https://") {
			if base, err := url.Parse(currentURL); err == nil {
				if rel, err := url.Parse(nextURL); err == nil {
					nextURL = base.ResolveReference(rel).String()
				}
			}
		}
		// Loop detection
		seenSamePos := false
		for _, h := range out.Hops {
			if h.URL == nextURL {
				seenSamePos = true
				break
			}
		}
		if seenSamePos {
			out.Note = "Redirect loop detected — chain references a previous URL"
			out.FinalURL = currentURL
			break
		}
		currentURL = nextURL
	}

	if out.FinalURL == "" {
		out.FinalURL = currentURL
		out.Note = fmt.Sprintf("Hit max-hops cap of %d without resolution", maxHops)
	}

	out.HopCount = len(out.Hops)
	for d := range domainSet {
		out.UniqueDomains = append(out.UniqueDomains, d)
	}

	// Suspicion scoring
	score := 0
	reasons := []string{}
	if out.HopCount >= 4 {
		score += 25
		reasons = append(reasons, fmt.Sprintf("%d redirect hops (≥4 is unusual)", out.HopCount))
	}
	if len(out.UniqueDomains) >= 3 {
		score += 20
		reasons = append(reasons, fmt.Sprintf("%d distinct domains in chain", len(out.UniqueDomains)))
	}
	for _, d := range out.UniqueDomains {
		if shortenerDomains[d] {
			score += 5
		}
	}
	if out.HasMetaRefresh {
		score += 15
		reasons = append(reasons, "uses meta-refresh redirect (often evasive)")
	}
	if out.HasJSRedirect {
		score += 30
		reasons = append(reasons, "⚠️  uses javascript:window.location (highly evasive — typical of malware/phishing)")
	}
	for _, h := range out.Hops {
		if h.Status >= 400 && h.Status != 404 {
			score += 5
			reasons = append(reasons, fmt.Sprintf("hop %d returned suspicious status %d", h.Number, h.Status))
		}
	}
	if score > 100 {
		score = 100
	}
	out.SuspicionScore = score
	out.SuspicionReasons = reasons

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func unfurlOneHop(ctx context.Context, client *http.Client, urlIn string) (RedirectHop, string, string) {
	hop := RedirectHop{URL: urlIn, Method: "GET"}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, urlIn, nil)
	if err != nil {
		return hop, "", ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		hop.Status = -1
		return hop, "", ""
	}
	defer resp.Body.Close()
	hop.Status = resp.StatusCode
	hop.LocationHdr = resp.Header.Get("Location")
	hop.Server = resp.Header.Get("Server")
	hop.ContentType = resp.Header.Get("Content-Type")
	for _, c := range resp.Header.Values("Set-Cookie") {
		// Truncate cookie value for privacy
		semi := strings.Index(c, ";")
		nameAttrs := c
		if semi > 0 {
			nameAttrs = c[:semi]
		}
		hop.SetCookies = append(hop.SetCookies, nameAttrs)
	}

	// 30x with Location header → use it
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && hop.LocationHdr != "" {
		return hop, hop.LocationHdr, fmt.Sprintf("HTTP %d Location", resp.StatusCode)
	}

	// 200 with HTML body → check for meta-refresh / js-window-location
	if resp.StatusCode == 200 && strings.Contains(strings.ToLower(hop.ContentType), "html") {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		bs := string(body)
		bsLow := strings.ToLower(bs)
		// meta-refresh: <meta http-equiv="refresh" content="0; url=...">
		if i := strings.Index(bsLow, `meta http-equiv="refresh"`); i >= 0 {
			seg := bs[i:minInt(i+400, len(bs))]
			urlIdx := strings.Index(strings.ToLower(seg), "url=")
			if urlIdx >= 0 {
				rest := seg[urlIdx+4:]
				rest = strings.Trim(rest, ` "'`)
				if endIdx := strings.IndexAny(rest, `"' `); endIdx > 0 {
					rest = rest[:endIdx]
				}
				rest = strings.TrimSpace(rest)
				if rest != "" {
					return hop, rest, "meta-refresh"
				}
			}
		}
		// js-window-location: window.location = "..." or window.location.href = "..."
		if i := strings.Index(bsLow, "window.location"); i >= 0 {
			seg := bs[i:minInt(i+400, len(bs))]
			eqIdx := strings.Index(seg, "=")
			if eqIdx >= 0 {
				rest := seg[eqIdx+1:]
				rest = strings.TrimLeft(rest, ` "'`)
				if endIdx := strings.IndexAny(rest, `"';`); endIdx > 0 {
					rest = rest[:endIdx]
				}
				rest = strings.TrimSpace(rest)
				if strings.HasPrefix(rest, "http") {
					return hop, rest, "js-window-location"
				}
			}
		}
	}

	return hop, "", ""
}
