package tools

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/unicode/norm"
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
	Mode  string `json:"mode"`
	Query string `json:"query,omitempty"`

	// name_match output
	NameA            string  `json:"name_a,omitempty"`
	NameB            string  `json:"name_b,omitempty"`
	Levenshtein      int     `json:"levenshtein_distance,omitempty"`
	LevenshteinScore float64 `json:"levenshtein_similarity,omitempty"` // 0..1
	JaroWinkler      float64 `json:"jaro_winkler_similarity,omitempty"`
	SoundexA         string  `json:"soundex_a,omitempty"`
	SoundexB         string  `json:"soundex_b,omitempty"`
	SoundexMatch     bool    `json:"soundex_match,omitempty"`
	CompositeScore   float64 `json:"composite_score,omitempty"`
	MatchVerdict     string  `json:"match_verdict,omitempty"` // "same"|"likely-same"|"possible-match"|"different"

	// name_variations output
	Variations      []string            `json:"variations,omitempty"`
	VariationGroups map[string][]string `json:"variation_groups,omitempty"`

	// username_variations output
	Usernames []string `json:"usernames,omitempty"`

	// email_canonicalize output
	EmailOriginal    string   `json:"email_original,omitempty"`
	EmailCanonical   string   `json:"email_canonical,omitempty"`   // deliverable form (lowercased + googlemail→gmail + +tag stripped)
	EmailMailboxKey  string   `json:"email_mailbox_key,omitempty"` // strongest dedup key (canonical + gmail dot-stripping)
	EmailLocal       string   `json:"email_local,omitempty"`
	EmailDomain      string   `json:"email_domain,omitempty"`
	EmailProvider    string   `json:"email_provider,omitempty"` // gmail|outlook|yahoo|icloud|proton|other
	EmailValid       bool     `json:"email_valid,omitempty"`
	EmailAliases     []string `json:"email_aliases,omitempty"` // alternate forms that resolve to the same mailbox
	EmailHadPlusTag  bool     `json:"email_had_plus_tag,omitempty"`
	EmailHadDotTrick bool     `json:"email_had_dot_trick,omitempty"` // gmail dot-aliasing was applied

	// phone_canonicalize output
	PhoneOriginal    string `json:"phone_original,omitempty"`
	PhoneE164        string `json:"phone_e164,omitempty"`         // canonical "+CC<digits>" — primary dedup key
	PhoneDigits      string `json:"phone_digits,omitempty"`       // digits only, no '+'
	PhoneCountryCode string `json:"phone_country_code,omitempty"` // numeric country code, e.g. "1", "44", "49"
	PhoneNational    string `json:"phone_national,omitempty"`     // digits without country prefix
	PhoneRegion      string `json:"phone_region,omitempty"`       // ISO-3166-1 alpha-2 best-guess: "US", "GB", "DE", … or "" if unknown
	PhoneExtension   string `json:"phone_extension,omitempty"`    // captured "ext 123" / "x123" / ";ext=123" / "p123"
	PhoneValid       bool   `json:"phone_valid,omitempty"`
	PhoneTollFree    bool   `json:"phone_toll_free,omitempty"` // NANP: starts with 800/833/844/855/866/877/888

	// social_canonicalize output
	SocialOriginal     string `json:"social_original,omitempty"`
	SocialPlatform     string `json:"social_platform,omitempty"` // twitter|instagram|tiktok|linkedin|github|reddit|youtube|facebook|mastodon|bluesky|threads|unknown
	SocialHandle       string `json:"social_handle,omitempty"`   // canonical handle (no @, lowercased for case-insensitive platforms)
	SocialKey          string `json:"social_key,omitempty"`      // dedup primary key: "<platform>:<handle>"
	SocialCanonicalURL string `json:"social_canonical_url,omitempty"`
	SocialValid        bool   `json:"social_valid,omitempty"`

	// url_canonicalize output
	URLOriginal       string   `json:"url_original,omitempty"`
	URLCanonical      string   `json:"url_canonical,omitempty"` // strongest dedup key
	URLScheme         string   `json:"url_scheme,omitempty"`    // forced to https unless URLSchemeFixed=true
	URLHost           string   `json:"url_host,omitempty"`      // lowercased, www-stripped, default-port-stripped, IDN→punycode
	URLPath           string   `json:"url_path,omitempty"`
	URLValid          bool     `json:"url_valid,omitempty"`
	URLRemovedParams  []string `json:"url_removed_params,omitempty"`  // tracking params dropped (utm_*, fbclid, gclid, …)
	URLOriginalScheme string   `json:"url_original_scheme,omitempty"` // surfaced when canonicalization downgraded http→https

	// ip_canonicalize output
	IPOriginal        string `json:"ip_original,omitempty"`
	IPCanonical       string `json:"ip_canonical,omitempty"` // strongest dedup key
	IPVersion         int    `json:"ip_version,omitempty"`   // 4 or 6
	IPClass           string `json:"ip_class,omitempty"`     // public|private|loopback|link-local|multicast|reserved|cgnat|documentation|unspecified
	IPValid           bool   `json:"ip_valid,omitempty"`
	IPIs4in6          bool   `json:"ip_is_4in6,omitempty"`           // input was IPv4-in-IPv6 (e.g., "::ffff:1.2.3.4")
	IPHadLeadingZeros bool   `json:"ip_had_leading_zeros,omitempty"` // input had leading-zero octets
	IPHadBrackets     bool   `json:"ip_had_brackets,omitempty"`      // input was bracket-wrapped (e.g., "[2001:db8::1]")
	IPSlashPrefix     int    `json:"ip_slash_prefix,omitempty"`      // CIDR prefix length if input was "addr/N"

	// domain_canonicalize output
	DomainOriginal     string `json:"domain_original,omitempty"`
	DomainCanonical    string `json:"domain_canonical,omitempty"`     // lowercased + IDN→punycode + trailing-dot-stripped
	DomainApex         string `json:"domain_apex,omitempty"`          // eTLD+1 — strongest "is this the same org?" dedup key
	DomainSubdomain    string `json:"domain_subdomain,omitempty"`     // everything before the apex (e.g. "mail" or "deeply.nested" or "" for apex itself)
	DomainPublicSuffix string `json:"domain_public_suffix,omitempty"` // the eTLD itself (e.g. "co.uk", "com")
	DomainValid        bool   `json:"domain_valid,omitempty"`
	DomainIsApex       bool   `json:"domain_is_apex,omitempty"` // true iff input had no subdomain (e.g. "example.com" not "www.example.com")
	DomainICANN        bool   `json:"domain_icann,omitempty"`   // true iff the public suffix is in the ICANN section of the PSL (excludes private suffixes like "blogspot.com")

	HighlightFindings []string `json:"highlight_findings"`
	Source            string   `json:"source"`
	TookMs            int64    `json:"tookMs"`
	Note              string   `json:"note,omitempty"`
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
		} else if _, ok := input["email"]; ok {
			mode = "email_canonicalize"
		} else if _, ok := input["phone"]; ok {
			mode = "phone_canonicalize"
		} else if _, ok := input["social"]; ok {
			mode = "social_canonicalize"
		} else if _, ok := input["url"]; ok {
			mode = "url_canonicalize"
		} else if _, ok := input["domain"]; ok {
			mode = "domain_canonicalize"
		} else if _, ok := input["ip"]; ok {
			mode = "ip_canonicalize"
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
		// Soundex on the normalized form so that accented variants
		// produce the same code (e.g. "Strauß" → "strauss" → S620).
		out.SoundexA = soundex(aNorm)
		out.SoundexB = soundex(bNorm)
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
		// Apply the Unicode/accent-fold normalizer (iter-2) before tokenizing
		// so "Łukasz Pawełczak" → "lukasz pawelczak" and "François Côté" →
		// "francois cote". Username platforms generally only accept ASCII
		// + dash/underscore, so emitting accented variants is wasted work
		// and silently misses real handles.
		//
		// IMPORTANT: normalizeForMatch strips non-letter/digit/space chars
		// (including hyphens). For hyphenated surnames like
		// "Garcia-Lopez" we want to detect the hyphen FIRST, then fold
		// each piece separately. See
		// TestUsernameVariations_InternationalRecallQuantitative.
		canonical := stripHonorifics(strings.ToLower(raw))
		// Tokenize on whitespace BEFORE folding, so the hyphen survives
		// long enough to be detected on the last token.
		tokensRaw := strings.Fields(canonical)
		tokens := make([]string, 0, len(tokensRaw))
		for _, t := range tokensRaw {
			// Fold each token individually; hyphen is preserved here
			// because we re-fold below per-half AFTER splitting.
			folded := normalizeForMatch(strings.ReplaceAll(t, "-", " ")) // hyphen → space for fold safety
			// But we ALSO need the original-with-hyphen-stripped-of-accents
			// form so the hyphen-detect path below can split it. Recompose:
			pieces := strings.Fields(folded)
			joined := strings.Join(pieces, "-")
			if !strings.Contains(t, "-") {
				joined = folded // no hyphen in original → use plain fold
			}
			tokens = append(tokens, joined)
		}
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
		// Rune-aware first-letter extraction. The legacy code used
		// `string(first[0])` which indexes BYTES — for any multi-byte
		// UTF-8 character (Ł, Ç, ñ, …) this produced an invalid string
		// fragment. Even though normalizeForMatch above folds most of
		// those, this guards against edge cases and CJK input that
		// the fold doesn't cover.
		firstInitial := func(s string) string {
			for _, r := range s {
				return string(r)
			}
			return ""
		}
		fInit := firstInitial(first)
		_ = firstInitial(last) // last-initial computed per-variant below

		// Hyphenated-surname expansion: when a last name contains a
		// hyphen ("Garcia-Lopez", "Hernandez-Smith"), platform users
		// typically register the FIRST half, the SECOND half, OR the
		// concatenated form depending on platform constraints. Emit
		// candidates for each piece.
		lastTokens := []string{last}
		if strings.Contains(last, "-") {
			parts := strings.Split(last, "-")
			lastTokens = append(lastTokens, parts...)                          // each part as standalone
			lastTokens = append(lastTokens, strings.ReplaceAll(last, "-", "")) // concatenated
		}

		// Generate the username pattern set for each (first, last_variant).
		for _, lt := range lastTokens {
			if lt == "" {
				continue
			}
			ltInit := firstInitial(lt)
			// Common patterns
			add(first + lt)
			add(fInit + lt)
			add(first + "." + lt)
			add(first + "_" + lt)
			add(first + "-" + lt)
			add(fInit + "." + lt)
			add(fInit + "_" + lt)
			add(first + ltInit)
			add(lt + first)
			add(lt + "." + first)
			add(lt + first + "1")
			add(lt + fInit)
			add(fInit + ltInit)
			// First name only
			add(first)
			add(lt)
			// With common numeric/profile suffixes (only for the canonical last)
			if lt == last {
				for _, suf := range []string{"1", "01", "123", "99", "_real"} {
					add(first + lt + suf)
					add(fInit + lt + suf)
					add(first + suf)
				}
				// Middle initial pattern if available
				if len(filtered) >= 3 {
					middle := filtered[1]
					mInit := firstInitial(middle)
					if mInit != "" {
						add(first + mInit + lt)
						add(fInit + mInit + lt)
						add(fInit + mInit + ltInit)
					}
				}
				// Truncated first-name forms
				if utf8RuneLen(first) >= 4 {
					add(runePrefix(first, 4) + lt)
				}
			}
		}

	case "email_canonicalize":
		raw, _ := input["email"].(string)
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("input.email required for email_canonicalize")
		}
		out.Query = raw
		canon := canonicalizeEmail(raw)
		out.EmailOriginal = raw
		out.EmailValid = canon.Valid
		out.EmailCanonical = canon.Canonical
		out.EmailMailboxKey = canon.MailboxKey
		out.EmailLocal = canon.Local
		out.EmailDomain = canon.Domain
		out.EmailProvider = canon.Provider
		out.EmailAliases = canon.Aliases
		out.EmailHadPlusTag = canon.HadPlusTag
		out.EmailHadDotTrick = canon.HadDotTrick

	case "phone_canonicalize":
		raw, _ := input["phone"].(string)
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("input.phone required for phone_canonicalize")
		}
		out.Query = raw
		ph := canonicalizePhone(raw)
		out.PhoneOriginal = raw
		out.PhoneValid = ph.Valid
		out.PhoneE164 = ph.E164
		out.PhoneDigits = ph.Digits
		out.PhoneCountryCode = ph.CountryCode
		out.PhoneNational = ph.National
		out.PhoneRegion = ph.Region
		out.PhoneExtension = ph.Extension
		out.PhoneTollFree = ph.TollFree

	case "social_canonicalize":
		raw, _ := input["social"].(string)
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("input.social required for social_canonicalize")
		}
		// Optional explicit platform hint — used when the input is just a
		// bare handle ("@johndoe") with no URL to disambiguate.
		hint, _ := input["platform"].(string)
		out.Query = raw
		soc := canonicalizeSocial(raw, hint)
		out.SocialOriginal = raw
		out.SocialValid = soc.Valid
		out.SocialPlatform = soc.Platform
		out.SocialHandle = soc.Handle
		out.SocialKey = soc.Key
		out.SocialCanonicalURL = soc.CanonicalURL

	case "url_canonicalize":
		raw, _ := input["url"].(string)
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("input.url required for url_canonicalize")
		}
		out.Query = raw
		uc := canonicalizeURL(raw)
		out.URLOriginal = raw
		out.URLValid = uc.Valid
		out.URLCanonical = uc.Canonical
		out.URLScheme = uc.Scheme
		out.URLHost = uc.Host
		out.URLPath = uc.Path
		out.URLRemovedParams = uc.RemovedParams
		out.URLOriginalScheme = uc.OriginalScheme

	case "domain_canonicalize":
		raw, _ := input["domain"].(string)
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("input.domain required for domain_canonicalize")
		}
		out.Query = raw
		dc := canonicalizeDomain(raw)
		out.DomainOriginal = raw
		out.DomainValid = dc.Valid
		out.DomainCanonical = dc.Canonical
		out.DomainApex = dc.Apex
		out.DomainSubdomain = dc.Subdomain
		out.DomainPublicSuffix = dc.PublicSuffix
		out.DomainIsApex = dc.IsApex
		out.DomainICANN = dc.ICANN

	case "ip_canonicalize":
		raw, _ := input["ip"].(string)
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("input.ip required for ip_canonicalize")
		}
		out.Query = raw
		ic := canonicalizeIP(raw)
		out.IPOriginal = raw
		out.IPValid = ic.Valid
		out.IPCanonical = ic.Canonical
		out.IPVersion = ic.Version
		out.IPClass = ic.Class
		out.IPIs4in6 = ic.Is4in6
		out.IPHadLeadingZeros = ic.HadLeadingZeros
		out.IPHadBrackets = ic.HadBrackets
		out.IPSlashPrefix = ic.SlashPrefix

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: name_match, name_variations, username_variations, email_canonicalize, phone_canonicalize, social_canonicalize, url_canonicalize, domain_canonicalize, ip_canonicalize", mode)
	}

	out.HighlightFindings = buildEntityMatchHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// ---------- Algorithms ----------

