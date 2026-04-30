package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// EntityMatch is a pure-compute ER helper. No external API calls — runs
// instantly and at any rate. Provides primitives the agent uses to expand
// name-based searches across the rest of the catalog (site_snippet_search,
// github_advanced_search, sec_edgar_search, documentcloud_search, etc.).
//
// Three modes:
//
//   - "name_match"          : compare two names, return Levenshtein +
//                             Jaro-Winkler + Soundex-equality + composite
//                             similarity score (0.0–1.0). Useful for
//                             dedupe (do these two records refer to the
//                             same person?).
//
//   - "name_variations"     : given a person's name, return common
//                             nickname / formal / informal variations from
//                             an embedded ~150-entry English given-name
//                             dictionary plus phonetic Soundex equivalents.
//                             E.g. "Catherine" → ["Cathy","Kate","Katie",
//                             "Cat","Trina","Katherine","Kathryn",...].
//
//   - "username_variations" : given a full name, generate cross-platform
//                             username candidates (jdoe, j.doe, johnd,
//                             john_doe, j_doe, jdoe123, etc.) for use
//                             with maigret/sherlock/holehe/site_snippet_search.

type EntityMatchOutput struct {
	Mode               string             `json:"mode"`
	Query              string             `json:"query,omitempty"`

	// name_match output
	NameA              string             `json:"name_a,omitempty"`
	NameB              string             `json:"name_b,omitempty"`
	Levenshtein        int                `json:"levenshtein_distance,omitempty"`
	LevenshteinScore   float64            `json:"levenshtein_similarity,omitempty"` // 0..1
	JaroWinkler        float64            `json:"jaro_winkler_similarity,omitempty"`
	SoundexA           string             `json:"soundex_a,omitempty"`
	SoundexB           string             `json:"soundex_b,omitempty"`
	SoundexMatch       bool               `json:"soundex_match,omitempty"`
	CompositeScore     float64            `json:"composite_score,omitempty"`
	MatchVerdict       string             `json:"match_verdict,omitempty"` // "same"|"likely-same"|"possible-match"|"different"

	// name_variations output
	Variations         []string           `json:"variations,omitempty"`
	VariationGroups    map[string][]string `json:"variation_groups,omitempty"`

	// username_variations output
	Usernames          []string           `json:"usernames,omitempty"`

	HighlightFindings  []string           `json:"highlight_findings"`
	Source             string             `json:"source"`
	TookMs             int64              `json:"tookMs"`
	Note               string             `json:"note,omitempty"`
}

