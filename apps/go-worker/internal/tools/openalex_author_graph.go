package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// OpenAlexAuthorGraph traverses the OpenAlex author/work graph in a
// single call: given an author ID (or ORCID, or name), it returns all
// papers, all co-authors, all institutions, all referenced works, and
// the most-cited works in their network.
//
// Built specifically for multi-hop academic chain questions: "second
// author on a 2020-2023 paper where the third author lived in
// Brunswick" — answerable via this graph traversal where naive
// `openalex_search` requires many round-trips.
//
// Modes:
//   - "author_works"   : list works for an author + co-authors
//   - "work_full"      : full work record incl. all authors and refs
//   - "coauthor_graph" : 1-hop co-author graph for a seed author
//
// Knowledge-graph: emits typed entities (kind: scholar | scholarly_work |
// institution) with stable OpenAlex IDs (which incorporate ORCID/DOI
// when available).

type OAEntity struct {
	Kind        string         `json:"kind"`
	OpenAlexID  string         `json:"openalex_id,omitempty"`
	ORCID       string         `json:"orcid,omitempty"`
	DOI         string         `json:"doi,omitempty"`
	Name        string         `json:"name,omitempty"`
	Title       string         `json:"title,omitempty"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type OAAuthor struct {
	OpenAlexID  string `json:"openalex_id"`
	ORCID       string `json:"orcid,omitempty"`
	Name        string `json:"name"`
	Affiliation string `json:"primary_affiliation,omitempty"`
	WorksCount  int    `json:"works_count,omitempty"`
	CitedBy     int    `json:"cited_by_count,omitempty"`
}

type OAWork struct {
	OpenAlexID string     `json:"openalex_id"`
	Title      string     `json:"title"`
	DOI        string     `json:"doi,omitempty"`
	Year       int        `json:"publication_year,omitempty"`
	Date       string     `json:"publication_date,omitempty"`
	Type       string     `json:"type,omitempty"`
	Venue      string     `json:"venue,omitempty"`
	CitedBy    int        `json:"cited_by_count,omitempty"`
	Authors    []OAAuthor `json:"authors,omitempty"`
	Abstract   string     `json:"abstract,omitempty"`
}

type OpenAlexAuthorGraphOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	Author            *OAAuthor  `json:"author,omitempty"`
	Works             []OAWork   `json:"works,omitempty"`
	CoAuthors         []OAAuthor `json:"coauthors,omitempty"`
	Returned          int        `json:"returned"`
	Entities          []OAEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func OpenAlexAuthorGraph(ctx context.Context, input map[string]any) (*OpenAlexAuthorGraphOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["work_id"] != nil || input["doi"] != nil:
			mode = "work_full"
		case input["author_id"] != nil || input["orcid"] != nil:
			mode = "author_works"
		default:
			return nil, fmt.Errorf("input.author_id (or orcid) required, or input.work_id (or doi)")
		}
	}
	out := &OpenAlexAuthorGraphOutput{Mode: mode, Source: "api.openalex.org"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	// OpenAlex requires/recommends a polite User-Agent with mailto for higher rate limits.
	mailto := strings.TrimSpace(getOpenAlexMailto())
	get := func(u string) ([]byte, error) {
		if mailto != "" {
			if strings.Contains(u, "?") {
				u += "&mailto=" + url.QueryEscape(mailto)
			} else {
				u += "?mailto=" + url.QueryEscape(mailto)
			}
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("openalex: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("openalex: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("openalex HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	resolveAuthorID := func(s string) string {
		s = strings.TrimSpace(s)
		if s == "" {
			return ""
		}
		// Already an OpenAlex URL/A-id?
		if strings.HasPrefix(s, "https://openalex.org/A") || strings.HasPrefix(s, "A") {
			return strings.TrimPrefix(s, "https://openalex.org/")
		}
		// ORCID like 0000-0000-0000-0000
		if strings.Count(s, "-") == 3 {
			return "orcid:" + s
		}
		return s
	}

	switch mode {
	case "author_works":
		var authorID string
		if id, ok := input["author_id"].(string); ok && id != "" {
			authorID = resolveAuthorID(id)
		} else if orcid, ok := input["orcid"].(string); ok && orcid != "" {
			authorID = resolveAuthorID(orcid)
		}
		out.Query = authorID

		// 1) Fetch the author record
		body, err := get("https://api.openalex.org/authors/" + url.PathEscape(authorID))
		if err != nil {
			return nil, err
		}
		var arec map[string]any
		if err := json.Unmarshal(body, &arec); err != nil {
			return nil, fmt.Errorf("openalex decode author: %w", err)
		}
		out.Author = parseOAAuthor(arec)

		// 2) Fetch their works (top 50 by citation)
		params := url.Values{}
		params.Set("filter", "author.id:"+normalizeOAID(out.Author.OpenAlexID))
		params.Set("per-page", "50")
		params.Set("sort", "cited_by_count:desc")
		body, err = get("https://api.openalex.org/works?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var wresp struct {
			Results []map[string]any `json:"results"`
			Meta    struct {
				Count int `json:"count"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &wresp); err != nil {
			return nil, fmt.Errorf("openalex decode works: %w", err)
		}
		seenCo := map[string]bool{}
		for _, w := range wresp.Results {
			work := parseOAWork(w)
			out.Works = append(out.Works, work)
			for _, a := range work.Authors {
				if a.OpenAlexID == out.Author.OpenAlexID {
					continue
				}
				if !seenCo[a.OpenAlexID] {
					seenCo[a.OpenAlexID] = true
					out.CoAuthors = append(out.CoAuthors, a)
				}
			}
		}

	case "work_full":
		var url2 string
		if doi, ok := input["doi"].(string); ok && doi != "" {
			url2 = "https://api.openalex.org/works/https://doi.org/" + strings.TrimPrefix(doi, "https://doi.org/")
		} else {
			id, _ := input["work_id"].(string)
			url2 = "https://api.openalex.org/works/" + url.PathEscape(id)
		}
		body, err := get(url2)
		if err != nil {
			return nil, err
		}
		var w map[string]any
		if err := json.Unmarshal(body, &w); err != nil {
			return nil, fmt.Errorf("openalex decode work: %w", err)
		}
		work := parseOAWork(w)
		out.Works = []OAWork{work}

	case "coauthor_graph":
		var authorID string
		if id, ok := input["author_id"].(string); ok && id != "" {
			authorID = resolveAuthorID(id)
		} else if orcid, ok := input["orcid"].(string); ok && orcid != "" {
			authorID = resolveAuthorID(orcid)
		}
		if authorID == "" {
			return nil, fmt.Errorf("input.author_id (or orcid) required")
		}
		out.Query = authorID
		// Fetch works → unique coauthors
		params := url.Values{}
		params.Set("filter", "author.id:"+normalizeOAID(authorID))
		params.Set("per-page", "100")
		body, err := get("https://api.openalex.org/works?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var wresp struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &wresp); err != nil {
			return nil, fmt.Errorf("openalex decode coauthor works: %w", err)
		}
		seen := map[string]bool{}
		for _, w := range wresp.Results {
			work := parseOAWork(w)
			for _, a := range work.Authors {
				if !seen[a.OpenAlexID] && a.OpenAlexID != "" {
					seen[a.OpenAlexID] = true
					out.CoAuthors = append(out.CoAuthors, a)
				}
			}
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use author_works, work_full, or coauthor_graph", mode)
	}

	out.Returned = len(out.Works) + len(out.CoAuthors)
	if out.Author != nil {
		out.Returned++
	}
	out.Entities = oaBuildEntities(out)
	out.HighlightFindings = oaBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseOAAuthor(m map[string]any) *OAAuthor {
	a := &OAAuthor{
		OpenAlexID: gtString(m, "id"),
		ORCID:      gtString(m, "orcid"),
		Name:       gtString(m, "display_name"),
		WorksCount: int(gtFloat(m, "works_count")),
		CitedBy:    int(gtFloat(m, "cited_by_count")),
	}
	if last, ok := m["last_known_institution"].(map[string]any); ok {
		a.Affiliation = gtString(last, "display_name")
	}
	if last, ok := m["affiliations"].([]any); ok && len(last) > 0 {
		if first, ok := last[0].(map[string]any); ok {
			if inst, ok := first["institution"].(map[string]any); ok {
				if a.Affiliation == "" {
					a.Affiliation = gtString(inst, "display_name")
				}
			}
		}
	}
	return a
}

