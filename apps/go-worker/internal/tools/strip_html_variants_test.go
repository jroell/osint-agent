package tools

import (
	"regexp"
	"strings"
	"testing"
)

// --- Legacy reproductions of pre-iter-6 stripHTML / stripHTMLFed ---
// Used to compute the BEFORE rate numerically.

func legacyStripHTMLHN(s string, max int) string {
	out := make([]rune, 0, len(s))
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			out = append(out, r)
		}
	}
	s = strings.TrimSpace(string(out))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

var legacyHtmlStripFedRe = regexp.MustCompile(`<[^>]*>`)
var legacyMultiSpaceFedRe = regexp.MustCompile(`\s+`)

func legacyStripHTMLFed(s string) string {
	s = legacyHtmlStripFedRe.ReplaceAllString(s, " ")
	s = legacyMultiSpaceFedRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// TestStripHTMLVariants_EntityDecodingQuantitative is the proof-of-improvement
// test for iteration 6 of the /loop "make osint-agent quantitatively
// better" task.
//
// The defect: iter 3 fixed entity decoding inside `stripHTMLBare`, but
// the codebase has TWO OTHER HTML-strippers — `stripHTML` (hackernews.go,
// 2-arg) and `stripHTMLFed` (fediverse_webfinger.go) — that were left
// unfixed. They are called from google_dork.go (Google snippet
// extraction, 6 call sites; extremely entity-heavy), hackernews.go
// (HN about + comment text), mastodon.go (status notes), and
// fediverse_webfinger.go (Mastodon bio + properties). Every one of
// those output paths was silently leaking `&amp;`, `&#39;`, `&nbsp;`
// into downstream entity names and snippets.
//
// The fix: route both helpers through `htmlPkgUnescape` +
// `normalizeHTMLWhitespace` after tag-strip — the same shared
// post-processing already used by `stripHTMLBare`.
//
// Quantitative metric: % of HTML-encoded fixture cases each helper
// decodes correctly. Computed as a 3-helper × N-case matrix. The
// improvement claim is on the AGGREGATE pass rate across all helpers.
func TestStripHTMLVariants_EntityDecodingQuantitative(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{"HN comment ampersand", "Smith &amp; Jones disagree", "Smith & Jones disagree"},
		{"Google snippet apostrophe", "Don&#39;t miss this", "Don't miss this"},
		{"Mastodon bio NBSP", "Web&nbsp;developer in Berlin", "Web developer in Berlin"},
		{"Google snippet quotes", "He said &ldquo;hello&rdquo;", "He said “hello”"},
		{"HN comment hex ref", "&#x27;Tagged&#x27; users", "'Tagged' users"},
		{"Mastodon copyright", "All content &copy; 2026", "All content © 2026"},
		{"Google snippet diacritic", "Caf&eacute; opens at 7", "Café opens at 7"},
		{"HN about euro", "EU price: &euro;100/yr", "EU price: €100/yr"},
		{"Mastodon em-dash", "Source &mdash; example.com", "Source — example.com"},
		{"Google snippet trade", "Vurvey&trade; platform", "Vurvey™ platform"},
		// controls
		{"plain text (no entities)", "no entities here", "no entities here"},
		{"already decoded", "Plain UTF-8 — done", "Plain UTF-8 — done"},
	}

	type stripperRow struct {
		name          string
		legacy        func(string) string
		current       func(string) string
		correctBefore int
		correctAfter  int
	}
	// The "max" cap on stripHTML is large enough that none of the
	// fixture cases truncate — so it's purely a tag+entity test.
	strippers := []*stripperRow{
		{
			name:    "stripHTML (hackernews/google_dork/mastodon)",
			legacy:  func(s string) string { return legacyStripHTMLHN(s, 1000) },
			current: func(s string) string { return stripHTML(s, 1000) },
		},
		{
			name:    "stripHTMLFed (fediverse_webfinger)",
			legacy:  legacyStripHTMLFed,
			current: stripHTMLFed,
		},
		{
			name:    "stripHTMLBare (iter-3 reference)",
			legacy:  legacyStripHTMLBare,
			current: stripHTMLBare,
		},
	}

	totalCells := 0
	beforeCorrect := 0
	afterCorrect := 0
	for _, st := range strippers {
		for _, c := range cases {
			totalCells++
			before := st.legacy(c.input)
			after := st.current(c.input)
			// stripHTMLFed inserts a single space where tags were, which
			// produces "  " runs around tag-replaced content. For the
			// purposes of "did it decode?" we only check entity decoding
			// — so check normalized-whitespace-collapsed equality.
			normalize := func(s string) string { return strings.Join(strings.Fields(s), " ") }
			expect := normalize(c.expected)
			if normalize(before) == expect {
				st.correctBefore++
				beforeCorrect++
			}
			if normalize(after) == expect {
				st.correctAfter++
				afterCorrect++
			}
		}
	}

	beforePct := float64(beforeCorrect) / float64(totalCells) * 100
	afterPct := float64(afterCorrect) / float64(totalCells) * 100
	delta := afterPct - beforePct

	t.Logf("HTML entity-decoding coverage across helper population")
	t.Logf("  Cases per helper: %d, helpers: %d, total cells: %d", len(cases), len(strippers), totalCells)
	for _, st := range strippers {
		bPct := float64(st.correctBefore) / float64(len(cases)) * 100
		aPct := float64(st.correctAfter) / float64(len(cases)) * 100
		t.Logf("  - %-50s  before=%d/%d (%.0f%%)  after=%d/%d (%.0f%%)",
			st.name, st.correctBefore, len(cases), bPct, st.correctAfter, len(cases), aPct)
	}
	t.Logf("  AGGREGATE before:  %d/%d = %.1f%%", beforeCorrect, totalCells, beforePct)
	t.Logf("  AGGREGATE after:   %d/%d = %.1f%%", afterCorrect, totalCells, afterPct)
	t.Logf("  improvement:       +%.1f percentage points", delta)

	// Hard claims:
	if afterPct < 95 {
		t.Errorf("aggregate after-rate %.1f%% — expected ≥95%%", afterPct)
	}
	if delta < 50 {
		t.Errorf("improvement only +%.1fpp — expected ≥+50pp", delta)
	}
	// Per-helper assertion: each helper individually must now hit ≥90%.
	for _, st := range strippers {
		aPct := float64(st.correctAfter) / float64(len(cases)) * 100
		if aPct < 90 {
			t.Errorf("helper %s only reached %.0f%% — expected ≥90%%", st.name, aPct)
		}
	}
}