// normalizeForMatch lowercases, applies Unicode NFKD decomposition,
// strips combining marks (so "é" → "e", "ñ" → "n"), maps a small set
// of Latin-extended characters that NFKD does NOT decompose
// (ł, ø, ß, æ, œ, đ, ð, þ, ı), and drops anything not letter/digit/space.
//
// Empirically this raises the "same person" classification rate on
// accented-vs-stripped name pairs from ~0% to ~100% — see
// TestNormalizeForMatch_AccentedNamesQuantitative.
func normalizeForMatch(s string) string {
	s = strings.ToLower(s)
	// Apply non-decomposing Latin-extended substitutions first (these don't
	// have NFKD decompositions because they aren't precomposed combinations).
	s = latinExtendedFold(s)
	// NFKD decomposes "é" into "e" + combining acute accent (U+0301).
	s = norm.NFKD.String(s)
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.IsMark(r) {
			// Drop combining marks (accents, tildes, cedillas, etc.)
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			out = append(out, r)
		}
	}
	return strings.TrimSpace(string(out))
}

// utf8RuneLen returns the number of runes in s.
func utf8RuneLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// runePrefix returns the first n runes of s as a string.
func runePrefix(s string, n int) string {
	out := make([]rune, 0, n)
	for _, r := range s {
		if len(out) == n {
			break
		}
		out = append(out, r)
	}
	return string(out)
}

