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

// CrossrefAuthor is one author on a Crossref work record.
type CrossrefAuthor struct {
	Given       string   `json:"given,omitempty"`
	Family      string   `json:"family,omitempty"`
	Name        string   `json:"name,omitempty"`
	ORCID       string   `json:"orcid,omitempty"`
	AffiliationNames []string `json:"affiliations,omitempty"`
}

// CrossrefWork is one scholarly work surfaced from Crossref.
type CrossrefWork struct {
	DOI            string           `json:"doi"`
	Title          string           `json:"title,omitempty"`
	Type           string           `json:"type,omitempty"`
	Container      string           `json:"container,omitempty"`
	Publisher      string           `json:"publisher,omitempty"`
	Year           int              `json:"year,omitempty"`
	IssuedDate     string           `json:"issued_date,omitempty"`
	Citations      int              `json:"citation_count"`
	ReferenceCount int              `json:"reference_count,omitempty"`
	URL            string           `json:"url,omitempty"`
	Subjects       []string         `json:"subjects,omitempty"`
	Authors        []CrossrefAuthor `json:"authors,omitempty"`
	Score          float64          `json:"relevance_score,omitempty"`
	Abstract       string           `json:"abstract,omitempty"`
}

// CrossrefAuthorAggregate is a co-author/author with cross-paper stats.
type CrossrefAuthorAggregate struct {
	Name      string   `json:"name"`
	ORCID     string   `json:"orcid,omitempty"`
	Papers    int      `json:"papers_in_set"`
	TotalCitations int `json:"total_citations_in_set"`
	Affiliations []string `json:"affiliations,omitempty"`
}

// CrossrefPaperSearchOutput is the aggregated response.
type CrossrefPaperSearchOutput struct {
	Mode             string                    `json:"mode"`
	Query            string                    `json:"query"`
	TotalRecords    int                       `json:"total_records"`
	Returned         int                       `json:"returned"`
	Works            []CrossrefWork            `json:"works"`
	TopByCitations   []CrossrefWork            `json:"top_by_citations,omitempty"`
	TopAuthors       []CrossrefAuthorAggregate `json:"top_authors,omitempty"`
	UniqueORCIDs     []string                  `json:"unique_orcids,omitempty"`
	UniqueAffiliations []string                `json:"unique_affiliations,omitempty"`
	UniquePublishers []string                  `json:"unique_publishers,omitempty"`
	UniqueSubjects   []string                  `json:"unique_subjects,omitempty"`
	YearRange        string                    `json:"year_range,omitempty"`
	HighlightFindings []string                 `json:"highlight_findings"`
	Source           string                    `json:"source"`
	TookMs           int64                     `json:"tookMs"`
	Note             string                    `json:"note,omitempty"`
}

type crossrefAuthorRaw struct {
	Given       string                 `json:"given"`
	Family      string                 `json:"family"`
	Name        string                 `json:"name"`
	ORCID       string                 `json:"ORCID"`
	Affiliation []map[string]any       `json:"affiliation"`
}

type crossrefItemRaw struct {
	DOI                 string              `json:"DOI"`
	Title               []string            `json:"title"`
	Type                string              `json:"type"`
	ContainerTitle      []string            `json:"container-title"`
	Publisher           string              `json:"publisher"`
	Issued              struct {
		DateParts [][]int `json:"date-parts"`
	} `json:"issued"`
	IsReferencedBy int                 `json:"is-referenced-by-count"`
	ReferencesCount int                `json:"references-count"`
	URL             string             `json:"URL"`
	Subject         []string           `json:"subject"`
	Author          []crossrefAuthorRaw `json:"author"`
	Score           float64            `json:"score"`
	Abstract        string             `json:"abstract"`
}

type crossrefMessageRaw struct {
	TotalResults int               `json:"total-results"`
	Items        []crossrefItemRaw `json:"items"`
}

