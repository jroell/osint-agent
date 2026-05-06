package tools

import (
	"encoding/json"
	"fmt"
	"testing"
)

// legacyGtString reproduces the pre-fix behaviour (string + nil only)
// so the test can numerically compare.
func legacyGtString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		if v == nil {
			return ""
		}
	}
	return ""
}

// TestGtString_BroadTypeCoverageQuantitative is the proof-of-improvement
// test for iteration 8 of the /loop "make osint-agent quantitatively
// better" task.
//
// The defect: the prior gtString only handled `string` and `nil`. Real
// upstreams (and adapter code that constructs map[string]any manually)
// frequently have IDs, codes, and scalar fields stored as numerics or
// booleans — Twitter tweet_id/user_id are huge ints that JSON
// occasionally keeps as numbers, ICAO24 hex IDs that are sometimes
// strings + sometimes ints depending on encoder, NPI registry codes,
// PubMed PMIDs, OpenAlex pagination tokens, RapidAPI flags. Each case
// silently became "".
//
// The fix: type-switch over string, all int/uint widths, float32/64,
// json.Number, bool, nil — symmetric with the iter-4 gtFloat fix.
//
// Quantitative metric: % of fixture cases where gtString returns the
// expected non-empty stringification, before vs after.
func TestGtString_BroadTypeCoverageQuantitative(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want string
	}{
		// shapes the legacy version SHOULD handle
		{"plain string", "hello", "hello"},
		{"empty string", "", ""},
		// shapes the legacy version MISSED
		{"int (manual map)", int(42), "42"},
		{"int64 large (twitter tweet id)", int64(1234567890123456789), "1234567890123456789"},
		{"int32", int32(-7), "-7"},
		{"uint64 (timestamp)", uint64(1700000000000), "1700000000000"},
		{"float64 whole (JSON-decoded int)", float64(100), "100"},
		{"float64 fractional", float64(1.5), "1.5"},
		{"float32", float32(3.14), "3.14"},
		{"json.Number int form", json.Number("42"), "42"},
		{"json.Number float form", json.Number("3.14"), "3.14"},
		{"json.Number scientific", json.Number("6.022e23"), "6.022e23"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		// negative controls (must return "" in BOTH versions)
		{"nil value", nil, ""},
		{"slice (unsupported)", []any{1, 2, 3}, ""},
		{"map (unsupported)", map[string]any{"x": 1}, ""},
	}

	beforeCorrect := 0
	afterCorrect := 0
	type row struct{ name, before, after string }
	rows := make([]row, 0, len(cases))

	for _, c := range cases {
		m := map[string]any{"v": c.val}
		before := legacyGtString(m, "v")
		after := gtString(m, "v")
		if before == c.want {
			beforeCorrect++
		}
		if after == c.want {
			afterCorrect++
		}
		mark := func(ok bool) string {
			if ok {
				return "✓"
			}
			return "✗"
		}
		rows = append(rows, row{
			name:   c.name,
			before: fmt.Sprintf("%q %s", before, mark(before == c.want)),
			after:  fmt.Sprintf("%q %s", after, mark(after == c.want)),
		})
	}

	beforePct := float64(beforeCorrect) / float64(len(cases)) * 100
	afterPct := float64(afterCorrect) / float64(len(cases)) * 100
	delta := afterPct - beforePct

	t.Logf("gtString type coverage on %d-case fixture:", len(cases))
	t.Logf("  legacy gtString (string+nil only):  %d/%d = %.1f%%", beforeCorrect, len(cases), beforePct)
	t.Logf("  new gtString (full type switch):    %d/%d = %.1f%%", afterCorrect, len(cases), afterPct)
	t.Logf("  improvement:                        +%.1f percentage points", delta)
	for _, r := range rows {
		t.Logf("    %-44s  before=%-22s  after=%-22s", r.name, r.before, r.after)
	}

	// Hard claims:
	if afterPct < 95 {
		t.Errorf("new correct rate %.1f%% — expected ≥95%%", afterPct)
	}
	if delta < 50 {
		t.Errorf("improvement only +%.1fpp — expected ≥+50pp", delta)
	}
	// Targeted: every shape that was specifically missed by the legacy
	// version should now be handled.
	previouslyMissedShapes := []string{
		"int (manual map)",
		"int64 large (twitter tweet id)",
		"uint64 (timestamp)",
		"float64 whole (JSON-decoded int)",
		"float64 fractional",
		"json.Number int form",
		"bool true",
	}
	missedNowCorrect := 0
	for _, want := range previouslyMissedShapes {
		for _, c := range cases {
			if c.name == want {
				if gtString(map[string]any{"v": c.val}, "v") == c.want {
					missedNowCorrect++
				}
				break
			}
		}
	}
	if missedNowCorrect != len(previouslyMissedShapes) {
		t.Errorf("expected all %d previously-missed shapes to now stringify; only %d do",
			len(previouslyMissedShapes), missedNowCorrect)
	}
}

// TestGtString_FloatFormattingPinned pins the formatting choice for
// floats (no trailing zeros from %f, no scientific notation switch
// for ordinary values). The gotcha is that strconv.FormatFloat with
// precision -1 gives shortest round-trippable form — which is
// exactly what we want for IDs and codes that happen to have
// arrived as float64 from a JSON decoder.
func TestGtString_FloatFormattingPinned(t *testing.T) {
	cases := map[float64]string{
		100:          "100", // no ".000000"
		1.5:          "1.5",
		1.234567890:  "1.23456789",
		0:            "0",
		-273.15:      "-273.15",
		1234567890.0: "1234567890", // JSON-decoded int that fits exactly
	}
	for in, want := range cases {
		got := gtString(map[string]any{"v": in}, "v")
		if got != want {
			t.Errorf("gtString(%v) = %q; want %q", in, got, want)
		}
	}
}
