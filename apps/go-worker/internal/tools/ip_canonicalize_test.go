package tools

import (
	"context"
	"strings"
	"testing"
)

// TestEntityMatch_IPCanonicalizeDedup is the proof-of-improvement
// test for iteration 21.
//
// The defect: tools that emit IPs (shodan, censys, ip_intel_lookup,
// asn, port_scan, urlscan, dns_lookup, ssl_cert_chain_inspect,
// hackertarget_recon, alienvault_otx, http_probe, reverse_dns) produce
// the same address in many surface forms — leading-zero octets,
// IPv6 zero-compression variants, IPv4-in-IPv6 wrappers, bracket
// wrappers, port suffixes. person_aggregate / panel_entity_resolution
// dedup by literal string, so the same host appears as 3-5 distinct
// "evidence" rows.
//
// The fix: a new ip_canonicalize mode on entity_match that runs
// inputs through Go's net/netip (strict canonicalization with
// IPv4-in-IPv6 unwrapping, IPv6 zero-compression to RFC 5952 form,
// case folding) plus a tolerance pass for surface forms netip rejects
// (leading-zero IPv4 octets, brackets, port/CIDR suffixes).
//
// Quantitative metric: dedup count on a curated fixture where 5
// unique addresses are written in 22 surface forms.
func TestEntityMatch_IPCanonicalizeDedup(t *testing.T) {
	groups := [][]string{
		// 1. IPv4 — leading-zero, port, CIDR, IPv4-in-IPv6 wrappers
		{
			"192.168.1.1",
			"192.168.001.001",
			"192.168.1.1:8080",
			"192.168.1.1/24",
			"::ffff:192.168.1.1",
			"::ffff:c0a8:0101",
		},
		// 2. IPv6 — zero-compression variants, case, brackets
		{
			"2001:0db8:0000:0000:0000:0000:0000:0001",
			"2001:db8::1",
			"2001:DB8::1",
			"[2001:db8::1]",
			"[2001:db8::1]:443",
		},
		// 3. Public IPv4 — multiple punctuation forms
		{
			"8.8.8.8",
			"8.8.8.8:53",
			"008.008.008.008",
		},
		// 4. IPv6 loopback
		{
			"::1",
			"0000:0000:0000:0000:0000:0000:0000:0001",
			"[::1]:8080",
		},
		// 5. Public IPv6
		{
			"2606:4700:4700::1111",   // Cloudflare DNS
			"2606:4700:4700:0:0:0:0:1111",
			"[2606:4700:4700::1111]",
		},
	}

	expectedKeys := len(groups) // 5

	allIPs := []string{}
	for _, g := range groups {
		allIPs = append(allIPs, g...)
	}

	// LEGACY: lowercase + trim — most generous string baseline.
	legacy := map[string]bool{}
	for _, ip := range allIPs {
		legacy[strings.ToLower(strings.TrimSpace(ip))] = true
	}

	canonical := map[string]bool{}
	groupHits := make([]map[string]bool, len(groups))
	for i := range groupHits {
		groupHits[i] = map[string]bool{}
	}
	failedToParse := []string{}

	for gi, g := range groups {
		for _, ip := range g {
			res, err := EntityMatch(context.Background(), map[string]any{
				"mode": "ip_canonicalize",
				"ip":   ip,
			})
			if err != nil {
				t.Fatalf("EntityMatch(%q): %v", ip, err)
			}
			if !res.IPValid {
				failedToParse = append(failedToParse, ip)
				continue
			}
			canonical[res.IPCanonical] = true
			groupHits[gi][res.IPCanonical] = true
		}
	}

	t.Logf("IP canonicalization dedup on %d surface forms (%d true addresses):",
		len(allIPs), expectedKeys)
	t.Logf("  legacy (ToLower+TrimSpace only):     %d distinct", len(legacy))
	t.Logf("  new    (entity_match canonicalize):  %d distinct", len(canonical))
	dedupRate := 1.0 - float64(len(canonical))/float64(len(legacy))
	t.Logf("  dedup-rate improvement:              %.1f%% reduction in apparent host count", dedupRate*100)

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

// TestEntityMatch_IPCanonicalize_RulesPinning pins individual rules
// and address-class detection.
func TestEntityMatch_IPCanonicalize_RulesPinning(t *testing.T) {
	type want struct {
		valid     bool
		canonical string
		version   int
		class     string
	}
	cases := map[string]want{
		// IPv4 basics + class detection
		"192.168.1.1":    {true, "192.168.1.1", 4, "private"},
		"10.0.0.1":       {true, "10.0.0.1", 4, "private"},
		"172.16.0.1":     {true, "172.16.0.1", 4, "private"},
		"127.0.0.1":      {true, "127.0.0.1", 4, "loopback"},
		"100.64.0.1":     {true, "100.64.0.1", 4, "cgnat"},
		"192.0.2.1":      {true, "192.0.2.1", 4, "documentation"},
		"198.51.100.42":  {true, "198.51.100.42", 4, "documentation"},
		"203.0.113.5":    {true, "203.0.113.5", 4, "documentation"},
		"240.0.0.1":      {true, "240.0.0.1", 4, "reserved"},
		"224.0.0.1":      {true, "224.0.0.1", 4, "multicast"},
		"169.254.1.1":    {true, "169.254.1.1", 4, "link-local"},
		"0.0.0.0":        {true, "0.0.0.0", 4, "unspecified"},
		"8.8.8.8":        {true, "8.8.8.8", 4, "public"},
		"1.1.1.1":        {true, "1.1.1.1", 4, "public"},
		// IPv4 leading-zero tolerance
		"008.008.008.008": {true, "8.8.8.8", 4, "public"},
		// IPv4 with port → port stripped
		"8.8.8.8:53": {true, "8.8.8.8", 4, "public"},
		// IPv4 CIDR → mask captured separately
		"10.0.0.1/24": {true, "10.0.0.1", 4, "private"},
		// IPv6 basics
		"2001:db8::1":           {true, "2001:db8::1", 6, "documentation"},
		"2001:0db8:0000::0001":  {true, "2001:db8::1", 6, "documentation"},
		"::1":                   {true, "::1", 6, "loopback"},
		"::":                    {true, "::", 6, "unspecified"},
		"fe80::1":               {true, "fe80::1", 6, "link-local"},
		"ff02::1":               {true, "ff02::1", 6, "multicast"},
		"fc00::1":               {true, "fc00::1", 6, "private"}, // ULA
		"2606:4700:4700::1111":  {true, "2606:4700:4700::1111", 6, "public"},
		// IPv4-in-IPv6 → unwrapped to IPv4
		"::ffff:192.168.1.1": {true, "192.168.1.1", 4, "private"},
		"::ffff:8.8.8.8":     {true, "8.8.8.8", 4, "public"},
		// Bracket-wrapped + port
		"[2001:db8::1]":      {true, "2001:db8::1", 6, "documentation"},
		"[2001:db8::1]:443":  {true, "2001:db8::1", 6, "documentation"},
		// Negatives
		"":                {false, "", 0, ""},
		"not an ip":       {false, "", 0, ""},
		"1.2.3":           {false, "", 0, ""},
		"999.999.999.999": {false, "", 0, ""},
		"::g":             {false, "", 0, ""},
	}
	for in, w := range cases {
		var res *EntityMatchOutput
		var err error
		if in == "" {
			_, err = EntityMatch(context.Background(), map[string]any{
				"mode": "ip_canonicalize",
				"ip":   in,
			})
			if err == nil {
				t.Errorf("%q: expected error for empty input", in)
			}
			continue
		}
		res, err = EntityMatch(context.Background(), map[string]any{
			"mode": "ip_canonicalize",
			"ip":   in,
		})
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if res.IPValid != w.valid {
			t.Errorf("%q: valid=%v, want %v (canonical=%q)", in, res.IPValid, w.valid, res.IPCanonical)
			continue
		}
		if !w.valid {
			continue
		}
		if res.IPCanonical != w.canonical {
			t.Errorf("%q → %q, want %q", in, res.IPCanonical, w.canonical)
		}
		if res.IPVersion != w.version {
			t.Errorf("%q: version=%d, want %d", in, res.IPVersion, w.version)
		}
		if res.IPClass != w.class {
			t.Errorf("%q: class=%q, want %q", in, res.IPClass, w.class)
		}
	}
}
