package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

type PromptInjectionFinding struct {
	Technique     string  `json:"technique"`
	Severity      string  `json:"severity"`        // critical | high | medium | low
	Snippet       string  `json:"snippet"`
	Context       string  `json:"context,omitempty"`
	LineHint      int     `json:"line_hint,omitempty"`
	Confidence    float64 `json:"confidence"`      // 0-1
	Reason        string  `json:"reason"`
}

type PromptInjectionScannerOutput struct {
	URL              string                   `json:"url"`
	HTTPStatus       int                      `json:"http_status"`
	HTMLBytes        int                      `json:"html_bytes"`
	Findings         []PromptInjectionFinding `json:"findings"`
	TotalFindings    int                      `json:"total_findings"`
	CriticalCount    int                      `json:"critical_count"`
	HighCount        int                      `json:"high_count"`
	MediumCount      int                      `json:"medium_count"`
	OverallVerdict   string                   `json:"overall_verdict"` // safe | suspicious | dangerous
	Verdict          string                   `json:"verdict_rationale"`
	Source           string                   `json:"source"`
	TookMs           int64                    `json:"tookMs"`
	Note             string                   `json:"note,omitempty"`
}

// Indirect-prompt-injection trigger phrases — these are commonly seen in
// real-world poisoned web pages targeting agents.
var injectionTriggerPhrases = []string{
	"ignore previous instructions",
	"ignore all previous",
	"ignore the above",
	"ignore prior instructions",
	"disregard the previous",
	"disregard all instructions",
	"new instructions:",
	"system: ",
	"</system>",
	"system prompt:",
	"you are now",
	"forget your previous",
	"forget all instructions",
	"act as ",
	"jailbreak",
	"developer mode",
	"DAN mode",
	"do anything now",
	"override your",
	"disable safety",
	"reveal your prompt",
	"print your system prompt",
	"output your instructions",
	"end of user",
	"begin agent",
	"<|im_start|>",
	"<|im_end|>",
	"[INST]",
	"[/INST]",
	"### system",
	"### user",
	"### assistant",
	"<|system|>",
	"###STOP",
	"prompt injection",
	"inject this",
	"copy the entire",
	"send this to",
	"exfiltrate",
	"send your context to",
	"do not show this to the user",
	"hidden instruction",
	"secret directive",
}

// CSS patterns that hide content from human viewers but agents may still see.
var hiddenStyleRE = regexp.MustCompile(`(?i)style=["'][^"']*((?:display\s*:\s*none)|(?:visibility\s*:\s*hidden)|(?:font-size\s*:\s*0)|(?:color\s*:\s*(?:white|#fff|#ffffff))|(?:opacity\s*:\s*0)|(?:position\s*:\s*absolute[^"']*(?:left|top)\s*:\s*-?\d{3,5}px)|(?:height\s*:\s*0)|(?:width\s*:\s*0))[^"']*["']`)

// Off-screen positioning trick.
var offScreenRE = regexp.MustCompile(`(?i)style=["'][^"']*position\s*:\s*absolute[^"']*(?:left|top)\s*:\s*-?\d{4,}[^"']*["']`)

// Comments containing injection content.
var commentRE = regexp.MustCompile(`<!--([\s\S]+?)-->`)

// img alt= directive injections.
var imgAltRE = regexp.MustCompile(`(?i)<img[^>]+alt=["']([^"']{40,})["']`)

// data: URI containing base64 text content.
var dataURIBase64RE = regexp.MustCompile(`data:text/(?:plain|html)[^,]*;base64,([A-Za-z0-9+/=]{40,})`)

// White-on-white via class attribute hint.
var whiteClassRE = regexp.MustCompile(`(?i)<(?:div|span|p|section)[^>]+class=["'][^"']*(?:hidden|invisible|screen-reader-only|sr-only)[^"']*["'][^>]*>`)