type crossrefRespRaw struct {
	Status  string             `json:"status"`
	Message crossrefMessageRaw `json:"message"`
}

// CrossrefPaperSearch queries the Crossref REST API (api.crossref.org) for
// scholarly works. Crossref indexes ~140M+ DOI-registered works across all
// publishers — the de-facto registry for academic citations. Free, no auth
// (just User-Agent with mailto for the polite pool, which we set).
//
// Three modes:
//   - "query"   : full-text-ish search across title/abstract/etc.
//   - "author"  : query by author name (returns matches across many people
//                 sharing that name — strong ER disambiguation surface)
//   - "orcid"   : filter by exact ORCID iD (every work that ORCID is on)
//
// Aggregations: top papers by citations, co-author counter (with ORCIDs when
// disclosed), unique ORCID list, unique affiliations, unique subjects.
//
// Why this matters for ER:
//   - ORCID is a globally-unique researcher identifier; cross-paper ORCID
//     persistence is a *hard* identity link. Two papers sharing an ORCID
//     across a name-change/affiliation-change is gold-standard ER.
//   - Co-author network from Crossref directly maps research-collaboration
//     graphs (complement to OpenAlex's institutional ER).
//   - Affiliation strings reveal employer-of-record at publication time.
func CrossrefPaperSearch(ctx context.Context, input map[string]any) (*CrossrefPaperSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "query"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)

	if query == "" {
		return nil, errors.New("input.query required (keyword for 'query' mode, name for 'author' mode, ORCID like '0000-0001-2345-6789' for 'orcid' mode)")
	}

	limit := 20
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	start := time.Now()

	params := url.Values{}
	params.Set("rows", fmt.Sprintf("%d", limit))
	params.Set("select", "DOI,title,type,container-title,publisher,issued,is-referenced-by-count,references-count,URL,subject,author,score,abstract")

	switch mode {
	case "query":
		params.Set("query", query)
	case "author":
		params.Set("query.author", query)
	case "orcid":
		orcid := normalizeORCID(query)
		if orcid == "" {
			return nil, fmt.Errorf("invalid ORCID '%s' — expected format 0000-0000-0000-0000", query)
		}
		params.Set("filter", "orcid:"+orcid)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: query, author, orcid", mode)
	}

	endpoint := "https://api.crossref.org/works?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1 (+https://github.com/jroell/osint-agent; mailto:abuse@osint-agent.local)")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crossref request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("crossref %d: %s", resp.StatusCode, string(body))
	}

	var raw crossrefRespRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("crossref decode: %w", err)
	}

	out := &CrossrefPaperSearchOutput{
		Mode:         mode,
		Query:        query,
		TotalRecords: raw.Message.TotalResults,
		Source:       "api.crossref.org",
	}

	// Materialize works
	authorAgg := map[string]*CrossrefAuthorAggregate{} // key by lowercased name+orcid
	orcidSet := map[string]struct{}{}
	affilSet := map[string]struct{}{}
	pubSet := map[string]struct{}{}
	subjectSet := map[string]struct{}{}
	minYear, maxYear := 0, 0

	for _, it := range raw.Message.Items {
		w := CrossrefWork{
			DOI:            it.DOI,
			Type:           it.Type,
			Publisher:      it.Publisher,
			Citations:      it.IsReferencedBy,
			ReferenceCount: it.ReferencesCount,
			URL:            it.URL,
			Subjects:       it.Subject,
			Score:          it.Score,
		}
		if len(it.Title) > 0 {
			w.Title = strings.TrimSpace(it.Title[0])
		}
		if len(it.ContainerTitle) > 0 {
			w.Container = strings.TrimSpace(it.ContainerTitle[0])
		}
		if len(it.Issued.DateParts) > 0 && len(it.Issued.DateParts[0]) > 0 {
			w.Year = it.Issued.DateParts[0][0]
			parts := []string{}
			for _, p := range it.Issued.DateParts[0] {
				parts = append(parts, fmt.Sprintf("%02d", p))
			}
			if len(parts) > 0 {
				parts[0] = fmt.Sprintf("%d", it.Issued.DateParts[0][0])
			}
			w.IssuedDate = strings.Join(parts, "-")
		}
		// strip JATS XML tags from abstract for cleanliness
		if it.Abstract != "" {
			w.Abstract = stripJATS(it.Abstract)
			if len(w.Abstract) > 600 {
				w.Abstract = w.Abstract[:600] + "..."
			}
		}

		for _, a := range it.Author {
			ca := CrossrefAuthor{
				Given:  a.Given,
				Family: a.Family,
				Name:   strings.TrimSpace(a.Name),
				ORCID:  a.ORCID,
			}
			if ca.Name == "" {
				ca.Name = strings.TrimSpace(a.Given + " " + a.Family)
			}
			for _, af := range a.Affiliation {
				if name, ok := af["name"].(string); ok && name != "" {
					ca.AffiliationNames = append(ca.AffiliationNames, name)
					affilSet[name] = struct{}{}
				}
			}
			w.Authors = append(w.Authors, ca)

			// aggregate
			if ca.Name == "" {
				continue
			}
			key := strings.ToLower(ca.Name)
			if ca.ORCID != "" {
				key += "|" + ca.ORCID
			}
			ag, found := authorAgg[key]
			if !found {
				ag = &CrossrefAuthorAggregate{Name: ca.Name, ORCID: ca.ORCID}
				authorAgg[key] = ag
			}
			ag.Papers++
			ag.TotalCitations += w.Citations
			for _, af := range ca.AffiliationNames {
				dupe := false
				for _, e := range ag.Affiliations {
					if e == af {
						dupe = true
						break
					}
				}
				if !dupe {
					ag.Affiliations = append(ag.Affiliations, af)
				}
			}
			if ca.ORCID != "" {
				orcidSet[ca.ORCID] = struct{}{}
			}
		}

		if w.Publisher != "" {
			pubSet[w.Publisher] = struct{}{}
		}
		for _, s := range w.Subjects {
			subjectSet[s] = struct{}{}
		}

		if w.Year > 0 {
			if minYear == 0 || w.Year < minYear {
				minYear = w.Year
			}
			if w.Year > maxYear {
				maxYear = w.Year
			}
		}

		out.Works = append(out.Works, w)
	}
	out.Returned = len(out.Works)

	// Top-by-citations: sort copy of Works
	if len(out.Works) > 0 {
		topCites := make([]CrossrefWork, len(out.Works))
		copy(topCites, out.Works)
		sort.SliceStable(topCites, func(i, j int) bool {
			return topCites[i].Citations > topCites[j].Citations
		})
		if len(topCites) > 5 {
			topCites = topCites[:5]
		}
		out.TopByCitations = topCites
	}

	// Top authors
	for _, ag := range authorAgg {
		out.TopAuthors = append(out.TopAuthors, *ag)
	}
	sort.SliceStable(out.TopAuthors, func(i, j int) bool {
		if out.TopAuthors[i].Papers != out.TopAuthors[j].Papers {
			return out.TopAuthors[i].Papers > out.TopAuthors[j].Papers
		}
		return out.TopAuthors[i].TotalCitations > out.TopAuthors[j].TotalCitations
	})
	if len(out.TopAuthors) > 15 {
		out.TopAuthors = out.TopAuthors[:15]
	}

	for k := range orcidSet {
		out.UniqueORCIDs = append(out.UniqueORCIDs, k)
	}
	sort.Strings(out.UniqueORCIDs)
	for k := range affilSet {
		out.UniqueAffiliations = append(out.UniqueAffiliations, k)
	}
	sort.Strings(out.UniqueAffiliations)
	for k := range pubSet {
		out.UniquePublishers = append(out.UniquePublishers, k)
	}
	sort.Strings(out.UniquePublishers)
	for k := range subjectSet {
		out.UniqueSubjects = append(out.UniqueSubjects, k)
	}
	sort.Strings(out.UniqueSubjects)

	if minYear > 0 && maxYear > 0 && minYear != maxYear {
		out.YearRange = fmt.Sprintf("%d-%d", minYear, maxYear)
	} else if maxYear > 0 {
		out.YearRange = fmt.Sprintf("%d", maxYear)
	}

	// highlights
	hi := []string{}
	hi = append(hi, fmt.Sprintf("%d total matching records (%d returned this page)", out.TotalRecords, out.Returned))
	if len(out.UniqueORCIDs) > 0 {
		hi = append(hi, fmt.Sprintf("%d unique ORCID-tagged authors in returned set — hard ER signal", len(out.UniqueORCIDs)))
	}
	if mode == "author" && len(out.TopAuthors) > 1 {
		// look for distinct ORCID variants of same name → namesake warning
		nameVariants := map[string]int{}
		for _, ag := range out.TopAuthors {
			fam := strings.ToLower(strings.TrimSpace(strings.Split(ag.Name, " ")[len(strings.Split(ag.Name, " "))-1]))
			if fam != "" {
				nameVariants[fam]++
			}
		}
		for fam, count := range nameVariants {
			if count >= 3 {
				hi = append(hi, fmt.Sprintf("⚠️  %d distinct authors with surname '%s' — likely namesakes; use ORCID/affiliation to disambiguate", count, fam))
			}
		}
	}
	if len(out.UniqueAffiliations) > 0 {
		hi = append(hi, fmt.Sprintf("%d unique affiliation strings (employer-of-record at publication time)", len(out.UniqueAffiliations)))
	}
	if out.YearRange != "" {
		hi = append(hi, "publication year range: "+out.YearRange)
	}
	if len(out.TopByCitations) > 0 && out.TopByCitations[0].Citations > 0 {
		t := out.TopByCitations[0]
		title := t.Title
		if len(title) > 70 {
			title = title[:70] + "..."
		}
		hi = append(hi, fmt.Sprintf("most-cited in set: '%s' (%d citations, %d, %s)", title, t.Citations, t.Year, t.DOI))
	}
	out.HighlightFindings = hi

	out.TookMs = time.Since(start).Milliseconds()
	if mode == "orcid" && out.TotalRecords == 0 {
		out.Note = "ORCID returned 0 works — author may have no Crossref-indexed works under this ID, or ID is invalid"
	}
	return out, nil
}

