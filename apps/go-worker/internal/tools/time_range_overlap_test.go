package tools

import "testing"

// legacyTimeRangesOverlap reproduces the pre-iter-11 behaviour: extract
// years, take min/max, plain interval check. No special handling for
// open-ended periods.
func legacyTimeRangesOverlap(a, b string) bool {
	yearsA := extractYears(a)
	yearsB := extractYears(b)
	if len(yearsA) == 0 || len(yearsB) == 0 {
		return false
	}
	aStart, aEnd := yearRange(yearsA)
	bStart, bEnd := yearRange(yearsB)
	return aStart <= bEnd && bStart <= aEnd
}

// legacyConfidenceFromOverlap reproduces the pre-iter-11 behaviour
// for verdict-counting. Used to compute the BEFORE rate of correctly
// classified verdict ("high" vs "medium").
func legacyConfidenceFromOverlap(periodA, periodB string) string {
	if (containsLower(periodA, "current") && containsLower(periodB, "current")) ||
		(periodA != "" && periodB != "" && legacyTimeRangesOverlap(periodA, periodB)) {
		return "high"
	}
	return "medium"
}

func containsLower(s, sub string) bool {
	// case-sensitive copy of strings.Contains for the legacy reproduction
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestTimeRangesOverlap_OpenEndedPeriodsQuantitative is the proof-of-improvement
// test for iteration 11. Defect: when a period was open-ended ("2018-current",
// "2025-present", "now" alone), extractYears either found a single year or none,
// collapsing the range to a single-year point and breaking standard interval
// overlap. Two people with overlapping ongoing tenures at the same employer
// got verdict="medium" instead of "high", weakening the connecting-the-dots
// engine's confidence signal.
//
// Fix: detect open-ended markers (current/present/ongoing/now/today) and
// extend the interval's right edge to a sentinel year (9999) so the standard
// overlap check works correctly. When both sides are open-ended with no
// extracted years, assume overlap.
//
// Quantitative metric: % of fixture period pairs correctly classified as
// overlap (true) vs no-overlap (false), before vs after.
func TestTimeRangesOverlap_OpenEndedPeriodsQuantitative(t *testing.T) {
	cases := []struct {
		name     string
		a, b     string
		want     bool
		category string // "open-ended" or "ordinary"
	}{
		// Pure ordinary cases (legacy should already pass these)
		{"both closed, overlap", "2018-2020", "2019-2021", true, "ordinary"},
		{"both closed, disjoint", "2018-2020", "2021-2023", false, "ordinary"},
		{"both closed, touching", "2018-2020", "2020-2022", true, "ordinary"},
		{"both closed, single-year inside", "2018-2020", "2019", true, "ordinary"},
		{"both closed, single-year outside", "2018-2020", "2025", false, "ordinary"},

		// Open-ended cases — these were the broken ones
		{"A open-ended, B inside A's window", "2018-current", "2020-2022", true, "open-ended"},
		{"A open-ended, B starts after A's start", "2018-current", "2025-2027", true, "open-ended"},
		{"A open-ended, B ends before A's start", "2020-current", "2015-2017", false, "open-ended"},
		{"both open-ended, A starts before B", "2018-current", "2025-current", true, "open-ended"},
		{"both open-ended, B starts before A", "2025-current", "2018-current", true, "open-ended"},
		{"A 'present' marker", "2018-present", "2020-2022", true, "open-ended"},
		{"A 'ongoing' marker", "2018-ongoing", "2020-2022", true, "open-ended"},
		{"A starts mid-B's range with present marker", "2021-present", "2020-2022", true, "open-ended"},
		{"A is just 'now' (no year)", "now", "2020-2022", true, "open-ended"},
		{"A 'today' marker no year", "today", "2020-2022", true, "open-ended"},

		// Negative controls — must NOT be classified as overlap
		{"empty A", "", "2020-2022", false, "ordinary"},
		{"empty B", "2020-2022", "", false, "ordinary"},
	}

	beforeCorrect := 0
	afterCorrect := 0
	beforeOpenEndedCorrect := 0
	afterOpenEndedCorrect := 0
	openEndedTotal := 0

	for _, c := range cases {
		// timeRangesOverlap doesn't itself handle the "" sentinel —
		// confidenceFromOverlap does, so run through that.
		legacyVerdict := legacyConfidenceFromOverlap(c.a, c.b) == "high"
		newVerdict := confidenceFromOverlap(c.a, c.b) == "high"
		if legacyVerdict == c.want {
			beforeCorrect++
		}
		if newVerdict == c.want {
			afterCorrect++
		}
		if c.category == "open-ended" {
			openEndedTotal++
			if legacyVerdict == c.want {
				beforeOpenEndedCorrect++
			}
			if newVerdict == c.want {
				afterOpenEndedCorrect++
			}
		}
	}

	beforePct := float64(beforeCorrect) / float64(len(cases)) * 100
	afterPct := float64(afterCorrect) / float64(len(cases)) * 100
	delta := afterPct - beforePct
	beforeOpenPct := float64(beforeOpenEndedCorrect) / float64(openEndedTotal) * 100
	afterOpenPct := float64(afterOpenEndedCorrect) / float64(openEndedTotal) * 100

	t.Logf("Time-range overlap classification on %d period pairs:", len(cases))
	t.Logf("  legacy  (no open-ended handling):  %d/%d = %.1f%%", beforeCorrect, len(cases), beforePct)
	t.Logf("  new     (open-ended aware):        %d/%d = %.1f%%", afterCorrect, len(cases), afterPct)
	t.Logf("  improvement:                       +%.1f percentage points", delta)
	t.Logf("Open-ended subset (%d pairs):", openEndedTotal)
	t.Logf("  legacy: %d/%d = %.1f%%", beforeOpenEndedCorrect, openEndedTotal, beforeOpenPct)
	t.Logf("  new:    %d/%d = %.1f%%", afterOpenEndedCorrect, openEndedTotal, afterOpenPct)

	// Per-pair diagnostic when after-rate is below threshold
	if afterPct < 95 {
		for _, c := range cases {
			before := legacyConfidenceFromOverlap(c.a, c.b) == "high"
			after := confidenceFromOverlap(c.a, c.b) == "high"
			t.Logf("    [%s]  before=%v after=%v want=%v  | %q vs %q",
				c.category, before, after, c.want, c.a, c.b)
		}
	}

	if afterPct < 95 {
		t.Errorf("new correctness %.1f%% — expected ≥95%%", afterPct)
	}
	if delta < 30 {
		t.Errorf("improvement only +%.1fpp — expected ≥+30pp", delta)
	}
	if afterOpenPct < 95 {
		t.Errorf("new correctness on open-ended subset %.1f%% — expected ≥95%%", afterOpenPct)
	}
	if beforeOpenPct >= 50 {
		t.Errorf("legacy already passed %.1f%% of open-ended cases — fixture isn't open-ended-heavy enough", beforeOpenPct)
	}
}

// TestIsOpenEndedPeriod_PinnedMarkers locks down the marker vocabulary.
func TestIsOpenEndedPeriod_PinnedMarkers(t *testing.T) {
	open := []string{
		"current", "Current", "present", "PRESENT",
		"ongoing", "now", "today", "2018-current",
		"started 2010, present", "2020 — Present",
	}
	closed := []string{
		"", "2018-2020", "2018", "2018-2025",
		"started 2010", "Q1 2024 to Q4 2025",
	}
	for _, s := range open {
		if !isOpenEndedPeriod(s) {
			t.Errorf("isOpenEndedPeriod(%q) = false; want true", s)
		}
	}
	for _, s := range closed {
		if isOpenEndedPeriod(s) {
			t.Errorf("isOpenEndedPeriod(%q) = true; want false", s)
		}
	}
}
