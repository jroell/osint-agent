package tools

import (
	"context"
	"strings"
	"testing"
)

// TestEntityMatch_URLCanonicalizeDedup is the proof-of-improvement
// test for iteration 19 — closes the dedup-primitive quartet
// (email/phone/social/URL).
//
// The defect: tools that emit URLs (firecrawl, google_dork,
// hackertarget_recon, wayback_url_history, internet_archive_search,
// common_crawl, github_advanced_search, linkedin_proxycurl,
// truepeoplesearch_lookup, panel_entity_resolution evidence URLs,
// tavily_search/perplexity_search citation URLs) routinely emit the
// same logical URL in 4-6 distinct surface forms — http vs https,
// www vs apex, default-port appended, trailing slash, IDN vs
// punycode, ?utm_*= tracking params, #fragment, query-param order.
// person_aggregate's evidence merge today dedups by literal string,
// so the same article appears as 5+ distinct citations.
//
// The fix: a new url_canonicalize mode on entity_match that
// applies the standard URL-normalization rules — lowercase scheme/
// host, strip www, strip default ports, IDN→punycode, strip
// fragment, drop tracking params (utm_*/mc_*/_hs*/fbclid/gclid/…),
// sort remaining params, strip trailing slash from non-root paths,
// http→https. Emits the canonical URL as the strongest dedup key.
//
// Quantitative metric: dedup count on a curated fixture where 5
// unique URLs are written in 22 surface forms.
func TestEntityMatch_URLCanonicalizeDedup(t *testing.T) {
	groups := [][]string{
		// 1. Article URL — 7 surface forms.
		{
			"https://example.com/article",
			"http://example.com/article",
			"https://www.example.com/article",
			"https://example.com/article/",
			"https://example.com/article?utm_source=twitter&utm_medium=share",
			"https://example.com/article?fbclid=IwAR123456",
			"https://example.com/article#section1",
		},
		// 2. URL with query params (some real, some tracking).
		{
			"https://news.example.com/story?id=42&utm_campaign=newsletter",
			"https://www.news.example.com/story?id=42",
			"http://news.example.com/story?id=42&fbclid=abc",
			"https://news.example.com/story?id=42&gclid=xyz",
			"https://news.example.com/story?id=42&mc_cid=foo&mc_eid=bar",
		},
		// 3. URL with multiple genuine query params (must preserve, sort).
		{
			"https://shop.example.com/search?q=widget&sort=price",
			"https://shop.example.com/search?sort=price&q=widget",
			"https://www.shop.example.com/search?q=widget&sort=price&utm_term=ads",
		},
		// 4. IDN host — punycode and Unicode forms.
		{
			"https://münchen.example/store",
			"https://xn--mnchen-3ya.example/store",
			"https://www.münchen.example/store/",
		},
		// 5. URL with default port.
		{
			"https://example.org:443/page",
			"http://example.org:80/page",
			"https://example.org/page",
			"https://example.org/page/",
		},
	}

	expectedKeys := len(groups) // 5

	allURLs := []string{}
	for _, g := range groups {
		allURLs = append(allURLs, g...)
	}

	// LEGACY: lowercase + trim — most generous baseline.
	legacy := map[string]bool{}
	for _, u := range allURLs {
		legacy[strings.ToLower(strings.TrimSpace(u))] = true
	}

	canonical := map[string]bool{}
	groupHits := make([]map[string]bool, len(groups))
	for i := range groupHits {
		groupHits[i] = map[string]bool{}
	}
	failedToParse := []string{}

	for gi, g := range groups {
		for _, u := range g {
			res, err := EntityMatch(context.Background(), map[string]any{
				"mode": "url_canonicalize",
				"url":  u,
			})
			if err != nil {
				t.Fatalf("EntityMatch(%q): %v", u, err)
			}
			if !res.URLValid {
				failedToParse = append(failedToParse, u)
				continue
			}
			canonical[res.URLCanonical] = true
			groupHits[gi][res.URLCanonical] = true
		}
	}

	t.Logf("URL canonicalization dedup on %d surface forms (%d true URLs):",
		len(allURLs), expectedKeys)
	t.Logf("  legacy   (ToLower+TrimSpace only):    %d distinct", len(legacy))
	t.Logf("  new      (entity_match canonicalize): %d distinct", len(canonical))
	dedupRate := 1.0 - float64(len(canonical))/float64(len(legacy))
	t.Logf("  dedup-rate improvement:               %.1f%% reduction in apparent URL count", dedupRate*100)

	for gi, hits := range groupHits {
		if len(hits) > 1 {
			collapsed := []string{}
			for k := range hits {
				collapsed = append(collapsed, k)
			}
			t.Errorf("group %d (e.g. %q) split into %d distinct keys: %v",
				gi, groups[gi][0], len(hits), collapsed)
		} else if len(hits) == 0 {
			t.Errorf("group %d (e.g. %q) produced no canonical key — every variant failed to parse",
				gi, groups[gi][0])
		}
	}

	if len(canonical) != expectedKeys {
		t.Errorf("got %d canonical keys, want exactly %d", len(canonical), expectedKeys)
	}
	if len(failedToParse) > 0 {
		t.Errorf("failed to parse: %v", failedToParse)
	}
	if len(canonical) >= len(legacy) {
		t.Errorf("canonicalization produced no improvement over legacy (%d ≥ %d)",
			len(canonical), len(legacy))
	}
	if len(legacy) < 3*len(canonical) {
		t.Errorf("legacy distinct count %d < 3× canonical (%d) — fixture isn't multi-form enough",
			len(legacy), len(canonical))
	}
}

