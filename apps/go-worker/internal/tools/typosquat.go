package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type TypoCandidate struct {
	Domain     string   `json:"domain"`
	Method     string   `json:"method"` // generation algorithm
	Resolved   bool     `json:"resolved"`
	A          []string `json:"a,omitempty"`
	AAAA       []string `json:"aaaa,omitempty"`
	MXPresent  bool     `json:"mx_present,omitempty"`  // MX records configured → mailbox-capable phishing
	Idn        bool     `json:"idn,omitempty"`         // contains non-ASCII (homoglyph)
}

type TypoSquatOutput struct {
	Target          string          `json:"target"`
	GeneratedCount  int             `json:"generated_count"`
	ResolvedCount   int             `json:"resolved_count"`
	WithMXCount     int             `json:"with_mx_count"`
	IDNCount        int             `json:"idn_count"`
	Registered      []TypoCandidate `json:"registered"`
	MethodBreakdown map[string]int  `json:"method_breakdown"`
	TLDsTried       []string        `json:"tlds_tried,omitempty"`
	TookMs          int64           `json:"tookMs"`
	Source          string          `json:"source"`
	Note            string          `json:"note,omitempty"`
}

// commonTLDs — the high-frequency typosquat TLDs. Used by tldSwap algorithm.
var commonTLDs = []string{
	"com", "net", "org", "io", "co", "app", "dev", "ai", "cc", "me",
	"info", "biz", "xyz", "online", "site", "shop", "store", "cloud",
	"tech", "click", "link",
}

// homoglyphMap — high-frequency confusable Unicode characters. Each ASCII
// char maps to visually-identical alternatives that are valid in IDN domains.
// Covers the cases that produce the highest user-deception rate.
var homoglyphMap = map[rune][]rune{
	'a': {'а', 'ӕ'},                    // Cyrillic а, ӕ
	'b': {'Ƅ'},                              // Latin Ƅ
	'c': {'с', 'ϲ'},                    // Cyrillic с, Greek ϲ
	'd': {'ԁ'},                              // Cyrillic ԁ
	'e': {'е', 'ҽ'},                    // Cyrillic е, ҽ
	'g': {'ց'},                              // Armenian ց
	'h': {'һ', 'հ'},                    // Cyrillic һ, Armenian հ
	'i': {'і', 'Ї', 'Ӏ'},          // Ukrainian і, Yi component
	'j': {'ј'},                              // Cyrillic ј
	'k': {'к'},                              // Cyrillic к
	'l': {'ӏ', 'ə'},                    // Cyrillic Ӏ, schwa
	'm': {'м'},                              // Cyrillic м
	'n': {'ո'},                              // Armenian ո
	'o': {'о', 'ο', 'օ'},          // Cyrillic о, Greek ο, Armenian օ
	'p': {'р'},                              // Cyrillic р
	'q': {'ԛ'},                              // Cyrillic ԛ
	'r': {'г'},                              // Cyrillic г (visual)
	's': {'Ѕ'},                              // Cyrillic ѕ
	't': {'т'},                              // Cyrillic т (rough)
	'u': {'υ', 'ս'},                    // Greek υ, Armenian ս
	'v': {'ν'},                              // Greek ν
	'w': {'ѡ', 'ԝ'},                    // Cyrillic ѡ, ԝ
	'x': {'х', 'ҳ'},                    // Cyrillic х, ҳ
	'y': {'у', 'ү'},                    // Cyrillic у, ү
	'z': {'ʐ'},                              // Latin ʐ
	'0': {'О', 'о'},                    // Cyrillic О / о
	'1': {'l', 'I'},                              // ASCII confusables
}

// qwertyNeighbors — keyboard-adjacency substitution (highest-frequency typos).
var qwertyNeighbors = map[rune]string{
	'q': "wa", 'w': "qeas", 'e': "wrds", 'r': "etdf", 't': "ryfg",
	'y': "tugh", 'u': "yihj", 'i': "uojk", 'o': "ipkl", 'p': "ol",
	'a': "qwsz", 's': "wedaxz", 'd': "erfsxc", 'f': "rtgdcv", 'g': "tyhfvb",
	'h': "yujgbn", 'j': "uikhnm", 'k': "iolm", 'l': "kop",
	'z': "asx", 'x': "zsdc", 'c': "xdfv", 'v': "cfgb", 'b': "vghn",
	'n': "bhjm", 'm': "njk",
}

