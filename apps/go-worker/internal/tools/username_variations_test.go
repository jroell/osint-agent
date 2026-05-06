package tools

import (
	"context"
	"strings"
	"testing"
)

// legacyUsernameVariations reproduces the pre-iter-12 logic so the test
// can numerically compare. Mirrors the original code path but with
// `string(first[0])` byte-indexing and no Unicode fold / hyphen expansion.
func legacyUsernameVariations(rawName string) []string {
	canonical := stripHonorifics(strings.ToLower(strings.TrimSpace(rawName)))
	tokens := strings.Fields(canonical)
	filtered := []string{}
	for _, t := range tokens {
		ts := strings.Trim(t, ".,")
		if ts == "jr" || ts == "sr" || ts == "ii" || ts == "iii" || ts == "iv" {
			continue
		}
		filtered = append(filtered, ts)
	}
	if len(filtered) == 0 {
		return nil
	}
	first := filtered[0]
	last := first
	if len(filtered) > 1 {
		last = filtered[len(filtered)-1]
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		s = strings.ToLower(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(first) > 0 && len(last) > 0 {
		add(first + last)
		add(string(first[0]) + last)
		add(first + "." + last)
		add(first + "_" + last)
		add(first + "-" + last)
		add(string(first[0]) + "." + last)
		add(string(first[0]) + "_" + last)
		add(first + string(last[0]))
		add(last + first)
		add(last + "." + first)
		add(last + string(first[0]))
		add(string(first[0]) + string(last[0]))
		add(first)
		add(last)
		for _, suf := range []string{"1", "01", "123", "99", "_real"} {
			add(first + last + suf)
			add(string(first[0]) + last + suf)
			add(first + suf)
		}
	}
	return out
}

// TestUsernameVariations_InternationalRecallQuantitative is the proof-of-improvement
// test for iteration 12.
//
// The defect: username_variations had three independent bugs:
//
//  1. `string(first[0])` indexed BYTES, not runes. For names starting
//     with a multi-byte UTF-8 character (Ł, Ç, Ñ, …) the "first letter"
//     was a single byte that's invalid as a string when re-encoded —
//     producing username candidates with invalid UTF-8 fragments
//     mixed in.
//  2. No Unicode/diacritic fold. "François Côté" produced candidates
//     like "francoiscote" only by coincidence on some platforms;
//     others got "françoiscôté" which is invalid on every major
//     platform's username field (GitHub/Twitter/Instagram all enforce
//     ASCII + dash/underscore). Real-world OSINT lookups against
//     Maigret/Sherlock/etc. silently missed real matches.
//  3. Hyphenated surnames ("Garcia-Lopez", "Müller-Wolfgang") were
//     treated as a single token. The actual handle on Twitter etc.
//     might be `jgarcia` OR `jlopez` OR `jgarcialopez` — three
//     independent registrations a name-only lookup needs to enumerate.
//
// The fix: apply normalizeForMatch (iter-2) before tokenizing, use
// rune-based first-letter extraction, expand hyphenated last names
// into all variants.
//
// Quantitative metric: % of curated "expected handle" candidates
// that appear in the generated set, averaged across 5 representative
// international/hyphenated names.
func TestUsernameVariations_InternationalRecallQuantitative(t *testing.T) {
	cases := []struct {
		name  string
		input string
		// Subset of expected candidates a determined OSINT analyst would
		// want generated. The set is REALISTIC, not exhaustive.
		expected []string
	}{
		{
			name:  "Polish slashed-l (Łukasz Pawełczak)",
			input: "Łukasz Pawełczak",
			expected: []string{
				"lukaszpawelczak",
				"lpawelczak",
				"lukasz.pawelczak",
				"lukasz_pawelczak",
				"l.pawelczak",
				"pawelczak.lukasz",
				"lukasz",
				"pawelczak",
			},
		},
		{
			name:  "French diacritics (François Côté)",
			input: "François Côté",
			expected: []string{
				"francoiscote",
				"fcote",
				"francois.cote",
				"francois_cote",
				"f.cote",
				"francois",
				"cote",
			},
		},
		{
			name:  "Spanish hyphenated surname (Maria Garcia-Lopez)",
			input: "Maria Garcia-Lopez",
			expected: []string{
				"mariagarcia-lopez", // verbatim hyphen form
				"mariagarcialopez",  // concat form
				"mgarcia",           // first half only
				"mlopez",            // second half only
				"maria.garcia",
				"maria.lopez",
				"garcialopez",
				"garcia",
				"lopez",
			},
		},
		{
			name:  "German umlaut (Björn Müller)",
			input: "Björn Müller",
			expected: []string{
				"bjornmuller",
				"bmuller",
				"bjorn.muller",
				"bjorn_muller",
				"b.muller",
				"muller.bjorn",
				"bjorn",
				"muller",
			},
		},
		{
			name:  "Vietnamese tones (Nguyễn Phú Trọng)",
			input: "Nguyễn Phú Trọng",
			expected: []string{
				"nguyentrong",
				"ntrong",
				"nguyen.trong",
				"nguyen_trong",
				"n.trong",
				"nguyenptrong", // first + middle initial + last
				"nptrong",      // first init + middle init + last
				"nguyen",
				"trong",
			},
		},
	}

	totalExpected := 0
	totalBeforeHits := 0
	totalAfterHits := 0

	for _, c := range cases {
		legacy := legacyUsernameVariations(c.input)
		legacySet := map[string]bool{}
		for _, u := range legacy {
			legacySet[u] = true
		}

		newOut, err := EntityMatch(context.Background(), map[string]any{
			"mode":      "username_variations",
			"full_name": c.input,
		})
		if err != nil {
			t.Fatalf("EntityMatch %q: %v", c.input, err)
		}
		newSet := map[string]bool{}
		for _, u := range newOut.Usernames {
			newSet[u] = true
		}

		caseBefore := 0
		caseAfter := 0
		missingAfter := []string{}
		for _, want := range c.expected {
			totalExpected++
			if legacySet[want] {
				caseBefore++
				totalBeforeHits++
			}
			if newSet[want] {
				caseAfter++
				totalAfterHits++
			} else {
				missingAfter = append(missingAfter, want)
			}
		}
		t.Logf("  %-50s before=%d/%d  after=%d/%d  | missing after: %v",
			c.name, caseBefore, len(c.expected), caseAfter, len(c.expected), missingAfter)
	}

	beforePct := float64(totalBeforeHits) / float64(totalExpected) * 100
	afterPct := float64(totalAfterHits) / float64(totalExpected) * 100
	delta := afterPct - beforePct

	t.Logf("")
	t.Logf("Username-variations recall on %d expected candidates across %d names:", totalExpected, len(cases))
	t.Logf("  legacy  (no fold, byte-init):      %d/%d = %.1f%%", totalBeforeHits, totalExpected, beforePct)
	t.Logf("  new     (fold + rune + hyphen):    %d/%d = %.1f%%", totalAfterHits, totalExpected, afterPct)
	t.Logf("  improvement:                       +%.1f percentage points", delta)

	if afterPct < 90 {
		t.Errorf("new recall %.1f%% — expected ≥90%%", afterPct)
	}
	if delta < 50 {
		t.Errorf("improvement only +%.1fpp — expected ≥+50pp", delta)
	}
	if beforePct >= 50 {
		t.Errorf("legacy recall %.1f%% — fixture isn't international/hyphenated enough", beforePct)
	}
}

// TestUsernameVariations_NoInvalidUTF8 pins that no generated handle
// contains a multi-byte UTF-8 character (which would be invalid on
// any major platform's username field). This catches the byte-indexing
// regression specifically.
func TestUsernameVariations_NoInvalidUTF8(t *testing.T) {
	for _, name := range []string{
		"Łukasz Pawełczak",
		"François Côté",
		"Müller",
		"Erdoğan Nuri",
		"José García",
	} {
		out, err := EntityMatch(context.Background(), map[string]any{
			"mode":      "username_variations",
			"full_name": name,
		})
		if err != nil {
			t.Fatalf("EntityMatch %q: %v", name, err)
		}
		for _, u := range out.Usernames {
			for _, r := range u {
				if r > 0x7E && r != '_' && r != '-' {
					t.Errorf("name %q produced username %q with non-ASCII rune %U",
						name, u, r)
					break
				}
			}
		}
	}
}
