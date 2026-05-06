package tools

import (
	"regexp"
	"strings"
	"testing"
)

// legacyStripHTMLBare reproduces the pre-fix behaviour (regex tag-strip
// only, NO HTML entity decoding) so the test can numerically compare
// before/after.
var legacyHTMLTagRE = regexp.MustCompile(`<[^>]+>`)

func legacyStripHTMLBare(s string) string {
	return strings.TrimSpace(legacyHTMLTagRE.ReplaceAllString(s, ""))
}

// TestStripHTMLBare_EntityDecodingQuantitative is the proof-of-improvement
// test for iteration 3 of the /loop "make osint-agent quantitatively
// better" task.
//
// The defect: stripHTMLBare stripped tags but did NOT decode HTML
// character references. 18+ tools (Trove, ICIJ, ADB, MathGenealogy,
// TVMaze, Chronicling America, HackerNews, etc.) feed scraped HTML
// snippets through stripHTMLBare and then use the result for entity
// names, titles, descriptions, and ER inputs. So `"Smith &amp; Jones"`
// was being passed downstream as `"Smith &amp; Jones"` (wrong) instead
// of `"Smith & Jones"` (right) — silently corrupting every
// downstream comparison/dedup pass.
//
// The fix: feed the tag-stripped string through
// html.UnescapeString (Go stdlib).
//
// Quantitative metric: % of HTML-encoded fixture snippets that
// stripHTMLBare returns equal to the gold-decoded reference.
func TestStripHTMLBare_EntityDecodingQuantitative(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{"named ampersand", "Smith &amp; Jones", "Smith & Jones"},
		{"single named entity", "Caf&eacute;", "Café"},
		{"decimal numeric ref", "Don&#39;t", "Don't"},
		{"hex numeric ref", "Don&#x27;t", "Don't"},
		{"copyright symbol", "&copy; 2026", "© 2026"},
		{"trademark", "Vurvey&trade;", "Vurvey™"},
		{"both lt and gt", "if a &lt; b &amp;&amp; c &gt; d", "if a < b && c > d"},
		{"non-breaking space", "Hello&nbsp;World", "Hello World"},
		{"euro symbol", "Price: &euro;100", "Price: €100"},
		{"em-dash", "Source &mdash; Reuters", "Source — Reuters"},
		{"quotes", "He said &ldquo;hello&rdquo;", "He said “hello”"},
		{"diacritic O", "Le&oacute;n", "León"},
		{"mixed with tags", "<p>Tom &amp; Jerry</p>", "Tom & Jerry"},
		{"mixed with break tag", "First<br/>Second &amp; Third", "FirstSecond & Third"},
		{"no entities (control)", "Plain text only", "Plain text only"},
		{"only tags (control)", "<span>visible</span>", "visible"},
		{"hex with letters", "&#xE9;cole &#xE0; Paris", "école à Paris"},
		{"icij sample", "Mossack Fonseca &amp; Co.", "Mossack Fonseca & Co."},
	}

	beforeCorrect := 0
	afterCorrect := 0
	t.Logf("Fixture (%d cases):", len(cases))
	for _, c := range cases {
		before := legacyStripHTMLBare(c.input)
		after := stripHTMLBare(c.input)
		bOK := before == c.expected
		aOK := after == c.expected
		if bOK {
			beforeCorrect++
		}
		if aOK {
			afterCorrect++
		}
		bMark := "✗"
		if bOK {
			bMark = "✓"
		}
		aMark := "✗"
		if aOK {
			aMark = "✓"
		}
		t.Logf("  before=%s after=%s  | input=%q  → expect=%q (got_before=%q got_after=%q)",
			bMark, aMark, c.input, c.expected, before, after)
	}

	beforePct := float64(beforeCorrect) / float64(len(cases)) * 100
	afterPct := float64(afterCorrect) / float64(len(cases)) * 100
	delta := afterPct - beforePct

	t.Logf("")
	t.Logf("HTML-entity decoding correctness on %d snippets:", len(cases))
	t.Logf("  legacy stripHTMLBare (tag-strip only): %d/%d = %.1f%%", beforeCorrect, len(cases), beforePct)
	t.Logf("  new stripHTMLBare (tag-strip + decode): %d/%d = %.1f%%", afterCorrect, len(cases), afterPct)
	t.Logf("  improvement:                            +%.1f percentage points", delta)

	// Hard claims:
	if beforePct > 25 {
		t.Errorf("legacy correct rate %.1f%% — fixture isn't entity-rich enough to demonstrate improvement", beforePct)
	}
	if afterPct < 95 {
		t.Errorf("new correct rate %.1f%% — expected ≥ 95%%", afterPct)
	}
	if delta < 60 {
		t.Errorf("improvement only +%.1fpp — expected ≥ +60pp", delta)
	}
}

// TestStripHTMLBare_NoOpOnPlainText proves the fix doesn't change
// behaviour for inputs that have no entities or tags — the common case.
func TestStripHTMLBare_NoOpOnPlainText(t *testing.T) {
	plain := []string{
		"Carl Friedrich Gauss",
		"Jason Roell — Vurvey",
		"María García",
		"42",
	}
	for _, p := range plain {
		out := stripHTMLBare(p)
		if out != p {
			t.Errorf("stripHTMLBare changed plain text: %q → %q", p, out)
		}
	}
}