func normalizeORCID(s string) string {
	// Accept "0000-0000-0000-0000" or "https://orcid.org/0000-0000-0000-0000"
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://orcid.org/")
	s = strings.TrimPrefix(s, "http://orcid.org/")
	s = strings.TrimPrefix(s, "orcid:")
	parts := strings.Split(s, "-")
	if len(parts) != 4 {
		return ""
	}
	for i, p := range parts {
		if i < 3 && len(p) != 4 {
			return ""
		}
		if i == 3 && len(p) != 4 {
			return ""
		}
	}
	return s
}

// stripJATS removes the most common JATS XML tags from an abstract.
func stripJATS(s string) string {
	repl := []string{
		"<jats:p>", "", "</jats:p>", " ",
		"<jats:title>", "", "</jats:title>", " ",
		"<jats:sec>", "", "</jats:sec>", " ",
		"<jats:italic>", "", "</jats:italic>", "",
		"<jats:bold>", "", "</jats:bold>", "",
		"<jats:sup>", "", "</jats:sup>", "",
		"<jats:sub>", "", "</jats:sub>", "",
	}
	for i := 0; i < len(repl); i += 2 {
		s = strings.ReplaceAll(s, repl[i], repl[i+1])
	}
	return strings.Join(strings.Fields(s), " ")
}
