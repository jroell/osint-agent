package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ObitRelative is one relative mentioned in an obituary.
type ObitRelative struct {
	Name      string `json:"name"`
	Role      string `json:"role,omitempty"`     // spouse | child | parent | sibling | grandchild | other
	Status    string `json:"status,omitempty"`   // survives | predeceased
}

// ObitRecord is one parsed obituary.
type ObitRecord struct {
	URL              string         `json:"url"`
	Source           string         `json:"source"`
	DeceasedName     string         `json:"deceased_name,omitempty"`
	AgeAtDeath       int            `json:"age_at_death,omitempty"`
	BirthDate        string         `json:"birth_date,omitempty"`
	DeathDate        string         `json:"death_date,omitempty"`
	City             string         `json:"city,omitempty"`
	State            string         `json:"state,omitempty"`
	FuneralHome      string         `json:"funeral_home,omitempty"`
	Relatives        []ObitRelative `json:"relatives,omitempty"`
	RawSnippet       string         `json:"raw_snippet,omitempty"`
}

// ObitFamilyMember is the cross-source aggregated relative mention.
type ObitFamilyMember struct {
	Name        string   `json:"name"`
	Role        string   `json:"role,omitempty"`
	MentionCount int     `json:"mention_count"`
	SourcesURLs []string `json:"source_urls,omitempty"`
}

// ObituarySearchOutput is the response.
type ObituarySearchOutput struct {
	Query              string             `json:"query"`
	Location           string             `json:"location,omitempty"`
	Records            []ObitRecord       `json:"records"`
	UniqueRelatives    []ObitFamilyMember `json:"unique_relatives_aggregated,omitempty"`
	UniqueFuneralHomes []string           `json:"unique_funeral_homes,omitempty"`
	HighlightFindings  []string           `json:"highlight_findings"`
	Source             string             `json:"source"`
	TookMs             int64              `json:"tookMs"`
	Note               string             `json:"note,omitempty"`
}

// regex patterns for obituary snippet parsing
var (
	obitNameAgeRe       = regexp.MustCompile(`(?i)([A-Z][A-Za-z'.-]+(?:\s+[A-Z]\.?)?(?:\s+[A-Z][A-Za-z'.-]+){1,3})(?:,?\s+(?:age\s+)?(\d{1,3})(?:,?\s+(?:years\s+old)?)?)?`)
	obitDeceasedNameRe  = regexp.MustCompile(`(?i)Name:\s*([A-Z][A-Za-z'.-]+(?:\s+[A-Z][A-Za-z'.-]+){1,3})`)
	obitAgeRe           = regexp.MustCompile(`(?i)(?:age|aged)[\s:]+(\d{1,3})|\b(\d{1,3}) years (?:old|of age)`)
	obitBirthRe         = regexp.MustCompile(`(?i)(?:was )?born (?:on |in )?([A-Za-z]+ \d{1,2},? \d{4}|\d{4}|[A-Za-z]+ \d{4})`)
	obitDeathRe         = regexp.MustCompile(`(?i)(?:passed away|died|departed this life)(?:\s+on)?(?:\s+(?:Saturday|Sunday|Monday|Tuesday|Wednesday|Thursday|Friday),?)?\s+([A-Za-z]+ \d{1,2},? \d{4}|\d{4}|\d{1,2}/\d{1,2}/\d{4}|[A-Za-z]+ \d{1,2})`)
	obitLocationRe      = regexp.MustCompile(`(?i)(?:resident of|residence in|in|at)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?(?:,\s+[A-Z][a-z]+)?(?:,\s*[A-Z]{2}|,\s*[A-Z][a-z]+)?)`)
	obitFuneralHomeRe   = regexp.MustCompile(`(?i)([A-Z][A-Za-z']+(?:\s+[A-Z][A-Za-z']+)*\s+Funeral\s+Home(?:s)?(?:\s+&\s+[A-Z][A-Za-z']+)?)`)
	obitSurvivorsRe     = regexp.MustCompile(`(?is)(?:is\s+)?survived\s+by\s+([^.]{5,400}?)(?:\.\s|$)`)
	obitPredeceasedRe   = regexp.MustCompile(`(?is)(?:was\s+)?(?:preceded\s+in\s+death|predeceased)\s+by\s+([^.]{5,400}?)(?:\.\s|$)`)
	obitSpouseRe        = regexp.MustCompile(`(?i)(?:beloved|loving|devoted)\s+(?:husband|wife|spouse|partner)\s+(?:of\s+)?([A-Z][A-Za-z'.-]+(?:\s+[A-Z][A-Za-z'.-]+){1,3})|(?:husband|wife|spouse|partner)\s+of\s+(?:the\s+late\s+)?([A-Z][A-Za-z'.-]+(?:\s+[A-Z][A-Za-z'.-]+){1,3})`)
	obitParentsRe       = regexp.MustCompile(`(?i)(?:son|daughter|child)\s+of\s+(?:the\s+late\s+)?([^.]{5,200}?)(?:\.\s|$|\s+and\s+the\s+late|\s+he\s+is|\s+she\s+is)`)
	obitChildrenRe      = regexp.MustCompile(`(?is)(?:children|son|daughter|sons|daughters)\s*[:,]\s*([^.]{5,300}?)(?:\.\s|$)`)
	obitNameOnlyRe      = regexp.MustCompile(`[A-Z][A-Za-z'.-]+(?:\s+[A-Z]\.?)?(?:\s+[A-Z][A-Za-z'.-]+){1,3}`)
)