// EntityMatch is the main entry point.
func EntityMatch(_ context.Context, input map[string]any) (*EntityMatchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["name_a"]; ok {
			mode = "name_match"
		} else if _, ok := input["full_name"]; ok {
			mode = "username_variations"
		} else {
			mode = "name_variations"
		}
	}

	out := &EntityMatchOutput{
		Mode:   mode,
		Source: "entity_match (pure-compute)",
	}
	start := time.Now()

	switch mode {
	case "name_match":
		a, _ := input["name_a"].(string)
		b, _ := input["name_b"].(string)
		a = strings.TrimSpace(a)
		b = strings.TrimSpace(b)
		if a == "" || b == "" {
			return nil, fmt.Errorf("input.name_a and input.name_b required for name_match")
		}
		out.NameA = a
		out.NameB = b
		aNorm := normalizeForMatch(a)
		bNorm := normalizeForMatch(b)

		out.Levenshtein = emLevenshtein2(aNorm, bNorm)
		maxLen := len(aNorm)
		if len(bNorm) > maxLen {
			maxLen = len(bNorm)
		}
		if maxLen > 0 {
			out.LevenshteinScore = 1.0 - float64(out.Levenshtein)/float64(maxLen)
		} else {
			out.LevenshteinScore = 1.0
		}
		out.JaroWinkler = jaroWinkler(aNorm, bNorm)
		out.SoundexA = soundex(a)
		out.SoundexB = soundex(b)
		out.SoundexMatch = out.SoundexA == out.SoundexB && out.SoundexA != ""

		// Composite: 0.5 * jaroWinkler + 0.3 * levenshtein + 0.2 * soundex
		composite := 0.5*out.JaroWinkler + 0.3*out.LevenshteinScore
		if out.SoundexMatch {
			composite += 0.2
		}
		if composite > 1.0 {
			composite = 1.0
		}
		out.CompositeScore = composite
		out.MatchVerdict = matchVerdict(composite)

	case "name_variations":
		raw, _ := input["name"].(string)
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("input.name required for name_variations")
		}
		out.Query = raw
		out.VariationGroups = map[string][]string{}
		canonical := strings.ToLower(raw)
		// Strip honorifics like "Dr.", "Mr.", etc.
		canonical = stripHonorifics(canonical)
		canonical = strings.TrimSpace(canonical)
		// Use first token as the given name to expand
		first := canonical
		var rest string
		if idx := strings.IndexByte(canonical, ' '); idx > 0 {
			first = canonical[:idx]
			rest = canonical[idx:]
		}
		// Look up nickname group for this given name
		group := lookupNicknameGroup(first)
		seen := map[string]struct{}{strings.ToLower(raw): {}}
		// Always include the original
		out.Variations = append(out.Variations, raw)

		// Add each nickname-group member with the rest of the name appended
		nickVariants := []string{}
		for _, alt := range group {
			candidate := titleCase(alt) + rest
			canonCandidate := strings.ToLower(candidate)
			if _, exists := seen[canonCandidate]; exists {
				continue
			}
			seen[canonCandidate] = struct{}{}
			out.Variations = append(out.Variations, candidate)
			nickVariants = append(nickVariants, candidate)
		}
		out.VariationGroups["nicknames"] = nickVariants

		// Phonetic variations: lookup names with the same Soundex as `first`
		soundexCode := soundex(first)
		phonVariants := []string{}
		if soundexCode != "" {
			for canon, members := range nicknameGroups {
				if soundex(canon) != soundexCode {
					continue
				}
				for _, m := range members {
					candidate := titleCase(m) + rest
					canonCandidate := strings.ToLower(candidate)
					if _, exists := seen[canonCandidate]; exists {
						continue
					}
					seen[canonCandidate] = struct{}{}
					out.Variations = append(out.Variations, candidate)
					phonVariants = append(phonVariants, candidate)
				}
			}
		}
		out.VariationGroups["phonetic"] = phonVariants

		// Initial-only variants ("J. Doe", "J Doe")
		initVariants := []string{}
		if rest != "" && len(first) > 1 {
			initials := []string{
				strings.ToUpper(first[:1]) + "." + rest,
				strings.ToUpper(first[:1]) + rest,
			}
			for _, iv := range initials {
				canonIv := strings.ToLower(iv)
				if _, exists := seen[canonIv]; exists {
					continue
				}
				seen[canonIv] = struct{}{}
				out.Variations = append(out.Variations, iv)
				initVariants = append(initVariants, iv)
			}
		}
		out.VariationGroups["initials"] = initVariants

	case "username_variations":
		raw, _ := input["full_name"].(string)
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("input.full_name required for username_variations")
		}
		out.Query = raw
		canonical := stripHonorifics(strings.ToLower(raw))
		// Split on whitespace
		tokens := strings.Fields(canonical)
		// Remove suffixes (jr, sr, ii, iii)
		filtered := []string{}
		for _, t := range tokens {
			ts := strings.Trim(t, ".,")
			if ts == "jr" || ts == "sr" || ts == "ii" || ts == "iii" || ts == "iv" {
				continue
			}
			filtered = append(filtered, ts)
		}
		if len(filtered) < 1 {
			return nil, fmt.Errorf("no usable name tokens")
		}
		first := filtered[0]
		last := first
		if len(filtered) > 1 {
			last = filtered[len(filtered)-1]
		}
		seen := map[string]struct{}{}
		add := func(s string) {
			s = strings.ToLower(s)
			if s == "" {
				return
			}
			if _, ok := seen[s]; ok {
				return
			}
			seen[s] = struct{}{}
			out.Usernames = append(out.Usernames, s)
		}
		// Common patterns
		add(first + last)        // jdoe → johndoe
		add(string(first[0]) + last)
		add(first + "." + last)
		add(first + "_" + last)
		add(first + "-" + last)
		add(string(first[0]) + "." + last)
		add(string(first[0]) + "_" + last)
		add(first + string(last[0]))
		add(last + first)
		add(last + "." + first)
		add(last + string(first[0]))
		add(string(first[0]) + string(last[0]))
		// First name only
		add(first)
		add(last)
		// With common suffixes
		for _, suf := range []string{"1", "01", "123", "99", "_real"} {
			add(first + last + suf)
			add(string(first[0]) + last + suf)
			add(first + suf)
		}
		// Middle initial pattern if available
		if len(filtered) >= 3 {
			middle := filtered[1]
			if len(middle) > 0 {
				add(first + middle[:1] + last)
				add(string(first[0]) + middle[:1] + last)
			}
		}
		// Truncated forms
		if len(first) >= 4 {
			add(first[:4] + last)
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: name_match, name_variations, username_variations", mode)
	}

	out.HighlightFindings = buildEntityMatchHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// ---------- Algorithms ----------

