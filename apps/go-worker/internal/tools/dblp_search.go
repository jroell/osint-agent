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

// DBLPAuthorHit is one hit from author search.
type DBLPAuthorHit struct {
	PID          string   `json:"pid"`            // DBLP author identifier
	Name         string   `json:"name"`
	Aliases      []string `json:"aliases,omitempty"`
	URL          string   `json:"url,omitempty"`
	Notes        []string `json:"notes,omitempty"` // affiliation notes when DBLP knows them
}

// DBLPPublication is one CS paper.
type DBLPPublication struct {
	Key       string   `json:"key,omitempty"`
	Type      string   `json:"type,omitempty"` // article | inproceedings | book
	Title     string   `json:"title"`
	Authors   []string `json:"authors,omitempty"`
	Venue     string   `json:"venue,omitempty"`
	Year      string   `json:"year,omitempty"`
	URL       string   `json:"url,omitempty"`
	DOI       string   `json:"doi,omitempty"`
	EE        string   `json:"electronic_edition,omitempty"`
}

// DBLPCrossPlatformLink is one external URL linked from an author profile.
type DBLPCrossPlatformLink struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	Handle   string `json:"handle,omitempty"`
}

// DBLPVenueAggregate counts publications per venue.
type DBLPVenueAggregate struct {
	Venue string `json:"venue"`
	Count int    `json:"count"`
}

// DBLPYearAggregate counts per year.
type DBLPYearAggregate struct {
	Year  string `json:"year"`
	Count int    `json:"count"`
}

// DBLPCoauthorAggregate counts coauthor frequency.
type DBLPCoauthorAggregate struct {
	Name  string `json:"name"`
	PID   string `json:"pid,omitempty"`
	Count int    `json:"count"`
}

// DBLPSearchOutput is the response.
type DBLPSearchOutput struct {
	Mode             string                  `json:"mode"`
	Query            string                  `json:"query,omitempty"`
	PID              string                  `json:"author_pid,omitempty"`
	AuthorName       string                  `json:"author_name,omitempty"`
	AuthorAliases    []string                `json:"author_aliases,omitempty"`
	TotalResults     int                     `json:"total_results"`
	AuthorHits       []DBLPAuthorHit         `json:"author_hits,omitempty"`
	Publications     []DBLPPublication       `json:"publications,omitempty"`
	CrossPlatformLinks []DBLPCrossPlatformLink `json:"cross_platform_links,omitempty"`
	TopVenues        []DBLPVenueAggregate    `json:"top_venues,omitempty"`
	YearDistribution []DBLPYearAggregate     `json:"year_distribution,omitempty"`
	TopCoauthors     []DBLPCoauthorAggregate `json:"top_coauthors,omitempty"`
	YearRange        string                  `json:"year_range,omitempty"`
	HighlightFindings []string               `json:"highlight_findings"`
	Source           string                  `json:"source"`
	TookMs           int64                   `json:"tookMs"`
	Note             string                  `json:"note,omitempty"`
}

// raw structures for DBLP responses
type dblpSearchRaw struct {
	Result struct {
		Hits struct {
			Total string `json:"@total"`
			Hit   []struct {
				ID   string `json:"@id"`
				Info json.RawMessage `json:"info"`
			} `json:"hit"`
		} `json:"hits"`
	} `json:"result"`
}

type dblpAuthorInfoRaw struct {
	PID     string `json:"@pid"`
	URL     string `json:"url"`
	Author  string `json:"author"`
	Aliases struct {
		Alias json.RawMessage `json:"alias"`
	} `json:"aliases"`
	Notes struct {
		Note json.RawMessage `json:"note"`
	} `json:"notes"`
}

type dblpPublInfoRaw struct {
	Key     string          `json:"@key"`
	Type    string          `json:"type"`
	Title   string          `json:"title"`
	Year    string          `json:"year"`
	Venue   string          `json:"venue"`
	URL     string          `json:"url"`
	DOI     string          `json:"doi"`
	EE      string          `json:"ee"`
	Authors json.RawMessage `json:"authors"`
}

