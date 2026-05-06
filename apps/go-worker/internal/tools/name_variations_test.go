package tools

import (
	"context"
	"strings"
	"testing"
)

// TestNameVariations_AccentedRecallQuantitative is the proof-of-improvement
// test for iteration 13.
//
// The defect: name_variations mode looks up the first-name token in
// nicknameGroups. Two independent gaps:
//
//  1. lookupNicknameGroup lowercased the key but did NOT fold accents.
//     "José" → looked up "josé", missed every dict entry. Result:
//     zero nickname expansions for any non-ASCII input.
//  2. The dict was overwhelmingly English-only, so even after a fold
//     to ASCII, common international forms ("Jose", "Juan", "Sofia",
//     "Maria", "François") had no entry to find.
//
// The fix: (a) apply normalizeForMatch (iter-2) inside
// lookupNicknameGroup so accented inputs resolve to the ASCII dict.
// (b) Augment the dict with ~25 international given-name groups
// covering the most common Spanish, French, German, Italian,
// Portuguese, Russian, and Polish forms.
//
// Quantitative metric: % of accented/international first-name inputs
// that produce ≥1 nickname expansion (i.e., len(VariationGroups["nicknames"]) > 0).
func TestNameVariations_AccentedRecallQuantitative(t *testing.T) {
	cases := []struct {
		input string
		// At least one of these expected variants must appear in the
		// resulting Variations list (after lowercase compare).
		expectAtLeastOneOf []string
	}{
		{"José Reggiani", []string{"joseph reggiani", "joe reggiani", "pepe reggiani"}},
		{"Juan García", []string{"john garcía", "john garcia", "johnny garcía", "ivan garcía"}},
		{"Francois Hollande", []string{"francis hollande", "frank hollande"}},
		{"Sofia Coppola", []string{"sophia coppola", "sophie coppola", "sof coppola"}},
		{"María Corina", []string{"mary corina", "marie corina", "molly corina"}},
		{"Jürgen Klopp", []string{"george klopp", "georgie klopp"}},
		{"Giovanni Battista", []string{"john battista", "gianni battista", "vanni battista"}},
		{"Łukasz Pawełczak", []string{"luke pawełczak", "luke pawelczak", "lucas pawełczak"}},
		{"Hans Müller", []string{"john müller", "johann müller", "john muller"}},
		{"Pedro Almodóvar", []string{"peter almodóvar", "pete almodóvar", "peter almodovar"}},
		// Negative control: an accented name with NO formal/nickname
		// equivalent in the (still-finite) dict should still pass through
		// without crashing — but we don't assert nickname expansion.
		{"Władysław Szpilman", nil},
	}

	beforeWithExpansion := 0
	afterWithExpansion := 0
	expectedTotal := 0

	matches := func(variations []string, candidates []string) bool {
		if len(candidates) == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, v := range variations {
			seen[strings.ToLower(v)] = true
		}
		for _, c := range candidates {
			if seen[strings.ToLower(c)] {
				return true
			}
		}
		return false
	}

	t.Logf("name_variations recall on %d international/accented inputs:", len(cases))
	for _, c := range cases {
		// BEFORE: simulate the legacy code path by clearing the
		// international dict additions and turning off the fold.
		before := simulateLegacyNameVariations(t, c.input)
		// AFTER: real EntityMatch
		after, err := EntityMatch(context.Background(), map[string]any{
			"mode": "name_variations",
			"name": c.input,
		})
		if err != nil {
			t.Fatalf("EntityMatch %q: %v", c.input, err)
		}

		if len(c.expectAtLeastOneOf) > 0 {
			expectedTotal++
			if matches(before, c.expectAtLeastOneOf) {
				beforeWithExpansion++
			}
			if matches(after.Variations, c.expectAtLeastOneOf) {
				afterWithExpansion++
			}
		}

		nicknameCount := 0
		if g := after.VariationGroups["nicknames"]; g != nil {
			nicknameCount = len(g)
		}
		t.Logf("  %-30s → %d nickname expansions, e.g. %v",
			c.input, nicknameCount, firstN(after.Variations, 4))
	}

	beforePct := float64(beforeWithExpansion) / float64(expectedTotal) * 100
	afterPct := float64(afterWithExpansion) / float64(expectedTotal) * 100
	delta := afterPct - beforePct

	t.Logf("")
	t.Logf("Accented-input nickname-expansion recall on %d cases:", expectedTotal)
	t.Logf("  legacy (no fold + English-only dict): %d/%d = %.1f%%", beforeWithExpansion, expectedTotal, beforePct)
	t.Logf("  new    (fold + intl. dict):           %d/%d = %.1f%%", afterWithExpansion, expectedTotal, afterPct)
	t.Logf("  improvement:                          +%.1f percentage points", delta)

	if afterPct < 90 {
		t.Errorf("new recall %.1f%% — expected ≥90%%", afterPct)
	}
	if delta < 60 {
		t.Errorf("improvement only +%.1fpp — expected ≥+60pp", delta)
	}
}

// simulateLegacyNameVariations approximates what name_variations would
// have produced before iter-13: ASCII lowercase only, English-only dict
// (no international entries). We approximate by directly looking up the
// raw ASCII-lowercased key in the dict — without the iter-13 fold and
// without the iter-13 international additions.
//
// Specifically: we look up only the keys that pre-existed in the dict
// before iter 13. Keys added in iter 13 (jose, juan, javier, miguel,
// pedro, diego, santiago, francois, jean, jurgen, johann, hans,
// giovanni, giuseppe, vladimir, ivan, sofia, maria, ana, olga, natasha,
// lukasz, sergio, pavel, juana) are excluded.
func simulateLegacyNameVariations(t *testing.T, raw string) []string {
	iter13Additions := map[string]bool{
		"jose": true, "juan": true, "juana": true, "javier": true,
		"miguel": true, "pedro": true, "diego": true, "santiago": true,
		"francois": true, "jean": true, "jurgen": true, "johann": true,
		"hans": true, "giovanni": true, "giuseppe": true, "vladimir": true,
		"ivan": true, "sofia": true, "maria": true, "ana": true,
		"olga": true, "natasha": true, "lukasz": true, "sergio": true,
		"pavel": true,
	}
	canonical := stripHonorifics(strings.ToLower(strings.TrimSpace(raw)))
	first := canonical
	rest := ""
	if idx := strings.IndexByte(canonical, ' '); idx > 0 {
		first = canonical[:idx]
		rest = canonical[idx:]
	}
	// Pre-iter-13: NO accent fold on the lookup key.
	out := []string{raw}
	if iter13Additions[first] {
		// This dict entry didn't exist pre-iter-13 → no expansion.
		return out
	}
	if g, ok := nicknameGroups[first]; ok {
		for _, alt := range g {
			out = append(out, titleCase(alt)+rest)
		}
	}
	for canon, nicks := range nicknameGroups {
		if iter13Additions[canon] {
			continue
		}
		for _, n := range nicks {
			if n == first {
				out = append(out, titleCase(canon)+rest)
				for _, alt := range nicks {
					if alt != first {
						out = append(out, titleCase(alt)+rest)
					}
				}
				break
			}
		}
	}
	return out
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
