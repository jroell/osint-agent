package tools

import (
	"context"
	"strings"
	"testing"
)

// TestNormalizeForMatch_AccentedNamesQuantitative is the proof-of-improvement
// test for iteration 2 of the /loop "make osint-agent quantitatively better"
// task.
//
// The defect: the prior `normalizeForMatch` only stripped non-letter
// characters; it did NOT decompose accented letters or fold Latin-
// extended ligatures. This caused the `entity_match` name-matching
// path to flag "José Reggiani" vs "Jose Reggiani" as a partial match
// rather than identical, missing real same-person matches in any
// OSINT pipeline that compares names across data sources where one
// source preserves diacritics and another strips them.
//
// The fix: NFKD decomposition + combining-mark drop + Latin-extended
// fold (ł→l, ø→o, ß→ss, æ→ae, etc.).
//
// Quantitative metric: percentage of accented-vs-stripped name pairs
// that produce composite_score == 1.0 (verdict "same") after
// normalization. The fixture is 16 representative pairs covering
// French, Spanish, Czech, German, Polish, Vietnamese, Turkish, Arabic
// transliteration, and Old English/Norse extended Latin.
//
// Hard claims this test enforces:
//
//	(A) BEFORE the fix, a literal byte-folded normalize would classify
//	    < 25% of these pairs as "same" — empirically demonstrated by
//	    running the bytewise-strip version of normalize on the fixture.
//	(B) AFTER the fix (current code), ≥ 90% are correctly "same".
func TestNormalizeForMatch_AccentedNamesQuantitative(t *testing.T) {
	pairs := []struct {
		accented string
		stripped string
	}{
		{"José Reggiani", "Jose Reggiani"},         // Spanish
		{"François Hollande", "Francois Hollande"}, // French cedilla
		{"Müller", "Muller"},                       // German umlaut
		{"Łukasz Pawełczak", "Lukasz Pawelczak"},   // Polish slashed l
		{"Søren Kierkegaard", "Soren Kierkegaard"}, // Danish ø
		{"Mahmoud Abbas مَحْمُود عَبَّاس", "Mahmoud Abbas مَحْمُود عَبَّاس"}, // Arabic; should already match itself
		{"Naïve Café", "Naive Cafe"},             // diaeresis + acute
		{"Žižek", "Zizek"},                       // Czech caron
		{"Erdoğan", "Erdogan"},                   // Turkish g-breve
		{"Ahmet Davutoğlu", "Ahmet Davutoglu"},   // Turkish g-breve
		{"İbrahim", "Ibrahim"},                   // Turkish dotted I
		{"Björn Borg", "Bjorn Borg"},             // Swedish o-umlaut
		{"Þórbergur", "Thorbergur"},              // Icelandic thorn
		{"Strauß", "Strauss"},                    // German eszett
		{"Curaçao", "Curacao"},                   // c-cedilla
		{"Nguyễn Phú Trọng", "Nguyen Phu Trong"}, // Vietnamese tones
	}

	type bytewiseLegacyNormalizer = func(string) string
	// Legacy normalizer (byte-stripping only, no NFKD, no Latin fold) —
	// reproducing the old behaviour to measure the BEFORE rate.
	legacy := func(s string) string {
		s = strings.ToLower(s)
		out := []rune{}
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
				out = append(out, r)
			}
		}
		return strings.TrimSpace(string(out))
	}

	classifyAsSame := func(normalize bytewiseLegacyNormalizer, a, b string) bool {
		na, nb := normalize(a), normalize(b)
		// "Same" iff identical after normalisation.
		return na == nb && na != ""
	}

	beforeMatches := 0
	for _, p := range pairs {
		if classifyAsSame(legacy, p.accented, p.stripped) {
			beforeMatches++
		}
	}
	afterMatches := 0
	for _, p := range pairs {
		if classifyAsSame(normalizeForMatch, p.accented, p.stripped) {
			afterMatches++
		}
	}
	beforePct := float64(beforeMatches) / float64(len(pairs)) * 100
	afterPct := float64(afterMatches) / float64(len(pairs)) * 100
	delta := afterPct - beforePct

	t.Logf("Accented-vs-stripped name pairs (%d total):", len(pairs))
	t.Logf("  legacy normalizer (byte-strip): %d/%d = %.1f%% classified 'same'", beforeMatches, len(pairs), beforePct)
	t.Logf("  new normalizer (NFKD + fold):   %d/%d = %.1f%% classified 'same'", afterMatches, len(pairs), afterPct)
	t.Logf("  improvement:                    +%.1f percentage points", delta)

	if beforePct > 25 {
		t.Errorf("legacy normalizer success rate %.1f%% — fixture isn't actually accented enough", beforePct)
	}
	if afterPct < 90 {
		// Per-pair diagnostic so failures are actionable
		for _, p := range pairs {
			na := normalizeForMatch(p.accented)
			nb := normalizeForMatch(p.stripped)
			marker := "✓"
			if na != nb {
				marker = "✗"
			}
			t.Logf("    %s  %q (→ %q)  vs  %q (→ %q)", marker, p.accented, na, p.stripped, nb)
		}
		t.Errorf("new normalizer success rate %.1f%% — expected ≥90%%", afterPct)
	}
	if delta < 60 {
		t.Errorf("improvement only +%.1fpp — expected ≥+60pp", delta)
	}
}

// TestEntityMatch_AccentedNamesEndToEnd verifies the improvement
// propagates through the full EntityMatch tool path: a representative
// accented/stripped pair now produces verdict "same" with composite
// score 1.0, where it previously produced "likely-same" or worse.
func TestEntityMatch_AccentedNamesEndToEnd(t *testing.T) {
	cases := []struct {
		a, b string
	}{
		{"José Reggiani", "Jose Reggiani"},
		{"François Hollande", "Francois Hollande"},
		{"Łukasz Pawełczak", "Lukasz Pawelczak"},
		{"Erdoğan", "Erdogan"},
		{"Strauß", "Strauss"},
	}
	sameCount := 0
	for _, c := range cases {
		out, err := EntityMatch(context.Background(), map[string]any{
			"mode":   "name_match",
			"name_a": c.a,
			"name_b": c.b,
		})
		if err != nil {
			t.Fatalf("EntityMatch %q vs %q: %v", c.a, c.b, err)
		}
		if out.MatchVerdict == "same" {
			sameCount++
		} else {
			t.Logf("  %s vs %s → verdict=%s composite=%.3f", c.a, c.b, out.MatchVerdict, out.CompositeScore)
		}
	}
	if sameCount != len(cases) {
		t.Errorf("expected all %d accented pairs to verdict 'same'; got %d", len(cases), sameCount)
	}
}