// latinExtendedFold maps a small set of letters that don't have an NFKD
// decomposition into a base + mark. Coverage targets the most common
// OSINT-relevant cases.
func latinExtendedFold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'ł':
			b.WriteByte('l')
		case 'Ł':
			b.WriteByte('l')
		case 'ø':
			b.WriteByte('o')
		case 'Ø':
			b.WriteByte('o')
		case 'đ':
			b.WriteByte('d')
		case 'Đ':
			b.WriteByte('d')
		case 'ð':
			b.WriteByte('d')
		case 'Ð':
			b.WriteByte('d')
		case 'þ':
			b.WriteString("th")
		case 'Þ':
			b.WriteString("th")
		case 'ß':
			b.WriteString("ss")
		case 'ẞ':
			b.WriteString("ss")
		case 'æ':
			b.WriteString("ae")
		case 'Æ':
			b.WriteString("ae")
		case 'œ':
			b.WriteString("oe")
		case 'Œ':
			b.WriteString("oe")
		case 'ı': // dotless i (Turkish)
			b.WriteByte('i')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
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
	"alexander":   {"alex", "al", "lex", "xander", "sasha", "alec"},
	"alexandra":   {"alex", "alexa", "ali", "lexi", "sasha", "sandra"},
	"andrew":      {"andy", "drew"},
	"anthony":     {"tony", "ant"},
	"barbara":     {"barb", "barbie", "babs"},
	"benjamin":    {"ben", "benji", "benny"},
	"catherine":   {"cathy", "cat", "kate", "katie", "kit", "katy", "trina"},
	"charles":     {"charlie", "chuck", "chip", "chaz"},
	"christine":   {"chris", "chrissy", "tina"},
	"christopher": {"chris", "topher", "kris"},
	"daniel":      {"dan", "danny", "dani"},
	"david":       {"dave", "davey", "davy"},
	"deborah":     {"deb", "debbie", "debs"},
	"dorothy":     {"dot", "dotty", "dolly"},
	"edward":      {"ed", "eddie", "ted", "ned", "teddy", "ward"},
	"elizabeth":   {"liz", "beth", "betsy", "betty", "lizzy", "eliza", "ellie", "lisa", "libby"},
	"emily":       {"em", "emmy", "emi"},
	"frances":     {"fran", "frannie", "francie"},
	"francis":     {"frank", "frankie"},
	"frederick":   {"fred", "freddie", "rick"},
	"gabriel":     {"gabe", "gabby"},
	"gabrielle":   {"gabby", "gaby", "ella"},
	"george":      {"georgie"},
	"gerald":      {"gerry", "jerry"},
	"gregory":     {"greg"},
	"harold":      {"hal", "harry"},
	"helen":       {"nell", "nellie"},
	"henry":       {"hank", "hal", "harry"},
	"isabella":    {"izzy", "bella", "bell", "isa"},
	"jacob":       {"jake", "jay"},
	"james":       {"jim", "jimmy", "jamie", "jay"},
	"janet":       {"jan", "jen"},
	"jennifer":    {"jen", "jenny", "jenna"},
	"john":        {"jack", "johnny", "jon", "jonny"},
	"jonathan":    {"jon", "john", "johnny"},
	"joseph":      {"joe", "joey", "jo"},
	"joshua":      {"josh"},
	"katherine":   {"kate", "katie", "kat", "kathy", "kit"},
	"kathleen":    {"kathy", "kate", "kay"},
	"kenneth":     {"ken", "kenny"},
	"kimberly":    {"kim", "kimmy"},
	"larry":       {"laurence", "lawrence", "lar"},
	"laurence":    {"larry", "laurie"},
	"lawrence":    {"larry", "lawrie"},
	"leonard":     {"leo", "lenny", "lenny"},
	"linda":       {"lin", "lindy"},
	"madeline":    {"maddie", "maddy"},
	"margaret":    {"meg", "maggie", "marge", "peggy", "midge", "greta", "margie", "rita"},
	"matthew":     {"matt", "matty"},
	"michael":     {"mike", "mikey", "mick", "mitch"},
	"michelle":    {"mickey", "shelly", "shell"},
	"nancy":       {"nan"},
	"nathaniel":   {"nat", "nate"},
	"nicholas":    {"nick", "nicky", "klaus"},
	"olivia":      {"liv", "livvy", "ollie"},
	"pamela":      {"pam", "pammy"},
	"patricia":    {"pat", "patty", "tricia", "trish"},
	"patrick":     {"pat", "paddy", "rick"},
	"peter":       {"pete", "petey"},
	"philip":      {"phil", "pip"},
	"rebecca":     {"becca", "becky", "becks"},
	"richard":     {"dick", "rich", "rick", "ricky", "rico"},
	"robert":      {"bob", "bobby", "rob", "robbie", "bert"},
	"ronald":      {"ron", "ronnie", "ronny"},
	"samantha":    {"sam", "sammy", "samm"},
	"samuel":      {"sam", "sammy"},
	"sandra":      {"sandy", "sandi"},
	"sarah":       {"sara", "sally", "sadie"},
	"stephen":     {"steve", "stevie", "steph"},
	"steven":      {"steve", "stevie"},
	"susan":       {"sue", "susie", "suzy"},
	"theodore":    {"ted", "teddy", "theo"},
	"thomas":      {"tom", "tommy"},
	"timothy":     {"tim", "timmy"},
	"victoria":    {"vicky", "vicki", "vickie", "tori", "tory"},
	"virginia":    {"ginny", "ginger"},
	"walter":      {"walt", "wally"},
	"william":     {"bill", "billy", "will", "willy", "willie", "liam"},
	"zachary":     {"zach", "zac", "zack"},

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

	// International given-name → English-formal mappings (iter 13).
	// Most upstream OSINT data carries the local form ("José" / "Juan" /
	// "Jürgen"), while English-language sources carry the formal Anglo
	// equivalent. Bidirectional lookup expands either side.
	"jose":     {"joseph", "joe", "joey", "pepe", "pepito"},
	"juan":     {"john", "johnny", "ivan"},
	"juana":    {"jane", "joan"},
	"javier":   {"xavier", "javi"},
	"miguel":   {"michael", "mike", "mick", "mig"},
	"pedro":    {"peter", "pete"},
	"diego":    {"james", "jimmy"},
	"santiago": {"james", "jim"},
	"francois": {"francis", "frank", "frankie"},
	"jean":     {"john", "johnny"},
	"jurgen":   {"george", "georgie"},
	"johann":   {"john", "hans", "johnny"},
	"hans":     {"john", "johann"},
	"giovanni": {"john", "gianni", "vanni"},
	"giuseppe": {"joseph", "joe", "peppe", "pino"},
	"vladimir": {"vlad", "volodya", "wolfram"},
	"ivan":     {"john", "ivanchuk", "vanya"},
	"sofia":    {"sophia", "sofie", "sophie", "sof"},
	"maria":    {"mary", "marie", "molly", "mia"},
	"ana":      {"anne", "ann", "annie", "anna"},
	"olga":     {"helga", "olya"},
	"natasha":  {"natalie", "natasja"},
	"lukasz":   {"luke", "lucas", "luca"},
	"sergio":   {"serge"},
	"pavel":    {"paul", "pasha"},
}

// lookupNicknameGroup returns the union of (canonical, nicknames) where
// `key` is either a canonical name or one of the nickname forms.
//
// The lookup key is Unicode/accent-folded (iter-2's normalizeForMatch)
// so accented inputs like "José" / "François" / "Łukasz" resolve to
// the ASCII dictionary entries. Without this fold, accented first
// names produced no nickname expansion at all. See
// TestNameVariations_AccentedRecallQuantitative.
func lookupNicknameGroup(key string) []string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = normalizeForMatch(key)
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

	case "email_canonicalize":
		if !o.EmailValid {
			hi = append(hi, fmt.Sprintf("✗ '%s' is not a parsable email address", o.EmailOriginal))
			break
		}
		hi = append(hi, fmt.Sprintf("✓ '%s' → canonical '%s' (mailbox-key '%s')", o.EmailOriginal, o.EmailCanonical, o.EmailMailboxKey))
		notes := []string{}
		if o.EmailHadPlusTag {
			notes = append(notes, "stripped +tag subaddress")
		}
		if o.EmailHadDotTrick {
			notes = append(notes, "stripped gmail dot-alias")
		}
		if len(notes) > 0 {
			hi = append(hi, "  normalizations: "+strings.Join(notes, "; "))
		}
		hi = append(hi, fmt.Sprintf("  provider: %s; %d alternate alias form(s)", o.EmailProvider, len(o.EmailAliases)))

	case "phone_canonicalize":
		if !o.PhoneValid {
			hi = append(hi, fmt.Sprintf("✗ '%s' is not a parsable phone number (need country code or NANP-style 10-digit)", o.PhoneOriginal))
			break
		}
		extra := ""
		if o.PhoneExtension != "" {
			extra = fmt.Sprintf(" ext %s", o.PhoneExtension)
		}
		hi = append(hi, fmt.Sprintf("✓ '%s' → E.164 '%s'%s (region %s)", o.PhoneOriginal, o.PhoneE164, extra, fallbackStr(o.PhoneRegion, "?")))
		if o.PhoneTollFree {
			hi = append(hi, "  flag: toll-free (NANP 8XX)")
		}

	case "social_canonicalize":
		if !o.SocialValid {
			hi = append(hi, fmt.Sprintf("✗ '%s' could not be resolved to a known social platform + handle", o.SocialOriginal))
			break
		}
		hi = append(hi, fmt.Sprintf("✓ '%s' → %s (key '%s')", o.SocialOriginal, o.SocialCanonicalURL, o.SocialKey))

	case "url_canonicalize":
		if !o.URLValid {
			hi = append(hi, fmt.Sprintf("✗ '%s' is not a parsable URL", o.URLOriginal))
			break
		}
		hi = append(hi, fmt.Sprintf("✓ '%s' → '%s'", o.URLOriginal, o.URLCanonical))
		if len(o.URLRemovedParams) > 0 {
			hi = append(hi, fmt.Sprintf("  removed tracking params: %s", strings.Join(o.URLRemovedParams, ", ")))
		}
		if o.URLOriginalScheme != "" {
			hi = append(hi, fmt.Sprintf("  scheme normalized: %s → %s", o.URLOriginalScheme, o.URLScheme))
		}

	case "domain_canonicalize":
		if !o.DomainValid {
			hi = append(hi, fmt.Sprintf("✗ '%s' is not a parsable domain", o.DomainOriginal))
			break
		}
		hi = append(hi, fmt.Sprintf("✓ '%s' → apex '%s' (eTLD '%s')", o.DomainOriginal, o.DomainApex, o.DomainPublicSuffix))
		if o.DomainSubdomain != "" {
			hi = append(hi, fmt.Sprintf("  subdomain: '%s'", o.DomainSubdomain))
		}
		if !o.DomainICANN {
			hi = append(hi, "  flag: private suffix (e.g. blogspot.com / github.io / herokuapp.com)")
		}

	case "ip_canonicalize":
		if !o.IPValid {
			hi = append(hi, fmt.Sprintf("✗ '%s' is not a parsable IP address", o.IPOriginal))
			break
		}
		hi = append(hi, fmt.Sprintf("✓ '%s' → IPv%d '%s' (%s)", o.IPOriginal, o.IPVersion, o.IPCanonical, o.IPClass))
		if o.IPIs4in6 {
			hi = append(hi, "  note: IPv4-in-IPv6 form unwrapped to IPv4 canonical")
		}
		if o.IPHadLeadingZeros {
			hi = append(hi, "  note: leading-zero octets normalized")
		}
		if o.IPSlashPrefix > 0 {
			hi = append(hi, fmt.Sprintf("  CIDR prefix preserved: /%d", o.IPSlashPrefix))
		}
	}
	return hi
}

func fallbackStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ---------- Email canonicalization (iter-14) ----------

// canonicalizedEmail captures every rendering of a single mailbox.
//
// MailboxKey is the strongest dedup key — every literal form that
// resolves to the same mailbox produces the same MailboxKey, including
// gmail's dot-aliasing and googlemail.com / gmail.com equivalence.
// Canonical is the deliverable form — what you'd actually send mail to.
//
// The motivating defect: across OSINT tools that emit emails (github_emails,
// hibp, holehe, hunter_io, dehashed, intelx, mail_correlate, gravatar,
// keybase, ghunt, hudsonrock_cavalier, …) a single mailbox routinely
// appears in 3-6 syntactically-distinct forms. person_aggregate then
// dedups by literal string equality and reports each variant as a
// separate finding — inflating the apparent identity surface area and
// breaking cross-tool linkage. See TestEntityMatch_EmailCanonicalizeDedup.
type canonicalizedEmail struct {
	Original    string
	Canonical   string
	MailboxKey  string
	Local       string
	Domain      string
	Provider    string
	Valid       bool
	Aliases     []string
	HadPlusTag  bool
	HadDotTrick bool
}

func canonicalizeEmail(raw string) canonicalizedEmail {
	out := canonicalizedEmail{Original: raw}
	s := strings.TrimSpace(raw)
	// Strip surrounding angle brackets ("<x@y>") and optional display name.
	if i := strings.LastIndexByte(s, '<'); i >= 0 {
		if j := strings.IndexByte(s[i:], '>'); j > 0 {
			s = s[i+1 : i+j]
		}
	}
	s = strings.TrimSpace(s)
	// Strip "mailto:" prefix (case-insensitive).
	if len(s) >= 7 && strings.EqualFold(s[:7], "mailto:") {
		s = s[7:]
	}
	// URL-decode (best-effort) — handles %40 for @ and any other percent
	// escapes lifted from scraped href="mailto:…" attributes.
	if strings.ContainsRune(s, '%') {
		if dec, err := url.QueryUnescape(s); err == nil {
			s = dec
		}
	}
	s = strings.TrimSpace(s)
	// Lowercase entire address. RFC 5321 says the local part is technically
	// case-sensitive, but virtually no production mail server enforces this
	// on the receiver side; folding to lowercase is the universal OSINT
	// dedup convention.
	s = strings.ToLower(s)
	if s == "" {
		return out
	}
	// Exactly one '@' is required. Multiple '@' (e.g. "two@@signs.com")
	// is malformed — never silently merge it with anything.
	if strings.Count(s, "@") != 1 {
		return out
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return out
	}
	local := s[:at]
	domain := s[at+1:]
	domain = strings.TrimSuffix(domain, ".")
	if local == "" || domain == "" || strings.ContainsAny(local, " \t") {
		return out
	}
	// Normalize domain to ASCII (punycode) — DNS resolves on the ASCII form,
	// so "münchen.de" and "xn--mnchen-3ya.de" are the same domain and must
	// dedup together.
	if asc, err := idna.Lookup.ToASCII(domain); err == nil && asc != "" {
		domain = asc
	}

	provider := classifyEmailProvider(domain)

	// Provider-specific local-part normalizations.
	mailboxLocal := local
	canonicalLocal := local
	hadPlus := false
	hadDot := false

	// Plus-tag (subaddress) stripping. Gmail, Outlook family, Yahoo,
	// iCloud, Proton, Fastmail — all support `local+anything@domain` as
	// an alias for `local@domain`. Strip it on those known providers.
	switch provider {
	case "gmail", "outlook", "yahoo", "icloud", "proton", "fastmail":
		if i := strings.IndexByte(canonicalLocal, '+'); i > 0 {
			canonicalLocal = canonicalLocal[:i]
			hadPlus = true
		}
		if i := strings.IndexByte(mailboxLocal, '+'); i > 0 {
			mailboxLocal = mailboxLocal[:i]
		}
	}

	// Gmail dot-aliasing: gmail.com (and the legacy googlemail.com) ignore
	// dots in the local part. `j.o.h.n@gmail.com`, `john@gmail.com`,
	// `jo.hn@gmail.com` all deliver to the same mailbox. Strip dots only
	// for the mailbox-key (not for the deliverable canonical).
	if provider == "gmail" {
		stripped := strings.ReplaceAll(mailboxLocal, ".", "")
		if stripped != mailboxLocal {
			hadDot = true
		}
		mailboxLocal = stripped
		// Also collapse googlemail.com → gmail.com on both forms.
		if domain == "googlemail.com" {
			domain = "gmail.com"
		}
	}

	canonical := canonicalLocal + "@" + domain
	mailboxKey := mailboxLocal + "@" + domain

	out.Valid = true
	out.Local = canonicalLocal
	out.Domain = domain
	out.Provider = provider
	out.Canonical = canonical
	out.MailboxKey = mailboxKey
	out.HadPlusTag = hadPlus
	out.HadDotTrick = hadDot

	// Aliases: alternate textual forms that all resolve to the same
	// mailbox. Useful for downstream search expansion.
	aliasSet := map[string]struct{}{}
	addAlias := func(s string) {
		if s == "" || s == raw || s == canonical {
			return
		}
		if _, ok := aliasSet[s]; ok {
			return
		}
		aliasSet[s] = struct{}{}
		out.Aliases = append(out.Aliases, s)
	}
	if provider == "gmail" {
		addAlias(mailboxLocal + "@gmail.com")
		addAlias(mailboxLocal + "@googlemail.com")
	}
	if hadPlus {
		addAlias(canonicalLocal + "@" + domain)
	}
	return out
}

// ---------- Phone canonicalization (iter-15) ----------

// canonicalizedPhone is the phone-side mirror of canonicalizedEmail.
//
// E164 is the strongest dedup key — every literal form of the same real
// number produces the same E164 ("+CCDDD…"). Extension is captured
// separately so it doesn't break the dedup key but isn't lost.
//
// Motivating defect (parallel to iter-14's email work): tools like
// people_data_labs, truepeoplesearch, hunter, holehe, hudsonrock,
// numverify, panel_entity_resolution emit phones in many surface forms
// — "(415) 555-2671", "+1-415-555-2671", "4155552671", "1.415.555.2671",
// "tel:+14155552671", "+1 415 555 2671 ext 99". person_aggregate
// currently dedups by literal string equality, so the same real number
// appears as 3-6 distinct findings in any merged view, breaking the
// cross-tool linkage the social-graph layer relies on.
type canonicalizedPhone struct {
	E164        string
	Digits      string
	CountryCode string
	National    string
	Region      string
	Extension   string
	Valid       bool
	TollFree    bool
}

// phoneCountryCodes maps ITU-T E.164 country calling codes to ISO-3166
// alpha-2 region codes. Greedy match — longer prefixes first.
//
// Coverage: every 1-2 digit code (high-confidence) and the most common
// 3-digit codes for OSINT-relevant regions (UA, IL, AE, HK, TW, SG-area,
// EU non-NANP, etc.). Ambiguous shared codes (1 = NANP family ≠ just
// US, 7 = RU+KZ) return the dominant region; downstream tools that
// need precise NANP routing should re-disambiguate.
var phoneCountryCodes = map[string]string{
	"1": "US", "7": "RU",
	"20": "EG", "27": "ZA", "30": "GR", "31": "NL", "32": "BE", "33": "FR",
	"34": "ES", "36": "HU", "39": "IT", "40": "RO", "41": "CH", "43": "AT",
	"44": "GB", "45": "DK", "46": "SE", "47": "NO", "48": "PL", "49": "DE",
	"51": "PE", "52": "MX", "53": "CU", "54": "AR", "55": "BR", "56": "CL",
	"57": "CO", "58": "VE", "60": "MY", "61": "AU", "62": "ID", "63": "PH",
	"64": "NZ", "65": "SG", "66": "TH", "81": "JP", "82": "KR", "84": "VN",
	"86": "CN", "90": "TR", "91": "IN", "92": "PK", "93": "AF", "94": "LK",
	"95": "MM", "98": "IR",
	"212": "MA", "213": "DZ", "216": "TN", "218": "LY", "220": "GM",
	"221": "SN", "234": "NG", "254": "KE", "255": "TZ", "256": "UG",
	"263": "ZW", "264": "NA", "351": "PT", "352": "LU", "353": "IE",
	"354": "IS", "355": "AL", "356": "MT", "357": "CY", "358": "FI",
	"359": "BG", "370": "LT", "371": "LV", "372": "EE", "373": "MD",
	"374": "AM", "375": "BY", "380": "UA", "381": "RS", "382": "ME",
	"385": "HR", "386": "SI", "387": "BA", "389": "MK", "420": "CZ",
	"421": "SK", "423": "LI", "501": "BZ", "502": "GT", "503": "SV",
	"504": "HN", "505": "NI", "506": "CR", "507": "PA", "591": "BO",
	"592": "GY", "593": "EC", "595": "PY", "598": "UY", "673": "BN",
	"852": "HK", "853": "MO", "855": "KH", "856": "LA", "880": "BD",
	"886": "TW", "960": "MV", "961": "LB", "962": "JO", "963": "SY",
	"964": "IQ", "965": "KW", "966": "SA", "967": "YE", "968": "OM",
	"970": "PS", "971": "AE", "972": "IL", "973": "BH", "974": "QA",
	"975": "BT", "976": "MN", "977": "NP", "992": "TJ", "993": "TM",
	"994": "AZ", "995": "GE", "996": "KG", "998": "UZ",
}

