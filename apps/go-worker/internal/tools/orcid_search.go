package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ORCIDSearch wraps ORCID's free no-auth public API. ~18M+ researchers
// globally with canonical 16-digit identifiers (e.g. 0000-0002-1992-2684
// = Yann LeCun) that cross-reference papers via DOI, employers via ROR,
// and external IDs (Scopus, Researcher ID, Loop, etc.).
//
// Why this is the academic identity-key piece: ORCID is to researchers
// what ISNI is to musicians and GLEIF is to legal entities. Every major
// publisher (Springer/Elsevier/Wiley), funder (NIH/NSF/EU H2020), and
// research org accepts ORCID as the canonical identity anchor. Pairs
// with `pubmed_search` (papers via DOI), `openalex_search` (citation
// graph), `crossref_paper_search` (DOI metadata), `ror_org_lookup`
// (institutional affiliations), `dblp_search` (CS-specific bibliography).
//
// Three modes:
//
//   - "search"   : fuzzy by given-name + family-name + affiliation +
//                   keywords → matching ORCID iDs. Use Lucene-style
//                   query syntax with field qualifiers
//                   (given-names: family-name: affiliation-org-name:
//                   email: text:).
//   - "profile"  : by ORCID iD → comprehensive record: name, biography,
//                   researcher URLs, keywords, country, alternate names,
//                   external identifier counts, employments count,
//                   educations count, works count.
//   - "works"    : by ORCID iD → published works (papers, books, etc.)
//                   with title, type, journal, publication year, DOI.

