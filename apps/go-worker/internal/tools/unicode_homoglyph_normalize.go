package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type HomoglyphFlag struct {
	Position    int    `json:"position"`
	Char        string `json:"char"`
	Codepoint   string `json:"codepoint"`
	Script      string `json:"script,omitempty"`
	LooksLike   string `json:"looks_like,omitempty"` // ASCII char it impersonates
	Severity    string `json:"severity"`              // critical (homoglyph attack) | high (non-ASCII) | low (formatting)
}

type UnicodeHomoglyphOutput struct {
	Original         string         `json:"original"`
	Normalized       string         `json:"ascii_normalized"`
	HasNonASCII      bool           `json:"has_non_ascii"`
	IsHomoglyphAttack bool          `json:"is_likely_homoglyph_attack"`
	IDNAEncoding     string         `json:"idna_encoding,omitempty"` // xn-- form
	Flags            []HomoglyphFlag `json:"flags,omitempty"`
	NonASCIICount    int            `json:"non_ascii_count"`
	UniqueScripts    []string       `json:"unique_scripts,omitempty"`
	Verdict          string         `json:"verdict"` // safe | suspicious | likely-attack
	Rationale        string         `json:"rationale"`
	Source           string         `json:"source"`
	TookMs           int64          `json:"tookMs"`
}

// Common homoglyph confusables — Unicode chars that visually resemble ASCII.
// Not exhaustive (Unicode has 700+ confusables) but covers the most-attacked.
var homoglyphReverseMap = map[rune]string{
	// Cyrillic
	'а': "a", 'А': "A", 'е': "e", 'Е': "E", 'о': "o", 'О': "O",
	'р': "p", 'Р': "P", 'с': "c", 'С': "C", 'у': "y", 'У': "Y",
	'х': "x", 'Х': "X", 'і': "i", 'І': "I", 'ј': "j", 'Ј': "J",
	'Ѕ': "S", 'ԛ': "q", 'Ԛ': "Q",
	// Greek
	'α': "a", 'Α': "A", 'ε': "e", 'Ε': "E", 'ο': "o", 'Ο': "O",
	'ρ': "p", 'Ρ': "P", 'τ': "t", 'Τ': "T", 'υ': "u", 'Υ': "Y",
	'ν': "v", 'Ν': "N", 'κ': "k", 'Κ': "K", 'ι': "i", 'Ι': "I",
	'ζ': "z", 'Ζ': "Z", 'η': "n", 'Η': "H", 'μ': "u", 'Μ': "M",
	// Latin lookalikes
	'ɑ': "a", 'ɡ': "g", 'ı': "i", 'ł': "l", 'ɴ': "n", 'ɔ': "o",
	'Ⲟ': "O", 'ʀ': "R", 'ѕ': "s", 'ᴄ': "c",
	// Mathematical alphanumerics
	'𝐚': "a", '𝐛': "b", '𝐜': "c", '𝐝': "d", '𝐞': "e",
	'𝟎': "0", '𝟏': "1", '𝟐': "2", '𝟑': "3", '𝟒': "4",
	// Fullwidth (CJK halfwidth)
	'ａ': "a", 'ｂ': "b", 'ｃ': "c", 'Ａ': "A", 'Ｂ': "B",
	'０': "0", '１': "1", '９': "9",
	// Arabic
	'ا': "1", 'و': "9",
	// Number look-alikes
	'٠': "0", '۰': "0",
	// Cyrillic small letter dze
	'ѻ': "o",
	// Hyphens that aren't ASCII -
	'‐': "-", '‑': "-", '‒': "-", '–': "-", '—': "-", '−': "-",
	// Apostrophes
	'‘': "'", '’': "'", 'ʹ': "'", 'ʻ': "'", 'ʼ': "'",
}

// runeScript returns the Unicode script name for a rune.
func runeScript(r rune) string {
	if r < 0x80 {
		return "Common (ASCII)"
	}
	switch {
	case unicode.In(r, unicode.Cyrillic):
		return "Cyrillic"
	case unicode.In(r, unicode.Greek):
		return "Greek"
	case unicode.In(r, unicode.Latin):
		return "Latin (extended)"
	case unicode.In(r, unicode.Arabic):
		return "Arabic"
	case unicode.In(r, unicode.Hebrew):
		return "Hebrew"
	case unicode.In(r, unicode.Han):
		return "Han (CJK)"
	case unicode.In(r, unicode.Hiragana):
		return "Hiragana"
	case unicode.In(r, unicode.Katakana):
		return "Katakana"
	case unicode.In(r, unicode.Hangul):
		return "Hangul"
	case unicode.In(r, unicode.Thai):
		return "Thai"
	case unicode.In(r, unicode.Devanagari):
		return "Devanagari"
	case r >= 0x1D400 && r <= 0x1D7FF:
		return "Mathematical Alphanumeric"
	case r >= 0xFF00 && r <= 0xFFEF:
		return "Halfwidth/Fullwidth Forms"
	}
	return "Unknown"
}