// DBLP author profile XML structures
type dblpPersonXML struct {
	XMLName xml.Name `xml:"dblpperson"`
	Name    string   `xml:"name,attr"`
	PID     string   `xml:"pid,attr"`
	N       int      `xml:"n,attr"`
	Person  struct {
		Author []struct {
			PID  string `xml:"pid,attr"`
			Text string `xml:",chardata"`
		} `xml:"author"`
		URL  []string `xml:"url"`
	} `xml:"person"`
	R []struct {
		// Each <r> wraps one publication record
		Article       *dblpRefXML `xml:"article"`
		InProceedings *dblpRefXML `xml:"inproceedings"`
		Book          *dblpRefXML `xml:"book"`
		Incollection  *dblpRefXML `xml:"incollection"`
		PhdThesis     *dblpRefXML `xml:"phdthesis"`
	} `xml:"r"`
}

type dblpRefXML struct {
	Key     string `xml:"key,attr"`
	Title   string `xml:"title"`
	Year    string `xml:"year"`
	Journal string `xml:"journal"`
	Booktitle string `xml:"booktitle"`
	Authors []struct {
		PID  string `xml:"pid,attr"`
		Text string `xml:",chardata"`
	} `xml:"author"`
	EE   []string `xml:"ee"`
	URL  string   `xml:"url"`
}