type ORCIDPerson struct {
	ORCID            string   `json:"orcid"`
	GivenName        string   `json:"given_name,omitempty"`
	FamilyName       string   `json:"family_name,omitempty"`
	CreditName       string   `json:"credit_name,omitempty"`
	Biography        string   `json:"biography,omitempty"`
	Country          string   `json:"country,omitempty"`
	OtherNames       []string `json:"other_names,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	ResearcherURLs   []ORCIDURL `json:"researcher_urls,omitempty"`
	ExternalIDsCount int      `json:"external_ids_count,omitempty"`
	WorksCount       int      `json:"works_count,omitempty"`
	EmploymentsCount int      `json:"employments_count,omitempty"`
	EducationsCount  int      `json:"educations_count,omitempty"`
	URL              string   `json:"orcid_url,omitempty"`
}

type ORCIDURL struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
}

type ORCIDSearchHit struct {
	ORCID string `json:"orcid"`
	URL   string `json:"orcid_url"`
}

type ORCIDWork struct {
	Title    string `json:"title"`
	Type     string `json:"type,omitempty"`
	Journal  string `json:"journal,omitempty"`
	Year     string `json:"year,omitempty"`
	DOI      string `json:"doi,omitempty"`
	URL      string `json:"url,omitempty"`
	PutCode  int    `json:"put_code,omitempty"`
}

type ORCIDSearchOutput struct {
	Mode              string           `json:"mode"`
	Query             string           `json:"query,omitempty"`
	TotalCount        int              `json:"total_count,omitempty"`
	Returned          int              `json:"returned"`
	Hits              []ORCIDSearchHit `json:"hits,omitempty"`
	Profile           *ORCIDPerson     `json:"profile,omitempty"`
	Works             []ORCIDWork      `json:"works,omitempty"`

	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
	Note              string           `json:"note,omitempty"`
}

func ORCIDSearch(ctx context.Context, input map[string]any) (*ORCIDSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if v, ok := input["orcid"].(string); ok && strings.TrimSpace(v) != "" {
			// If it's an orcid + flag → works
			if w, ok := input["works"].(bool); ok && w {
				mode = "works"
			} else {
				mode = "profile"
			}
		} else {
			mode = "search"
		}
	}

	out := &ORCIDSearchOutput{
		Mode:   mode,
		Source: "pub.orcid.org/v3.0",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "search":
		// Build Lucene-style query
		queryParts := []string{}
		if v, ok := input["given_name"].(string); ok && v != "" {
			queryParts = append(queryParts, "given-names:"+v)
		}
		if v, ok := input["family_name"].(string); ok && v != "" {
			queryParts = append(queryParts, "family-name:"+v)
		}
		if v, ok := input["affiliation"].(string); ok && v != "" {
			queryParts = append(queryParts, fmt.Sprintf("affiliation-org-name:\"%s\"", v))
		}
		if v, ok := input["keywords"].(string); ok && v != "" {
			queryParts = append(queryParts, "keyword:"+v)
		}
		if v, ok := input["query"].(string); ok && v != "" {
			queryParts = append(queryParts, v) // raw query
		}
		if len(queryParts) == 0 {
			return nil, fmt.Errorf("at least one of: given_name + family_name, affiliation, keywords, or query required")
		}
		q := strings.Join(queryParts, " AND ")
		out.Query = q
		params := url.Values{}
		params.Set("q", q)
		rows := 5
		if r, ok := input["limit"].(float64); ok && r > 0 && r <= 20 {
			rows = int(r)
		}
		params.Set("rows", fmt.Sprintf("%d", rows))
		body, err := orcidGet(ctx, cli, "https://pub.orcid.org/v3.0/search/?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			NumFound int `json:"num-found"`
			Result   []struct {
				OrcidIdentifier struct {
					Path string `json:"path"`
					URI  string `json:"uri"`
				} `json:"orcid-identifier"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("orcid decode: %w", err)
		}
		out.TotalCount = raw.NumFound
		for _, h := range raw.Result {
			out.Hits = append(out.Hits, ORCIDSearchHit{
				ORCID: h.OrcidIdentifier.Path,
				URL:   h.OrcidIdentifier.URI,
			})
		}
		out.Returned = len(out.Hits)

	case "profile":
		orcidID, _ := input["orcid"].(string)
		orcidID = strings.TrimSpace(orcidID)
		if orcidID == "" {
			return nil, fmt.Errorf("input.orcid required for profile mode")
		}
		out.Query = orcidID
		body, err := orcidGet(ctx, cli, "https://pub.orcid.org/v3.0/"+orcidID+"/record")
		if err != nil {
			return nil, err
		}
		profile, err := parseORCIDRecord(body, orcidID)
		if err != nil {
			return nil, err
		}
		out.Profile = profile
		out.Returned = 1

	case "works":
		orcidID, _ := input["orcid"].(string)
		orcidID = strings.TrimSpace(orcidID)
		if orcidID == "" {
			return nil, fmt.Errorf("input.orcid required for works mode")
		}
		out.Query = orcidID
		body, err := orcidGet(ctx, cli, "https://pub.orcid.org/v3.0/"+orcidID+"/works")
		if err != nil {
			return nil, err
		}
		works, err := parseORCIDWorks(body)
		if err != nil {
			return nil, err
		}
		// Sort by year desc
		sort.SliceStable(works, func(i, j int) bool { return works[i].Year > works[j].Year })
		limit := 50
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
			limit = int(l)
		}
		out.TotalCount = len(works)
		if len(works) > limit {
			out.Note = fmt.Sprintf("returning %d of %d works", limit, len(works))
			works = works[:limit]
		}
		out.Works = works
		out.Returned = len(out.Works)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, profile, works", mode)
	}

	out.HighlightFindings = buildORCIDHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseORCIDRecord(body []byte, orcidID string) (*ORCIDPerson, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("orcid record decode: %w", err)
	}
	p := &ORCIDPerson{
		ORCID: orcidID,
		URL:   "https://orcid.org/" + orcidID,
	}
	if person, ok := raw["person"].(map[string]any); ok {
		if name, ok := person["name"].(map[string]any); ok {
			if gn, ok := name["given-names"].(map[string]any); ok {
				p.GivenName = gtString(gn, "value")
			}
			if fn, ok := name["family-name"].(map[string]any); ok {
				p.FamilyName = gtString(fn, "value")
			}
			if cn, ok := name["credit-name"].(map[string]any); ok {
				p.CreditName = gtString(cn, "value")
			}
		}
		if bio, ok := person["biography"].(map[string]any); ok {
			p.Biography = hfTruncate(gtString(bio, "content"), 500)
		}
		if otherNames, ok := person["other-names"].(map[string]any); ok {
			if names, ok := otherNames["name"].([]any); ok {
				for _, n := range names {
					if nm, ok := n.(map[string]any); ok {
						if c := gtString(nm, "content"); c != "" {
							p.OtherNames = append(p.OtherNames, c)
						}
					}
				}
			}
		}
		if kw, ok := person["keywords"].(map[string]any); ok {
			if kwList, ok := kw["keyword"].([]any); ok {
				for _, k := range kwList {
					if km, ok := k.(map[string]any); ok {
						if c := gtString(km, "content"); c != "" {
							p.Keywords = append(p.Keywords, c)
						}
					}
				}
			}
		}
		if urls, ok := person["researcher-urls"].(map[string]any); ok {
			if urlList, ok := urls["researcher-url"].([]any); ok {
				for _, u := range urlList {
					if um, ok := u.(map[string]any); ok {
						urlObj := ORCIDURL{
							Name: gtString(um, "url-name"),
						}
						if urlField, ok := um["url"].(map[string]any); ok {
							urlObj.URL = gtString(urlField, "value")
						}
						if urlObj.URL != "" {
							p.ResearcherURLs = append(p.ResearcherURLs, urlObj)
						}
					}
				}
			}
		}
		if addrs, ok := person["addresses"].(map[string]any); ok {
			if addrList, ok := addrs["address"].([]any); ok && len(addrList) > 0 {
				if a, ok := addrList[0].(map[string]any); ok {
					if c, ok := a["country"].(map[string]any); ok {
						p.Country = gtString(c, "value")
					}
				}
			}
		}
		if extIDs, ok := person["external-identifiers"].(map[string]any); ok {
			if list, ok := extIDs["external-identifier"].([]any); ok {
				p.ExternalIDsCount = len(list)
			}
		}
	}
	if act, ok := raw["activities-summary"].(map[string]any); ok {
		if works, ok := act["works"].(map[string]any); ok {
			if grp, ok := works["group"].([]any); ok {
				p.WorksCount = len(grp)
			}
		}
		if emp, ok := act["employments"].(map[string]any); ok {
			if grp, ok := emp["affiliation-group"].([]any); ok {
				p.EmploymentsCount = len(grp)
			}
		}
		if edu, ok := act["educations"].(map[string]any); ok {
			if grp, ok := edu["affiliation-group"].([]any); ok {
				p.EducationsCount = len(grp)
			}
		}
	}
	return p, nil
}