// nanpTollFreeAreaCodes covers NANP (country code 1) toll-free prefixes.
var nanpTollFreeAreaCodes = map[string]bool{
	"800": true, "833": true, "844": true, "855": true, "866": true,
	"877": true, "888": true,
}

func canonicalizePhone(raw string) canonicalizedPhone {
	out := canonicalizedPhone{}
	s := strings.TrimSpace(raw)
	if s == "" {
		return out
	}
	// Strip "tel:" prefix.
	if len(s) >= 4 && strings.EqualFold(s[:4], "tel:") {
		s = s[4:]
	}
	// URL-decode.
	if strings.ContainsRune(s, '%') {
		if dec, err := url.QueryUnescape(s); err == nil {
			s = dec
		}
	}
	s = strings.TrimSpace(s)

	// Capture and remove extension. Recognize the common patterns:
	//   ";ext=NNN"  ",NNN" (DTMF pause)  " ext NNN" / " ext.NNN" /
	//   " extension NNN"  " x NNN"  " #NNN"  " p NNN"
	ext := ""
	lower := strings.ToLower(s)
	for _, marker := range []string{
		";ext=", ";ext.", ";ext ", "ext.", "extension", " ext ", "ext ", " x", " p", "#",
	} {
		if i := strings.LastIndex(lower, marker); i >= 0 {
			tail := s[i+len(marker):]
			digits := digitsOnly(tail)
			if len(digits) >= 1 && len(digits) <= 8 {
				ext = digits
				s = s[:i]
				break
			}
		}
	}
	// Some scraped forms use "x123" with no leading space — try that.
	if ext == "" {
		ll := strings.ToLower(s)
		if i := strings.LastIndexByte(ll, 'x'); i > 0 {
			tail := s[i+1:]
			digits := digitsOnly(tail)
			head := s[:i]
			// Only treat as extension if the head still has plausible digits
			// and the tail is purely numeric (no letters left).
			if len(digits) >= 1 && len(digits) <= 8 && digits == strings.TrimSpace(tail) && len(digitsOnly(head)) >= 7 {
				ext = digits
				s = head
			}
		}
	}

	// Capture the leading-+ flag BEFORE stripping formatting.
	hasPlus := strings.HasPrefix(strings.TrimSpace(s), "+")
	digits := digitsOnly(s)

	// 00 international-access prefix → treat as +.
	if !hasPlus && strings.HasPrefix(digits, "00") && len(digits) >= 10 {
		digits = digits[2:]
		hasPlus = true
	}

	// NANP normalization without leading +.
	//   10 digits → assume NANP (US/CA): prepend "1"
	//   11 digits starting with 1 → assume NANP: keep as-is, treat 1 as country
	if !hasPlus {
		switch {
		case len(digits) == 10:
			digits = "1" + digits
			hasPlus = true
		case len(digits) == 11 && strings.HasPrefix(digits, "1"):
			hasPlus = true
		}
	}

	// E.164 says max 15 digits; min ~8 for any international number.
	if !hasPlus || len(digits) < 8 || len(digits) > 15 {
		out.Extension = ext
		return out
	}

	// Greedy country-code match: try 3, 2, 1 digits.
	cc := ""
	for _, n := range []int{3, 2, 1} {
		if len(digits) <= n {
			continue
		}
		head := digits[:n]
		if _, ok := phoneCountryCodes[head]; ok {
			cc = head
			break
		}
	}
	if cc == "" {
		out.Extension = ext
		return out
	}
	national := digits[len(cc):]
	if len(national) < 4 {
		out.Extension = ext
		return out
	}

	region := phoneCountryCodes[cc]
	tollFree := false
	if cc == "1" && len(national) >= 3 && nanpTollFreeAreaCodes[national[:3]] {
		tollFree = true
	}

	out.Valid = true
	out.E164 = "+" + digits
	out.Digits = digits
	out.CountryCode = cc
	out.National = national
	out.Region = region
	out.Extension = ext
	out.TollFree = tollFree
	return out
}

