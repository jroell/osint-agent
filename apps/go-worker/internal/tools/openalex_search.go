package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type OpenAlexAuthor struct {
	ID            string   `json:"openalex_id,omitempty"`
	ORCID         string   `json:"orcid,omitempty"`
	Name          string   `json:"name"`
	WorksCount    int      `json:"works_count,omitempty"`
	CitedByCount  int      `json:"cited_by_count,omitempty"`
	HIndex        int      `json:"h_index,omitempty"`
	I10Index      int      `json:"i10_index,omitempty"`
	Affiliations  []string `json:"affiliations,omitempty"`
	LastKnownInst string   `json:"last_known_institution,omitempty"`
	URL           string   `json:"url,omitempty"`
}

type OpenAlexWork struct {
	ID              string   `json:"openalex_id,omitempty"`
	DOI             string   `json:"doi,omitempty"`
	Title           string   `json:"title"`
	PublicationYear int      `json:"publication_year,omitempty"`
	PublicationDate string   `json:"publication_date,omitempty"`
	Type            string   `json:"type,omitempty"`
	IsOA            bool     `json:"is_open_access"`
	OAUrl           string   `json:"open_access_url,omitempty"`
	CitedByCount    int      `json:"cited_by_count,omitempty"`
	ReferencedCount int      `json:"referenced_works_count,omitempty"`
	Authors         []string `json:"authors,omitempty"`
	Source          string   `json:"source,omitempty"` // venue (arxiv, journal name)
	FieldsOfStudy   []string `json:"fields_of_study,omitempty"`
	Concepts        []string `json:"top_concepts,omitempty"`
	Abstract        string   `json:"abstract_excerpt,omitempty"`
}

type OpenAlexSearchOutput struct {
	Mode             string         `json:"mode"`
	Query            string         `json:"query"`
	TotalCount       int            `json:"total_matching_records"`
	Returned         int            `json:"returned"`
	Authors          []OpenAlexAuthor `json:"authors,omitempty"`
	Works            []OpenAlexWork `json:"works,omitempty"`
	UniqueAffiliations []string     `json:"unique_affiliations,omitempty"`
	HighlightFindings []string      `json:"highlight_findings"`
	Source           string         `json:"source"`
	TookMs           int64          `json:"tookMs"`
	Note             string         `json:"note,omitempty"`
}