// DBLPSearch queries DBLP for CS academic ER. Free, no auth.
//
// Three modes:
//   - "author_search"      : fuzzy name → PID + aliases + URL
//   - "author_profile"     : full author profile by name OR PID — includes
//                            external URL list (ORCID, Twitter, Scholar,
//                            Wikipedia, ACM, etc) which is the **identity-
//                            bridge feature** unique to DBLP.
//   - "publication_search" : full-text title/keyword search
//
// Why this matters for ER:
//   - DBLP is the canonical CS publication index with ~6M+ papers and
//     stable author identifiers (PIDs).
//   - DBLP author profiles aggregate external links the author has
//     declared/been associated with: ORCID, Twitter, Wikipedia, Google
//     Scholar, ACM Digital Library, MathSciNet, Wikidata, personal
//     homepage, etc. This makes DBLP a **multi-platform identity bridge**
//     for any CS researcher — pivotal complement to crossref_paper_search
//     (which ties via ORCID), openalex_search (h-index), arxiv_search
//     (preprints), and huggingface_hub_search (AI/ML models).
//   - Coauthor network is the CS-side of research-collaboration graph.
//   - Venue history reveals research subfield (NeurIPS = ML; CRYPTO =
//     cryptography; SIGGRAPH = graphics).
func DBLPSearch(ctx context.Context, input map[string]any) (*DBLPSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "author_search"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (author name for author_*, paper keyword for publication_search)")
	}

	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &DBLPSearchOutput{
		Mode:   mode,
		Query:  query,
		Source: "dblp.org",
	}
	start := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "author_search":
		hits, total, err := dblpAuthorSearch(ctx, client, query, limit)
		if err != nil {
			return nil, err
		}
		out.AuthorHits = hits
		out.TotalResults = total

	case "author_profile":
		// Strip dblp.org URL if pasted
		pid := query
		if strings.Contains(query, "dblp.org/pid/") {
			i := strings.Index(query, "/pid/")
			pid = query[i+len("/pid/"):]
			pid = strings.TrimSuffix(pid, ".html")
			pid = strings.TrimSuffix(pid, ".xml")
		}
		// If no slash, it's likely a name → resolve via search
		if !strings.Contains(pid, "/") {
			hits, _, _ := dblpAuthorSearch(ctx, client, query, 1)
			if len(hits) == 0 {
				out.Note = fmt.Sprintf("no DBLP author matching '%s'", query)
				out.HighlightFindings = []string{out.Note}
				out.TookMs = time.Since(start).Milliseconds()
				return out, nil
			}
			pid = hits[0].PID
			out.AuthorName = hits[0].Name
			out.AuthorAliases = hits[0].Aliases
		}
		// Fetch the per-author XML
		profile, pubs, links, err := dblpAuthorProfile(ctx, client, pid, limit)
		if err != nil {
			return nil, err
		}
		out.PID = pid
		if profile != "" {
			out.AuthorName = profile
		}
		out.Publications = pubs
		out.CrossPlatformLinks = links
		out.TotalResults = len(pubs)
		// Aggregations
		venueAgg, yearAgg, coauthorAgg, minYear, maxYear := dblpAggregate(pubs, out.AuthorName)
		out.TopVenues = venueAgg
		out.YearDistribution = yearAgg
		out.TopCoauthors = coauthorAgg
		if minYear != "" && maxYear != "" {
			if minYear == maxYear {
				out.YearRange = minYear
			} else {
				out.YearRange = minYear + "-" + maxYear
			}
		}

	case "publication_search":
		pubs, total, err := dblpPublicationSearch(ctx, client, query, limit)
		if err != nil {
			return nil, err
		}
		out.Publications = pubs
		out.TotalResults = total

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: author_search, author_profile, publication_search", mode)
	}

	out.HighlightFindings = buildDBLPHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func dblpAuthorSearch(ctx context.Context, client *http.Client, query string, limit int) ([]DBLPAuthorHit, int, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("h", fmt.Sprintf("%d", limit))
	endpoint := "https://dblp.org/search/author/api?" + params.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("dblp author search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, 0, fmt.Errorf("dblp %d: %s", resp.StatusCode, string(body))
	}
	var raw dblpSearchRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	total := 0
	fmt.Sscanf(raw.Result.Hits.Total, "%d", &total)
	hits := []DBLPAuthorHit{}
	for _, h := range raw.Result.Hits.Hit {
		var info dblpAuthorInfoRaw
		if err := json.Unmarshal(h.Info, &info); err != nil {
			continue
		}
		// PID may be in @pid field OR derivable from URL (e.g. https://dblp.org/pid/l/YannLeCun)
		pid := info.PID
		if pid == "" && info.URL != "" {
			if i := strings.Index(info.URL, "/pid/"); i >= 0 {
				pid = strings.TrimSuffix(info.URL[i+len("/pid/"):], ".html")
			}
		}
		hit := DBLPAuthorHit{
			PID:  pid,
			Name: info.Author,
			URL:  info.URL,
		}
		// Aliases is sometimes a string, sometimes an array
		if len(info.Aliases.Alias) > 0 {
			var single string
			if err := json.Unmarshal(info.Aliases.Alias, &single); err == nil {
				hit.Aliases = []string{single}
			} else {
				var arr []string
				if err := json.Unmarshal(info.Aliases.Alias, &arr); err == nil {
					hit.Aliases = arr
				}
			}
		}
		// Notes (affiliations) may also be string or array
		if len(info.Notes.Note) > 0 {
			var single string
			if err := json.Unmarshal(info.Notes.Note, &single); err == nil {
				hit.Notes = []string{single}
			} else {
				var arr []map[string]any
				if err := json.Unmarshal(info.Notes.Note, &arr); err == nil {
					for _, n := range arr {
						if t, ok := n["text"].(string); ok {
							hit.Notes = append(hit.Notes, t)
						}
					}
				}
			}
		}
		hits = append(hits, hit)
	}
	return hits, total, nil
}

