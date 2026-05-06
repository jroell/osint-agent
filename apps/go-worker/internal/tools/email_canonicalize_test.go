package tools

import (
	"context"
	"strings"
	"testing"
)

// TestEntityMatch_EmailCanonicalizeDedup is the proof-of-improvement
// test for iteration 14.
//
// The defect: across the OSINT catalog, ~12 tools emit emails
// (github_emails, hibp, holehe, hunter_io, dehashed, intelx,
// mail_correlate, gravatar, keybase, ghunt, hudsonrock_cavalier,
// people_data_labs). person_aggregate currently dedups by literal
// string equality with at most a ToLower+Trim normalization. As a
// result a single mailbox routinely shows up as 3-6 distinct
// "findings" in the merged output, inflating the apparent identity
// surface area, breaking cross-tool linkage, and making downstream
// graph traversal (sherlock + RapidAPI follower-list pivots) emit
// false-distinct edges.
//
// The fix: a new email_canonicalize mode on entity_match that
// applies provider-aware canonicalization (gmail dot-aliasing +
// googlemail-equivalence; plus-tag subaddress stripping for gmail/
// outlook/yahoo/icloud/proton/fastmail; punycode IDN normalization;
// trailing-dot domain stripping; mailto: + percent-decoding).
//
// Quantitative metric: dedup count on a curated fixture where 6 unique
// real-world mailboxes are written in 19 different surface forms.
// Before: ~16-18 distinct strings (the baseline ToLower+TrimSpace
// flow can only collapse pure-case differences). After: exactly 6
// distinct mailbox keys.
func TestEntityMatch_EmailCanonicalizeDedup(t *testing.T) {
	// Each inner slice is one true mailbox; every element is a different
	// string a real-world OSINT tool might emit for that same mailbox.
	groups := [][]string{
		// 1. Gmail with dot-alias variations + plus-tag + googlemail mirror
		//    + display-name wrapping + leading/trailing whitespace.
		{
			"John.Doe@Gmail.com",
			"johndoe@gmail.com",
			"j.o.h.n.d.o.e@gmail.com",
			"JohnDoe+work@gmail.com",
			"johndoe@googlemail.com",
			"  JohnDoe@gmail.com  ",
			"\"John Doe\" <johndoe@gmail.com>",
		},
		// 2. Outlook plus-tag subaddress.
		{
			"alice@outlook.com",
			"Alice@OUTLOOK.com",
			"alice+newsletter@outlook.com",
			"alice+abc@outlook.com",
		},
		// 3. Yahoo plus-tag.
		{
			"bob@yahoo.com",
			"Bob+spam@yahoo.com",
		},
		// 4. mailto: scraped form + percent-encoding (common from href
		//    extraction pipelines).
		{
			"mailto:carol@example.com",
			"carol@example.com",
			"carol%40example.com",
		},
		// 5. IDN domain normalization (punycode <-> Unicode).
		{
			"dave@münchen.de",
			"dave@xn--mnchen-3ya.de",
		},
		// 6. Trailing-dot fully-qualified domain.
		{
			"eve@example.org",
			"eve@example.org.",
		},
	}

	expectedMailboxes := len(groups) // 6

	allEmails := []string{}
	for _, g := range groups {
		allEmails = append(allEmails, g...)
	}

	// LEGACY: lowercase + trimspace dedup (the current person_aggregate path).
	legacy := map[string]bool{}
	for _, e := range allEmails {
		legacy[strings.ToLower(strings.TrimSpace(e))] = true
	}

	// NEW: route through entity_match.email_canonicalize, key on MailboxKey.
	canonical := map[string]bool{}
	groupHits := make([]map[string]bool, len(groups))
	for i := range groupHits {
		groupHits[i] = map[string]bool{}
	}
	failedToParse := []string{}

	for gi, g := range groups {
		for _, e := range g {
			res, err := EntityMatch(context.Background(), map[string]any{
				"mode":  "email_canonicalize",
				"email": e,
			})
			if err != nil {
				t.Fatalf("EntityMatch(%q): %v", e, err)
			}
			if !res.EmailValid {
				failedToParse = append(failedToParse, e)
				continue
			}
			canonical[res.EmailMailboxKey] = true
			groupHits[gi][res.EmailMailboxKey] = true
		}
	}

	t.Logf("Email canonicalization dedup on %d surface forms (%d true mailboxes):",
		len(allEmails), expectedMailboxes)
	t.Logf("  legacy   (ToLower+TrimSpace only):     %d distinct", len(legacy))
	t.Logf("  new      (entity_match canonicalize):  %d distinct", len(canonical))
	dedupRate := 1.0 - float64(len(canonical))/float64(len(legacy))
	t.Logf("  dedup-rate improvement:                %.1f%% reduction in apparent identity count", dedupRate*100)

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

	if len(canonical) != expectedMailboxes {
		t.Errorf("got %d canonical keys, want exactly %d", len(canonical), expectedMailboxes)
	}
	if len(failedToParse) > 0 {
		t.Logf("  unparsable inputs: %v", failedToParse)
	}
	if len(canonical) >= len(legacy) {
		t.Errorf("canonicalization produced no improvement over legacy (%d ≥ %d)",
			len(canonical), len(legacy))
	}
	// Hard floor: the legacy method must underperform by ≥50%.
	minLegacy := len(canonical) * 2
	if len(legacy) < minLegacy {
		t.Errorf("legacy distinct count %d < %d (2× canonical) — fixture isn't multi-form enough to demonstrate the gap",
			len(legacy), minLegacy)
	}
}

