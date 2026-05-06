package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MathGenealogy scrapes the Mathematics Genealogy Project at
// genealogy.math.ndsu.nodak.edu — the authoritative public dataset
// mapping PhD-supervisor chains for mathematics, statistics, and
// adjacent quantitative fields.
//
// No public API; the project provides static HTML pages keyed by
// numeric ID. We parse minimally and extract:
//   - mathematician name + dissertation
//   - advisor(s) (as MGP IDs + names)
//   - student(s) (as MGP IDs + names)
//   - school + year
//
// Modes:
//   - "by_id"     : fetch by MGP id
//   - "search"    : search by name (returns candidate ids)
//
// Knowledge-graph: emits typed entities (kind: "scholar") with stable
// MGP IDs and discriminator attributes for advisor/student edges.

type MGPPerson struct {
	MGPID        string    `json:"mgp_id"`
	Name         string    `json:"name"`
	Dissertation string    `json:"dissertation,omitempty"`
	School       string    `json:"school,omitempty"`
	Year         int       `json:"year,omitempty"`
	Country      string    `json:"country,omitempty"`
	Advisors     []MGPLink `json:"advisors,omitempty"`
	Students     []MGPLink `json:"students,omitempty"`
	URL          string    `json:"url"`
}

type MGPLink struct {
	MGPID string `json:"mgp_id"`
	Name  string `json:"name"`
}