// TyposquatScan generates plausible typosquat domains for the target via 9
// dnstwist-class algorithms (replacement, omission, insertion, transposition,
// repetition, homoglyph IDN, bitsquat, TLD swap, hyphen insertion), then
// resolves each via DNS in parallel. Returns only registered/resolving names
// with their A/AAAA/MX records.
//
// Output flags:
//   `idn:true`         → contains non-ASCII Unicode (IDN homograph attack)
//   `mx_present:true`  → has MX records → mailbox-capable phishing infra
//
// The agent should follow up on registered hits with favicon_pivot (to confirm
// brand-favicon copying = phishing) and http_probe (to grab the live title).
func TyposquatScan(ctx context.Context, input map[string]any) (*TypoSquatOutput, error) {
	target, _ := input["target"].(string)
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return nil, errors.New("input.target required (apex domain, e.g. \"vurvey.app\")")
	}
	parts := strings.SplitN(target, ".", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("input.target must include a TLD (got %q)", target)
	}
	stem, tld := parts[0], parts[1]

	checkMX := true
	if v, ok := input["check_mx"].(bool); ok {
		checkMX = v
	}
	concurrency := 40
	if v, ok := input["concurrency"].(float64); ok && v > 0 {
		concurrency = int(v)
	}
	maxCandidates := 1500
	if v, ok := input["max_candidates"].(float64); ok && v > 0 {
		maxCandidates = int(v)
	}

	start := time.Now()

	// Generate candidate domain → algorithm map.
	candidates := map[string]string{} // domain → method
	add := func(stem, tldUse, method string) {
		if stem == "" || tldUse == "" {
			return
		}
		// DNS is case-insensitive ("Vurvey.app" == "vurvey.app"), so lowercase
		// before comparison + storage. Eliminates the bitsquat false positives
		// where 'a' (0x61) → 'A' (0x41) produces a single-bit-flipped uppercase
		// variant that resolves back to the original target.
		d := strings.ToLower(stem + "." + tldUse)
		if d == target {
			return
		}
		if _, ok := candidates[d]; !ok {
			candidates[d] = method
		}
	}

	// 1. character omission (vurvey → vrvey, vurey, urvey)
	for i := 0; i < len(stem); i++ {
		add(stem[:i]+stem[i+1:], tld, "omission")
	}
	// 2. transposition (vurvey → vruvey, vuervy, …)
	for i := 0; i < len(stem)-1; i++ {
		s := []byte(stem)
		s[i], s[i+1] = s[i+1], s[i]
		add(string(s), tld, "transposition")
	}
	// 3. repetition (vurvey → vurvvey)
	for i := 0; i < len(stem); i++ {
		add(stem[:i+1]+string(stem[i])+stem[i+1:], tld, "repetition")
	}
	// 4. character insertion — qwerty-adjacent (vurvey → vutrvey)
	for i := 0; i < len(stem); i++ {
		neighbors, ok := qwertyNeighbors[rune(stem[i])]
		if !ok {
			continue
		}
		for _, n := range neighbors {
			add(stem[:i]+string(n)+stem[i:], tld, "insertion")
		}
	}
	// 5. replacement — qwerty-adjacent (vurvey → vurvyy)
	for i := 0; i < len(stem); i++ {
		neighbors, ok := qwertyNeighbors[rune(stem[i])]
		if !ok {
			continue
		}
		for _, n := range neighbors {
			add(stem[:i]+string(n)+stem[i+1:], tld, "replacement")
		}
	}
	// 6. bitsquat — single bit flip in each ASCII char (catches DNS-cache poisoning + memory errors)
	for i := 0; i < len(stem); i++ {
		c := stem[i]
		for bit := 0; bit < 7; bit++ {
			flipped := c ^ (1 << bit)
			if flipped < 0x21 || flipped > 0x7e {
				continue
			}
			r := rune(flipped)
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
				continue
			}
			add(stem[:i]+string(flipped)+stem[i+1:], tld, "bitsquat")
		}
	}
	// 7. homoglyph — replace each char with a Unicode confusable (IDN attacks)
	for i := 0; i < len(stem); i++ {
		alts, ok := homoglyphMap[rune(stem[i])]
		if !ok {
			continue
		}
		for _, alt := range alts {
			add(stem[:i]+string(alt)+stem[i+1:], tld, "homoglyph_idn")
		}
	}
	// 8. TLD swap — vurvey.app → vurvey.com, vurvey.io, etc.
	for _, t := range commonTLDs {
		if t == tld {
			continue
		}
		add(stem, t, "tld_swap")
	}
	// 9. hyphen insertion — vurvey → vu-rvey, v-urvey, vurvey- (latter rejected by registrars)
	for i := 1; i < len(stem); i++ {
		add(stem[:i]+"-"+stem[i:], tld, "hyphen_insertion")
	}
	// 10. subdomain-split — vurvey.app → vurvey-app.com, vurveyapp.com
	add(stem+"-"+tld, "com", "subdomain_split")
	add(stem+tld, "com", "subdomain_split")

	if len(candidates) > maxCandidates {
		// Trim deterministically — keep most-likely-malicious algorithms first.
		priority := map[string]int{
			"homoglyph_idn": 0, "tld_swap": 1, "replacement": 2, "omission": 3,
			"transposition": 4, "insertion": 5, "repetition": 6, "bitsquat": 7,
			"hyphen_insertion": 8, "subdomain_split": 9,
		}
		type kv struct{ k, v string }
		all := make([]kv, 0, len(candidates))
		for k, v := range candidates {
			all = append(all, kv{k, v})
		}
		sort.Slice(all, func(i, j int) bool { return priority[all[i].v] < priority[all[j].v] })
		all = all[:maxCandidates]
		candidates = map[string]string{}
		for _, p := range all {
			candidates[p.k] = p.v
		}
	}

	out := &TypoSquatOutput{
		Target:          target,
		GeneratedCount:  len(candidates),
		Registered:      []TypoCandidate{},
		MethodBreakdown: map[string]int{},
		TLDsTried:       commonTLDs,
		Source:          "typosquat_scan (dnstwist-style algorithms, pure Go)",
	}

	// Resolve all candidates in parallel.
	type job struct {
		domain, method string
	}
	jobs := make([]job, 0, len(candidates))
	for d, m := range candidates {
		jobs = append(jobs, job{d, m})
	}
	results := make(chan TypoCandidate, len(jobs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	resolver := &net.Resolver{PreferGo: true}

	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			tc := TypoCandidate{Domain: j.domain, Method: j.method}
			// Detect IDN
			for _, r := range j.domain {
				if r > 127 {
					tc.Idn = true
					break
				}
			}
			ips, err := resolver.LookupIPAddr(cctx, j.domain)
			if err != nil || len(ips) == 0 {
				return
			}
			tc.Resolved = true
			for _, ip := range ips {
				if ip.IP.To4() != nil {
					tc.A = append(tc.A, ip.IP.String())
				} else {
					tc.AAAA = append(tc.AAAA, ip.IP.String())
				}
			}
			if checkMX {
				mxs, err := resolver.LookupMX(cctx, j.domain)
				if err == nil && len(mxs) > 0 {
					tc.MXPresent = true
				}
			}
			results <- tc
		}(j)
	}
	go func() { wg.Wait(); close(results) }()

	for r := range results {
		out.Registered = append(out.Registered, r)
		out.MethodBreakdown[r.Method]++
		if r.MXPresent {
			out.WithMXCount++
		}
		if r.Idn {
			out.IDNCount++
		}
	}
	out.ResolvedCount = len(out.Registered)
	sort.Slice(out.Registered, func(i, j int) bool {
		// Sort: MX-present first (mailbox-capable = higher threat), then IDN, then alphabetical.
		a, b := out.Registered[i], out.Registered[j]
		if a.MXPresent != b.MXPresent {
			return a.MXPresent
		}
		if a.Idn != b.Idn {
			return a.Idn
		}
		return a.Domain < b.Domain
	})
	out.TookMs = time.Since(start).Milliseconds()
	out.Note = "follow up on registered hits with `favicon_pivot` to detect brand-favicon copying (high-confidence phishing) and `http_probe` to capture live page title/server"
	return out, nil
}
