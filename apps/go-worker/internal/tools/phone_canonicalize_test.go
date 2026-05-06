package tools

import (
	"context"
	"strings"
	"testing"
)

// TestEntityMatch_PhoneCanonicalizeDedup is the proof-of-improvement
// test for iteration 15.
//
// The defect (parallel to iter-14's email work): tools that emit phone
// numbers (people_data_labs, truepeoplesearch, hunter, holehe,
// hudsonrock, numverify, panel_entity_resolution, person_aggregate)
// produce phones in many surface forms — "(415) 555-2671",
// "+1-415-555-2671", "4155552671", "1.415.555.2671", "tel:+14155552671",
// "+1 415 555 2671 ext 99". person_aggregate's literal-string dedup
// treats every variant as a separate finding, breaking the cross-tool
// linkage the social-graph layer needs.
//
// The fix: a new phone_canonicalize mode on entity_match that:
//   - parses extensions (ext / x / ;ext= / # / p)
//   - strips formatting (parens, dashes, dots, slashes, spaces)
//   - handles tel: + percent-decoding
//   - 00-prefix → +
//   - NANP heuristic: 10 digits → +1; 11 digits starting with 1 → +
//   - greedy ITU-T E.164 country-code match (1/2/3-digit prefixes)
//   - validates digit count (8-15)
//   - emits E164 as the strongest dedup primary key
//
// Quantitative metric: dedup count on a curated fixture where 6
// unique real-world numbers are written in 24 surface forms.
// Before: ~22-24 distinct strings. After: exactly 6.
func TestEntityMatch_PhoneCanonicalizeDedup(t *testing.T) {
	groups := [][]string{
		// 1. US number, 8 surface forms
		{
			"(415) 555-2671",
			"415-555-2671",
			"415.555.2671",
			"4155552671",
			"+1 415 555 2671",
			"+1-415-555-2671",
			"1-415-555-2671",
			"+14155552671",
		},
		// 2. UK number with various international prefixes
		{
			"+44 20 7946 0958",
			"0044 20 7946 0958",
			"+442079460958",
			"tel:+442079460958",
		},
		// 3. NYC number with extension (extension preserved separately,
		//    must NOT split the dedup key)
		{
			"(212) 555-1234 ext 567",
			"+1 212-555-1234 x567",
			"+12125551234;ext=567",
			"212.555.1234 #567",
		},
		// 4. German number — three forms
		{
			"+49 30 12345678",
			"+493012345678",
			"0049 30 12345678",
		},
		// 5. NANP toll-free (the toll-free flag is informational; same number)
		{
			"1-800-555-0199",
			"+1 800 555 0199",
			"(800) 555-0199",
		},
		// 6. India mobile
		{
			"+91 98765 43210",
			"+919876543210",
			"00919876543210",
		},
	}

	expectedKeys := len(groups) // 6

	allPhones := []string{}
	for _, g := range groups {
		allPhones = append(allPhones, g...)
	}

	// LEGACY: lowercase + trim + collapse multiple spaces (the most
	// generous baseline; even this isn't enough).
	legacy := map[string]bool{}
	for _, p := range allPhones {
		k := strings.ToLower(strings.TrimSpace(p))
		k = strings.Join(strings.Fields(k), " ")
		legacy[k] = true
	}

	// NEW: route through entity_match.phone_canonicalize.
	canonical := map[string]bool{}
	groupHits := make([]map[string]bool, len(groups))
	for i := range groupHits {
		groupHits[i] = map[string]bool{}
	}
	failedToParse := []string{}

	for gi, g := range groups {
		for _, p := range g {
			res, err := EntityMatch(context.Background(), map[string]any{
				"mode":  "phone_canonicalize",
				"phone": p,
			})
			if err != nil {
				t.Fatalf("EntityMatch(%q): %v", p, err)
			}
			if !res.PhoneValid {
				failedToParse = append(failedToParse, p)
				continue
			}
			canonical[res.PhoneE164] = true
			groupHits[gi][res.PhoneE164] = true
		}
	}

	t.Logf("Phone canonicalization dedup on %d surface forms (%d true numbers):",
		len(allPhones), expectedKeys)
	t.Logf("  legacy   (ToLower+TrimSpace+squash):  %d distinct", len(legacy))
	t.Logf("  new      (entity_match canonicalize): %d distinct", len(canonical))
	dedupRate := 1.0 - float64(len(canonical))/float64(len(legacy))
	t.Logf("  dedup-rate improvement:               %.1f%% reduction in apparent identity count", dedupRate*100)

	for gi, hits := range groupHits {
		if len(hits) > 1 {
			collapsed := []string{}
			for k := range hits {
				collapsed = append(collapsed, k)
			}
			t.Errorf("group %d (e.g. %q) split into %d distinct E164 keys: %v",
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
	// Hard floor: legacy must underperform by ≥3× (this fixture is
	// designed to demonstrate the gap).
	if len(legacy) < 3*len(canonical) {
		t.Errorf("legacy distinct count %d < 3× canonical (%d) — fixture isn't multi-form enough",
			len(legacy), len(canonical))
	}
}

// TestEntityMatch_PhoneCanonicalize_RegionAndExtension pins the
// auxiliary outputs (region, country-code, extension capture, toll-free
// flag) on representative cases.
func TestEntityMatch_PhoneCanonicalize_RegionAndExtension(t *testing.T) {
	type want struct {
		valid    bool
		e164     string
		cc       string
		region   string
		ext      string
		tollFree bool
	}
	cases := map[string]want{
		"+14155552671":         {true, "+14155552671", "1", "US", "", false},
		"(415) 555-2671":       {true, "+14155552671", "1", "US", "", false},
		"+44 20 7946 0958":     {true, "+442079460958", "44", "GB", "", false},
		"+49 30 12345678":      {true, "+493012345678", "49", "DE", "", false},
		"+91 98765 43210":      {true, "+919876543210", "91", "IN", "", false},
		"+1 212-555-1234 x567": {true, "+12125551234", "1", "US", "567", false},
		"1-800-555-0199":       {true, "+18005550199", "1", "US", "", true},
		"+9712 555 1234":       {true, "+97125551234", "971", "AE", "", false},
		"tel:+442079460958":    {true, "+442079460958", "44", "GB", "", false},
		// negative controls
		"555-1234": {false, "", "", "", "", false}, // no area code, undeterminable
		"abc":      {false, "", "", "", "", false},
		"":         {false, "", "", "", "", false},
	}
	for in, w := range cases {
		var res *EntityMatchOutput
		var err error
		if in == "" {
			res = &EntityMatchOutput{}
			_, err = EntityMatch(context.Background(), map[string]any{
				"mode":  "phone_canonicalize",
				"phone": in,
			})
			if err == nil {
				t.Errorf("%q: expected error for empty input", in)
			}
			continue
		}
		res, err = EntityMatch(context.Background(), map[string]any{
			"mode":  "phone_canonicalize",
			"phone": in,
		})
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if res.PhoneValid != w.valid {
			t.Errorf("%q: valid=%v, want %v (e164=%q)", in, res.PhoneValid, w.valid, res.PhoneE164)
			continue
		}
		if !w.valid {
			continue
		}
		if res.PhoneE164 != w.e164 {
			t.Errorf("%q: e164=%q, want %q", in, res.PhoneE164, w.e164)
		}
		if res.PhoneCountryCode != w.cc {
			t.Errorf("%q: cc=%q, want %q", in, res.PhoneCountryCode, w.cc)
		}
		if res.PhoneRegion != w.region {
			t.Errorf("%q: region=%q, want %q", in, res.PhoneRegion, w.region)
		}
		if res.PhoneExtension != w.ext {
			t.Errorf("%q: ext=%q, want %q", in, res.PhoneExtension, w.ext)
		}
		if res.PhoneTollFree != w.tollFree {
			t.Errorf("%q: tollFree=%v, want %v", in, res.PhoneTollFree, w.tollFree)
		}
	}
}