func parseORCIDWorks(body []byte) ([]ORCIDWork, error) {
	var raw struct {
		Group []struct {
			WorkSummary []map[string]any `json:"work-summary"`
		} `json:"group"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("orcid works decode: %w", err)
	}
	out := []ORCIDWork{}
	for _, g := range raw.Group {
		if len(g.WorkSummary) == 0 {
			continue
		}
		// Take first version
		ws := g.WorkSummary[0]
		w := ORCIDWork{
			Type:    gtString(ws, "type"),
			PutCode: gtInt(ws, "put-code"),
		}
		if titleObj, ok := ws["title"].(map[string]any); ok {
			if t, ok := titleObj["title"].(map[string]any); ok {
				w.Title = gtString(t, "value")
			}
		}
		if jt, ok := ws["journal-title"].(map[string]any); ok {
			w.Journal = gtString(jt, "value")
		}
		if pd, ok := ws["publication-date"].(map[string]any); ok {
			if y, ok := pd["year"].(map[string]any); ok {
				w.Year = gtString(y, "value")
			}
		}
		if extIDs, ok := ws["external-ids"].(map[string]any); ok {
			if list, ok := extIDs["external-id"].([]any); ok {
				for _, e := range list {
					if em, ok := e.(map[string]any); ok {
						if gtString(em, "external-id-type") == "doi" {
							w.DOI = gtString(em, "external-id-value")
							break
						}
					}
				}
			}
		}
		if w.DOI != "" {
			w.URL = "https://doi.org/" + w.DOI
		}
		if w.Title != "" {
			out = append(out, w)
		}
	}
	return out, nil
}

func orcidGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("orcid: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("orcid HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildORCIDHighlights(o *ORCIDSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d researchers match '%s' (returning %d)", o.TotalCount, o.Query, o.Returned))
		for i, h := range o.Hits {
			if i >= 8 {
				break
			}
			hi = append(hi, fmt.Sprintf("  • %s — %s", h.ORCID, h.URL))
		}

	case "profile":
		if o.Profile == nil {
			hi = append(hi, "✗ no profile")
			break
		}
		p := o.Profile
		display := strings.TrimSpace(p.GivenName + " " + p.FamilyName)
		if p.CreditName != "" {
			display = p.CreditName
		}
		hi = append(hi, fmt.Sprintf("✓ %s — ORCID %s", display, p.ORCID))
		stats := []string{}
		if p.WorksCount > 0 {
			stats = append(stats, fmt.Sprintf("%d works", p.WorksCount))
		}
		if p.EmploymentsCount > 0 {
			stats = append(stats, fmt.Sprintf("%d employments", p.EmploymentsCount))
		}
		if p.EducationsCount > 0 {
			stats = append(stats, fmt.Sprintf("%d educations", p.EducationsCount))
		}
		if p.ExternalIDsCount > 0 {
			stats = append(stats, fmt.Sprintf("%d external IDs", p.ExternalIDsCount))
		}
		if len(stats) > 0 {
			hi = append(hi, "  "+strings.Join(stats, " · "))
		}
		if p.Country != "" {
			hi = append(hi, "  country: "+p.Country)
		}
		if p.Biography != "" {
			hi = append(hi, "  bio: "+hfTruncate(p.Biography, 250))
		}
		if len(p.Keywords) > 0 {
			kws := strings.Join(p.Keywords, "; ")
			hi = append(hi, "  keywords: "+hfTruncate(kws, 200))
		}
		if len(p.OtherNames) > 0 {
			hi = append(hi, "  other names: "+strings.Join(p.OtherNames, "; "))
		}
		if len(p.ResearcherURLs) > 0 {
			hi = append(hi, fmt.Sprintf("  researcher URLs (%d):", len(p.ResearcherURLs)))
			for i, u := range p.ResearcherURLs {
				if i >= 5 {
					break
				}
				name := u.Name
				if name == "" {
					name = "(unnamed)"
				}
				hi = append(hi, fmt.Sprintf("    %s: %s", name, u.URL))
			}
		}

	case "works":
		hi = append(hi, fmt.Sprintf("✓ %d works (returning %d)", o.TotalCount, o.Returned))
		for i, w := range o.Works {
			if i >= 8 {
				break
			}
			meta := []string{}
			if w.Year != "" {
				meta = append(meta, w.Year)
			}
			if w.Type != "" {
				meta = append(meta, w.Type)
			}
			if w.Journal != "" {
				meta = append(meta, w.Journal)
			}
			doi := ""
			if w.DOI != "" {
				doi = " · DOI " + w.DOI
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s%s", strings.Join(meta, " · "), hfTruncate(w.Title, 80), doi))
		}
	}
	return hi
}
