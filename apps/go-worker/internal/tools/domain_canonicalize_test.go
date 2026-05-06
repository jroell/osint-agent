package tools

import (
	"context"
	"strings"
	"testing"
)

// TestEntityMatch_DomainCanonicalize_ApexCollapseQuantitative is the
// proof-of-improvement test for iteration 20.
//
// The defect: many tools emit hostnames (whois, dns_lookup, asn,
// http_probe, ssl_cert_chain_inspect, port_scan, censys, shodan,
// subfinder, ct_brand_watch, takeover, securitytrails, urlscan,
// reverse_dns, well_known_recon). To answer "is mail.example.com the
// same organization as www.example.com?" they need the registered
// domain (eTLD+1, IANA Public Suffix List). Today there's no such
// primitive, so dedup-by-organization is effectively impossible.
//
// The fix: domain_canonicalize mode that uses x/net/publicsuffix to
// extract the apex (eTLD+1). Strips schemes, paths, ports, "*."
// wildcards, leading "@", trailing dots; lowercases; converts IDN to
// punycode.
//
// Quantitative metric: dedup count on a curated fixture where 6
// unique organizations (apex domains) are written in 24 surface forms
// across subdomains, schemes, IDN, wildcards, etc.
func TestEntityMatch_DomainCanonicalize_ApexCollapseQuantitative(t *testing.T) {
	groups := [][]string{
		// 1. example.com — many subdomains and surface forms
		{
			"example.com",
			"www.example.com",
			"mail.example.com",
			"blog.example.com",
			"api.example.com",
			"https://www.example.com/some/path",
			"EXAMPLE.com",
			"*.example.com",
			"example.com.",
		},
		// 2. example.co.uk (multi-part eTLD)
		{
			"example.co.uk",
			"www.example.co.uk",
			"mail.example.co.uk",
			"shop.example.co.uk",
		},
		// 3. github.com
		{
			"github.com",
			"api.github.com",
			"raw.githubusercontent.com",  // DIFFERENT registered domain — should NOT collapse
			"https://github.com/org/repo",
			"@github.com",
		},
		// 4. IDN domain — Unicode vs punycode
		{
			"münchen.de",
			"www.münchen.de",
			"xn--mnchen-3ya.de",
		},
		// 5. anthropic.com
		{
			"anthropic.com",
			"www.anthropic.com",
			"docs.anthropic.com",
		},
		// 6. nytimes.com (with port + URL forms)
		{
			"nytimes.com:443",
			"https://www.nytimes.com/section/world",
			"nytimes.com",
		},
	}

	// Note: group 3 has 5 entries but 2 distinct apexes
	// (github.com + githubusercontent.com). So we expect 7 unique apex
	// keys total, NOT 6. Verifying this distinction is part of what the
	// test pins (private/CDN domains MUST NOT silently merge).
	expectedApexes := 7

	allDomains := []string{}
	for _, g := range groups {
		allDomains = append(allDomains, g...)
	}

	// LEGACY: lowercase + strip-www-only — the most common ad-hoc dedup
	// across the catalog. Stripping www is a real partial fix that some
	// callers do, but it can't collapse "mail.example.com" → "example.com"
	// without the PSL.
	legacy := map[string]bool{}
	for _, d := range allDomains {
		k := strings.ToLower(strings.TrimSpace(d))
		k = strings.TrimPrefix(k, "*.")
		k = strings.TrimPrefix(k, "@")
		// Strip URL prefix if present.
		if i := strings.Index(k, "://"); i >= 0 {
			rest := k[i+3:]
			if j := strings.IndexAny(rest, "/?#"); j >= 0 {
				rest = rest[:j]
			}
			k = rest
		}
		// Strip path if any.
		if i := strings.IndexAny(k, "/?#"); i >= 0 {
			k = k[:i]
		}
		k = strings.TrimSuffix(k, ".")
		k = strings.TrimPrefix(k, "www.")
		legacy[k] = true
	}

	canonical := map[string]bool{}
	subdomainsPerApex := map[string]map[string]bool{}
	failedToParse := []string{}

	for _, d := range allDomains {
		res, err := EntityMatch(context.Background(), map[string]any{
			"mode":   "domain_canonicalize",
			"domain": d,
		})
		if err != nil {
			t.Fatalf("EntityMatch(%q): %v", d, err)
		}
		if !res.DomainValid {
			failedToParse = append(failedToParse, d)
			continue
		}
		canonical[res.DomainApex] = true
		if subdomainsPerApex[res.DomainApex] == nil {
			subdomainsPerApex[res.DomainApex] = map[string]bool{}
		}
		subdomainsPerApex[res.DomainApex][res.DomainSubdomain] = true
	}

	t.Logf("Domain canonicalization apex-collapse on %d surface forms (%d true organizations):",
		len(allDomains), expectedApexes)
	t.Logf("  legacy (lowercase + www-strip):    %d distinct", len(legacy))
	t.Logf("  new    (entity_match apex):        %d distinct", len(canonical))
	dedupRate := 1.0 - float64(len(canonical))/float64(len(legacy))
	t.Logf("  dedup-rate improvement:            %.1f%% reduction in apparent organization count", dedupRate*100)
	for apex, subs := range subdomainsPerApex {
		subList := []string{}
		for s := range subs {
			if s == "" {
				subList = append(subList, "(apex)")
			} else {
				subList = append(subList, s)
			}
		}
		t.Logf("    %s ← %d subdomain(s): %v", apex, len(subs), subList)
	}

	if len(failedToParse) > 0 {
		t.Errorf("failed to parse: %v", failedToParse)
	}
	if len(canonical) != expectedApexes {
		t.Errorf("got %d apex keys, want exactly %d", len(canonical), expectedApexes)
	}
	if len(canonical) >= len(legacy) {
		t.Errorf("canonicalization produced no improvement over legacy (%d ≥ %d)",
			len(canonical), len(legacy))
	}
	if len(legacy) < 2*len(canonical) {
		t.Errorf("legacy distinct count %d < 2× canonical (%d) — fixture isn't multi-form enough",
			len(legacy), len(canonical))
	}
}