func digitsOnly(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// ---------- Social handle canonicalization (iter-16) ----------

// canonicalizedSocial captures everything we need to dedup the same
// social-platform identity across the many surface forms it shows up
// in across OSINT tool outputs:
//
//   - Bare handle: "@johndoe", "johndoe", "JohnDoe"
//   - Mention: "u/spez", "/u/spez", "@spez@mastodon.social"
//   - Profile URLs: "https://twitter.com/johndoe", "https://x.com/JohnDoe/",
//     "https://www.linkedin.com/in/john-doe-12345/", "github.com/octocat"
//   - Intent / share / embed URLs: twitter intent, instagram tagged,
//     youtube /@handle vs /c/customname vs /channel/UCID
//
// The Key field is the strongest dedup primary key — every textual
// form that resolves to the same platform identity produces the same
// "<platform>:<handle>" pair. CanonicalURL is the deliverable
// public-profile URL.
//
// Why this matters for the social-graph layer: once a sherlock or
// maigret run produces N candidate "presence" hits, each may be
// emitted in a different surface form by different tools. Without a
// platform-typed dedup key, person_aggregate counts each surface form
// as a separate "evidence record" and inflates the perceived
// uniqueness count. This breaks (a) the "at least 2 sources" hard
// floor used in panel_entity_resolution, and (b) any cross-platform
// follower-graph join that needs <platform,handle> as the node key.
type canonicalizedSocial struct {
	Platform     string
	Handle       string
	Key          string
	CanonicalURL string
	Valid        bool
}

// socialPlatformHosts maps the hostnames (including legacy / mirror /
// shortcode hosts) we recognize to a canonical platform tag. Lookups
// are by lowercased host with any leading "www." stripped.
var socialPlatformHosts = map[string]string{
	"twitter.com":          "twitter",
	"x.com":                "twitter",
	"mobile.twitter.com":   "twitter",
	"m.twitter.com":        "twitter",
	"nitter.net":           "twitter",
	"instagram.com":        "instagram",
	"instagr.am":           "instagram",
	"tiktok.com":           "tiktok",
	"vm.tiktok.com":        "tiktok",
	"linkedin.com":         "linkedin",
	"github.com":           "github",
	"reddit.com":           "reddit",
	"old.reddit.com":       "reddit",
	"new.reddit.com":       "reddit",
	"np.reddit.com":        "reddit",
	"youtube.com":          "youtube",
	"m.youtube.com":        "youtube",
	"youtu.be":             "youtube",
	"facebook.com":         "facebook",
	"m.facebook.com":       "facebook",
	"fb.com":               "facebook",
	"fb.me":                "facebook",
	"threads.net":          "threads",
	"threads.com":          "threads",
	"bsky.app":             "bluesky",
	"medium.com":           "medium",
	"substack.com":         "substack",
	"keybase.io":           "keybase",
	"about.me":             "about-me",
	"hackernews.com":       "hackernews",
	"news.ycombinator.com": "hackernews",
}

// caseSensitivePlatforms identifies platforms whose handle namespace
// is genuinely case-sensitive on the wire. For everything else, lowercasing
// the handle is the canonical OSINT dedup convention.
//
// Note: Twitter/X, Instagram, TikTok, LinkedIn, GitHub, Reddit, YouTube,
// Facebook, Mastodon-local-part, Threads are all case-insensitive in
// practice. The only meaningfully case-sensitive identifier in this
// space is YouTube channel-IDs ("UC..." after /channel/), which are
// surfaced as a separate handle namespace below.
var caseSensitivePlatforms = map[string]bool{}

func canonicalizeSocial(raw, hint string) canonicalizedSocial {
	out := canonicalizedSocial{}
	s := strings.TrimSpace(raw)
	if s == "" {
		return out
	}
	// Strip surrounding quotes / angle brackets that occur in scrape outputs.
	s = strings.Trim(s, "\"'<>")

	// Mastodon-style "@user@instance" or "user@instance" — recognize
	// BEFORE URL parsing, because the scheme-less URL fallback would
	// otherwise greedily treat "instance.tld" as the host and lose the
	// userinfo half.
	if at := strings.Count(s, "@"); at >= 1 && !strings.Contains(s, "://") {
		stripped := strings.TrimPrefix(s, "@")
		if i := strings.IndexByte(stripped, '@'); i > 0 && strings.Contains(stripped[i+1:], ".") {
			user := strings.ToLower(strings.TrimSpace(stripped[:i]))
			instance := strings.ToLower(strings.TrimSpace(stripped[i+1:]))
			if user != "" && instance != "" && validHandleChars(user) {
				out.Platform = "mastodon"
				out.Handle = user + "@" + instance
				out.Key = "mastodon:" + out.Handle
				out.CanonicalURL = "https://" + instance + "/@" + user
				out.Valid = true
				return out
			}
		}
	}

	// If the input parses as a URL with a host, route to the URL parser.
	if u := tryParseSocialURL(s); u != nil {
		return resolveSocialFromURL(u)
	}

	// Otherwise treat as a bare handle. Strip leading "@", possible
	// "u/", "user/", "r/" prefixes that bleed through from copy-paste.
	low := strings.ToLower(s)
	platform := strings.ToLower(strings.TrimSpace(hint))

	// Reddit prefixes
	switch {
	case strings.HasPrefix(low, "/u/") || strings.HasPrefix(low, "u/"):
		bare := strings.TrimPrefix(strings.TrimPrefix(low, "/"), "u/")
		if validHandleChars(bare) {
			out.Platform = "reddit"
			out.Handle = bare
			out.Key = "reddit:" + bare
			out.CanonicalURL = "https://www.reddit.com/user/" + bare
			out.Valid = true
			return out
		}
	case strings.HasPrefix(low, "/r/") || strings.HasPrefix(low, "r/"):
		// Subreddit, not a user — out of scope for this tool.
		return out
	}

	bare := strings.TrimPrefix(s, "@")
	bare = strings.TrimSpace(bare)
	if !validHandleChars(strings.ToLower(bare)) {
		return out
	}
	if platform == "" {
		// Without a URL or hint we can't pick a single platform. Emit
		// "unknown" so the caller knows it needs to qualify.
		out.Platform = "unknown"
		out.Handle = strings.ToLower(bare)
		out.Key = "unknown:" + out.Handle
		out.Valid = true
		return out
	}
	out.Platform = platform
	out.Handle = canonicalHandleCase(platform, bare)
	out.Key = platform + ":" + out.Handle
	out.CanonicalURL = canonicalProfileURL(platform, out.Handle)
	out.Valid = true
	return out
}

// tryParseSocialURL accepts both schemed and scheme-less inputs.
// "github.com/octocat" is treated as if it were
// "https://github.com/octocat". Returns nil if the input isn't
// URL-shaped.
func tryParseSocialURL(s string) *url.URL {
	candidate := s
	if !strings.Contains(candidate, "://") {
		// Scheme-less host: only treat as URL if it looks like one (has
		// a dot in the first path-less segment).
		head := candidate
		if i := strings.IndexAny(candidate, "/?"); i > 0 {
			head = candidate[:i]
		}
		if !strings.Contains(head, ".") {
			return nil
		}
		candidate = "https://" + candidate
	}
	u, err := url.Parse(candidate)
	if err != nil || u == nil || u.Host == "" {
		return nil
	}
	return u
}

func resolveSocialFromURL(u *url.URL) canonicalizedSocial {
	out := canonicalizedSocial{}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	platform, ok := socialPlatformHosts[host]
	if !ok {
		// Unknown host — possibly a Mastodon instance ("https://mastodon.social/@user").
		// Recognize by path shape "/@user".
		path := strings.TrimPrefix(u.Path, "/")
		if strings.HasPrefix(path, "@") {
			user := strings.TrimPrefix(path, "@")
			user = strings.TrimSuffix(user, "/")
			if i := strings.IndexAny(user, "/?#"); i >= 0 {
				user = user[:i]
			}
			user = strings.ToLower(user)
			if user != "" && validHandleChars(user) {
				out.Platform = "mastodon"
				out.Handle = user + "@" + host
				out.Key = "mastodon:" + out.Handle
				out.CanonicalURL = "https://" + host + "/@" + user
				out.Valid = true
				return out
			}
		}
		return out
	}
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, "/")
	q := u.Query()

	switch platform {
	case "twitter":
		// /<handle>, /<handle>/status/<id>, /intent/user?screen_name=<h>,
		// /i/user/<id> (numeric — not handle-based; skip).
		if path == "intent/user" || path == "i/user/screen_name" {
			if h := q.Get("screen_name"); h != "" {
				return makeSocial("twitter", strings.ToLower(h))
			}
			return out
		}
		first := firstPathSegment(path)
		if first == "" || isTwitterReservedPath(first) {
			return out
		}
		return makeSocial("twitter", strings.ToLower(strings.TrimPrefix(first, "@")))

	case "instagram":
		// /<handle>, /<handle>/, /p/<shortcode> (post — skip), /reel/...
		first := firstPathSegment(path)
		if first == "" || first == "p" || first == "reel" || first == "tv" || first == "explore" || first == "accounts" {
			return out
		}
		return makeSocial("instagram", strings.ToLower(strings.TrimPrefix(first, "@")))

	case "tiktok":
		// /@<handle>, /@<handle>/video/<id>
		first := firstPathSegment(path)
		if !strings.HasPrefix(first, "@") {
			return out
		}
		h := strings.ToLower(strings.TrimPrefix(first, "@"))
		return makeSocial("tiktok", h)

	case "linkedin":
		// /in/<slug>, /in/<slug>/, /in/<slug>/details/...
		// /company/<slug> = company, surface as platform=linkedin-company.
		first := firstPathSegment(path)
		if first == "in" {
			rest := strings.TrimPrefix(path, "in/")
			h := firstPathSegment(rest)
			h = strings.ToLower(h)
			if !validHandleChars(h) {
				return out
			}
			out.Platform = "linkedin"
			out.Handle = h
			out.Key = "linkedin:" + h
			out.CanonicalURL = "https://www.linkedin.com/in/" + h + "/"
			out.Valid = true
			return out
		}
		if first == "company" {
			rest := strings.TrimPrefix(path, "company/")
			h := firstPathSegment(rest)
			h = strings.ToLower(h)
			if !validHandleChars(h) {
				return out
			}
			out.Platform = "linkedin-company"
			out.Handle = h
			out.Key = "linkedin-company:" + h
			out.CanonicalURL = "https://www.linkedin.com/company/" + h + "/"
			out.Valid = true
			return out
		}
		return out

	case "github":
		first := firstPathSegment(path)
		if first == "" || isGitHubReservedPath(first) {
			return out
		}
		return makeSocial("github", strings.ToLower(first))

	case "reddit":
		// /user/<u>, /u/<u>, /r/<sub> (out of scope)
		first := firstPathSegment(path)
		switch first {
		case "user", "u":
			rest := strings.TrimPrefix(path, first+"/")
			h := firstPathSegment(rest)
			h = strings.ToLower(h)
			if !validHandleChars(h) {
				return out
			}
			return makeSocial("reddit", h)
		}
		return out

	case "youtube":
		// /@handle, /c/customname, /channel/UCID, /user/legacyname,
		// short URLs / video URLs (out of scope).
		if strings.HasPrefix(path, "@") {
			h := strings.TrimPrefix(path, "@")
			h = firstPathSegment(h)
			h = strings.ToLower(h)
			if !validHandleChars(h) {
				return out
			}
			return makeSocial("youtube", h)
		}
		first := firstPathSegment(path)
		switch first {
		case "c":
			rest := strings.TrimPrefix(path, "c/")
			h := firstPathSegment(rest)
			h = strings.ToLower(h)
			if !validHandleChars(h) {
				return out
			}
			return makeSocial("youtube", h)
		case "channel":
			// Channel ID — case-sensitive and a different namespace.
			rest := strings.TrimPrefix(path, "channel/")
			h := firstPathSegment(rest)
			if !validHandleChars(strings.ToLower(h)) {
				return out
			}
			out.Platform = "youtube-channel"
			out.Handle = h
			out.Key = "youtube-channel:" + h
			out.CanonicalURL = "https://www.youtube.com/channel/" + h
			out.Valid = true
			return out
		case "user":
			rest := strings.TrimPrefix(path, "user/")
			h := firstPathSegment(rest)
			h = strings.ToLower(h)
			if !validHandleChars(h) {
				return out
			}
			return makeSocial("youtube", h)
		}
		return out

	case "facebook":
		first := firstPathSegment(path)
		if first == "" || first == "profile.php" || first == "groups" || first == "pages" {
			if first == "profile.php" {
				if id := q.Get("id"); id != "" {
					out.Platform = "facebook"
					out.Handle = "id:" + id
					out.Key = "facebook:id:" + id
					out.CanonicalURL = "https://www.facebook.com/profile.php?id=" + id
					out.Valid = true
					return out
				}
			}
			return out
		}
		return makeSocial("facebook", strings.ToLower(first))

	case "threads":
		first := firstPathSegment(path)
		if !strings.HasPrefix(first, "@") {
			return out
		}
		return makeSocial("threads", strings.ToLower(strings.TrimPrefix(first, "@")))

	case "bluesky":
		// bsky.app/profile/<handle>
		if strings.HasPrefix(path, "profile/") {
			rest := strings.TrimPrefix(path, "profile/")
			h := firstPathSegment(rest)
			h = strings.ToLower(h)
			if h == "" {
				return out
			}
			out.Platform = "bluesky"
			out.Handle = h
			out.Key = "bluesky:" + h
			out.CanonicalURL = "https://bsky.app/profile/" + h
			out.Valid = true
			return out
		}
		return out

	case "hackernews":
		if strings.HasPrefix(path, "user") {
			h := q.Get("id")
			if h != "" {
				out.Platform = "hackernews"
				out.Handle = h // HN is case-sensitive on display but treated insensitive for dedup
				out.Key = "hackernews:" + strings.ToLower(h)
				out.CanonicalURL = "https://news.ycombinator.com/user?id=" + h
				out.Valid = true
				return out
			}
		}
		return out

	case "medium", "substack", "keybase", "about-me":
		first := firstPathSegment(path)
		if first == "" {
			return out
		}
		// Medium / Substack often use "@" prefix for usernames.
		first = strings.TrimPrefix(first, "@")
		return makeSocial(platform, strings.ToLower(first))
	}
	return out
}

