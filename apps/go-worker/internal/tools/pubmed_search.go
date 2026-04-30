package tools

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// PubMedAuthor is one author on a paper.
type PubMedAuthor struct {
	ForeName    string `json:"fore_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Initials    string `json:"initials,omitempty"`
	FullName    string `json:"full_name,omitempty"`
	ORCID       string `json:"orcid,omitempty"`
	Affiliation string `json:"affiliation,omitempty"`
}

// PubMedPaper is one paper.
type PubMedPaper struct {
	PMID            string         `json:"pmid"`
	Title           string         `json:"title"`
	Journal         string         `json:"journal,omitempty"`
	JournalISO      string         `json:"journal_iso,omitempty"`
	PubYear         string         `json:"pub_year,omitempty"`
	PubDate         string         `json:"pub_date,omitempty"`
	DOI             string         `json:"doi,omitempty"`
	Abstract        string         `json:"abstract,omitempty"`
	Authors         []PubMedAuthor `json:"authors,omitempty"`
	MeSHTerms       []string       `json:"mesh_terms,omitempty"`
	URL             string         `json:"pubmed_url"`
	UniqueAffiliations []string    `json:"unique_affiliations_in_paper,omitempty"`
	UniqueORCIDs    []string       `json:"unique_orcids_in_paper,omitempty"`
}

// PubMedAffiliationAggregate counts (author, affiliation) pairs across papers.
type PubMedAffiliationAggregate struct {
	Affiliation string `json:"affiliation"`
	PaperCount  int    `json:"paper_count"`
}

// PubMedAuthorAggregate counts (author, paper) pairs.
type PubMedAuthorAggregate struct {
	FullName   string `json:"full_name"`
	ORCID      string `json:"orcid,omitempty"`
	PaperCount int    `json:"paper_count"`
}

// PubMedMeshAggregate counts MeSH term frequency.
type PubMedMeshAggregate struct {
	Term  string `json:"term"`
	Count int    `json:"count"`
}

// PubMedSearchOutput is the response.
type PubMedSearchOutput struct {
	Mode             string                       `json:"mode"`
	Query            string                       `json:"query"`
	TotalResults     int                          `json:"total_results"`
	Returned         int                          `json:"returned"`
	Papers           []PubMedPaper                `json:"papers"`
	UniqueAffiliations []PubMedAffiliationAggregate `json:"unique_affiliations,omitempty"`
	UniqueORCIDs     []string                     `json:"unique_orcids,omitempty"`
	TopAuthors       []PubMedAuthorAggregate      `json:"top_authors,omitempty"`
	TopMeshTerms     []PubMedMeshAggregate        `json:"top_mesh_terms,omitempty"`
	YearRange        string                       `json:"year_range,omitempty"`
	HighlightFindings []string                    `json:"highlight_findings"`
	Source           string                       `json:"source"`
	TookMs           int64                        `json:"tookMs"`
	Note             string                       `json:"note,omitempty"`
}

type esearchRaw struct {
	ESearchResult struct {
		Count    string   `json:"count"`
		IdList   []string `json:"idlist"`
	} `json:"esearchresult"`
}

// XML structs for efetch
type efetchSetXML struct {
	XMLName  xml.Name           `xml:"PubmedArticleSet"`
	Articles []efetchArticleXML `xml:"PubmedArticle"`
}

type efetchArticleXML struct {
	MedlineCitation struct {
		PMID    string `xml:"PMID"`
		Article struct {
			Journal struct {
				ISOAbbreviation string `xml:"ISOAbbreviation"`
				Title           string `xml:"Title"`
				JournalIssue    struct {
					PubDate struct {
						Year     string `xml:"Year"`
						Month    string `xml:"Month"`
						Day      string `xml:"Day"`
						MedlineDate string `xml:"MedlineDate"`
					} `xml:"PubDate"`
				} `xml:"JournalIssue"`
			} `xml:"Journal"`
			ArticleTitle string `xml:"ArticleTitle"`
			Abstract     struct {
				AbstractText []string `xml:"AbstractText"`
			} `xml:"Abstract"`
			AuthorList struct {
				Authors []efetchAuthorXML `xml:"Author"`
			} `xml:"AuthorList"`
			ELocationIDs []struct {
				EIDType string `xml:"EIdType,attr"`
				Value   string `xml:",chardata"`
			} `xml:"ELocationID"`
		} `xml:"Article"`
		MeshHeadingList struct {
			MeshHeadings []struct {
				DescriptorName string `xml:"DescriptorName"`
			} `xml:"MeshHeading"`
		} `xml:"MeshHeadingList"`
	} `xml:"MedlineCitation"`
}

type efetchAuthorXML struct {
	LastName    string `xml:"LastName"`
	ForeName    string `xml:"ForeName"`
	Initials    string `xml:"Initials"`
	Identifier  []struct {
		Source string `xml:"Source,attr"`
		Value  string `xml:",chardata"`
	} `xml:"Identifier"`
	AffiliationInfo []struct {
		Affiliation string `xml:"Affiliation"`
	} `xml:"AffiliationInfo"`
}

// PubMedSearch queries NCBI's PubMed E-utilities API. Free, no auth (rate
// limits apply: 3 req/s without API key, 10 req/s with NCBI_API_KEY).
//
// 3 modes:
//   - "search"        : keyword search → PMIDs + summary metadata
//   - "author_search" : author name (Last+Initials format) → their papers
//                       with per-paper affiliation tracking (employer trail)
//   - "pmid_lookup"   : full metadata for specific PMIDs (comma-separated)
//                       — authors with ORCID + affiliations, abstract, MeSH
//
// Why this matters for ER:
//   - PubMed indexes ~37M biomedical papers. Direct complement to NIH
//     RePORTER (grants), Crossref (DOIs), OpenAlex (citations).
//   - Per-author ORCID linkage is hard cross-paper ER (same as Crossref).
//   - Per-paper affiliation strings reveal employer-of-record at time
//     of publication — temporal trail across a researcher's career.
//   - MeSH (Medical Subject Headings) controlled vocabulary enables
//     precise topic-based ER queries.
//   - Pairs with NIH RePORTER for biomed researcher recon: NIH gives
//     funding history; PubMed gives publication output; together they
//     reveal "who's funded vs who's productive" gaps.
func PubMedSearch(ctx context.Context, input map[string]any) (*PubMedSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "search"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required")
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &PubMedSearchOutput{
		Mode:   mode,
		Query:  query,
		Source: "eutils.ncbi.nlm.nih.gov (PubMed)",
	}
	start := time.Now()
	client := &http.Client{Timeout: 60 * time.Second}

	var pmids []string
	var totalCount int

	switch mode {
	case "search":
		ids, total, err := pmEsearch(ctx, client, query, limit)
		if err != nil {
			return nil, err
		}
		pmids = ids
		totalCount = total
	case "author_search":
		// Format author query: "LastName Initials" or just "LastName"
		authorQuery := query + "[Author]"
		ids, total, err := pmEsearch(ctx, client, authorQuery, limit)
		if err != nil {
			return nil, err
		}
		pmids = ids
		totalCount = total
	case "pmid_lookup":
		// query is comma-separated PMIDs
		pmids = strings.Split(query, ",")
		for i, p := range pmids {
			pmids[i] = strings.TrimSpace(p)
		}
		totalCount = len(pmids)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, author_search, pmid_lookup", mode)
	}

	out.TotalResults = totalCount

	if len(pmids) == 0 {
		out.Note = fmt.Sprintf("no PubMed results for '%s' (mode=%s)", query, mode)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// efetch full XML for the PMIDs
	papers, err := pmEfetch(ctx, client, pmids)
	if err != nil {
		return nil, err
	}
	out.Papers = papers
	out.Returned = len(papers)

	// Aggregations
	affMap := map[string]int{}
	authorMap := map[string]*PubMedAuthorAggregate{}
	orcidSet := map[string]struct{}{}
	meshMap := map[string]int{}
	minYear, maxYear := "", ""

	for _, p := range papers {
		if p.PubYear != "" {
			if minYear == "" || p.PubYear < minYear {
				minYear = p.PubYear
			}
			if p.PubYear > maxYear {
				maxYear = p.PubYear
			}
		}
		// Track affiliation evolution
		seenAffInPaper := map[string]bool{}
		for _, a := range p.Authors {
			if a.Affiliation != "" && !seenAffInPaper[a.Affiliation] {
				seenAffInPaper[a.Affiliation] = true
				affMap[a.Affiliation]++
			}
			// Author counter
			key := a.FullName
			if a.ORCID != "" {
				key = a.FullName + "|" + a.ORCID
			}
			if a.FullName != "" {
				ag, ok := authorMap[key]
				if !ok {
					ag = &PubMedAuthorAggregate{FullName: a.FullName, ORCID: a.ORCID}
					authorMap[key] = ag
				}
				ag.PaperCount++
			}
			if a.ORCID != "" {
				orcidSet[a.ORCID] = struct{}{}
			}
		}
		for _, m := range p.MeSHTerms {
			meshMap[m]++
		}
	}

	for af, c := range affMap {
		out.UniqueAffiliations = append(out.UniqueAffiliations, PubMedAffiliationAggregate{Affiliation: af, PaperCount: c})
	}
	sort.SliceStable(out.UniqueAffiliations, func(i, j int) bool {
		return out.UniqueAffiliations[i].PaperCount > out.UniqueAffiliations[j].PaperCount
	})
	if len(out.UniqueAffiliations) > 15 {
		out.UniqueAffiliations = out.UniqueAffiliations[:15]
	}

	for _, ag := range authorMap {
		out.TopAuthors = append(out.TopAuthors, *ag)
	}
	sort.SliceStable(out.TopAuthors, func(i, j int) bool {
		return out.TopAuthors[i].PaperCount > out.TopAuthors[j].PaperCount
	})
	if len(out.TopAuthors) > 15 {
		out.TopAuthors = out.TopAuthors[:15]
	}

	for o := range orcidSet {
		out.UniqueORCIDs = append(out.UniqueORCIDs, o)
	}
	sort.Strings(out.UniqueORCIDs)

	for m, c := range meshMap {
		out.TopMeshTerms = append(out.TopMeshTerms, PubMedMeshAggregate{Term: m, Count: c})
	}
	sort.SliceStable(out.TopMeshTerms, func(i, j int) bool { return out.TopMeshTerms[i].Count > out.TopMeshTerms[j].Count })
	if len(out.TopMeshTerms) > 15 {
		out.TopMeshTerms = out.TopMeshTerms[:15]
	}

	if minYear != "" && maxYear != "" {
		if minYear == maxYear {
			out.YearRange = minYear
		} else {
			out.YearRange = minYear + "-" + maxYear
		}
	}

	out.HighlightFindings = buildPubMedHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func pmEsearch(ctx context.Context, client *http.Client, query string, limit int) ([]string, int, error) {
	params := url.Values{}
	params.Set("db", "pubmed")
	params.Set("term", query)
	params.Set("retmode", "json")
	params.Set("retmax", fmt.Sprintf("%d", limit))
	params.Set("sort", "relevance")
	endpoint := "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("pubmed esearch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("pubmed esearch %d", resp.StatusCode)
	}
	var raw esearchRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	total := 0
	fmt.Sscanf(raw.ESearchResult.Count, "%d", &total)
	return raw.ESearchResult.IdList, total, nil
}

func pmEfetch(ctx context.Context, client *http.Client, pmids []string) ([]PubMedPaper, error) {
	if len(pmids) == 0 {
		return nil, nil
	}
	params := url.Values{}
	params.Set("db", "pubmed")
	params.Set("id", strings.Join(pmids, ","))
	params.Set("retmode", "xml")
	params.Set("rettype", "abstract")
	endpoint := "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/efetch.fcgi?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/xml")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pubmed efetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("pubmed efetch %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var raw efetchSetXML
	if err := xml.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("pubmed efetch xml: %w", err)
	}
	out := []PubMedPaper{}
	for _, art := range raw.Articles {
		mc := art.MedlineCitation
		p := PubMedPaper{
			PMID:       mc.PMID,
			Title:      strings.TrimSpace(mc.Article.ArticleTitle),
			Journal:    mc.Article.Journal.Title,
			JournalISO: mc.Article.Journal.ISOAbbreviation,
		}
		// pub date
		pd := mc.Article.Journal.JournalIssue.PubDate
		if pd.Year != "" {
			p.PubYear = pd.Year
			p.PubDate = pd.Year
			if pd.Month != "" {
				p.PubDate += " " + pd.Month
			}
			if pd.Day != "" {
				p.PubDate += " " + pd.Day
			}
		} else if pd.MedlineDate != "" {
			// Extract first 4 digits as year
			for i := 0; i < len(pd.MedlineDate)-3; i++ {
				if pd.MedlineDate[i] >= '1' && pd.MedlineDate[i] <= '9' {
					p.PubYear = pd.MedlineDate[i : i+4]
					break
				}
			}
			p.PubDate = pd.MedlineDate
		}
		// abstract — concat all AbstractText
		abs := strings.Join(mc.Article.Abstract.AbstractText, " ")
		if len(abs) > 1000 {
			abs = abs[:1000] + "..."
		}
		p.Abstract = abs
		// DOI
		for _, e := range mc.Article.ELocationIDs {
			if strings.EqualFold(e.EIDType, "doi") {
				p.DOI = strings.TrimSpace(e.Value)
				break
			}
		}
		// Authors
		affSet := map[string]struct{}{}
		orcidSet := map[string]struct{}{}
		for _, a := range mc.Article.AuthorList.Authors {
			pa := PubMedAuthor{
				LastName: a.LastName,
				ForeName: a.ForeName,
				Initials: a.Initials,
			}
			pa.FullName = strings.TrimSpace(a.ForeName + " " + a.LastName)
			if pa.FullName == "" {
				pa.FullName = a.LastName + " " + a.Initials
			}
			for _, id := range a.Identifier {
				if strings.EqualFold(id.Source, "ORCID") {
					orcid := strings.TrimSpace(id.Value)
					orcid = strings.TrimPrefix(orcid, "https://orcid.org/")
					pa.ORCID = orcid
					orcidSet[orcid] = struct{}{}
				}
			}
			if len(a.AffiliationInfo) > 0 {
				pa.Affiliation = strings.TrimSpace(a.AffiliationInfo[0].Affiliation)
				if pa.Affiliation != "" {
					affSet[pa.Affiliation] = struct{}{}
				}
			}
			p.Authors = append(p.Authors, pa)
		}
		for af := range affSet {
			p.UniqueAffiliations = append(p.UniqueAffiliations, af)
		}
		for o := range orcidSet {
			p.UniqueORCIDs = append(p.UniqueORCIDs, o)
		}
		// MeSH
		for _, m := range mc.MeshHeadingList.MeshHeadings {
			if m.DescriptorName != "" {
				p.MeSHTerms = append(p.MeSHTerms, m.DescriptorName)
			}
		}
		p.URL = "https://pubmed.ncbi.nlm.nih.gov/" + mc.PMID + "/"
		out = append(out, p)
	}
	return out, nil
}

func buildPubMedHighlights(o *PubMedSearchOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d PubMed results for mode=%s query='%s' (returned %d)", o.TotalResults, o.Mode, o.Query, o.Returned))
	if o.YearRange != "" {
		hi = append(hi, "publication year range: "+o.YearRange)
	}
	if len(o.UniqueAffiliations) > 0 {
		topAffs := []string{}
		for _, a := range o.UniqueAffiliations[:min2(5, len(o.UniqueAffiliations))] {
			affShort := a.Affiliation
			if len(affShort) > 80 {
				affShort = affShort[:80] + "..."
			}
			topAffs = append(topAffs, fmt.Sprintf("%dx %s", a.PaperCount, affShort))
		}
		hi = append(hi, "🏛 top affiliations across returned papers (per-paper temporal employer trail):")
		for _, t := range topAffs {
			hi = append(hi, "  "+t)
		}
	}
	if len(o.UniqueORCIDs) > 0 {
		hi = append(hi, fmt.Sprintf("✓ %d unique ORCIDs across returned papers — hard cross-paper ER signal (links to crossref/openalex)", len(o.UniqueORCIDs)))
	}
	if len(o.TopAuthors) > 0 && o.Mode != "author_search" {
		topA := []string{}
		for _, a := range o.TopAuthors[:min2(5, len(o.TopAuthors))] {
			suffix := ""
			if a.ORCID != "" {
				suffix = " (ORCID:" + a.ORCID + ")"
			}
			topA = append(topA, fmt.Sprintf("%s (%dx)%s", a.FullName, a.PaperCount, suffix))
		}
		hi = append(hi, "👥 top authors in returned set: "+strings.Join(topA, " | "))
	}
	if len(o.TopMeshTerms) > 0 {
		topM := []string{}
		for _, m := range o.TopMeshTerms[:min2(8, len(o.TopMeshTerms))] {
			topM = append(topM, fmt.Sprintf("%s (%d)", m.Term, m.Count))
		}
		hi = append(hi, "🔬 top MeSH terms (controlled-vocabulary classification): "+strings.Join(topM, ", "))
	}
	if len(o.Papers) > 0 {
		p := o.Papers[0]
		hi = append(hi, fmt.Sprintf("most recent paper: '%s' (%s, PMID %s)", hfTruncate(p.Title, 80), p.Journal, p.PMID))
	}
	return hi
}
