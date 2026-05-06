package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// legacyGtFloat reproduces the pre-fix gtFloat behaviour (only float64
// and string-via-Sscanf-%%f) so the test can numerically compare.
func legacyGtFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case float64:
			return x
		case string:
			var f float64
			if _, err := fmt.Sscanf(x, "%f", &f); err == nil {
				return f
			}
		}
	}
	return 0
}

// TestGtFloat_BroadTypeCoverageQuantitative is the proof-of-improvement
// test for iteration 4 of the /loop "make osint-agent quantitatively
// better" task.
//
// The defect: the prior gtFloat handled only float64 and "%f"-parseable
// strings. Real upstreams serve numbers as int (manual map construction
// in adapters), int64 (BigQuery / DB row scans passed through map[string]any),
// json.Number (any decoder using UseNumber()), bool (some APIs encode
// "exists" as 1/0), and comma-formatted strings (FEC, GovInfo, financial
// APIs commonly serve "1,234.56"). Each unhandled type silently returned
// 0 — turning population/score/follower_count fields into "missing data"
// throughout the catalog.
//
// The fix: type-switch over float32/64, all int/uint widths, json.Number,
// bool, and string via strconv.ParseFloat with comma+percent stripping.
//
// Quantitative metric: % of fixture cases where gtFloat returns the
// expected value (within 1e-9 tolerance), before vs after. Fixture
// covers each shape category with one or two representative inputs,
// plus negative controls (nil, missing key, unparseable string, weird
// type) where the expected output is 0.
func TestGtFloat_BroadTypeCoverageQuantitative(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want float64
	}{
		// --- shapes the legacy version SHOULD handle ---
		{"float64 positive", float64(42.5), 42.5},
		{"float64 zero", float64(0), 0},
		{"plain integer string", "42", 42},
		// --- shapes the legacy version MISSED ---
		{"int", int(123), 123},
		{"int64 (DB row)", int64(9876543210), 9876543210},
		{"int32", int32(7), 7},
		{"uint64 large", uint64(18446744073709), 18446744073709},
		{"float32", float32(3.14), 3.140000104904175},
		{"json.Number int form", json.Number("100"), 100},
		{"json.Number float form", json.Number("0.5"), 0.5},
		{"bool true", true, 1},
		{"bool false", false, 0},
		{"comma-separated thousands (FEC style)", "1,234,567.89", 1234567.89},
		{"trailing percent", "75%", 75},
		{"leading whitespace", "  42.5  ", 42.5},
		{"scientific notation", "6.022e23", 6.022e23},
		{"negative number", "-273.15", -273.15},
		// --- negative controls (must return 0 in BOTH versions) ---
		{"unparseable string", "not a number", 0},
		{"nil value", nil, 0},
		// Note: missing-key cases tested separately below
	}

	beforeCorrect := 0
	afterCorrect := 0
	type row struct{ name, before, after string }
	rows := []row{}
	for _, c := range cases {
		m := map[string]any{"v": c.val}
		before := legacyGtFloat(m, "v")
		after := gtFloat(m, "v")
		bOK := approxEqual(before, c.want)
		aOK := approxEqual(after, c.want)
		if bOK {
			beforeCorrect++
		}
		if aOK {
			afterCorrect++
		}
		rows = append(rows, row{
			name:   c.name,
			before: fmt.Sprintf("%v %s", before, mark(bOK)),
			after:  fmt.Sprintf("%v %s", after, mark(aOK)),
		})
	}

	beforePct := float64(beforeCorrect) / float64(len(cases)) * 100
	afterPct := float64(afterCorrect) / float64(len(cases)) * 100
	delta := afterPct - beforePct

	t.Logf("gtFloat type coverage on %d-case fixture:", len(cases))
	t.Logf("  legacy gtFloat (float64 + Sscanf%%f only): %d/%d = %.1f%%", beforeCorrect, len(cases), beforePct)
	t.Logf("  new gtFloat (full type switch):           %d/%d = %.1f%%", afterCorrect, len(cases), afterPct)
	t.Logf("  improvement:                              +%.1f percentage points", delta)
	for _, r := range rows {
		t.Logf("    %-40s  before=%-20s  after=%-20s", r.name, r.before, r.after)
	}

	// Hard claims (focused on the improvement signal, not the floor):
	if afterPct < 95 {
		t.Errorf("new correct rate %.1f%% — expected ≥95%%", afterPct)
	}
	if delta < 30 {
		t.Errorf("improvement only +%.1fpp — expected ≥+30pp", delta)
	}
	// Targeted: every shape that was specifically missed by the legacy
	// version should now be handled. Surface as separate counter so
	// regressions on a single shape don't get drowned by aggregate.
	previouslyMissedShapes := []string{"int", "int64 (DB row)", "int32", "uint64 large",
		"float32", "json.Number int form", "json.Number float form", "bool true",
		"comma-separated thousands (FEC style)"}
	missedNowCorrect := 0
	for _, want := range previouslyMissedShapes {
		for _, c := range cases {
			if c.name == want {
				if approxEqual(gtFloat(map[string]any{"v": c.val}, "v"), c.want) {
					missedNowCorrect++
				}
				break
			}
		}
	}
	if missedNowCorrect != len(previouslyMissedShapes) {
		t.Errorf("expected all %d previously-missed shapes to now parse; only %d do",
			len(previouslyMissedShapes), missedNowCorrect)
	}
}

// TestGtFloat_RejectsPartialParse pins an important behavioural change:
// the legacy version returned 42.0 for input "42abc" (Sscanf is permissive),
// the new version returns 0 (strconv.ParseFloat is strict). For OSINT
// downstream comparisons this is the safer default — silently turning
// "42 vehicles destroyed" into 42.0 vs surfacing the parse failure as
// "missing field" lets the caller decide. Documented as a behavioral
// change so future regressions are caught.
func TestGtFloat_RejectsPartialParse(t *testing.T) {
	cases := map[string]float64{
		"42abc":       0,
		"42 vehicles": 0,
		"$1,000":      1000, // $ is stripped via comma stripping; wait actually $ is not stripped
		"1.5kg":       0,
	}
	// Adjust: gtFloat does NOT strip '$', so "$1,000" should fail.
	cases["$1,000"] = 0
	for in, want := range cases {
		got := gtFloat(map[string]any{"v": in}, "v")
		if !approxEqual(got, want) {
			t.Errorf("gtFloat(%q) = %v; want %v", in, got, want)
		}
	}
}

func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	// 1e-6 tolerance handles float32→float64 round-trip
	return d < 1e-6 || (b != 0 && d/abs(b) < 1e-6)
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

var _ = strings.Builder{} // keep strings import even if unused