// TestEntityMatch_DomainCanonicalize_RulesPinning pins individual
// canonicalization rules and edge cases.
func TestEntityMatch_DomainCanonicalize_RulesPinning(t *testing.T) {
	type want struct {
		valid     bool
		apex      string
		subdomain string
		suffix    string
		isApex    bool
		icann     bool
	}
	cases := map[string]want{
		// basic
		"example.com":                {true, "example.com", "", "com", true, true},
		"www.example.com":            {true, "example.com", "www", "com", false, true},
		"deeply.nested.example.com":  {true, "example.com", "deeply.nested", "com", false, true},
		// multi-part eTLD
		"example.co.uk":              {true, "example.co.uk", "", "co.uk", true, true},
		"www.example.co.uk":          {true, "example.co.uk", "www", "co.uk", false, true},
		// private suffix (PSL "PRIVATE" section)
		"someuser.blogspot.com":      {true, "someuser.blogspot.com", "", "blogspot.com", true, false},
		"someproj.github.io":         {true, "someproj.github.io", "", "github.io", true, false},
		// URL-shaped input
		"https://api.example.com/foo": {true, "example.com", "api", "com", false, true},
		// port + wildcard
		"*.example.com":              {true, "example.com", "", "com", true, true},
		"example.com:443":            {true, "example.com", "", "com", true, true},
		// IDN
		"münchen.de":                 {true, "xn--mnchen-3ya.de", "", "de", true, true},
		"www.münchen.de":             {true, "xn--mnchen-3ya.de", "www", "de", false, true},
		// trailing dot
		"example.com.":               {true, "example.com", "", "com", true, true},
		// leading @
		"@example.com":               {true, "example.com", "", "com", true, true},
		// negatives
		"":                           {false, "", "", "", false, false},
		"localhost":                  {false, "", "", "", false, false},
		"com":                        {false, "", "", "", false, false}, // public suffix only
		"not a domain":               {false, "", "", "", false, false},
	}
	for in, w := range cases {
		var res *EntityMatchOutput
		var err error
		if in == "" {
			_, err = EntityMatch(context.Background(), map[string]any{
				"mode":   "domain_canonicalize",
				"domain": in,
			})
			if err == nil {
				t.Errorf("%q: expected error for empty input", in)
			}
			continue
		}
		res, err = EntityMatch(context.Background(), map[string]any{
			"mode":   "domain_canonicalize",
			"domain": in,
		})
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if res.DomainValid != w.valid {
			t.Errorf("%q: valid=%v, want %v (apex=%q)", in, res.DomainValid, w.valid, res.DomainApex)
			continue
		}
		if !w.valid {
			continue
		}
		if res.DomainApex != w.apex {
			t.Errorf("%q: apex=%q, want %q", in, res.DomainApex, w.apex)
		}
		if res.DomainSubdomain != w.subdomain {
			t.Errorf("%q: subdomain=%q, want %q", in, res.DomainSubdomain, w.subdomain)
		}
		if res.DomainPublicSuffix != w.suffix {
			t.Errorf("%q: suffix=%q, want %q", in, res.DomainPublicSuffix, w.suffix)
		}
		if res.DomainIsApex != w.isApex {
			t.Errorf("%q: isApex=%v, want %v", in, res.DomainIsApex, w.isApex)
		}
		if res.DomainICANN != w.icann {
			t.Errorf("%q: icann=%v, want %v", in, res.DomainICANN, w.icann)
		}
	}
}
