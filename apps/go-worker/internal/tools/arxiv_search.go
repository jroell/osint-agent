package tools

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ArxivPaper struct {
	ID          string   `json:"arxiv_id"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary,omitempty"`
	Published   string   `json:"published,omitempty"`
	Updated     string   `json:"updated,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	PrimaryCategory string `json:"primary_category,omitempty"`
	PDFURL      string   `json:"pdf_url,omitempty"`
	AbsURL      string   `json:"abs_url,omitempty"`
	DOI         string   `json:"doi,omitempty"`
	JournalRef  string   `json:"journal_ref,omitempty"`
	Comment     string   `json:"comment,omitempty"`
}

type ArxivSearchOutput struct {
	Query           string       `json:"query"`
	TotalResults    int          `json:"total_matching_records"`
	Returned        int          `json:"returned"`
	Papers          []ArxivPaper `json:"papers"`
	UniqueAuthors   []string     `json:"unique_authors,omitempty"`
	UniqueCategories []string    `json:"unique_categories,omitempty"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source          string       `json:"source"`
	TookMs          int64        `json:"tookMs"`
	Note            string       `json:"note,omitempty"`
}

// arxiv Atom feed structure
type arxivFeed struct {
	XMLName        xml.Name `xml:"feed"`
	TotalResults   int      `xml:"http://a9.com/-/spec/opensearch/1.1/ totalResults"`
	StartIndex     int      `xml:"http://a9.com/-/spec/opensearch/1.1/ startIndex"`
	ItemsPerPage   int      `xml:"http://a9.com/-/spec/opensearch/1.1/ itemsPerPage"`
	Entries        []arxivEntry `xml:"entry"`
}

type arxivEntry struct {
	ID         string         `xml:"id"`
	Title      string         `xml:"title"`
	Summary    string         `xml:"summary"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	Authors    []arxivAuthor  `xml:"author"`
	Categories []arxivCategory `xml:"category"`
	Links      []arxivLink    `xml:"link"`
	DOI        string         `xml:"http://arxiv.org/schemas/atom doi"`
	JournalRef string         `xml:"http://arxiv.org/schemas/atom journal_ref"`
	Comment    string         `xml:"http://arxiv.org/schemas/atom comment"`
	PrimaryCategory arxivCategory `xml:"http://arxiv.org/schemas/atom primary_category"`
}

type arxivAuthor struct {
	Name        string `xml:"name"`
	Affiliation string `xml:"http://arxiv.org/schemas/atom affiliation"`
}

type arxivCategory struct {
	Term string `xml:"term,attr"`
}

type arxivLink struct {
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

// ArxivSearch queries arxiv.org's free public API for preprint papers.
// arxiv uses Atom XML; we parse it.
//
// Search modes (encoded into search_query):
//   - "title" (default): search paper titles
//   - "abstract": search abstracts (broader)
//   - "author": search by author name
//   - "category": filter by arxiv subject (cs.AI, cs.LG, cs.CL, etc.)
//   - "all": search all fields
//
// Use cases:
//   - "What's the latest research on RLHF?" — agentic_AI keyword search
//   - Author profile: "All papers by Yoshua Bengio"
//   - Topic discovery: "Papers in cs.AI from this week"
//
// Free, no key. Polite User-Agent recommended; arxiv enforces ~3 sec/query
// for heavy users.
func ArxivSearch(ctx context.Context, input map[string]any) (*ArxivSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	mode, _ := input["mode"].(string)
	if mode == "" {
		mode = "all"
	}
	limit := 20
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}
	sort, _ := input["sort"].(string)
	if sort == "" {
		sort = "relevance" // also: lastUpdatedDate, submittedDate
	}

	// Build search_query
	var searchQuery string
	switch mode {
	case "title":
		searchQuery = "ti:" + q
	case "abstract":
		searchQuery = "abs:" + q
	case "author":
		searchQuery = "au:" + q
	case "category":
		searchQuery = "cat:" + q
	default:
		searchQuery = "all:" + q
	}

	start := time.Now()
	endpoint := fmt.Sprintf("https://export.arxiv.org/api/query?search_query=%s&start=0&max_results=%d&sortBy=%s&sortOrder=descending",
		url.QueryEscape(searchQuery), limit, url.QueryEscape(sort))

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/arxiv (https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/atom+xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("arxiv fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("arxiv status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var feed arxivFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("arxiv parse: %w", err)
	}

	out := &ArxivSearchOutput{
		Query: q, TotalResults: feed.TotalResults,
		Source: "export.arxiv.org",
	}
	authorSet := map[string]bool{}
	categorySet := map[string]bool{}
	for _, e := range feed.Entries {
		paper := ArxivPaper{
			ID:        strings.TrimPrefix(strings.TrimPrefix(e.ID, "http://arxiv.org/abs/"), "https://arxiv.org/abs/"),
			Title:     normalizeXMLText(e.Title),
			Summary:   truncate(normalizeXMLText(e.Summary), 600),
			Published: e.Published,
			Updated:   e.Updated,
			DOI:       e.DOI,
			JournalRef: e.JournalRef,
			Comment:   normalizeXMLText(e.Comment),
			AbsURL:    e.ID,
			PrimaryCategory: e.PrimaryCategory.Term,
		}
		// Authors
		for _, a := range e.Authors {
			n := strings.TrimSpace(a.Name)
			if n != "" {
				paper.Authors = append(paper.Authors, n)
				authorSet[n] = true
			}
		}
		// Categories
		for _, c := range e.Categories {
			if c.Term != "" {
				paper.Categories = append(paper.Categories, c.Term)
				categorySet[c.Term] = true
			}
		}
		// PDF URL
		for _, l := range e.Links {
			if l.Type == "application/pdf" || l.Title == "pdf" {
				paper.PDFURL = l.Href
				break
			}
		}
		out.Papers = append(out.Papers, paper)
	}
	out.Returned = len(out.Papers)
	for a := range authorSet {
		out.UniqueAuthors = append(out.UniqueAuthors, a)
	}
	for c := range categorySet {
		out.UniqueCategories = append(out.UniqueCategories, c)
	}

	highlights := []string{
		fmt.Sprintf("%d total matches; returned %d (mode=%s, sort=%s)", out.TotalResults, out.Returned, mode, sort),
	}
	if len(out.Papers) > 0 {
		top := out.Papers[0]
		highlights = append(highlights, fmt.Sprintf("top: '%s' (%s)", truncate(top.Title, 80), strings.Split(top.Published, "T")[0]))
		if len(top.Authors) > 0 {
			authors := strings.Join(top.Authors[:minInt(4, len(top.Authors))], ", ")
			if len(top.Authors) > 4 {
				authors += fmt.Sprintf(" + %d more", len(top.Authors)-4)
			}
			highlights = append(highlights, "top authors: "+authors)
		}
	}
	if len(out.UniqueAuthors) > 0 {
		highlights = append(highlights, fmt.Sprintf("%d unique authors across results", len(out.UniqueAuthors)))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func normalizeXMLText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