func makeSocial(platform, handle string) canonicalizedSocial {
	if !validHandleChars(handle) || handle == "" {
		return canonicalizedSocial{}
	}
	return canonicalizedSocial{
		Platform:     platform,
		Handle:       handle,
		Key:          platform + ":" + handle,
		CanonicalURL: canonicalProfileURL(platform, handle),
		Valid:        true,
	}
}

func canonicalProfileURL(platform, handle string) string {
	switch platform {
	case "twitter":
		return "https://twitter.com/" + handle
	case "instagram":
		return "https://www.instagram.com/" + handle + "/"
	case "tiktok":
		return "https://www.tiktok.com/@" + handle
	case "linkedin":
		return "https://www.linkedin.com/in/" + handle + "/"
	case "linkedin-company":
		return "https://www.linkedin.com/company/" + handle + "/"
	case "github":
		return "https://github.com/" + handle
	case "reddit":
		return "https://www.reddit.com/user/" + handle
	case "youtube":
		return "https://www.youtube.com/@" + handle
	case "youtube-channel":
		return "https://www.youtube.com/channel/" + handle
	case "facebook":
		return "https://www.facebook.com/" + handle
	case "threads":
		return "https://www.threads.net/@" + handle
	case "bluesky":
		return "https://bsky.app/profile/" + handle
	case "medium":
		return "https://medium.com/@" + handle
	case "substack":
		return "https://" + handle + ".substack.com/"
	case "keybase":
		return "https://keybase.io/" + handle
	case "about-me":
		return "https://about.me/" + handle
	case "hackernews":
		return "https://news.ycombinator.com/user?id=" + handle
	}
	return ""
}

func canonicalHandleCase(platform, raw string) string {
	if caseSensitivePlatforms[platform] {
		return raw
	}
	return strings.ToLower(raw)
}

func firstPathSegment(p string) string {
	if i := strings.IndexAny(p, "/?#"); i >= 0 {
		return p[:i]
	}
	return p
}

func validHandleChars(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '_' || r == '-' || r == '.' || r == '@' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		// Reject anything else — keeps us from emitting "key:" forms
		// based on accidentally-matched URL fragments.
		return false
	}
	return true
}

// isTwitterReservedPath rejects URL paths that aren't user profiles
// (compose, settings, search, hashtags, etc.).
func isTwitterReservedPath(seg string) bool {
	switch seg {
	case "home", "explore", "notifications", "messages", "i", "settings",
		"compose", "search", "hashtag", "intent", "share", "login",
		"signup", "tos", "privacy", "about":
		return true
	}
	return false
}

// isGitHubReservedPath rejects URL paths that aren't user/org profiles.
func isGitHubReservedPath(seg string) bool {
	switch seg {
	case "settings", "marketplace", "orgs", "trending", "issues",
		"pulls", "explore", "topics", "collections", "about", "login",
		"join", "search", "notifications":
		return true
	}
	return false
}

// ---------- URL canonicalization (iter-19) ----------

// canonicalizedURL captures every variant of one canonical URL location.
//
// Canonical is the strongest dedup key — every textual form of the same
// page produces the same Canonical string. Used downstream by
// person_aggregate / panel_entity_resolution / wayback / firecrawl /
// google_dork / hackertarget / common_crawl evidence-merge passes.
//
// Rules:
//   - Lowercase scheme + host
//   - Strip "www." prefix from host
//   - Strip default ports (:80 for http, :443 for https)
//   - IDN→punycode for non-ASCII hosts
//   - Drop URL fragment ("#section")
//   - Drop tracking query params (utm_*, fbclid, gclid, mc_*, _hsenc,
//     yclid, msclkid, dclid, igshid, ref, ref_src, source, share, …)
//   - Sort remaining query params alphabetically (so order doesn't
//     produce different strings for the same page)
//   - Strip trailing slash from path (except the root "/")
//   - Force scheme to https when the original was http (treat as
//     same logical resource for dedup; OriginalScheme surfaces the
//     fact when this happens)
type canonicalizedURL struct {
	Canonical      string
	Scheme         string
	Host           string
	Path           string
	Valid          bool
	RemovedParams  []string
	OriginalScheme string
}

// urlTrackingParams lists query keys that are tracking decoration only
// — present on the same page regardless of how it was reached, so they
// have no business in a dedup key. Any param matching a literal key OR
// the "utm_*" / "mc_*" / "_hs*" prefixes is dropped.
//
// Ordering note: the literal map handles lookups; prefix tests are
// hardcoded in canonicalizeURL.
var urlTrackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "yclid": true, "msclkid": true, "dclid": true,
	"igshid": true, "twclid": true,
	"ref": true, "ref_src": true, "ref_url": true, "referrer": true,
	"source": true, "share": true, "shared": true, "_branch_match_id": true,
	"yclid_": true,
	// Mailchimp / Pardot / HubSpot / Marketo / Eloqua tracking
	"_hsenc": true, "_hsmi": true, "__hstc": true, "__hssc": true,
	"hsa_acc": true, "hsa_cam": true, "hsa_grp": true, "hsa_ad": true, "hsa_src": true,
	"vero_conv": true, "vero_id": true,
	"pk_campaign": true, "pk_kwd": true, "pk_source": true,
	"trk": true, "trkCampaign": true,
	"oly_anon_id": true, "oly_enc_id": true,
	// Twitter/Reddit/LinkedIn share tracking
	"s": true, "t": true, // Twitter share params
	"si": true, // YouTube
}

func canonicalizeURL(raw string) canonicalizedURL {
	out := canonicalizedURL{}
	s := strings.TrimSpace(raw)
	if s == "" {
		return out
	}
	// Strip surrounding quotes / angle brackets.
	s = strings.Trim(s, "\"'<>()[]")
	if s == "" {
		return out
	}

	// Reject URI schemes that aren't web URLs (must be rejected before
	// the scheme-less branch, which would otherwise treat "mailto:x@y"
	// as a host-shaped string).
	lower := strings.ToLower(s)
	for _, bad := range []string{"mailto:", "tel:", "sms:", "javascript:", "data:", "file:", "ftp://", "ftps://"} {
		if strings.HasPrefix(lower, bad) {
			return out
		}
	}

	// Scheme-less inputs: prepend "https://" if it looks like a URL.
	hadScheme := strings.Contains(s, "://")
	if !hadScheme {
		head := s
		if i := strings.IndexAny(s, "/?#"); i > 0 {
			head = s[:i]
		}
		if !strings.Contains(head, ".") {
			return out
		}
		s = "https://" + s
	}

	u, err := url.Parse(s)
	if err != nil || u == nil || u.Host == "" {
		return out
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		return out
	}
	originalScheme := scheme
	// Treat http:// and https:// as the same logical resource for dedup.
	// Other schemes were already rejected at the prefix check above.
	if scheme == "http" {
		scheme = "https"
	} else if scheme != "https" {
		return out
	}
	if originalScheme == scheme {
		originalScheme = ""
	}

	host := strings.ToLower(u.Host)
	// Strip default ports.
	if strings.HasSuffix(host, ":80") && scheme == "https" {
		host = strings.TrimSuffix(host, ":80")
	} else if strings.HasSuffix(host, ":443") && scheme == "https" {
		host = strings.TrimSuffix(host, ":443")
	}
	// Strip leading "www." — same content lives at both.
	host = strings.TrimPrefix(host, "www.")
	// IDN → punycode for the host (DNS resolves on the ASCII form).
	if asc, err := idna.Lookup.ToASCII(host); err == nil && asc != "" {
		host = asc
	}
	if host == "" {
		return out
	}

	path := u.Path
	if path == "" {
		path = "/"
	}
	// Strip trailing slash from non-root paths.
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
	}

	// Filter tracking params; sort the rest.
	removed := []string{}
	q := u.Query()
	keptKeys := make([]string, 0, len(q))
	for k := range q {
		lk := strings.ToLower(k)
		if urlTrackingParams[lk] || strings.HasPrefix(lk, "utm_") ||
			strings.HasPrefix(lk, "mc_") || strings.HasPrefix(lk, "_hs") {
			removed = append(removed, k)
			continue
		}
		keptKeys = append(keptKeys, k)
	}
	sort.Strings(keptKeys)
	sort.Strings(removed)

	queryStr := ""
	if len(keptKeys) > 0 {
		parts := make([]string, 0, len(keptKeys))
		for _, k := range keptKeys {
			vals := q[k]
			sort.Strings(vals)
			for _, v := range vals {
				parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
			}
		}
		queryStr = "?" + strings.Join(parts, "&")
	}

	canonical := scheme + "://" + host + path + queryStr

	out.Valid = true
	out.Canonical = canonical
	out.Scheme = scheme
	out.Host = host
	out.Path = path
	out.RemovedParams = removed
	out.OriginalScheme = originalScheme
	return out
}

// ---------- Domain canonicalization (iter-20) ----------