func dblpAuthorProfile(ctx context.Context, client *http.Client, pid string, limit int) (string, []DBLPPublication, []DBLPCrossPlatformLink, error) {
	endpoint := "https://dblp.org/pid/" + pid + ".xml"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/xml")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, nil, fmt.Errorf("dblp author profile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return "", nil, nil, fmt.Errorf("DBLP author PID '%s' not found", pid)
	}
	if resp.StatusCode != 200 {
		return "", nil, nil, fmt.Errorf("dblp %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8_000_000))
	if err != nil {
		return "", nil, nil, err
	}
	var raw dblpPersonXML
	if err := xml.Unmarshal(body, &raw); err != nil {
		return "", nil, nil, fmt.Errorf("dblp xml decode: %w", err)
	}
	authorName := raw.Name

	// Extract external URLs into structured cross-platform links
	links := []DBLPCrossPlatformLink{}
	for _, u := range raw.Person.URL {
		links = append(links, classifyDBLPURL(u))
	}

	// Collect publications
	pubs := []DBLPPublication{}
	for _, r := range raw.R {
		var ref *dblpRefXML
		var typ string
		switch {
		case r.Article != nil:
			ref = r.Article
			typ = "article"
		case r.InProceedings != nil:
			ref = r.InProceedings
			typ = "inproceedings"
		case r.Book != nil:
			ref = r.Book
			typ = "book"
		case r.Incollection != nil:
			ref = r.Incollection
			typ = "incollection"
		case r.PhdThesis != nil:
			ref = r.PhdThesis
			typ = "phdthesis"
		default:
			continue
		}
		pub := DBLPPublication{
			Key:   ref.Key,
			Type:  typ,
			Title: strings.TrimSpace(ref.Title),
			Year:  ref.Year,
		}
		if ref.Journal != "" {
			pub.Venue = ref.Journal
		} else if ref.Booktitle != "" {
			pub.Venue = ref.Booktitle
		}
		for _, a := range ref.Authors {
			pub.Authors = append(pub.Authors, strings.TrimSpace(a.Text))
		}
		// Pick the first http(s) ee link
		for _, ee := range ref.EE {
			if strings.HasPrefix(ee, "http") {
				pub.EE = ee
				if strings.Contains(ee, "doi.org/") {
					i := strings.Index(ee, "doi.org/")
					pub.DOI = ee[i+len("doi.org/"):]
				}
				break
			}
		}
		pubs = append(pubs, pub)
	}
	// Most recent first (DBLP returns reverse-chrono usually but ensure)
	sort.SliceStable(pubs, func(i, j int) bool { return pubs[i].Year > pubs[j].Year })
	if len(pubs) > limit {
		pubs = pubs[:limit]
	}
	return authorName, pubs, links, nil
}

func dblpPublicationSearch(ctx context.Context, client *http.Client, query string, limit int) ([]DBLPPublication, int, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("h", fmt.Sprintf("%d", limit))
	endpoint := "https://dblp.org/search/publ/api?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("dblp publ search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("dblp %d", resp.StatusCode)
	}
	var raw dblpSearchRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	total := 0
	fmt.Sscanf(raw.Result.Hits.Total, "%d", &total)
	pubs := []DBLPPublication{}
	for _, h := range raw.Result.Hits.Hit {
		var info dblpPublInfoRaw
		if err := json.Unmarshal(h.Info, &info); err != nil {
			continue
		}
		pub := DBLPPublication{
			Key:   info.Key,
			Type:  info.Type,
			Title: strings.TrimSpace(info.Title),
			Year:  info.Year,
			Venue: info.Venue,
			URL:   info.URL,
			DOI:   info.DOI,
			EE:    info.EE,
		}
		// Authors is polymorphic
		if len(info.Authors) > 0 {
			var asMap struct {
				Author json.RawMessage `json:"author"`
			}
			if err := json.Unmarshal(info.Authors, &asMap); err == nil {
				if len(asMap.Author) > 0 {
					// could be a single object or array
					var arr []map[string]any
					if err := json.Unmarshal(asMap.Author, &arr); err == nil {
						for _, a := range arr {
							if t, ok := a["text"].(string); ok {
								pub.Authors = append(pub.Authors, t)
							}
						}
					} else {
						var single map[string]any
						if err := json.Unmarshal(asMap.Author, &single); err == nil {
							if t, ok := single["text"].(string); ok {
								pub.Authors = append(pub.Authors, t)
							}
						}
					}
				}
			}
		}
		pubs = append(pubs, pub)
	}
	return pubs, total, nil
}

