package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

type TrackerExtractOutput struct {
	URL                string              `json:"url"`
	HTTPStatus         int                 `json:"http_status"`
	Trackers           map[string][]string `json:"trackers"`           // platform → ids
	UniqueIDsCount     int                 `json:"unique_ids_count"`
	Platforms          []string            `json:"platforms_detected"`
	HighValueIDs       []TrackerHit        `json:"high_value_ids"`     // GA/FB/GTM only — strongest ER signals
	OutboundDomains    []string            `json:"outbound_third_party_domains,omitempty"` // tracking-related 3rd-party domains observed
	PivotHints         []string            `json:"pivot_hints"`        // suggestions for chaining
	Source             string              `json:"source"`
	TookMs             int64               `json:"tookMs"`
	Note               string              `json:"note,omitempty"`
}

type TrackerHit struct {
	Platform string `json:"platform"`
	ID       string `json:"id"`
	Strength string `json:"strength"` // strong | medium | weak
}

// Tracker patterns — most are ECMAScript-compatible Go regex.
// Strength-rated by:
//   - strong: rare, account-bound (GA UA-, FB pixel ID, Hotjar hjid, GTM-) → near-certain operator binding
//   - medium: bound but more common (Stripe pk, LinkedIn _li_, Mixpanel) → high-confidence signal
//   - weak: per-site config IDs that may rotate (Cloudflare ray, GA4 stream)
type trackerSpec struct {
	Platform string
	Strength string
	Patterns []*regexp.Regexp
}

var trackerSpecs = []trackerSpec{
	{
		Platform: "google_analytics_universal", Strength: "strong",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`UA-\d{4,10}-\d{1,4}`),
		},
	},
	{
		Platform: "google_analytics_4", Strength: "strong",
		Patterns: []*regexp.Regexp{
			// GA4 measurement IDs — G- followed by 10 alphanumerics
			regexp.MustCompile(`G-[A-Z0-9]{8,12}`),
		},
	},
	{
		Platform: "google_tag_manager", Strength: "strong",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`GTM-[A-Z0-9]{4,10}`),
		},
	},
	{
		Platform: "google_adsense", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:ca-pub-|client=ca-pub-)\d{16}`),
		},
	},
	{
		Platform: "facebook_pixel", Strength: "strong",
		Patterns: []*regexp.Regexp{
			// fbq('init', '1234567890') or connect.facebook.net/...?id=...
			regexp.MustCompile(`fbq\(\s*['"]init['"]\s*,\s*['"](\d{10,18})['"]`),
			regexp.MustCompile(`facebook\.com/tr\?id=(\d{10,18})`),
		},
	},
	{
		Platform: "hotjar", Strength: "strong",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`hjid\s*[:=]\s*['"]?(\d{5,10})`),
			regexp.MustCompile(`static\.hotjar\.com/c/hotjar-(\d+)\.js`),
		},
	},
	{
		Platform: "mixpanel", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`mixpanel\.init\(\s*['"]([0-9a-f]{32})['"]`),
		},
	},
	{
		Platform: "segment", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`analytics\.load\(\s*['"]([A-Za-z0-9]{20,40})['"]`),
		},
	},
	{
		Platform: "stripe_publishable_key", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`pk_(?:live|test)_[A-Za-z0-9]{20,250}`),
		},
	},
	{
		Platform: "linkedin_insight", Strength: "strong",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`_linkedin_partner_id\s*=\s*['"]?(\d{5,10})`),
			regexp.MustCompile(`px\.ads\.linkedin\.com/collect/?\?[^"']*pid=(\d+)`),
		},
	},
	{
		Platform: "twitter_pixel", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`twq\(\s*['"](?:config|init)['"]\s*,\s*['"]([a-z0-9]{5,15})['"]`),
		},
	},
	{
		Platform: "pinterest_tag", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`pintrk\(\s*['"]load['"]\s*,\s*['"](\d{10,20})['"]`),
		},
	},
	{
		Platform: "tiktok_pixel", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`ttq\.load\(\s*['"]([A-Z0-9]{18,24})['"]`),
		},
	},
	{
		Platform: "klaviyo", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`klaviyo\.com/onsite/js/\?company_id=([A-Za-z0-9]{4,10})`),
			regexp.MustCompile(`_learnq\.push.*?company\s*[:=]\s*['"]([A-Za-z0-9]{4,10})`),
		},
	},
	{
		Platform: "intercom", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`Intercom\(\s*['"]boot['"]\s*,\s*\{[^}]*app_id\s*[:=]\s*['"]([a-z0-9]{6,10})['"]`),
			regexp.MustCompile(`widget\.intercom\.io/widget/([a-z0-9]{6,10})`),
		},
	},
	{
		Platform: "drift", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`drift\.load\(\s*['"]([a-z0-9]{8,20})['"]`),
		},
	},
	{
		Platform: "fullstory", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`window\['_fs_org'\]\s*=\s*['"]([A-Z0-9]{6,10})['"]`),
		},
	},
	{
		Platform: "amplitude", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`amplitude\.(?:init|getInstance\(\)\.init)\(\s*['"]([a-f0-9]{32})['"]`),
		},
	},
	{
		Platform: "heap", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`heap\.load\(\s*['"]?(\d{6,12})['"]?`),
		},
	},
	{
		Platform: "hubspot_id", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`js\.hs-scripts\.com/(\d{5,10})\.js`),
			regexp.MustCompile(`hsforms\.com/forms/v2\.js[^'"]*portalId\s*[:=]\s*['"]?(\d{5,10})`),
		},
	},
	{
		Platform: "shopify_id", Strength: "strong",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`Shopify\.shop\s*=\s*['"]([a-z0-9-]+\.myshopify\.com)['"]`),
			regexp.MustCompile(`"shop_id"\s*:\s*(\d{5,12})`),
		},
	},
	{
		Platform: "yandex_metrica", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`ym\(\s*(\d{6,10})\s*,`),
		},
	},
	{
		Platform: "vk_pixel", Strength: "medium",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`VK\.Retargeting\.Init\(\s*['"](VK-RTRG-\d+-[A-Za-z0-9]+)['"]`),
		},
	},
}