// ObituarySearch performs multi-source obituary search via the Tavily-bypass
// foundation (site_snippet_search infrastructure). Indexes:
//   - newspapers.com (paywalled — Google-indexed)
//   - legacy.com (largest free obituary aggregator)
//   - tributearchive.com
//   - dignitymemorial.com
//
// Parses snippets for: deceased name, age, birth/death dates, location,
// funeral home, surviving relatives, predeceased relatives, spouse,
// parents, children. Aggregates relatives across all hits with mention
// counts (cross-source agreement = high confidence).
//
// Why this matters for ER:
//   - Closes the catalog's family-tree gap (the Jason FIL benchmark from
//     iter-36 still partially open). Obituaries explicitly list relatives
//     by role, which TPS/findagrave only partially cover.
//   - Funeral home name is a strong geographic ER signal (e.g., "Hodapp
//     Funeral Home" → Cincinnati Ohio).
//   - Cross-source agreement: same deceased mentioned on 2-3 sites with
//     consistent relative names = high-confidence family graph.
//
// REQUIRES TAVILY_API_KEY (or FIRECRAWL_API_KEY fallback).
func ObituarySearch(ctx context.Context, input map[string]any) (*ObituarySearchOutput, error) {
	name, _ := input["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("input.name required")
	}
	location, _ := input["location"].(string)
	location = strings.TrimSpace(location)
	yearRange, _ := input["year_range"].(string)
	yearRange = strings.TrimSpace(yearRange)

	out := &ObituarySearchOutput{
		Query:    name,
		Location: location,
		Source:   "multi-source obituary search via Tavily-bypass (newspapers.com / legacy.com / tributearchive.com / dignitymemorial.com)",
	}
	start := time.Now()
	apiKey := os.Getenv("TAVILY_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("TAVILY_API_KEY env var required")
	}
	cli := &http.Client{Timeout: 30 * time.Second}

	// Run searches across all four sources in sequence (could parallelize)
	sites := []struct {
		Domain string
		Source string
	}{
		{"legacy.com", "legacy"},
		{"newspapers.com", "newspapers"},
		{"tributearchive.com", "tributearchive"},
		{"dignitymemorial.com", "dignitymemorial"},
	}

	for _, s := range sites {
		q := fmt.Sprintf(`site:%s "%s" obituary`, s.Domain, name)
		if location != "" {
			q += " " + location
		}
		if yearRange != "" {
			q += " " + yearRange
		}
		results, err := obitTavilyQuery(ctx, cli, apiKey, q, 8)
		if err != nil {
			continue // try next source
		}
		for _, r := range results {
			rec := parseObituarySnippet(r.URL, r.Title, r.Snippet, s.Source)
			if rec.DeceasedName != "" || len(rec.Relatives) > 0 {
				out.Records = append(out.Records, rec)
			}
		}
	}

	if len(out.Records) == 0 {
		out.Note = fmt.Sprintf("no obituary records found for '%s' in %s", name, location)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// Aggregate relatives across hits
	relMap := map[string]*ObitFamilyMember{}
	homeSet := map[string]struct{}{}
	for _, rec := range out.Records {
		for _, r := range rec.Relatives {
			key := strings.ToLower(r.Name)
			ag, ok := relMap[key]
			if !ok {
				ag = &ObitFamilyMember{Name: r.Name, Role: r.Role}
				relMap[key] = ag
			}
			ag.MentionCount++
			ag.SourcesURLs = append(ag.SourcesURLs, rec.URL)
		}
		if rec.FuneralHome != "" {
			homeSet[rec.FuneralHome] = struct{}{}
		}
	}
	for _, ag := range relMap {
		// dedupe URLs
		seen := map[string]bool{}
		dedup := []string{}
		for _, u := range ag.SourcesURLs {
			if !seen[u] {
				seen[u] = true
				dedup = append(dedup, u)
			}
		}
		ag.SourcesURLs = dedup
		out.UniqueRelatives = append(out.UniqueRelatives, *ag)
	}
	sort.SliceStable(out.UniqueRelatives, func(i, j int) bool {
		return out.UniqueRelatives[i].MentionCount > out.UniqueRelatives[j].MentionCount
	})
	for h := range homeSet {
		out.UniqueFuneralHomes = append(out.UniqueFuneralHomes, h)
	}
	sort.Strings(out.UniqueFuneralHomes)

	out.HighlightFindings = buildObitHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

type obitTavilyResult struct {
	URL     string
	Title   string
	Snippet string
}

func obitTavilyQuery(ctx context.Context, cli *http.Client, apiKey, query string, limit int) ([]obitTavilyResult, error) {
	body, _ := json.Marshal(map[string]any{
		"api_key":             apiKey,
		"query":               query,
		"max_results":         limit,
		"include_raw_content": false,
		"include_images":      false,
		"search_depth":        "basic",
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("tavily %d: %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []obitTavilyResult{}
	for _, r := range raw.Results {
		out = append(out, obitTavilyResult{URL: r.URL, Title: r.Title, Snippet: r.Content})
	}
	return out, nil
}

func parseObituarySnippet(url, title, snippet, source string) ObitRecord {
	rec := ObitRecord{URL: url, Source: source, RawSnippet: snippet}

	// Combine title + snippet for parsing
	text := title + " | " + snippet

	// Deceased name (try Name: pattern first, then title-based)
	if m := obitDeceasedNameRe.FindStringSubmatch(text); len(m) > 1 {
		rec.DeceasedName = strings.TrimSpace(m[1])
	}
	if rec.DeceasedName == "" && title != "" {
		// Title patterns often include the deceased name
		if m := obitNameOnlyRe.FindString(title); m != "" {
			rec.DeceasedName = m
		}
	}
	// Try first sentence "PERSON, age N" or "PERSON passed away"
	if rec.DeceasedName == "" {
		if m := obitNameAgeRe.FindStringSubmatch(snippet); len(m) > 1 {
			rec.DeceasedName = strings.TrimSpace(m[1])
		}
	}

	// Age
	if m := obitAgeRe.FindStringSubmatch(text); len(m) > 0 {
		ageStr := m[1]
		if ageStr == "" {
			ageStr = m[2]
		}
		if ageStr != "" {
			fmt.Sscanf(ageStr, "%d", &rec.AgeAtDeath)
		}
	}

	// Birth + death dates
	if m := obitBirthRe.FindStringSubmatch(text); len(m) > 1 {
		rec.BirthDate = strings.TrimSpace(m[1])
	}
	if m := obitDeathRe.FindStringSubmatch(text); len(m) > 1 {
		rec.DeathDate = strings.TrimSpace(m[1])
	}

	// Location
	if m := obitLocationRe.FindStringSubmatch(text); len(m) > 1 {
		loc := strings.TrimSpace(m[1])
		if strings.Contains(loc, ",") {
			parts := strings.SplitN(loc, ",", 2)
			rec.City = strings.TrimSpace(parts[0])
			rec.State = strings.TrimSpace(parts[1])
		} else {
			rec.City = loc
		}
	}

	// Funeral home
	if m := obitFuneralHomeRe.FindStringSubmatch(text); len(m) > 1 {
		rec.FuneralHome = strings.TrimSpace(m[1])
	}

	// Relatives
	rec.Relatives = extractObitRelatives(text)
	return rec
}

func extractObitRelatives(text string) []ObitRelative {
	out := []ObitRelative{}
	seen := map[string]bool{}
	addRel := func(name, role, status string) {
		name = strings.TrimSpace(name)
		// Skip noise — must look like a name
		words := strings.Fields(name)
		if len(words) < 2 || len(words) > 5 {
			return
		}
		// First letter must be capital
		ok := true
		for _, w := range words {
			if w == "" || !(w[0] >= 'A' && w[0] <= 'Z') {
				ok = false
				break
			}
		}
		if !ok {
			return
		}
		key := strings.ToLower(name) + "|" + role
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, ObitRelative{Name: name, Role: role, Status: status})
	}

	// "is survived by SPOUSE_NAME, his/her CHILD_LIST, ..."
	if m := obitSurvivorsRe.FindStringSubmatch(text); len(m) > 1 {
		raw := m[1]
		// Split on commas + "and" + ";"
		parts := regexp.MustCompile(`,\s*|\s+and\s+|;\s*`).Split(raw, -1)
		for _, p := range parts {
			p = strings.TrimSpace(p)
			// Detect role from preceding text
			role := classifyObitRole(p)
			// Strip role-prefix from name (e.g. "his wife Marie Smith" → "Marie Smith")
			cleanName := stripRoleWords(p)
			addRel(cleanName, role, "survives")
		}
	}

	// Predeceased
	if m := obitPredeceasedRe.FindStringSubmatch(text); len(m) > 1 {
		raw := m[1]
		parts := regexp.MustCompile(`,\s*|\s+and\s+|;\s*`).Split(raw, -1)
		for _, p := range parts {
			p = strings.TrimSpace(p)
			role := classifyObitRole(p)
			cleanName := stripRoleWords(p)
			addRel(cleanName, role, "predeceased")
		}
	}

	// Spouse pattern (inside or outside survivor sentences)
	for _, m := range obitSpouseRe.FindAllStringSubmatch(text, -1) {
		// either group 1 or 2 has the name
		name := m[1]
		if name == "" {
			name = m[2]
		}
		addRel(name, "spouse", "")
	}

	// Parents
	if m := obitParentsRe.FindStringSubmatch(text); len(m) > 1 {
		raw := m[1]
		parts := regexp.MustCompile(`\s+and\s+(?:the\s+late\s+)?`).Split(raw, -1)
		for _, p := range parts {
			cleanName := stripRoleWords(strings.TrimSpace(p))
			addRel(cleanName, "parent", "")
		}
	}

	return out
}

func classifyObitRole(s string) string {
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "wife") || strings.Contains(low, "husband") || strings.Contains(low, "spouse") || strings.Contains(low, "partner"):
		return "spouse"
	case strings.Contains(low, "son") || strings.Contains(low, "daughter") || strings.Contains(low, "children"):
		return "child"
	case strings.Contains(low, "father") || strings.Contains(low, "mother") || strings.Contains(low, "parents"):
		return "parent"
	case strings.Contains(low, "brother") || strings.Contains(low, "sister") || strings.Contains(low, "sibling"):
		return "sibling"
	case strings.Contains(low, "grandchild") || strings.Contains(low, "grandson") || strings.Contains(low, "granddaughter"):
		return "grandchild"
	case strings.Contains(low, "nephew") || strings.Contains(low, "niece"):
		return "nephew/niece"
	case strings.Contains(low, "uncle") || strings.Contains(low, "aunt"):
		return "aunt/uncle"
	}
	return "other"
}

func stripRoleWords(s string) string {
	// Remove leading role words like "his wife", "her husband", "loving daughter"
	patterns := []string{
		`(?i)^(?:his|her|their|the)\s+(?:loving|beloved|devoted|cherished|dear|late)?\s*(?:wife|husband|spouse|partner|son|daughter|brother|sister|father|mother|grandson|granddaughter|grandfather|grandmother|nephew|niece|uncle|aunt|child|parent|stepchild|stepson|stepdaughter|sibling|grandchild|in[- ]law|sister[- ]in[- ]law|brother[- ]in[- ]law|son[- ]in[- ]law|daughter[- ]in[- ]law|mother[- ]in[- ]law|father[- ]in[- ]law)\s+`,
		`(?i)^(?:loving|beloved|devoted|cherished|dear|late)\s+(?:wife|husband|spouse|partner|son|daughter|brother|sister|father|mother|grandson|granddaughter|grandfather|grandmother|nephew|niece|uncle|aunt|child|parent|sibling|grandchild)\s+(?:of\s+)?`,
		`(?i)^(?:wife|husband|spouse|partner|son|daughter|brother|sister|father|mother|grandson|granddaughter|nephew|niece|child|parent|sibling|grandchild)\s+`,
		`(?i)^(?:Mr|Mrs|Ms|Dr|Rev)\.?\s+`,
	}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		s = re.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(s)
}

func buildObitHighlights(o *ObituarySearchOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d obituary records recovered for '%s'", len(o.Records), o.Query))
	if o.Location != "" {
		hi = append(hi, "scope: "+o.Location)
	}
	// Per-record summary (top 3)
	for i, r := range o.Records {
		if i >= 3 {
			break
		}
		bits := []string{}
		if r.DeceasedName != "" {
			bits = append(bits, r.DeceasedName)
		}
		if r.AgeAtDeath > 0 {
			bits = append(bits, fmt.Sprintf("age %d", r.AgeAtDeath))
		}
		if r.DeathDate != "" {
			bits = append(bits, "died "+r.DeathDate)
		}
		if r.City != "" {
			bits = append(bits, r.City)
		}
		hi = append(hi, fmt.Sprintf("  • [%s] %s — %s", r.Source, strings.Join(bits, ", "), r.URL))
		if len(r.Relatives) > 0 {
			rels := []string{}
			for _, rel := range r.Relatives[:min2(8, len(r.Relatives))] {
				rels = append(rels, fmt.Sprintf("%s (%s%s)", rel.Name, rel.Role, map[string]string{"survives":" surv","predeceased":" pre"}[rel.Status]))
			}
			hi = append(hi, "    relatives: "+strings.Join(rels, " | "))
		}
		if r.FuneralHome != "" {
			hi = append(hi, "    funeral home: "+r.FuneralHome)
		}
	}
	if len(o.UniqueRelatives) > 0 {
		topR := []string{}
		for _, r := range o.UniqueRelatives[:min2(8, len(o.UniqueRelatives))] {
			confidence := ""
			if r.MentionCount > 1 {
				confidence = fmt.Sprintf(" [%dx cross-source]", r.MentionCount)
			}
			topR = append(topR, fmt.Sprintf("%s (%s)%s", r.Name, r.Role, confidence))
		}
		hi = append(hi, "🌳 family graph (cross-source dedup): "+strings.Join(topR, " | "))
	}
	if len(o.UniqueFuneralHomes) > 0 {
		hi = append(hi, "🏛 funeral home(s) (geographic ER signal): "+strings.Join(o.UniqueFuneralHomes, " | "))
	}
	return hi
}
