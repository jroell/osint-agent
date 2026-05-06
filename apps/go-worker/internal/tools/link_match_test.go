package tools

import (
	"strings"
	"testing"
)

// legacyLinkMatch reproduces the pre-fix behaviour (raw ID equality +
// case-fold name compare) so the test can numerically compare.
func legacyLinkMatch(idA, nameA, idB, nameB string) bool {
	if idA != "" && idA == idB {
		return true
	}
	if nameA == "" || nameB == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(nameA), strings.TrimSpace(nameB))
}

// TestLinkMatch_IDAndOrgFormVariantsQuantitative is the proof-of-improvement
// test for iteration 5 of the /loop "make osint-agent quantitatively
// better" task.
//
// The defect: linkMatch — the predicate that decides whether two
// graph neighbors refer to the same employer/school/board for the
// connecting-the-dots `entity_link_finder` tool — failed silently on:
//
//  1. ID-form variants. Real upstreams emit "http://diffbot.com/entity/E123",
//     "https://diffbot.com/entity/E123", "E123", "diffbot.com/entity/E123" —
//     all referring to the same entity.
//  2. Corporate-suffix mismatches. "Vurvey Labs, Inc." vs "Vurvey Labs"
//     vs "VURVEY" all refer to the same company.
//  3. Punctuation/spacing/diacritic mismatches. "L'Oréal SA" vs "L'Oreal".
//
// Each of these silently dropped a real shared-employer / shared-school /
// shared-board connection, hurting the marquee tool's recall.
//
// The fix: normalizeIDForMatch (strip KG URI prefixes, take last segment)
// + normalizeOrgNameForMatch (Unicode fold + corp-suffix strip +
// punctuation strip + whitespace collapse).
//
// Quantitative metric: % of same-entity pairs in a representative
// fixture correctly matched, before vs after.
func TestLinkMatch_IDAndOrgFormVariantsQuantitative(t *testing.T) {
	type pair struct{ idA, nameA, idB, nameB string }
	// All pairs SHOULD match (they refer to the same real-world entity).
	matchPairs := []pair{
		// ID-form variants
		{"http://diffbot.com/entity/E123", "", "E123", ""},
		{"https://diffbot.com/entity/E123", "", "E123", ""},
		{"diffbot.com/entity/E123", "", "E123", ""},
		{"https://www.wikidata.org/entity/Q42", "", "Q42", ""},
		{"http://kg.diffbot.com/kg/E_xyz", "", "E_xyz", ""},
		// Corporate-suffix mismatches
		{"", "Vurvey Labs, Inc.", "", "Vurvey Labs"},
		{"", "Vurvey Labs Inc", "", "VURVEY"},
		{"", "Apple Inc.", "", "Apple"},
		{"", "Alphabet Inc", "", "Alphabet"},
		{"", "Anthropic, PBC", "", "Anthropic"},
		{"", "OpenAI, Inc", "", "OpenAI"},
		{"", "Google LLC", "", "Google"},
		{"", "Microsoft Corporation", "", "Microsoft"},
		{"", "Berkshire Hathaway Inc.", "", "Berkshire Hathaway"},
		// Diacritics + corp suffixes
		{"", "L'Oréal SA", "", "L'Oreal"},
		{"", "Société Générale", "", "Societe Generale"},
		// Spacing / punctuation
		{"", "Vurvey  Labs ", "", "vurvey labs"},
		{"", "AT&T Inc.", "", "AT&T"},
	}
	// Negative controls: pairs that should NOT match in either version.
	nonMatchPairs := []pair{
		{"", "Apple", "", "Microsoft"},
		{"E123", "", "E456", ""},
		{"", "Vurvey", "", "Verily"},
	}

	beforeMatch := 0
	afterMatch := 0
	for _, p := range matchPairs {
		if legacyLinkMatch(p.idA, p.nameA, p.idB, p.nameB) {
			beforeMatch++
		}
		if linkMatch(p.idA, p.nameA, p.idB, p.nameB) {
			afterMatch++
		}
	}

	beforeNonRegression := 0
	afterNonRegression := 0
	for _, p := range nonMatchPairs {
		if !legacyLinkMatch(p.idA, p.nameA, p.idB, p.nameB) {
			beforeNonRegression++
		}
		if !linkMatch(p.idA, p.nameA, p.idB, p.nameB) {
			afterNonRegression++
		}
	}

	beforePct := float64(beforeMatch) / float64(len(matchPairs)) * 100
	afterPct := float64(afterMatch) / float64(len(matchPairs)) * 100
	delta := afterPct - beforePct

	t.Logf("linkMatch recall on %d same-entity pairs:", len(matchPairs))
	t.Logf("  legacy linkMatch (raw eq + case-fold): %d/%d = %.1f%%", beforeMatch, len(matchPairs), beforePct)
	t.Logf("  new linkMatch    (id+name normalize):  %d/%d = %.1f%%", afterMatch, len(matchPairs), afterPct)
	t.Logf("  improvement:                           +%.1f percentage points", delta)
	t.Logf("Non-regression on %d distinct-entity pairs (must NOT match):", len(nonMatchPairs))
	t.Logf("  legacy: %d/%d correctly rejected", beforeNonRegression, len(nonMatchPairs))
	t.Logf("  new:    %d/%d correctly rejected", afterNonRegression, len(nonMatchPairs))

	// Per-pair diagnostic when after-rate is below threshold so failures
	// are actionable.
	if afterPct < 95 {
		for _, p := range matchPairs {
			before := legacyLinkMatch(p.idA, p.nameA, p.idB, p.nameB)
			after := linkMatch(p.idA, p.nameA, p.idB, p.nameB)
			t.Logf("    before=%v after=%v  | %q/%q  vs  %q/%q",
				mark2(before), mark2(after),
				p.idA, p.nameA, p.idB, p.nameB)
		}
	}

	// Hard claims:
	if afterPct < 95 {
		t.Errorf("new linkMatch recall %.1f%% — expected ≥95%%", afterPct)
	}
	if delta < 50 {
		t.Errorf("improvement only +%.1fpp — expected ≥+50pp", delta)
	}
	if afterNonRegression < len(nonMatchPairs) {
		t.Errorf("non-regression FAILED: %d/%d distinct-entity pairs correctly rejected",
			afterNonRegression, len(nonMatchPairs))
	}
}

// TestNormalizeIDForMatch_PinnedShapes verifies the ID extractor on
// each prefix shape we expect to see from upstreams.
func TestNormalizeIDForMatch_PinnedShapes(t *testing.T) {
	cases := map[string]string{
		"http://diffbot.com/entity/E123":      "e123",
		"https://diffbot.com/entity/E123":     "e123",
		"E123":                                "e123",
		"diffbot.com/entity/E123":             "e123",
		"https://www.wikidata.org/entity/Q42": "q42",
		"":                                    "",
		"  E123  ":                            "e123",
	}
	for in, want := range cases {
		got := normalizeIDForMatch(in)
		if got != want {
			t.Errorf("normalizeIDForMatch(%q) = %q; want %q", in, got, want)
		}
	}
}

func mark2(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}