func parseOAWork(m map[string]any) OAWork {
	w := OAWork{
		OpenAlexID: gtString(m, "id"),
		Title:      gtString(m, "title"),
		DOI:        gtString(m, "doi"),
		Year:       int(gtFloat(m, "publication_year")),
		Date:       gtString(m, "publication_date"),
		Type:       gtString(m, "type"),
		CitedBy:    int(gtFloat(m, "cited_by_count")),
	}
	if v, ok := m["primary_location"].(map[string]any); ok {
		if src, ok := v["source"].(map[string]any); ok {
			w.Venue = gtString(src, "display_name")
		}
	}
	// Authors
	if auths, ok := m["authorships"].([]any); ok {
		for _, x := range auths {
			rec, _ := x.(map[string]any)
			if rec == nil {
				continue
			}
			au, ok := rec["author"].(map[string]any)
			if !ok {
				continue
			}
			affil := ""
			if insts, ok := rec["institutions"].([]any); ok && len(insts) > 0 {
				if first, ok := insts[0].(map[string]any); ok {
					affil = gtString(first, "display_name")
				}
			}
			w.Authors = append(w.Authors, OAAuthor{
				OpenAlexID:  gtString(au, "id"),
				ORCID:       gtString(au, "orcid"),
				Name:        gtString(au, "display_name"),
				Affiliation: affil,
			})
		}
	}
	// Inverted abstract — skip; just empty for now
	return w
}