// OpenAlexSearch queries the OpenAlex public API (the open-replacement for
// Microsoft Academic Graph). Free, no key required, ~250M scholarly works
// indexed, ~100M authors, ~200K institutions.
//
// Modes:
//   - "works" (default): paper search by title/abstract/keyword
//   - "authors": researcher search with h-index, citations, affiliations
//   - "author_works": list a specific author's papers (requires author_id like 'A5066197394')
//
// Use cases:
//   - Academic ER: "find Dario Amodei" → 47 works, h-index 32, Caltech affiliation
//   - Competitive R&D intel: papers on a specific topic + their authors
//   - Recruiting: identify researchers in a field with high citation counts
//   - Co-author network mapping (works mode returns author lists per paper)
//
// OpenAlex requests a polite User-Agent with mailto for higher rate limits;
// we set a sensible default and recommend the operator override via the
// OPENALEX_MAILTO env var.
func OpenAlexSearch(ctx context.Context, input map[string]any) (*OpenAlexSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "works"
	}
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	limit := 20
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 200 {
		limit = int(v)
	}

	start := time.Now()
	out := &OpenAlexSearchOutput{
		Mode: mode, Query: q,
		Source: "api.openalex.org",
	}

	var endpoint string
	switch mode {
	case "works":
		endpoint = fmt.Sprintf("https://api.openalex.org/works?search=%s&per-page=%d&select=id,doi,title,publication_year,publication_date,type,open_access,cited_by_count,referenced_works_count,authorships,primary_location,concepts,abstract_inverted_index",
			url.QueryEscape(q), limit)
	case "authors":
		endpoint = fmt.Sprintf("https://api.openalex.org/authors?search=%s&per-page=%d&select=id,orcid,display_name,works_count,cited_by_count,summary_stats,affiliations,last_known_institutions",
			url.QueryEscape(q), limit)
	case "author_works":
		// q is the OpenAlex author ID (e.g. 'A5066197394' or full URL)
		authorID := q
		if !strings.HasPrefix(authorID, "A") && !strings.Contains(authorID, "/") {
			return nil, errors.New("author_works mode requires OpenAlex author ID (e.g. 'A5066197394')")
		}
		if !strings.Contains(authorID, "/") {
			authorID = "https://openalex.org/" + authorID
		}
		endpoint = fmt.Sprintf("https://api.openalex.org/works?filter=authorships.author.id:%s&per-page=%d&sort=cited_by_count:desc&select=id,doi,title,publication_year,publication_date,type,open_access,cited_by_count,referenced_works_count,authorships,primary_location,concepts",
			url.QueryEscape(authorID), limit)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use works, authors, or author_works", mode)
	}

	body, err := openalexFetch(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openalex parse: %w", err)
	}
	out.TotalCount = parsed.Meta.Count

	if mode == "authors" {
		affSet := map[string]bool{}
		for _, raw := range parsed.Results {
			a, err := parseOpenAlexAuthor(raw)
			if err == nil {
				out.Authors = append(out.Authors, a)
				for _, aff := range a.Affiliations {
					affSet[aff] = true
				}
			}
		}
		for s := range affSet {
			out.UniqueAffiliations = append(out.UniqueAffiliations, s)
		}
		sort.Strings(out.UniqueAffiliations)
		out.Returned = len(out.Authors)
	} else {
		// works or author_works
		for _, raw := range parsed.Results {
			w, err := parseOpenAlexWork(raw)
			if err == nil {
				out.Works = append(out.Works, w)
			}
		}
		out.Returned = len(out.Works)
	}

	// Highlights
	highlights := []string{
		fmt.Sprintf("%d total matches; returned %d (mode=%s)", out.TotalCount, out.Returned, mode),
	}
	if mode == "authors" && len(out.Authors) > 0 {
		top := out.Authors[0]
		highlights = append(highlights, fmt.Sprintf("top hit: %s (%d works, %d citations, h-index=%d)",
			top.Name, top.WorksCount, top.CitedByCount, top.HIndex))
		if top.LastKnownInst != "" {
			highlights = append(highlights, "current affiliation: "+top.LastKnownInst)
		}
		if top.ID != "" {
			highlights = append(highlights, "→ chain: bigquery_patents on this person's affiliation, or `openalex_search mode=author_works query="+strings.TrimPrefix(top.ID, "https://openalex.org/")+"` for their papers")
		}
	}
	if (mode == "works" || mode == "author_works") && len(out.Works) > 0 {
		topW := out.Works[0]
		highlights = append(highlights, fmt.Sprintf("top paper: '%s' (%d, cited %d×)",
			truncate(topW.Title, 80), topW.PublicationYear, topW.CitedByCount))
		if len(topW.Authors) > 0 {
			highlights = append(highlights, fmt.Sprintf("authors: %s", strings.Join(topW.Authors[:minInt(5, len(topW.Authors))], ", ")))
		}
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func openalexFetch(ctx context.Context, endpoint string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	mailto := "osint@example.com"
	req.Header.Set("User-Agent", "osint-agent/openalex (mailto:"+mailto+")")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openalex fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openalex status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func parseOpenAlexAuthor(raw json.RawMessage) (OpenAlexAuthor, error) {
	var a struct {
		ID           string  `json:"id"`
		ORCID        string  `json:"orcid"`
		DisplayName  string  `json:"display_name"`
		WorksCount   int     `json:"works_count"`
		CitedByCount int     `json:"cited_by_count"`
		SummaryStats struct {
			HIndex   int `json:"h_index"`
			I10Index int `json:"i10_index"`
		} `json:"summary_stats"`
		Affiliations []struct {
			Institution struct {
				DisplayName string `json:"display_name"`
			} `json:"institution"`
			Years []int `json:"years"`
		} `json:"affiliations"`
		LastKnownInstitutions []struct {
			DisplayName string `json:"display_name"`
		} `json:"last_known_institutions"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return OpenAlexAuthor{}, err
	}
	out := OpenAlexAuthor{
		ID: strings.TrimPrefix(a.ID, "https://openalex.org/"), ORCID: a.ORCID,
		Name: a.DisplayName, WorksCount: a.WorksCount, CitedByCount: a.CitedByCount,
		HIndex: a.SummaryStats.HIndex, I10Index: a.SummaryStats.I10Index,
		URL: a.ID,
	}
	affSet := map[string]bool{}
	for _, aff := range a.Affiliations {
		if aff.Institution.DisplayName != "" {
			affSet[aff.Institution.DisplayName] = true
		}
	}
	for k := range affSet {
		out.Affiliations = append(out.Affiliations, k)
	}
	sort.Strings(out.Affiliations)
	if len(a.LastKnownInstitutions) > 0 {
		out.LastKnownInst = a.LastKnownInstitutions[0].DisplayName
	}
	return out, nil
}

func parseOpenAlexWork(raw json.RawMessage) (OpenAlexWork, error) {
	var w struct {
		ID              string `json:"id"`
		DOI             string `json:"doi"`
		Title           string `json:"title"`
		PublicationYear int    `json:"publication_year"`
		PublicationDate string `json:"publication_date"`
		Type            string `json:"type"`
		OpenAccess      struct {
			IsOA  bool   `json:"is_oa"`
			OAUrl string `json:"oa_url"`
		} `json:"open_access"`
		CitedByCount        int `json:"cited_by_count"`
		ReferencedWorksCount int `json:"referenced_works_count"`
		Authorships         []struct {
			Author struct {
				DisplayName string `json:"display_name"`
			} `json:"author"`
		} `json:"authorships"`
		PrimaryLocation struct {
			Source struct {
				DisplayName string `json:"display_name"`
			} `json:"source"`
		} `json:"primary_location"`
		Concepts []struct {
			DisplayName string  `json:"display_name"`
			Score       float64 `json:"score"`
			Level       int     `json:"level"`
		} `json:"concepts"`
		AbstractInvIndex map[string][]int `json:"abstract_inverted_index"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return OpenAlexWork{}, err
	}
	out := OpenAlexWork{
		ID: strings.TrimPrefix(w.ID, "https://openalex.org/"), DOI: w.DOI,
		Title: w.Title, PublicationYear: w.PublicationYear, PublicationDate: w.PublicationDate,
		Type: w.Type, IsOA: w.OpenAccess.IsOA, OAUrl: w.OpenAccess.OAUrl,
		CitedByCount: w.CitedByCount, ReferencedCount: w.ReferencedWorksCount,
		Source: w.PrimaryLocation.Source.DisplayName,
	}
	for _, a := range w.Authorships {
		if a.Author.DisplayName != "" {
			out.Authors = append(out.Authors, a.Author.DisplayName)
		}
	}
	// Top concepts (level 1-3, descending score)
	for _, c := range w.Concepts {
		if c.Level >= 1 && c.Level <= 3 && c.Score > 0.3 {
			out.Concepts = append(out.Concepts, c.DisplayName)
		}
	}
	if len(out.Concepts) > 6 {
		out.Concepts = out.Concepts[:6]
	}
	// Reconstruct abstract from inverted index (best-effort, first 200 chars)
	if len(w.AbstractInvIndex) > 0 {
		out.Abstract = reconstructAbstract(w.AbstractInvIndex, 250)
	}
	return out, nil
}

func reconstructAbstract(idx map[string][]int, maxLen int) string {
	// Build position → word map
	maxPos := 0
	for _, positions := range idx {
		for _, p := range positions {
			if p > maxPos {
				maxPos = p
			}
		}
	}
	if maxPos > 1000 {
		maxPos = 1000
	}
	words := make([]string, maxPos+1)
	for word, positions := range idx {
		for _, p := range positions {
			if p <= maxPos {
				words[p] = word
			}
		}
	}
	result := strings.Join(words, " ")
	if len(result) > maxLen {
		result = result[:maxLen] + "…"
	}
	return strings.TrimSpace(result)
}