func dblpAggregate(pubs []DBLPPublication, ownerName string) (
	venueAgg []DBLPVenueAggregate,
	yearAgg []DBLPYearAggregate,
	coauthorAgg []DBLPCoauthorAggregate,
	minYear string, maxYear string,
) {
	vMap := map[string]int{}
	yMap := map[string]int{}
	cMap := map[string]int{}
	for _, p := range pubs {
		if p.Venue != "" {
			vMap[p.Venue]++
		}
		if p.Year != "" {
			yMap[p.Year]++
			if minYear == "" || p.Year < minYear {
				minYear = p.Year
			}
			if p.Year > maxYear {
				maxYear = p.Year
			}
		}
		// coauthors (skip the owner themselves)
		ownerLow := strings.ToLower(strings.TrimSpace(ownerName))
		for _, a := range p.Authors {
			if strings.ToLower(strings.TrimSpace(a)) == ownerLow {
				continue
			}
			cMap[a]++
		}
	}
	for v, c := range vMap {
		venueAgg = append(venueAgg, DBLPVenueAggregate{Venue: v, Count: c})
	}
	sort.SliceStable(venueAgg, func(i, j int) bool { return venueAgg[i].Count > venueAgg[j].Count })
	if len(venueAgg) > 10 {
		venueAgg = venueAgg[:10]
	}
	for y, c := range yMap {
		yearAgg = append(yearAgg, DBLPYearAggregate{Year: y, Count: c})
	}
	sort.SliceStable(yearAgg, func(i, j int) bool { return yearAgg[i].Year < yearAgg[j].Year })
	for n, c := range cMap {
		coauthorAgg = append(coauthorAgg, DBLPCoauthorAggregate{Name: n, Count: c})
	}
	sort.SliceStable(coauthorAgg, func(i, j int) bool { return coauthorAgg[i].Count > coauthorAgg[j].Count })
	if len(coauthorAgg) > 15 {
		coauthorAgg = coauthorAgg[:15]
	}
	return
}

// classifyDBLPURL maps a known external URL to platform + handle.
func classifyDBLPURL(rawURL string) DBLPCrossPlatformLink {
	link := DBLPCrossPlatformLink{URL: rawURL}
	low := strings.ToLower(rawURL)
	switch {
	case strings.Contains(low, "orcid.org/"):
		link.Platform = "orcid"
		i := strings.LastIndex(rawURL, "orcid.org/")
		link.Handle = rawURL[i+len("orcid.org/"):]
	case strings.Contains(low, "scholar.google.com/citations"):
		link.Platform = "google_scholar"
		if i := strings.Index(rawURL, "user="); i > 0 {
			h := rawURL[i+len("user="):]
			if amp := strings.Index(h, "&"); amp > 0 {
				h = h[:amp]
			}
			link.Handle = h
		}
	case strings.Contains(low, "twitter.com/") || strings.Contains(low, "x.com/"):
		link.Platform = "twitter"
		i := strings.LastIndex(rawURL, "/")
		link.Handle = rawURL[i+1:]
	case strings.Contains(low, "github.com/"):
		link.Platform = "github"
		i := strings.LastIndex(rawURL, "/")
		link.Handle = rawURL[i+1:]
	case strings.Contains(low, "wikipedia.org/wiki/"):
		link.Platform = "wikipedia"
		i := strings.LastIndex(rawURL, "/wiki/")
		link.Handle = rawURL[i+len("/wiki/"):]
	case strings.Contains(low, "wikidata.org/entity/") || strings.Contains(low, "wikidata.org/wiki/"):
		link.Platform = "wikidata"
		idx := strings.LastIndex(rawURL, "/")
		link.Handle = rawURL[idx+1:]
	case strings.Contains(low, "openreview.net/profile"):
		link.Platform = "openreview"
		if i := strings.Index(rawURL, "id="); i > 0 {
			link.Handle = rawURL[i+3:]
		}
	case strings.Contains(low, "dl.acm.org/profile/"):
		link.Platform = "acm"
		i := strings.LastIndex(rawURL, "/")
		link.Handle = rawURL[i+1:]
	case strings.Contains(low, "mathscinet.ams.org"):
		link.Platform = "mathscinet"
	case strings.Contains(low, "zbmath.org"):
		link.Platform = "zbmath"
	case strings.Contains(low, "mathgenealogy.org"):
		link.Platform = "math_genealogy"
		if i := strings.Index(rawURL, "id="); i > 0 {
			link.Handle = rawURL[i+3:]
		}
	case strings.Contains(low, "huggingface.co/"):
		link.Platform = "huggingface"
		i := strings.LastIndex(rawURL, "/")
		link.Handle = rawURL[i+1:]
	case strings.Contains(low, "linkedin.com/in/"):
		link.Platform = "linkedin"
		i := strings.LastIndex(rawURL, "/in/")
		link.Handle = strings.TrimSuffix(rawURL[i+len("/in/"):], "/")
	case strings.Contains(low, "researchgate.net"):
		link.Platform = "researchgate"
	default:
		link.Platform = "homepage"
	}
	return link
}