func normalizeForMatch(s string) string {
	s = strings.ToLower(s)
	out := []rune{}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			out = append(out, r)
		}
	}
	return strings.TrimSpace(string(out))
}

func emLevenshtein2(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	la := len(ar)
	lb := len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			d1 := prev[j] + 1
			d2 := curr[j-1] + 1
			d3 := prev[j-1] + cost
			m := d1
			if d2 < m {
				m = d2
			}
			if d3 < m {
				m = d3
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func jaroWinkler(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if a == "" || b == "" {
		return 0
	}
	ar := []rune(a)
	br := []rune(b)
	la := len(ar)
	lb := len(br)
	matchDist := la
	if lb > la {
		matchDist = lb
	}
	matchDist = matchDist/2 - 1
	if matchDist < 0 {
		matchDist = 0
	}
	aMatched := make([]bool, la)
	bMatched := make([]bool, lb)
	matches := 0
	for i := 0; i < la; i++ {
		start := i - matchDist
		if start < 0 {
			start = 0
		}
		end := i + matchDist + 1
		if end > lb {
			end = lb
		}
		for j := start; j < end; j++ {
			if bMatched[j] {
				continue
			}
			if ar[i] != br[j] {
				continue
			}
			aMatched[i] = true
			bMatched[j] = true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}
	// Transpositions
	transpositions := 0
	k := 0
	for i := 0; i < la; i++ {
		if !aMatched[i] {
			continue
		}
		for !bMatched[k] {
			k++
		}
		if ar[i] != br[k] {
			transpositions++
		}
		k++
	}
	transpositions /= 2

	m := float64(matches)
	jaro := (m/float64(la) + m/float64(lb) + (m-float64(transpositions))/m) / 3.0

	// Winkler boost for common prefix up to 4 chars
	prefix := 0
	for i := 0; i < la && i < lb && i < 4; i++ {
		if ar[i] == br[i] {
			prefix++
		} else {
			break
		}
	}
	return jaro + float64(prefix)*0.1*(1.0-jaro)
}

// Soundex (American)
func soundex(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	// Strip non-letters
	out := []rune{}
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return ""
	}
	// First letter retained
	result := []rune{out[0]}
	prev := soundexCode(out[0])
	for i := 1; i < len(out); i++ {
		c := soundexCode(out[i])
		if c == '0' {
			// vowel/HW: don't emit, but also reset prev so adjacent dupes get separated
			if out[i] != 'H' && out[i] != 'W' {
				prev = '0'
			}
			continue
		}
		if c != prev {
			result = append(result, c)
		}
		prev = c
		if len(result) >= 4 {
			break
		}
	}
	for len(result) < 4 {
		result = append(result, '0')
	}
	return string(result)
}

func soundexCode(r rune) rune {
	switch r {
	case 'B', 'F', 'P', 'V':
		return '1'
	case 'C', 'G', 'J', 'K', 'Q', 'S', 'X', 'Z':
		return '2'
	case 'D', 'T':
		return '3'
	case 'L':
		return '4'
	case 'M', 'N':
		return '5'
	case 'R':
		return '6'
	default:
		return '0'
	}
}

func matchVerdict(score float64) string {
	switch {
	case score >= 0.95:
		return "same"
	case score >= 0.85:
		return "likely-same"
	case score >= 0.70:
		return "possible-match"
	default:
		return "different"
	}
}

func stripHonorifics(s string) string {
	prefixes := []string{
		"dr.", "dr ", "mr.", "mr ", "mrs.", "mrs ", "ms.", "ms ", "miss ",
		"prof.", "prof ", "professor ", "rev.", "rev ", "sir ", "lord ", "lady ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(s[len(p):])
		}
	}
	return s
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	for i := 1; i < len(r); i++ {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

// ---------- Nickname dictionary ----------

// nicknameGroups maps the canonical (formal) given name to a slice of
// alternative forms (nicknames + spelling variants). All keys + values
// stored lowercase.
//
// Curated from common English given-name nickname lists. Bidirectional
// lookup is handled by lookupNicknameGroup.
var nicknameGroups = map[string][]string{
	"alexander":  {"alex", "al", "lex", "xander", "sasha", "alec"},
	"alexandra":  {"alex", "alexa", "ali", "lexi", "sasha", "sandra"},
	"andrew":     {"andy", "drew"},
	"anthony":    {"tony", "ant"},
	"barbara":    {"barb", "barbie", "babs"},
	"benjamin":   {"ben", "benji", "benny"},
	"catherine":  {"cathy", "cat", "kate", "katie", "kit", "katy", "trina"},
	"charles":    {"charlie", "chuck", "chip", "chaz"},
	"christine":  {"chris", "chrissy", "tina"},
	"christopher": {"chris", "topher", "kris"},
	"daniel":     {"dan", "danny", "dani"},
	"david":      {"dave", "davey", "davy"},
	"deborah":    {"deb", "debbie", "debs"},
	"dorothy":    {"dot", "dotty", "dolly"},
	"edward":     {"ed", "eddie", "ted", "ned", "teddy", "ward"},
	"elizabeth":  {"liz", "beth", "betsy", "betty", "lizzy", "eliza", "ellie", "lisa", "libby"},
	"emily":      {"em", "emmy", "emi"},
	"frances":    {"fran", "frannie", "francie"},
	"francis":    {"frank", "frankie"},
	"frederick":  {"fred", "freddie", "rick"},
	"gabriel":    {"gabe", "gabby"},
	"gabrielle":  {"gabby", "gaby", "ella"},
	"george":     {"georgie"},
	"gerald":     {"gerry", "jerry"},
	"gregory":    {"greg"},
	"harold":     {"hal", "harry"},
	"helen":      {"nell", "nellie"},
	"henry":      {"hank", "hal", "harry"},
	"isabella":   {"izzy", "bella", "bell", "isa"},
	"jacob":      {"jake", "jay"},
	"james":      {"jim", "jimmy", "jamie", "jay"},
	"janet":      {"jan", "jen"},
	"jennifer":   {"jen", "jenny", "jenna"},
	"john":       {"jack", "johnny", "jon", "jonny"},
	"jonathan":   {"jon", "john", "johnny"},
	"joseph":     {"joe", "joey", "jo"},
	"joshua":     {"josh"},
	"katherine":  {"kate", "katie", "kat", "kathy", "kit"},
	"kathleen":   {"kathy", "kate", "kay"},
	"kenneth":    {"ken", "kenny"},
	"kimberly":   {"kim", "kimmy"},
	"larry":      {"laurence", "lawrence", "lar"},
	"laurence":   {"larry", "laurie"},
	"lawrence":   {"larry", "lawrie"},
	"leonard":    {"leo", "lenny", "lenny"},
	"linda":      {"lin", "lindy"},
	"madeline":   {"maddie", "maddy"},
	"margaret":   {"meg", "maggie", "marge", "peggy", "midge", "greta", "margie", "rita"},
	"matthew":    {"matt", "matty"},
	"michael":    {"mike", "mikey", "mick", "mitch"},
	"michelle":   {"mickey", "shelly", "shell"},
	"nancy":      {"nan"},
	"nathaniel":  {"nat", "nate"},
	"nicholas":   {"nick", "nicky", "klaus"},
	"olivia":     {"liv", "livvy", "ollie"},
	"pamela":     {"pam", "pammy"},
	"patricia":   {"pat", "patty", "tricia", "trish"},
	"patrick":    {"pat", "paddy", "rick"},
	"peter":      {"pete", "petey"},
	"philip":     {"phil", "pip"},
	"rebecca":    {"becca", "becky", "becks"},
	"richard":    {"dick", "rich", "rick", "ricky", "rico"},
	"robert":     {"bob", "bobby", "rob", "robbie", "bert"},
	"ronald":     {"ron", "ronnie", "ronny"},
	"samantha":   {"sam", "sammy", "samm"},
	"samuel":     {"sam", "sammy"},
	"sandra":     {"sandy", "sandi"},
	"sarah":      {"sara", "sally", "sadie"},
	"stephen":    {"steve", "stevie", "steph"},
	"steven":     {"steve", "stevie"},
	"susan":      {"sue", "susie", "suzy"},
	"theodore":   {"ted", "teddy", "theo"},
	"thomas":     {"tom", "tommy"},
	"timothy":    {"tim", "timmy"},
	"victoria":   {"vicky", "vicki", "vickie", "tori", "tory"},
	"virginia":   {"ginny", "ginger"},
	"walter":     {"walt", "wally"},
	"william":    {"bill", "billy", "will", "willy", "willie", "liam"},
	"zachary":    {"zach", "zac", "zack"},

	// Common transliteration clusters
	"muhammad":   {"mohamed", "mohammed", "mohamad", "mohamud", "mehmet"},
	"mohammed":   {"muhammad", "mohamed", "mohamad", "mehmet"},
	"yusuf":      {"yousef", "youssef", "joseph", "jusuf"},
	"hassan":     {"hasan"},
	"hussein":    {"hussain", "husayn", "hossein"},
	"alyssa":     {"alyssa", "alisa", "alisha"},
	"katarina":   {"catherine", "katherine", "kate"},
	"yekaterina": {"katherine", "catherine", "kate", "katya"},
	"natalia":    {"natalie", "nat", "talia"},
	"yelena":     {"helen", "elena", "lena", "nellie"},
	"andrei":     {"andrew", "andy"},
	"dmitry":     {"dimitri", "demetrius"},
}

// lookupNicknameGroup returns the union of (canonical, nicknames) where
// `key` is either a canonical name or one of the nickname forms.
func lookupNicknameGroup(key string) []string {
	key = strings.ToLower(strings.TrimSpace(key))
	seen := map[string]struct{}{}
	out := []string{}
	add := func(s string) {
		if s == "" || s == key {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	// Direct hit
	if g, ok := nicknameGroups[key]; ok {
		for _, n := range g {
			add(n)
		}
	}
	// Reverse: find groups that include `key` as a nickname
	for canon, nicks := range nicknameGroups {
		for _, n := range nicks {
			if n == key {
				add(canon)
				for _, sib := range nicks {
					add(sib)
				}
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func buildEntityMatchHighlights(o *EntityMatchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "name_match":
		hi = append(hi, fmt.Sprintf("✓ Comparing '%s' vs '%s'", o.NameA, o.NameB))
		hi = append(hi, fmt.Sprintf("  Levenshtein: distance %d → similarity %.4f", o.Levenshtein, o.LevenshteinScore))
		hi = append(hi, fmt.Sprintf("  Jaro-Winkler: %.4f", o.JaroWinkler))
		hi = append(hi, fmt.Sprintf("  Soundex: %s vs %s — %v", o.SoundexA, o.SoundexB, map[bool]string{true: "MATCH", false: "no match"}[o.SoundexMatch]))
		hi = append(hi, fmt.Sprintf("  Composite score: %.4f → verdict: %s", o.CompositeScore, o.MatchVerdict))

	case "name_variations":
		hi = append(hi, fmt.Sprintf("✓ %d variations for '%s'", len(o.Variations), o.Query))
		if nicks, ok := o.VariationGroups["nicknames"]; ok && len(nicks) > 0 {
			hi = append(hi, fmt.Sprintf("  nicknames (%d): %s", len(nicks), strings.Join(nicks, ", ")))
		}
		if phon, ok := o.VariationGroups["phonetic"]; ok && len(phon) > 0 {
			limit := phon
			suffix := ""
			if len(limit) > 8 {
				limit = limit[:8]
				suffix = fmt.Sprintf(" … +%d more", len(phon)-8)
			}
			hi = append(hi, fmt.Sprintf("  phonetic (%d): %s%s", len(phon), strings.Join(limit, ", "), suffix))
		}
		if ini, ok := o.VariationGroups["initials"]; ok && len(ini) > 0 {
			hi = append(hi, fmt.Sprintf("  initials: %s", strings.Join(ini, ", ")))
		}

	case "username_variations":
		hi = append(hi, fmt.Sprintf("✓ %d username candidates for '%s'", len(o.Usernames), o.Query))
		display := o.Usernames
		if len(display) > 20 {
			display = display[:20]
		}
		hi = append(hi, "  candidates: "+strings.Join(display, ", "))
		if len(o.Usernames) > 20 {
			hi = append(hi, fmt.Sprintf("  …and %d more", len(o.Usernames)-20))
		}
	}
	return hi
}