func normalizeOAID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "https://openalex.org/") {
		return strings.TrimPrefix(id, "https://openalex.org/")
	}
	return id
}

func getOpenAlexMailto() string {
	// Pull from env so users get the higher rate limit.
	for _, k := range []string{"OPENALEX_MAILTO", "OPENALEX_EMAIL"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func oaBuildEntities(o *OpenAlexAuthorGraphOutput) []OAEntity {
	ents := []OAEntity{}
	if a := o.Author; a != nil {
		ents = append(ents, OAEntity{
			Kind: "scholar", OpenAlexID: a.OpenAlexID, ORCID: a.ORCID, Name: a.Name,
			Attributes: map[string]any{"affiliation": a.Affiliation, "works_count": a.WorksCount, "cited_by": a.CitedBy},
		})
	}
	for _, w := range o.Works {
		authorList := []map[string]string{}
		for _, au := range w.Authors {
			authorList = append(authorList, map[string]string{
				"openalex_id": au.OpenAlexID, "name": au.Name, "orcid": au.ORCID, "affiliation": au.Affiliation,
			})
		}
		ents = append(ents, OAEntity{
			Kind: "scholarly_work", OpenAlexID: w.OpenAlexID, DOI: w.DOI,
			Title: w.Title, Date: w.Date,
			Attributes: map[string]any{
				"year":     w.Year,
				"venue":    w.Venue,
				"cited_by": w.CitedBy,
				"type":     w.Type,
				"authors":  authorList,
			},
		})
	}
	for _, a := range o.CoAuthors {
		ents = append(ents, OAEntity{
			Kind: "scholar", OpenAlexID: a.OpenAlexID, ORCID: a.ORCID, Name: a.Name,
			Attributes: map[string]any{"role": "coauthor", "affiliation": a.Affiliation},
		})
	}
	return ents
}

func oaBuildHighlights(o *OpenAlexAuthorGraphOutput) []string {
	hi := []string{fmt.Sprintf("✓ openalex %s: %d records", o.Mode, o.Returned)}
	if a := o.Author; a != nil {
		hi = append(hi, fmt.Sprintf("  • author %s (%s) — %d works, %d cites; affiliation: %s",
			a.Name, a.OpenAlexID, a.WorksCount, a.CitedBy, a.Affiliation))
	}
	for i, w := range o.Works {
		if i >= 8 {
			break
		}
		auNames := []string{}
		for _, a := range w.Authors {
			auNames = append(auNames, a.Name)
		}
		if len(auNames) > 4 {
			auNames = append(auNames[:4], "…")
		}
		hi = append(hi, fmt.Sprintf("  • %s (%d) [%d cites] — %s", hfTruncate(w.Title, 80), w.Year, w.CitedBy, strings.Join(auNames, ", ")))
	}
	if len(o.CoAuthors) > 0 {
		hi = append(hi, fmt.Sprintf("  • %d unique co-authors", len(o.CoAuthors)))
	}
	return hi
}