// canonicalizedDomain captures every variant of a single registered
// domain. Apex (eTLD+1) is the strongest "is this the same
// organization?" dedup key — every subdomain variant of the same
// registered domain produces the same Apex value via the IANA Public
// Suffix List.
//
// Distinguishes ICANN suffixes (genuine TLDs like .com, .co.uk) from
// PRIVATE suffixes (blogspot.com, github.io, s3.amazonaws.com, …).
// For private suffixes the apex still computes correctly but the
// flag is exposed so downstream code can decide whether
// "blog1.blogspot.com" and "blog2.blogspot.com" should merge as
// "same org" (almost always NO — they're independent users on a
// shared platform).
type canonicalizedDomain struct {
	Canonical    string
	Apex         string
	Subdomain    string
	PublicSuffix string
	Valid        bool
	IsApex       bool
	ICANN        bool
}

func canonicalizeDomain(raw string) canonicalizedDomain {
	out := canonicalizedDomain{}
	s := strings.TrimSpace(raw)
	if s == "" {
		return out
	}
	// Strip surrounding quotes / angle brackets.
	s = strings.Trim(s, "\"'<>()[]")
	// If the input is a URL, peel off the scheme + path so we can run
	// the host through the same normalizer.
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u != nil && u.Host != "" {
			s = u.Host
		}
	}
	// Strip a leading "*." (wildcard cert subjects often surface as "*.example.com").
	s = strings.TrimPrefix(s, "*.")
	// Strip leading "@" (sometimes domains are written "@example.com").
	s = strings.TrimPrefix(s, "@")
	// Strip a port suffix.
	if i := strings.LastIndexByte(s, ':'); i > 0 && !strings.Contains(s[i+1:], ".") {
		s = s[:i]
	}
	// Drop a path / query / fragment if any survived (e.g. "example.com/foo").
	for _, sep := range "/?#" {
		if i := strings.IndexRune(s, sep); i > 0 {
			s = s[:i]
		}
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	s = strings.ToLower(s)
	if s == "" || !strings.Contains(s, ".") {
		return out
	}
	if asc, err := idna.Lookup.ToASCII(s); err == nil && asc != "" {
		s = asc
	}

	suffix, icann := publicsuffix.PublicSuffix(s)
	apex, err := publicsuffix.EffectiveTLDPlusOne(s)
	if err != nil || apex == "" {
		// Some valid hosts (single-label like "localhost", or hosts whose
		// entire string IS the public suffix) can't form an eTLD+1.
		return out
	}

	subdomain := ""
	if s != apex {
		subdomain = strings.TrimSuffix(s, "."+apex)
	}

	out.Valid = true
	out.Canonical = s
	out.Apex = apex
	out.Subdomain = subdomain
	out.PublicSuffix = suffix
	out.IsApex = (subdomain == "")
	out.ICANN = icann
	return out
}

// ---------- IP address canonicalization (iter-21) ----------
//
// canonicalizedIP is the network-identifier mirror of the string-side
// canonicalizers (email/phone/social/URL/domain). Tools that emit IPs
// (shodan, censys, ip_intel_lookup, asn, port_scan, urlscan, dns_lookup,
// ssl_cert_chain_inspect, hackertarget_recon, alienvault_otx, http_probe,
// reverse_dns) produce the same address in many surface forms:
//
//   - IPv4 with leading zeros: "192.168.001.001"
//   - IPv4-in-IPv6: "::ffff:192.168.1.1", "::ffff:c0a8:0101"
//   - IPv6 zero-compression: "2001:0db8:0000:0000:0000:0000:0000:0001" vs "2001:db8::1"
//   - IPv6 case: "2001:DB8::1" vs "2001:db8::1"
//   - Bracketed: "[2001:db8::1]"
//   - With port: "1.2.3.4:8080" / "[2001:db8::1]:8080"
//   - With CIDR: "10.0.0.1/24"
//
// Canonical is the strongest dedup key — every textual form of the same
// real address produces the same Canonical string. Class flags
// private / loopback / link-local / multicast / reserved / cgnat /
// documentation / unspecified — useful as a quick "is this internet-
// routable?" signal for OSINT triage.
type canonicalizedIP struct {
	Canonical       string
	Version         int
	Class           string
	Valid           bool
	Is4in6          bool
	HadLeadingZeros bool
	HadBrackets     bool
	SlashPrefix     int
}

func canonicalizeIP(raw string) canonicalizedIP {
	out := canonicalizedIP{}
	s := strings.TrimSpace(raw)
	if s == "" {
		return out
	}
	s = strings.Trim(s, "\"'<>()")

	// Strip brackets ("[2001:db8::1]" → "2001:db8::1"). Track for surfacing.
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return out
		}
		out.HadBrackets = true
		bracketed := s[1:end]
		// Allow trailing port after "]:..."
		s = bracketed
		// Drop any trailing port spec on the original (we discard it for canon).
	}

	// CIDR prefix (e.g. "10.0.0.1/24"). Capture and strip.
	if i := strings.IndexByte(s, '/'); i > 0 {
		mask := s[i+1:]
		// Only strip if mask is purely numeric.
		if isAllDigits(mask) {
			if n, err := atoiFast(mask); err == nil && n >= 0 && n <= 128 {
				out.SlashPrefix = n
				s = s[:i]
			}
		}
	}

	// Strip a trailing port (only sensible for IPv4; for IPv6 the bracket
	// path above already handled it). For IPv4, strip ":N" if N is digits.
	if !strings.Contains(s, ":") || strings.Count(s, ":") == 1 {
		// Either no colon (IPv4 plain) or exactly one colon (IPv4:port).
		if i := strings.LastIndexByte(s, ':'); i > 0 {
			tail := s[i+1:]
			head := s[:i]
			if isAllDigits(tail) && strings.Count(head, ".") == 3 {
				s = head
			}
		}
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}

	// Detect leading-zero octets in IPv4 inputs (e.g. "192.168.001.001"),
	// because Go's netip.ParseAddr is strict and rejects them. We strip
	// leading zeros and re-parse.
	if strings.Count(s, ".") == 3 {
		parts := strings.Split(s, ".")
		needFix := false
		for _, p := range parts {
			if len(p) > 1 && p[0] == '0' && isAllDigits(p) {
				needFix = true
				break
			}
		}
		if needFix {
			out.HadLeadingZeros = true
			fixed := make([]string, 4)
			for i, p := range parts {
				p = strings.TrimLeft(p, "0")
				if p == "" {
					p = "0"
				}
				fixed[i] = p
			}
			s = strings.Join(fixed, ".")
		}
	}

	addr, err := netip.ParseAddr(s)
	if err != nil {
		return out
	}

	// Unwrap IPv4-in-IPv6 (e.g. "::ffff:1.2.3.4").
	if addr.Is4In6() {
		out.Is4in6 = true
		addr = addr.Unmap()
	}

	out.Valid = true
	out.Canonical = addr.String()
	if addr.Is4() {
		out.Version = 4
	} else {
		out.Version = 6
	}
	out.Class = classifyIP(addr)
	return out
}

// classifyIP returns a coarse address-class tag. Multicast wins over
// link-local-multicast because the multicast-ness is the primary
// network-OSINT signal (mostly non-routable, special semantics);
// link-local is reserved for unicast scope only.
func classifyIP(a netip.Addr) string {
	switch {
	case a.IsUnspecified():
		return "unspecified"
	case a.IsLoopback():
		return "loopback"
	case a.IsMulticast():
		return "multicast"
	case a.IsLinkLocalUnicast():
		return "link-local"
	case a.IsPrivate():
		return "private"
	}
	// Special ranges netip.Addr doesn't expose a flag for.
	if a.Is4() {
		b := a.As4()
		// CGNAT 100.64.0.0/10
		if b[0] == 100 && (b[1]&0xC0) == 64 {
			return "cgnat"
		}
		// Documentation: 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24
		if (b[0] == 192 && b[1] == 0 && b[2] == 2) ||
			(b[0] == 198 && b[1] == 51 && b[2] == 100) ||
			(b[0] == 203 && b[1] == 0 && b[2] == 113) {
			return "documentation"
		}
		// Reserved 240.0.0.0/4 (excluding 255.255.255.255 broadcast)
		if b[0] >= 240 && !(b[0] == 255 && b[1] == 255 && b[2] == 255 && b[3] == 255) {
			return "reserved"
		}
	} else {
		// IPv6 documentation: 2001:db8::/32
		b := a.As16()
		if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x0d && b[3] == 0xb8 {
			return "documentation"
		}
	}
	return "public"
}

// atoiFast parses small non-negative integers without allocating.
func atoiFast(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// classifyEmailProvider returns a coarse provider tag used to gate
// provider-specific canonicalization rules.
func classifyEmailProvider(domain string) string {
	switch domain {
	case "gmail.com", "googlemail.com":
		return "gmail"
	case "outlook.com", "hotmail.com", "live.com", "msn.com", "passport.com":
		return "outlook"
	case "yahoo.com", "ymail.com", "rocketmail.com", "yahoo.co.uk", "yahoo.fr", "yahoo.de", "yahoo.co.jp":
		return "yahoo"
	case "icloud.com", "me.com", "mac.com":
		return "icloud"
	case "protonmail.com", "proton.me", "pm.me", "protonmail.ch":
		return "proton"
	case "fastmail.com", "fastmail.fm":
		return "fastmail"
	}
	return "other"
}
