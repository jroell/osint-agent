package tools

import (
	"encoding/json"
	"fmt"
	"testing"
)

// legacyGtInt reproduces the pre-fix gtInt behaviour (float64 + int only).
func legacyGtInt(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case float64:
			return int(x)
		case int:
			return x
		}
	}
	return 0
}

// legacyGtBool reproduces the pre-fix gtBool behaviour (bool only).
func legacyGtBool(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// TestGtInt_BroadTypeCoverageQuantitative is the proof-of-improvement
// test for iteration 9 of the /loop "make osint-agent quantitatively
// better" task — completing the gt* helper-family fix trilogy started
// in iter 4 (gtFloat) and continued in iter 8 (gtString).
//
// The defect: prior gtInt only handled float64 and int. Real upstreams
// serve integer fields as int64 (BigQuery counts), uint64 (timestamps),
// json.Number (decoders using UseNumber()), bool (RapidAPI flags), and
// string-encoded numbers ("42", "1,234") — each silently became 0,
// turning page counts, follower counts, vote counts, percentages,
// and pagination cursors into "missing data" downstream.
//
// The fix: type-switch over float32/64, all int/uint widths, json.Number,
// bool, and string via strconv.ParseInt with comma+percent stripping.
//
// Quantitative metric: % of fixture cases where gtInt returns the
// expected integer, before vs after.
func TestGtInt_BroadTypeCoverageQuantitative(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int
	}{
		// shapes the legacy version SHOULD handle
		{"float64", float64(42.7), 42}, // truncates
		{"int", int(7), 7},
		// shapes the legacy version MISSED
		{"int64 (BigQuery row count)", int64(123456789), 123456789},
		{"int32", int32(-7), -7},
		{"uint64 (epoch ms)", uint64(1700000000000), 1700000000000},
		{"uint", uint(42), 42},
		{"float32", float32(3.7), 3},
		{"json.Number int form", json.Number("100"), 100},
		{"json.Number float form (truncates)", json.Number("3.7"), 3},
		{"plain integer string", "42", 42},
		{"comma-separated string (FEC style)", "1,234,567", 1234567},
		{"trailing percent", "75%", 75},
		{"leading whitespace", "  42  ", 42},
		{"negative integer", "-273", -273},
		{"float string truncates", "42.7", 42},
		{"bool true", true, 1},
		{"bool false", false, 0},
		// negative controls (must return 0 in BOTH versions)
		{"unparseable string", "not a number", 0},
		{"nil value", nil, 0},
	}

	beforeCorrect := 0
	afterCorrect := 0
	for _, c := range cases {
		m := map[string]any{"v": c.val}
		if legacyGtInt(m, "v") == c.want {
			beforeCorrect++
		}
		if gtInt(m, "v") == c.want {
			afterCorrect++
		}
	}

	beforePct := float64(beforeCorrect) / float64(len(cases)) * 100
	afterPct := float64(afterCorrect) / float64(len(cases)) * 100
	delta := afterPct - beforePct

	t.Logf("gtInt type coverage on %d-case fixture:", len(cases))
	t.Logf("  legacy gtInt (float64+int only):  %d/%d = %.1f%%", beforeCorrect, len(cases), beforePct)
	t.Logf("  new gtInt (full type switch):     %d/%d = %.1f%%", afterCorrect, len(cases), afterPct)
	t.Logf("  improvement:                      +%.1f percentage points", delta)
	for _, c := range cases {
		m := map[string]any{"v": c.val}
		before := legacyGtInt(m, "v")
		after := gtInt(m, "v")
		bMark := "✗"
		aMark := "✗"
		if before == c.want {
			bMark = "✓"
		}
		if after == c.want {
			aMark = "✓"
		}
		t.Logf("    %-44s  before=%-12s  after=%-12s",
			c.name,
			fmt.Sprintf("%d %s", before, bMark),
			fmt.Sprintf("%d %s", after, aMark))
	}

	if afterPct < 95 {
		t.Errorf("new gtInt correct rate %.1f%% — expected ≥95%%", afterPct)
	}
	if delta < 50 {
		t.Errorf("improvement only +%.1fpp — expected ≥+50pp", delta)
	}
	// Targeted check: every previously-missed shape now correct.
	previouslyMissedShapes := []string{
		"int64 (BigQuery row count)",
		"int32",
		"uint64 (epoch ms)",
		"uint",
		"float32",
		"json.Number int form",
		"plain integer string",
		"comma-separated string (FEC style)",
		"bool true",
	}
	correctNow := 0
	for _, want := range previouslyMissedShapes {
		for _, c := range cases {
			if c.name == want {
				if gtInt(map[string]any{"v": c.val}, "v") == c.want {
					correctNow++
				}
				break
			}
		}
	}
	if correctNow != len(previouslyMissedShapes) {
		t.Errorf("expected all %d previously-missed shapes to now parse; %d do",
			len(previouslyMissedShapes), correctNow)
	}
}

// TestGtBool_BroadTypeCoverageQuantitative — companion to gtInt for the
// gtBool helper.
//
// The defect: legacy gtBool only handled native `bool`. APIs commonly
// serve booleans as 0/1 ints (RapidAPI, MarineTraffic) or as strings
// ("true"/"false"/"yes"/"no"/"1"/"0"/"on"/"off" — common in form-
// encoded responses, CSV imports, MCP tool inputs).
//
// The fix: handle all numeric types (non-zero → true), json.Number,
// and a defined string vocabulary.
func TestGtBool_BroadTypeCoverageQuantitative(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want bool
	}{
		// the legacy version HANDLES these
		{"native bool true", true, true},
		{"native bool false", false, false},
		// MISSED:
		{"int 1 (RapidAPI flag)", 1, true},
		{"int 0", 0, false},
		{"int 5 (any non-zero)", 5, true},
		{"float64 1.0", float64(1), true},
		{"float64 0.0", float64(0), false},
		{"int64 1", int64(1), true},
		{"json.Number 1", json.Number("1"), true},
		{"json.Number 0", json.Number("0"), false},
		{"string 'true'", "true", true},
		{"string 'false'", "false", false},
		{"string 'yes'", "yes", true},
		{"string 'no'", "no", false},
		{"string '1'", "1", true},
		{"string '0'", "0", false},
		{"string 'on'", "on", true},
		{"string 'off'", "off", false},
		{"string 'TRUE' (case-insensitive)", "TRUE", true},
		// negative controls
		{"unrecognized string", "maybe", false},
		{"empty string", "", false},
		{"nil", nil, false},
	}

	beforeCorrect := 0
	afterCorrect := 0
	for _, c := range cases {
		m := map[string]any{"v": c.val}
		if legacyGtBool(m, "v") == c.want {
			beforeCorrect++
		}
		if gtBool(m, "v") == c.want {
			afterCorrect++
		}
	}

	beforePct := float64(beforeCorrect) / float64(len(cases)) * 100
	afterPct := float64(afterCorrect) / float64(len(cases)) * 100
	delta := afterPct - beforePct

	t.Logf("gtBool type coverage on %d-case fixture:", len(cases))
	t.Logf("  legacy gtBool (bool only):  %d/%d = %.1f%%", beforeCorrect, len(cases), beforePct)
	t.Logf("  new gtBool (extended):      %d/%d = %.1f%%", afterCorrect, len(cases), afterPct)
	t.Logf("  improvement:                +%.1f percentage points", delta)

	if afterPct < 95 {
		t.Errorf("new gtBool correct rate %.1f%% — expected ≥95%%", afterPct)
	}
	if delta < 40 {
		t.Errorf("improvement only +%.1fpp — expected ≥+40pp", delta)
	}
}