type MGPEntity struct {
	Kind        string         `json:"kind"`
	MGPID       string         `json:"mgp_id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type MathGenealogyOutput struct {
	Mode              string      `json:"mode"`
	Query             string      `json:"query"`
	Person            *MGPPerson  `json:"person,omitempty"`
	Candidates        []MGPLink   `json:"candidates,omitempty"`
	Returned          int         `json:"returned"`
	Entities          []MGPEntity `json:"entities"`
	HighlightFindings []string    `json:"highlight_findings"`
	Source            string      `json:"source"`
	TookMs            int64       `json:"tookMs"`
}

func MathGenealogy(ctx context.Context, input map[string]any) (*MathGenealogyOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["mgp_id"] != nil:
			mode = "by_id"
		case input["name"] != nil:
			mode = "search"
		default:
			return nil, fmt.Errorf("input.mgp_id or input.name required")
		}
	}
	out := &MathGenealogyOutput{Mode: mode, Source: "genealogy.math.ndsu.nodak.edu"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) (string, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return "", fmt.Errorf("mgp: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("mgp HTTP %d", resp.StatusCode)
		}
		return string(body), nil
	}

	switch mode {
	case "by_id":
		id := fmt.Sprintf("%v", input["mgp_id"])
		out.Query = id
		html, err := get(fmt.Sprintf("https://www.mathgenealogy.org/id.php?id=%s", id))
		if err != nil {
			return nil, err
		}
		p, err := parseMGPPersonPage(html, id)
		if err != nil {
			return nil, err
		}
		p.URL = fmt.Sprintf("https://www.mathgenealogy.org/id.php?id=%s", id)
		out.Person = p

	case "search":
		nm, _ := input["name"].(string)
		if nm == "" {
			return nil, fmt.Errorf("input.name required")
		}
		out.Query = nm
		// MGP search by surname uses the search.php endpoint
		parts := strings.Fields(nm)
		surname := parts[len(parts)-1]
		given := strings.Join(parts[:len(parts)-1], " ")
		formURL := fmt.Sprintf("https://www.mathgenealogy.org/search.php?searchTerm=%s&searchSurname=%s&searchGivenname=%s",
			urlEsc(nm), urlEsc(surname), urlEsc(given))
		html, err := get(formURL)
		if err != nil {
			return nil, err
		}
		out.Candidates = parseMGPCandidates(html)

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Candidates)
	if out.Person != nil {
		out.Returned = 1 + len(out.Person.Advisors) + len(out.Person.Students)
	}
	out.Entities = mgpBuildEntities(out)
	out.HighlightFindings = mgpBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func urlEsc(s string) string {
	r := strings.NewReplacer(" ", "+", "&", "%26", "?", "%3F")
	return r.Replace(s)
}

var (
	mgpAdvisorRe   = regexp.MustCompile(`(?i)Advisor[^:]*:\s*(?:<[^>]*>)*\s*<a[^>]*href="id\.php\?id=(\d+)"[^>]*>([^<]+)</a>`)
	mgpStudentRe   = regexp.MustCompile(`<tr[^>]*>\s*<td[^>]*><a[^>]*href="id\.php\?id=(\d+)"[^>]*>([^<]+)</a>\s*</td>`)
	mgpSchoolRe    = regexp.MustCompile(`(?is)<span[^>]*>\s*([^<]{3,})</span>\s*<span[^>]*>\s*(\d{4})\s*</span>`)
	mgpDissRe      = regexp.MustCompile(`(?is)<span[^>]*id="thesisTitle"[^>]*>\s*([^<]+)</span>`)
	mgpNameH2      = regexp.MustCompile(`(?is)<h2[^>]*>\s*([^<]+)</h2>`)
	mgpCandidateRe = regexp.MustCompile(`(?is)<a[^>]*href="id\.php\?id=(\d+)"[^>]*>([^<]+)</a>`)
)

func parseMGPPersonPage(html, id string) (*MGPPerson, error) {
	p := &MGPPerson{MGPID: id}
	if m := mgpNameH2.FindStringSubmatch(html); len(m) >= 2 {
		p.Name = strings.TrimSpace(stripHTMLBare(m[1]))
	}
	if m := mgpDissRe.FindStringSubmatch(html); len(m) >= 2 {
		p.Dissertation = strings.TrimSpace(m[1])
	}
	if m := mgpSchoolRe.FindStringSubmatch(html); len(m) >= 3 {
		p.School = strings.TrimSpace(m[1])
		if y, err := strconv.Atoi(m[2]); err == nil {
			p.Year = y
		}
	}
	for _, m := range mgpAdvisorRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 3 {
			p.Advisors = append(p.Advisors, MGPLink{MGPID: m[1], Name: strings.TrimSpace(m[2])})
		}
	}
	for _, m := range mgpStudentRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 3 {
			// Skip the advisor self-link by checking it's not in advisors already
			already := false
			for _, ad := range p.Advisors {
				if ad.MGPID == m[1] {
					already = true
					break
				}
			}
			if already {
				continue
			}
			p.Students = append(p.Students, MGPLink{MGPID: m[1], Name: strings.TrimSpace(m[2])})
		}
	}
	if p.Name == "" {
		return nil, fmt.Errorf("mgp: could not parse name from page (id=%s)", id)
	}
	return p, nil
}

func parseMGPCandidates(html string) []MGPLink {
	out := []MGPLink{}
	seen := map[string]bool{}
	for _, m := range mgpCandidateRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 3 && !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, MGPLink{MGPID: m[1], Name: strings.TrimSpace(m[2])})
		}
	}
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

func mgpBuildEntities(o *MathGenealogyOutput) []MGPEntity {
	ents := []MGPEntity{}
	if p := o.Person; p != nil {
		ents = append(ents, MGPEntity{
			Kind: "scholar", MGPID: p.MGPID, Name: p.Name, URL: p.URL,
			Description: p.Dissertation,
			Attributes: map[string]any{
				"school":   p.School,
				"year":     p.Year,
				"advisors": p.Advisors,
				"students": p.Students,
			},
		})
		for _, a := range p.Advisors {
			ents = append(ents, MGPEntity{
				Kind: "scholar", MGPID: a.MGPID, Name: a.Name,
				URL:        fmt.Sprintf("https://www.mathgenealogy.org/id.php?id=%s", a.MGPID),
				Attributes: map[string]any{"role": "advisor_of:" + p.MGPID},
			})
		}
		for _, s := range p.Students {
			ents = append(ents, MGPEntity{
				Kind: "scholar", MGPID: s.MGPID, Name: s.Name,
				URL:        fmt.Sprintf("https://www.mathgenealogy.org/id.php?id=%s", s.MGPID),
				Attributes: map[string]any{"role": "student_of:" + p.MGPID},
			})
		}
	}
	for _, c := range o.Candidates {
		ents = append(ents, MGPEntity{
			Kind: "scholar", MGPID: c.MGPID, Name: c.Name,
			URL:        fmt.Sprintf("https://www.mathgenealogy.org/id.php?id=%s", c.MGPID),
			Attributes: map[string]any{"role": "search_candidate"},
		})
	}
	return ents
}

func mgpBuildHighlights(o *MathGenealogyOutput) []string {
	hi := []string{fmt.Sprintf("✓ mathgenealogy %s: %d records", o.Mode, o.Returned)}
	if p := o.Person; p != nil {
		hi = append(hi, fmt.Sprintf("  • %s — %s (%s, %d)", p.Name, p.Dissertation, p.School, p.Year))
		for _, a := range p.Advisors {
			hi = append(hi, fmt.Sprintf("    advisor: %s [%s]", a.Name, a.MGPID))
		}
		for i, s := range p.Students {
			if i >= 5 {
				hi = append(hi, fmt.Sprintf("    + %d more students", len(p.Students)-5))
				break
			}
			hi = append(hi, fmt.Sprintf("    student: %s [%s]", s.Name, s.MGPID))
		}
	}
	for i, c := range o.Candidates {
		if i >= 8 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • candidate: %s [%s]", c.Name, c.MGPID))
	}
	return hi
}