// UnicodeHomoglyphNormalize takes a string (typically a domain) and:
//  1. Maps each char to its likely ASCII counterpart via a confusables table
//  2. Reports which chars are homoglyphs (visually similar to ASCII but
//     codepoint-different) — these enable IDN homoglyph attacks
//  3. Identifies the Unicode scripts present (mixed scripts in a single
//     domain = HUGE red flag)
//  4. Returns a verdict: safe / suspicious / likely-attack
//
// Use case: pair with `typosquat_scan` (which generates lookalikes) and
// `ct_brand_watch` (which finds newly-issued certs for lookalikes). When
// a candidate domain comes in, run this to confirm whether it's a true
// homoglyph attack vs just a non-Latin spelling.
func UnicodeHomoglyphNormalize(_ context.Context, input map[string]any) (*UnicodeHomoglyphOutput, error) {
	s, _ := input["text"].(string)
	if s == "" {
		s, _ = input["domain"].(string)
	}
	if s == "" {
		return nil, errors.New("input.text or input.domain required")
	}
	start := time.Now()

	out := &UnicodeHomoglyphOutput{Original: s, Source: "unicode_homoglyph_normalize"}
	scriptSet := map[string]bool{}
	hasASCIIOnly := true
	homoglyphCount := 0

	var b strings.Builder
	for i, r := range s {
		if r < 128 {
			// Pure ASCII
			b.WriteRune(r)
			scriptSet["Common (ASCII)"] = true
			continue
		}
		hasASCIIOnly = false
		out.NonASCIICount++
		script := runeScript(r)
		scriptSet[script] = true

		flag := HomoglyphFlag{
			Position:  i,
			Char:      string(r),
			Codepoint: fmt.Sprintf("U+%04X", r),
			Script:    script,
		}
		if asciiSub, ok := homoglyphReverseMap[r]; ok {
			b.WriteString(asciiSub)
			flag.LooksLike = asciiSub
			flag.Severity = "critical"
			homoglyphCount++
		} else {
			b.WriteRune(r) // keep as-is if no known confusable
			flag.Severity = "high"
		}
		out.Flags = append(out.Flags, flag)
	}
	out.HasNonASCII = !hasASCIIOnly
	out.Normalized = b.String()
	for s := range scriptSet {
		out.UniqueScripts = append(out.UniqueScripts, s)
	}

	// IDNA punycode encoding (xn-- form) for domain checks
	// Best-effort: simple check for standard IDNA-eligible chars
	if out.HasNonASCII && (strings.Contains(s, ".") || !strings.Contains(s, " ")) {
		out.IDNAEncoding = idnaToASCII(s)
	}

	// Verdict
	mixedScripts := len(out.UniqueScripts) > 1
	hasASCIIScript := scriptSet["Common (ASCII)"]
	hasNonLatin := false
	for k := range scriptSet {
		if k != "Common (ASCII)" && k != "Latin (extended)" {
			hasNonLatin = true
			break
		}
	}

	switch {
	case homoglyphCount >= 1 && hasASCIIScript && hasNonLatin:
		out.IsHomoglyphAttack = true
		out.Verdict = "likely-attack"
		out.Rationale = fmt.Sprintf("%d homoglyph character(s) impersonating ASCII + mixed-script (ASCII + %s) — classic IDN homoglyph attack pattern", homoglyphCount, strings.Join(filterScript(out.UniqueScripts, "Common (ASCII)"), "/"))
	case homoglyphCount >= 1:
		out.Verdict = "suspicious"
		out.Rationale = fmt.Sprintf("%d homoglyph character(s) detected — confusable with ASCII", homoglyphCount)
	case mixedScripts && hasASCIIScript && hasNonLatin:
		out.Verdict = "suspicious"
		out.Rationale = "Mixed-script string (ASCII + non-Latin) without recognized homoglyphs — non-attack but unusual"
	case out.HasNonASCII:
		out.Verdict = "safe"
		out.Rationale = "Non-ASCII characters present but no homoglyph attack pattern (legitimate non-Latin text)"
	default:
		out.Verdict = "safe"
		out.Rationale = "Pure ASCII — no homoglyph risk"
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func filterScript(xs []string, exclude string) []string {
	out := []string{}
	for _, x := range xs {
		if x != exclude {
			out = append(out, x)
		}
	}
	return out
}

// idnaToASCII does a minimal Punycode-eligible check + returns the original
// or a hint that punycode encoding would be applied. We don't pull a heavy
// IDNA library — caller can use the actual `xn--` encoded form via DNS
// resolution if needed.
func idnaToASCII(s string) string {
	// Just return a hint: domains with non-ASCII labels would be encoded
	// via Punycode. We don't compute the actual xn-- here.
	return "Use Go's golang.org/x/net/idna or `idn` CLI to compute Punycode (xn--) form"
}