// TestEntityMatch_URLCanonicalize_RulesPinning pins individual
// canonicalization rules so regressions surface specifically.
func TestEntityMatch_URLCanonicalize_RulesPinning(t *testing.T) {
	type want struct {
		valid     bool
		canonical string
	}
	cases := map[string]want{
		// scheme normalization http → https
		"http://example.com/foo":  {true, "https://example.com/foo"},
		"https://example.com/foo": {true, "https://example.com/foo"},
		// www stripping
		"https://www.example.com/foo": {true, "https://example.com/foo"},
		// default ports
		"https://example.com:443/foo": {true, "https://example.com/foo"},
		"http://example.com:80/foo":   {true, "https://example.com/foo"},
		// trailing slash on non-root
		"https://example.com/foo/": {true, "https://example.com/foo"},
		"https://example.com/":     {true, "https://example.com/"},
		// fragment drop
		"https://example.com/foo#bar": {true, "https://example.com/foo"},
		// utm + fbclid + gclid + mc_* + _hs* drop
		"https://example.com/foo?utm_source=tw&utm_campaign=x": {true, "https://example.com/foo"},
		"https://example.com/foo?fbclid=ABC123":                {true, "https://example.com/foo"},
		"https://example.com/foo?id=1&utm_source=tw":           {true, "https://example.com/foo?id=1"},
		// keeps real params, sorts them
		"https://example.com/foo?b=2&a=1": {true, "https://example.com/foo?a=1&b=2"},
		// IDN → punycode
		"https://münchen.de/page": {true, "https://xn--mnchen-3ya.de/page"},
		// scheme-less input
		"github.com/octocat": {true, "https://github.com/octocat"},
		// negatives
		"not a url":            {false, ""},
		"javascript:alert(1)":  {false, ""},
		"":                     {false, ""},
		"mailto:x@example.com": {false, ""},
	}
	for in, w := range cases {
		var res *EntityMatchOutput
		var err error
		if in == "" {
			_, err = EntityMatch(context.Background(), map[string]any{
				"mode": "url_canonicalize",
				"url":  in,
			})
			if err == nil {
				t.Errorf("%q: expected error for empty input", in)
			}
			continue
		}
		res, err = EntityMatch(context.Background(), map[string]any{
			"mode": "url_canonicalize",
			"url":  in,
		})
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if res.URLValid != w.valid {
			t.Errorf("%q: valid=%v, want %v (canonical=%q)", in, res.URLValid, w.valid, res.URLCanonical)
			continue
		}
		if !w.valid {
			continue
		}
		if res.URLCanonical != w.canonical {
			t.Errorf("%q → %q, want %q", in, res.URLCanonical, w.canonical)
		}
	}
}