func buildDBLPHighlights(o *DBLPSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "author_search":
		hi = append(hi, fmt.Sprintf("%d author hits for '%s'", o.TotalResults, o.Query))
		if len(o.AuthorHits) >= 2 {
			hi = append(hi, fmt.Sprintf("⚠️  %d distinct DBLP authors with similar name — likely namesakes; use PID to drill down", len(o.AuthorHits)))
		}
		for i, h := range o.AuthorHits {
			if i >= 5 {
				break
			}
			alias := ""
			if len(h.Aliases) > 0 {
				alias = " (aliases: " + strings.Join(h.Aliases, ", ") + ")"
			}
			notes := ""
			if len(h.Notes) > 0 {
				notes = " [" + strings.Join(h.Notes, "; ") + "]"
			}
			hi = append(hi, fmt.Sprintf("  %s — pid=%s%s%s", h.Name, h.PID, alias, notes))
		}
	case "author_profile":
		hi = append(hi, fmt.Sprintf("✓ %s (DBLP pid=%s) — %d publications", o.AuthorName, o.PID, o.TotalResults))
		if len(o.AuthorAliases) > 0 {
			hi = append(hi, "aliases: "+strings.Join(o.AuthorAliases, ", "))
		}
		if o.YearRange != "" {
			hi = append(hi, "publication year range: "+o.YearRange)
		}
		// Cross-platform identity bridge — the killer feature
		if len(o.CrossPlatformLinks) > 0 {
			parts := []string{}
			for _, l := range o.CrossPlatformLinks {
				if l.Handle != "" {
					parts = append(parts, fmt.Sprintf("%s=%s", l.Platform, l.Handle))
				} else {
					parts = append(parts, l.Platform)
				}
			}
			hi = append(hi, fmt.Sprintf("🔗 %d cross-platform identity links: %s", len(o.CrossPlatformLinks), strings.Join(parts, " | ")))
		}
		if len(o.TopVenues) > 0 {
			topV := []string{}
			for _, v := range o.TopVenues[:min2(5, len(o.TopVenues))] {
				topV = append(topV, fmt.Sprintf("%s (%d)", v.Venue, v.Count))
			}
			hi = append(hi, "🎯 top venues: "+strings.Join(topV, ", "))
		}
		if len(o.TopCoauthors) > 0 {
			topC := []string{}
			for _, c := range o.TopCoauthors[:min2(5, len(o.TopCoauthors))] {
				topC = append(topC, fmt.Sprintf("%s (%dx)", c.Name, c.Count))
			}
			hi = append(hi, "👥 top coauthors: "+strings.Join(topC, ", "))
		}
	case "publication_search":
		hi = append(hi, fmt.Sprintf("%d total CS publications match '%s' (returned %d)", o.TotalResults, o.Query, len(o.Publications)))
		yMap := map[string]int{}
		for _, p := range o.Publications {
			if p.Year != "" {
				yMap[p.Year]++
			}
		}
		if len(yMap) > 0 {
			years := []string{}
			for y := range yMap {
				years = append(years, y)
			}
			sort.Strings(years)
			if len(years) > 0 {
				hi = append(hi, fmt.Sprintf("year span (in returned set): %s-%s", years[0], years[len(years)-1]))
			}
		}
	}
	return hi
}