// TestEntityMatch_EmailCanonicalize_ProviderTags pins the provider
// classification — important because the plus-tag and dot-alias
// rules are gated on it.
func TestEntityMatch_EmailCanonicalize_ProviderTags(t *testing.T) {
	cases := map[string]string{
		"a@gmail.com":      "gmail",
		"a@googlemail.com": "gmail",
		"a@outlook.com":    "outlook",
		"a@hotmail.com":    "outlook",
		"a@live.com":       "outlook",
		"a@yahoo.com":      "yahoo",
		"a@yahoo.co.uk":    "yahoo",
		"a@icloud.com":     "icloud",
		"a@me.com":         "icloud",
		"a@proton.me":      "proton",
		"a@fastmail.com":   "fastmail",
		"a@batterii.com":   "other",
		"a@university.edu": "other",
	}
	for in, wantProvider := range cases {
		res, err := EntityMatch(context.Background(), map[string]any{
			"mode":  "email_canonicalize",
			"email": in,
		})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if !res.EmailValid {
			t.Errorf("%q parsed as invalid", in)
			continue
		}
		if res.EmailProvider != wantProvider {
			t.Errorf("%q: provider %q, want %q", in, res.EmailProvider, wantProvider)
		}
	}
}

// TestEntityMatch_EmailCanonicalize_RejectMalformed pins that the
// mode safely rejects garbage rather than producing a phantom
// canonical form (which would silently merge unrelated rows).
func TestEntityMatch_EmailCanonicalize_RejectMalformed(t *testing.T) {
	bad := []string{
		"not-an-email",
		"@nolocal.com",
		"nolocal@",
		"two@@signs.com",
		"spaces in local@domain.com",
		"",
		"   ",
	}
	for _, in := range bad {
		res, err := EntityMatch(context.Background(), map[string]any{
			"mode":  "email_canonicalize",
			"email": in,
		})
		if in == "" || in == "   " {
			if err == nil {
				t.Errorf("%q: expected error for empty input, got valid=%v", in, res.EmailValid)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if res.EmailValid {
			t.Errorf("%q: should not parse as valid (got mailbox_key=%q)", in, res.EmailMailboxKey)
		}
	}
}