// 3rd-party tracking domains we'll inventory as a coarse tally
var thirdPartyTrackerDomainsRE = regexp.MustCompile(
	`(?i)https?://([a-z0-9-]+\.)?(google-analytics|googletagmanager|connect\.facebook|static\.hotjar|cdn\.mxpnl|cdn\.segment|js\.stripe|snap\.licdn|static\.ads-twitter|s\.pinimg|analytics\.tiktok|klaviyo|widget\.intercom|api\.amplitude|cdn\.heap|js\.hs-scripts|cdn\.fullstory|drift|googleadservices|doubleclick|criteo|adnxs|outbrain|taboola|bing|yandex|vk\.com)\.[a-z]{2,}`,
)

func TrackerExtract(ctx context.Context, input map[string]any) (*TrackerExtractOutput, error) {
	urlIn, _ := input["url"].(string)
	urlIn = strings.TrimSpace(urlIn)
	preRenderedHTML, _ := input["html"].(string)

	if urlIn == "" && preRenderedHTML == "" {
		return nil, errors.New("input.url or input.html required")
	}
	if urlIn != "" && !strings.HasPrefix(urlIn, "http://") && !strings.HasPrefix(urlIn, "https://") {
		urlIn = "https://" + urlIn
	}
	start := time.Now()

	var html string
	httpStatus := 0

	if preRenderedHTML != "" {
		// Skip the fetch — caller already rendered the page (e.g. via firecrawl_scrape)
		html = preRenderedHTML
		httpStatus = 200
	} else {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, urlIn, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch failed: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		html = string(body)
		httpStatus = resp.StatusCode
	}

	out := &TrackerExtractOutput{
		URL:        urlIn,
		HTTPStatus: httpStatus,
		Trackers:   map[string][]string{},
		Source:     "tracker_extract",
		TookMs:     time.Since(start).Milliseconds(),
	}

	totalIDs := 0
	platforms := map[string]bool{}
	highValue := []TrackerHit{}

	for _, spec := range trackerSpecs {
		seen := map[string]bool{}
		for _, re := range spec.Patterns {
			matches := re.FindAllStringSubmatch(html, -1)
			for _, m := range matches {
				var id string
				if len(m) >= 2 && m[1] != "" {
					id = m[1]
				} else {
					id = m[0]
				}
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				out.Trackers[spec.Platform] = append(out.Trackers[spec.Platform], id)
				platforms[spec.Platform] = true
				totalIDs++
				if spec.Strength == "strong" {
					highValue = append(highValue, TrackerHit{Platform: spec.Platform, ID: id, Strength: spec.Strength})
				}
			}
		}
	}

	// 3rd-party tracker domains (coarse inventory — not de-duped per platform).
	domSeen := map[string]bool{}
	for _, m := range thirdPartyTrackerDomainsRE.FindAllStringSubmatch(html, -1) {
		full := m[0]
		host := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(full, "https://"), "http://"), "/", 2)[0]
		if !domSeen[host] {
			domSeen[host] = true
			out.OutboundDomains = append(out.OutboundDomains, host)
		}
	}
	sort.Strings(out.OutboundDomains)

	platformList := make([]string, 0, len(platforms))
	for p := range platforms {
		platformList = append(platformList, p)
	}
	sort.Strings(platformList)
	out.Platforms = platformList
	out.UniqueIDsCount = totalIDs
	out.HighValueIDs = highValue

	// Pivot hints — direct the agent toward chained ER queries.
	if len(highValue) > 0 {
		out.PivotHints = append(out.PivotHints,
			"Use urlscan_search with task.url containing the high-value tracker ID to find sister sites with the same operator.",
			"For Google Analytics IDs, try `publicwww.com` (free 100 q/day) for cross-site enumeration.",
			"For Facebook Pixel IDs, the same ID across domains nearly always indicates a shared operator.",
		)
	}
	if len(out.OutboundDomains) >= 5 {
		out.PivotHints = append(out.PivotHints,
			"Heavy 3rd-party tracker presence — site uses analytics aggressively; correlate timing of pixel firing with user-agent for fingerprint stacking.",
		)
	}

	if totalIDs == 0 {
		out.Note = "No tracker IDs detected — site may be SPA-rendered (try JS-rendering proxy first), use SSR with delayed loading, or be tracker-free"
	}
	return out, nil
}