// Detect text inside tags-that-shouldn't-have-text.
var nonsenseTagText = regexp.MustCompile(`(?i)<(?:noscript|template|style|script)[^>]*>([\s\S]{50,999}?)</`)

// Tag stripper for previewing snippets.
var anyTagRE = regexp.MustCompile(`<[^>]+>`)

// PromptInjectionScanner fetches a URL and scans its HTML for hidden prompt
// injections targeting visiting LLM agents. Detects:
//   - CSS-hidden text containing trigger phrases (display:none, opacity:0,
//     font-size:0, white-on-white, off-screen positioning)
//   - HTML comments containing system/user/assistant role markers or
//     ChatML/Llama tokens
//   - Suspiciously long alt= attributes containing trigger phrases
//   - Base64-encoded directives in data: URIs
//   - sr-only / screen-reader-only divs with directive content
//   - Trigger phrases in <noscript>/<template> blocks
//
// SOTA for the 2026 agent-vs-agent threat model. As LLM agents become
// commonplace web visitors, attackers plant invisible directives in pages
// to manipulate them — exfil session data, run unwanted tool calls, etc.
//
// Use case: every time an agent fetches a URL it doesn't fully trust,
// a defensive pre-pass via this tool prevents prompt-injection RCE-equivalent.
func PromptInjectionScanner(ctx context.Context, input map[string]any) (*PromptInjectionScannerOutput, error) {
	urlIn, _ := input["url"].(string)
	urlIn = strings.TrimSpace(urlIn)
	if urlIn == "" {
		return nil, errors.New("input.url required")
	}
	if !strings.HasPrefix(urlIn, "http://") && !strings.HasPrefix(urlIn, "https://") {
		urlIn = "https://" + urlIn
	}

	// Optional raw HTML — agent can pass HTML directly to skip the fetch
	// (useful for chaining with firecrawl_scrape on SPAs).
	rawHTML, _ := input["html"].(string)

	start := time.Now()
	out := &PromptInjectionScannerOutput{URL: urlIn, Source: "prompt_injection_scanner"}

	if rawHTML == "" {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, urlIn, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch failed: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		rawHTML = string(body)
		out.HTTPStatus = resp.StatusCode
	}
	out.HTMLBytes = len(rawHTML)
	low := strings.ToLower(rawHTML)

	// 1. Hidden-styled text containing trigger phrases.
	for _, m := range hiddenStyleRE.FindAllStringIndex(rawHTML, -1) {
		block := extractTagBlock(rawHTML, m[0])
		blockLow := strings.ToLower(block)
		for _, trigger := range injectionTriggerPhrases {
			if strings.Contains(blockLow, strings.ToLower(trigger)) {
				out.Findings = append(out.Findings, PromptInjectionFinding{
					Technique:  "css-hidden-with-injection",
					Severity:   "critical",
					Snippet:    truncate(stripTags(block), 250),
					Context:    truncate(block, 350),
					Confidence: 0.95,
					Reason:     fmt.Sprintf("CSS-hidden element contains trigger: %q", trigger),
				})
				break
			}
		}
	}

	// 2. Off-screen text containing trigger phrases.
	for _, m := range offScreenRE.FindAllStringIndex(rawHTML, -1) {
		block := extractTagBlock(rawHTML, m[0])
		blockLow := strings.ToLower(block)
		for _, trigger := range injectionTriggerPhrases {
			if strings.Contains(blockLow, strings.ToLower(trigger)) {
				out.Findings = append(out.Findings, PromptInjectionFinding{
					Technique:  "off-screen-positioned-injection",
					Severity:   "critical",
					Snippet:    truncate(stripTags(block), 250),
					Confidence: 0.95,
					Reason:     fmt.Sprintf("Off-screen element contains trigger: %q", trigger),
				})
				break
			}
		}
	}

	// 3. sr-only / hidden-class with trigger content.
	for _, m := range whiteClassRE.FindAllStringIndex(rawHTML, -1) {
		block := extractTagBlock(rawHTML, m[0])
		blockLow := strings.ToLower(block)
		for _, trigger := range injectionTriggerPhrases {
			if strings.Contains(blockLow, strings.ToLower(trigger)) {
				out.Findings = append(out.Findings, PromptInjectionFinding{
					Technique:  "sr-only-class-with-injection",
					Severity:   "high",
					Snippet:    truncate(stripTags(block), 250),
					Confidence: 0.85,
					Reason:     fmt.Sprintf("sr-only/hidden-class element contains trigger: %q", trigger),
				})
				break
			}
		}
	}

	// 4. Comments containing trigger phrases (very high false-positive in
	// developer commentary, but agents reading comments is a real attack).
	for _, m := range commentRE.FindAllStringSubmatch(rawHTML, -1) {
		if len(m) < 2 {
			continue
		}
		commentText := strings.ToLower(m[1])
		for _, trigger := range injectionTriggerPhrases {
			tLow := strings.ToLower(trigger)
			if strings.Contains(commentText, tLow) {
				severity := "medium"
				// ChatML/Llama tokens in comments = high
				if strings.HasPrefix(tLow, "<|") || strings.HasPrefix(tLow, "[inst") || strings.HasPrefix(tLow, "###") {
					severity = "high"
				}
				out.Findings = append(out.Findings, PromptInjectionFinding{
					Technique:  "html-comment-injection",
					Severity:   severity,
					Snippet:    truncate(strings.TrimSpace(m[1]), 250),
					Confidence: 0.6,
					Reason:     fmt.Sprintf("HTML comment contains trigger: %q", trigger),
				})
				break
			}
		}
	}

	// 5. Long alt= attributes containing triggers.
	for _, m := range imgAltRE.FindAllStringSubmatch(rawHTML, -1) {
		if len(m) < 2 {
			continue
		}
		altLow := strings.ToLower(m[1])
		for _, trigger := range injectionTriggerPhrases {
			if strings.Contains(altLow, strings.ToLower(trigger)) {
				out.Findings = append(out.Findings, PromptInjectionFinding{
					Technique:  "img-alt-injection",
					Severity:   "high",
					Snippet:    truncate(m[1], 250),
					Confidence: 0.85,
					Reason:     fmt.Sprintf("img alt= contains trigger: %q (alt is read by agents but invisible to humans)", trigger),
				})
				break
			}
		}
	}

	// 6. Base64 directives in data: URIs.
	for _, m := range dataURIBase64RE.FindAllStringSubmatch(rawHTML, -1) {
		if len(m) < 2 {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil {
			continue
		}
		dec := strings.ToLower(string(decoded))
		for _, trigger := range injectionTriggerPhrases {
			if strings.Contains(dec, strings.ToLower(trigger)) {
				out.Findings = append(out.Findings, PromptInjectionFinding{
					Technique:  "base64-data-uri-injection",
					Severity:   "critical",
					Snippet:    truncate(string(decoded), 250),
					Confidence: 0.95,
					Reason:     fmt.Sprintf("base64-decoded data URI contains trigger: %q", trigger),
				})
				break
			}
		}
	}

	// 7. <noscript>/<template> with directive content.
	for _, m := range nonsenseTagText.FindAllStringSubmatch(rawHTML, -1) {
		if len(m) < 2 {
			continue
		}
		txtLow := strings.ToLower(m[1])
		for _, trigger := range injectionTriggerPhrases {
			if strings.Contains(txtLow, strings.ToLower(trigger)) {
				out.Findings = append(out.Findings, PromptInjectionFinding{
					Technique:  "non-rendered-tag-injection",
					Severity:   "high",
					Snippet:    truncate(strings.TrimSpace(m[1]), 250),
					Confidence: 0.7,
					Reason:     fmt.Sprintf("non-rendered tag (<noscript>/<template>/etc) contains trigger: %q", trigger),
				})
				break
			}
		}
	}

	// 8. Direct trigger-phrase scan on visible body text (lowest-confidence;
	// flags article content that may be discussing prompt injection rather
	// than executing one).
	for _, trigger := range injectionTriggerPhrases {
		tLow := strings.ToLower(trigger)
		idx := strings.Index(low, tLow)
		if idx < 0 {
			continue
		}
		// Skip if already caught above
		alreadyCaught := false
		for _, f := range out.Findings {
			if strings.Contains(strings.ToLower(f.Snippet), tLow) {
				alreadyCaught = true
				break
			}
		}
		if alreadyCaught {
			continue
		}
		startIdx := max3(0, idx-80)
		endIdx := minInt(idx+len(trigger)+80, len(rawHTML))
		out.Findings = append(out.Findings, PromptInjectionFinding{
			Technique:  "trigger-phrase-in-visible-text",
			Severity:   "low",
			Snippet:    truncate(rawHTML[startIdx:endIdx], 250),
			Confidence: 0.3,
			Reason:     fmt.Sprintf("trigger phrase %q in visible page text — could be benign discussion or actual injection", trigger),
		})
		break // only flag once per scan to avoid noise
	}

	// Aggregate counts + verdict.
	severityRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(out.Findings, func(i, j int) bool {
		ra, rb := severityRank[out.Findings[i].Severity], severityRank[out.Findings[j].Severity]
		if ra != rb {
			return ra < rb
		}
		return out.Findings[i].Confidence > out.Findings[j].Confidence
	})

	for _, f := range out.Findings {
		switch f.Severity {
		case "critical":
			out.CriticalCount++
		case "high":
			out.HighCount++
		case "medium":
			out.MediumCount++
		}
	}
	out.TotalFindings = len(out.Findings)

	switch {
	case out.CriticalCount > 0:
		out.OverallVerdict = "dangerous"
		out.Verdict = fmt.Sprintf("⛔ %d critical-severity injection technique(s) detected — DO NOT pass this content to an LLM agent without sanitization", out.CriticalCount)
	case out.HighCount > 0:
		out.OverallVerdict = "suspicious"
		out.Verdict = fmt.Sprintf("⚠️  %d high-severity finding(s) — sanitize before LLM consumption", out.HighCount)
	case out.MediumCount > 0:
		out.OverallVerdict = "suspicious"
		out.Verdict = fmt.Sprintf("⚠️  %d medium-severity finding(s) — manual review recommended", out.MediumCount)
	case out.TotalFindings > 0:
		out.OverallVerdict = "suspicious"
		out.Verdict = "Low-confidence trigger phrases detected — likely benign discussion of prompt injection, but verify."
	default:
		out.OverallVerdict = "safe"
		out.Verdict = "No prompt-injection patterns detected. Content appears safe for LLM consumption."
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// extractTagBlock returns the substring containing a tag opening and its
// rough text content (best-effort, doesn't fully parse HTML).
func extractTagBlock(html string, openIdx int) string {
	end := openIdx
	depth := 0
	maxScan := minInt(openIdx+2000, len(html))
	// Find close of opening tag.
	for end < maxScan && html[end] != '>' {
		end++
	}
	if end >= maxScan {
		return html[openIdx:maxScan]
	}
	// Scan forward looking for matching close.
	depth = 1
	scan := end + 1
	for scan < maxScan-1 {
		if html[scan] == '<' && scan+1 < maxScan && html[scan+1] == '/' {
			depth--
			if depth <= 0 {
				closeEnd := scan
				for closeEnd < maxScan && html[closeEnd] != '>' {
					closeEnd++
				}
				return html[openIdx:minInt(closeEnd+1, maxScan)]
			}
		} else if html[scan] == '<' && scan+1 < maxScan && html[scan+1] != '/' {
			depth++
		}
		scan++
	}
	return html[openIdx:maxScan]
}

func stripTags(s string) string {
	return strings.TrimSpace(anyTagRE.ReplaceAllString(s, " "))
}

func max3(a, b int) int {
	if a > b {
		return a
	}
	return b
}
