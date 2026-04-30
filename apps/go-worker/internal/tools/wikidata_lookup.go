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

// WikidataLookup queries Wikidata via three complementary endpoints:
//
//   - "search"  : fuzzy text → top candidate entities (QIDs + labels + descriptions),
//                 used for disambiguation. Wraps `wbsearchentities`.
//   - "entity"  : QID → richly structured entity card. Pulls 50+ curated OSINT
//                 properties in ONE SPARQL round-trip with qualifier dates
//                 (start/end) for relationship-type claims. Returns a clean
//                 grouped record (identity / family / career / org / identifiers).
//                 This is the killer ER feature — Wikidata's temporal context
//                 ("spouse 1983-2014", "Prime Minister 1999-2000", "KGB 1975-1990")
//                 is uniquely available here for free.
//   - "sparql"  : raw SPARQL pass-through to https://query.wikidata.org/sparql.
//                 60s timeout, 1MB result cap.
//
// Free, no auth. Wikidata is the largest open structured-knowledge graph
// (~110M items, ~1.5B statements) and the only practical way to do
// cross-entity ER queries like "all CEOs of subsidiaries of X" or
// "doctoral grandparent of Y" in a single hop.

type WDValue struct {
	QID       string `json:"qid,omitempty"`         // present for entity-typed values
	Label     string `json:"label,omitempty"`       // English label of the value
	Text      string `json:"text,omitempty"`        // raw value (string/url/external-id/time/coord)
	StartDate string `json:"start_date,omitempty"`  // P580 qualifier
	EndDate   string `json:"end_date,omitempty"`    // P582 qualifier
}

type WDSearchResult struct {
	QID         string `json:"qid"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

type WDEntityCard struct {
	QID         string                  `json:"qid"`
	Label       string                  `json:"label"`
	Description string                  `json:"description,omitempty"`
	Aliases     []string                `json:"aliases,omitempty"`
	URL         string                  `json:"url"`

	// Grouped curated properties. Keys are human-readable property labels
	// (e.g. "spouse", "position held", "founder"). Values include start/end
	// dates where Wikidata records them as qualifiers.
	Identity      map[string][]WDValue `json:"identity,omitempty"`
	Family        map[string][]WDValue `json:"family,omitempty"`
	Career        map[string][]WDValue `json:"career,omitempty"`
	Organization  map[string][]WDValue `json:"organization,omitempty"`
	Identifiers   map[string]string    `json:"identifiers,omitempty"` // single-value (twitter, github, etc.)
	OtherProps    map[string][]WDValue `json:"other,omitempty"`

	// All raw claims by property label, for callers that need everything
	AllClaims map[string][]WDValue `json:"all_claims,omitempty"`
}

type WikidataLookupOutput struct {
	Mode              string             `json:"mode"`
	Query             string             `json:"query,omitempty"`
	QID               string             `json:"qid,omitempty"`

	SearchResults     []WDSearchResult   `json:"search_results,omitempty"`
	Entity            *WDEntityCard      `json:"entity,omitempty"`
	SPARQLBindings    []map[string]any   `json:"sparql_bindings,omitempty"`
	SPARQLVars        []string           `json:"sparql_vars,omitempty"`

	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
	Note              string             `json:"note,omitempty"`
}

// Curated property catalog. Keys map Wikidata Pxxx IDs to (group, label).
// Update this list to widen entity coverage.
type wdPropDef struct {
	Group string
	Label string
}

var wdProps = map[string]wdPropDef{
	// Identity & demographics
	"P31":  {"identity", "instance of"},
	"P21":  {"identity", "gender"},
	"P569": {"identity", "date of birth"},
	"P19":  {"identity", "place of birth"},
	"P570": {"identity", "date of death"},
	"P20":  {"identity", "place of death"},
	"P1196": {"identity", "manner of death"},
	"P27":  {"identity", "country of citizenship"},
	"P172": {"identity", "ethnic group"},
	"P140": {"identity", "religion"},
	"P551": {"identity", "residence"},
	"P735": {"identity", "given name"},
	"P734": {"identity", "family name"},

	// Family
	"P22":  {"family", "father"},
	"P25":  {"family", "mother"},
	"P26":  {"family", "spouse"},
	"P40":  {"family", "child"},
	"P3373": {"family", "sibling"},
	"P1038": {"family", "relative"},

	// Career & education
	"P69":  {"career", "educated at"},
	"P512": {"career", "academic degree"},
	"P106": {"career", "occupation"},
	"P101": {"career", "field of work"},
	"P108": {"career", "employer"},
	"P39":  {"career", "position held"},
	"P184": {"career", "doctoral advisor"},
	"P185": {"career", "doctoral student"},
	"P166": {"career", "award received"},
	"P800": {"career", "notable work"},
	"P937": {"career", "work location"},
	"P241": {"career", "military branch"},
	"P410": {"career", "military rank"},
	"P607": {"career", "conflict participated in"},

	// Org-specific
	"P159":  {"organization", "headquarters location"},
	"P571":  {"organization", "inception"},
	"P576":  {"organization", "dissolved date"},
	"P112":  {"organization", "founded by"},
	"P169":  {"organization", "CEO"},
	"P488":  {"organization", "chairperson"},
	"P3320": {"organization", "board member"},
	"P749":  {"organization", "parent organization"},
	"P355":  {"organization", "subsidiary"},
	"P127":  {"organization", "owned by"},
	"P452":  {"organization", "industry"},
	"P17":   {"organization", "country"},
	"P1454": {"organization", "legal form"},
	"P1278": {"organization", "Legal Entity Identifier (LEI)"},
	"P249":  {"organization", "ticker symbol"},
	"P946":  {"organization", "ISIN"},
	"P2139": {"organization", "total revenue"},
	"P1128": {"organization", "employee count"},
	"P3608": {"organization", "VAT number"},
	"P1297": {"organization", "IRS Employer ID Number"},
	"P1056": {"organization", "product or material produced"},

	// Cross-platform identifiers (single-value, treat specially)
	"P2002": {"identifier", "twitter username"},
	"P2013": {"identifier", "facebook id"},
	"P2037": {"identifier", "github username"},
	"P3744": {"identifier", "linkedin profile"},
	"P4264": {"identifier", "linkedin company"},
	"P4033": {"identifier", "mastodon address"},
	"P2397": {"identifier", "youtube channel id"},
	"P496":  {"identifier", "orcid"},
	"P1960": {"identifier", "google scholar id"},
	"P2671": {"identifier", "google knowledge graph id"},
	"P646":  {"identifier", "freebase id"},
	"P345":  {"identifier", "imdb id"},
	"P227":  {"identifier", "gnd id"},
	"P244":  {"identifier", "library of congress authority id"},
	"P856":  {"identifier", "official website"},
	"P1581": {"identifier", "official blog url"},
	"P1422": {"identifier", "sandrart.net id"},
	"P5294": {"identifier", "github topic"},
}

func WikidataLookup(ctx context.Context, input map[string]any) (*WikidataLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// auto-detect: if input has "qid" → entity, "sparql" → sparql, "query" → search
		if _, ok := input["qid"]; ok {
			mode = "entity"
		} else if _, ok := input["sparql"]; ok {
			mode = "sparql"
		} else {
			mode = "search"
		}
	}

	out := &WikidataLookupOutput{
		Mode:   mode,
		Source: "wikidata.org / query.wikidata.org",
	}
	start := time.Now()

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.query required for search mode")
		}
		out.Query = q
		limit := 8
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		results, err := wdSearchEntities(ctx, q, limit)
		if err != nil {
			return nil, err
		}
		out.SearchResults = results

	case "entity":
		qid, _ := input["qid"].(string)
		qid = strings.TrimSpace(qid)
		if qid == "" {
			return nil, fmt.Errorf("input.qid required for entity mode (e.g. 'Q7747')")
		}
		if !strings.HasPrefix(qid, "Q") {
			return nil, fmt.Errorf("qid must start with 'Q'")
		}
		out.QID = qid
		card, err := wdFetchEntity(ctx, qid)
		if err != nil {
			return nil, err
		}
		out.Entity = card

	case "sparql":
		query, _ := input["sparql"].(string)
		query = strings.TrimSpace(query)
		if query == "" {
			return nil, fmt.Errorf("input.sparql required for sparql mode")
		}
		out.Query = query
		bindings, vars, err := wdRunSPARQL(ctx, query)
		if err != nil {
			return nil, err
		}
		out.SPARQLBindings = bindings
		out.SPARQLVars = vars
		// Hard cap: 200 rows in the response
		if len(out.SPARQLBindings) > 200 {
			out.Note = fmt.Sprintf("truncated to first 200 of %d rows", len(out.SPARQLBindings))
			out.SPARQLBindings = out.SPARQLBindings[:200]
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, entity, sparql", mode)
	}

	out.HighlightFindings = buildWDHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// wdSearchEntities calls the wbsearchentities REST endpoint.
func wdSearchEntities(ctx context.Context, q string, limit int) ([]WDSearchResult, error) {
	params := url.Values{}
	params.Set("action", "wbsearchentities")
	params.Set("search", q)
	params.Set("language", "en")
	params.Set("format", "json")
	params.Set("limit", fmt.Sprintf("%d", limit))
	u := "https://www.wikidata.org/w/api.php?" + params.Encode()
	cli := &http.Client{Timeout: 20 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0 (https://github.com/jroell/osint-agent)")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikidata search: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var raw struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
			ConceptURI  string `json:"concepturi"`
		} `json:"search"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	results := make([]WDSearchResult, 0, len(raw.Search))
	for _, r := range raw.Search {
		results = append(results, WDSearchResult{
			QID: r.ID, Label: r.Label, Description: r.Description, URL: r.ConceptURI,
		})
	}
	return results, nil
}

// wdFetchEntity pulls the curated property set in one SPARQL trip and returns
// a structured entity card.
func wdFetchEntity(ctx context.Context, qid string) (*WDEntityCard, error) {
	// Build VALUES block
	var valuesLines []string
	for pid := range wdProps {
		valuesLines = append(valuesLines, fmt.Sprintf("    (p:%s ps:%s wd:%s)", pid, pid, pid))
	}
	sort.Strings(valuesLines) // deterministic order

	query := fmt.Sprintf(`SELECT ?propLabel ?value ?valueLabel ?startDate ?endDate WHERE {
  VALUES (?p ?ps ?propEntity) {
%s
  }
  wd:%s ?p ?st.
  ?st ?ps ?value.
  OPTIONAL { ?st pq:P580 ?startDate. }
  OPTIONAL { ?st pq:P582 ?endDate. }
  SERVICE wikibase:label {
    bd:serviceParam wikibase:language "en".
    ?propEntity rdfs:label ?propLabel.
    ?value rdfs:label ?valueLabel.
  }
}`, strings.Join(valuesLines, "\n"), qid)

	bindings, _, err := wdRunSPARQL(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("entity sparql: %w", err)
	}

	// Also fetch labels/descriptions/aliases via the lighter API call
	hdr, err := wdFetchEntityHeader(ctx, qid)
	if err != nil {
		return nil, err
	}

	card := &WDEntityCard{
		QID:         qid,
		Label:       hdr.Label,
		Description: hdr.Description,
		Aliases:     hdr.Aliases,
		URL:         "https://www.wikidata.org/wiki/" + qid,
		Identity:     map[string][]WDValue{},
		Family:       map[string][]WDValue{},
		Career:       map[string][]WDValue{},
		Organization: map[string][]WDValue{},
		Identifiers:  map[string]string{},
		OtherProps:   map[string][]WDValue{},
		AllClaims:    map[string][]WDValue{},
	}

	// Build a reverse-lookup propLabel → group/label by indexing wdProps.
	// Because the SPARQL returns ?propLabel which is the human-readable label,
	// we group by that label directly.
	labelToGroup := map[string]string{}
	for _, def := range wdProps {
		labelToGroup[def.Label] = def.Group
	}

	for _, b := range bindings {
		propLabel := getStringFromBinding(b, "propLabel")
		if propLabel == "" {
			continue
		}
		val := WDValue{}
		// value can be a Wikidata entity URI or a literal
		raw := getStringFromBinding(b, "value")
		valLabel := getStringFromBinding(b, "valueLabel")
		if strings.HasPrefix(raw, "http://www.wikidata.org/entity/Q") {
			val.QID = strings.TrimPrefix(raw, "http://www.wikidata.org/entity/")
			if valLabel != "" && valLabel != val.QID {
				val.Label = valLabel
			}
		} else {
			val.Text = raw
		}
		val.StartDate = getStringFromBinding(b, "startDate")
		val.EndDate = getStringFromBinding(b, "endDate")

		group := labelToGroup[propLabel]
		switch group {
		case "identity":
			card.Identity[propLabel] = append(card.Identity[propLabel], val)
		case "family":
			card.Family[propLabel] = append(card.Family[propLabel], val)
		case "career":
			card.Career[propLabel] = append(card.Career[propLabel], val)
		case "organization":
			card.Organization[propLabel] = append(card.Organization[propLabel], val)
		case "identifier":
			// take first non-empty
			if _, exists := card.Identifiers[propLabel]; !exists {
				if val.Text != "" {
					card.Identifiers[propLabel] = val.Text
				} else if val.Label != "" {
					card.Identifiers[propLabel] = val.Label
				} else if val.QID != "" {
					card.Identifiers[propLabel] = val.QID
				}
			}
		default:
			card.OtherProps[propLabel] = append(card.OtherProps[propLabel], val)
		}
		card.AllClaims[propLabel] = append(card.AllClaims[propLabel], val)
	}

	// Sort relationships chronologically by start date when present
	for _, m := range []map[string][]WDValue{card.Career, card.Family, card.Identity, card.Organization, card.OtherProps} {
		for k, vs := range m {
			sort.SliceStable(vs, func(i, j int) bool {
				if vs[i].StartDate != "" && vs[j].StartDate != "" {
					return vs[i].StartDate < vs[j].StartDate
				}
				return vs[i].StartDate != ""
			})
			m[k] = vs
		}
	}

	return card, nil
}

type wdEntityHeader struct {
	Label       string
	Description string
	Aliases     []string
}

func wdFetchEntityHeader(ctx context.Context, qid string) (*wdEntityHeader, error) {
	params := url.Values{}
	params.Set("action", "wbgetentities")
	params.Set("ids", qid)
	params.Set("props", "labels|descriptions|aliases")
	params.Set("languages", "en")
	params.Set("format", "json")
	u := "https://www.wikidata.org/w/api.php?" + params.Encode()
	cli := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var raw struct {
		Entities map[string]struct {
			Labels       map[string]struct{ Value string } `json:"labels"`
			Descriptions map[string]struct{ Value string } `json:"descriptions"`
			Aliases      map[string][]struct{ Value string } `json:"aliases"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	e, ok := raw.Entities[qid]
	if !ok {
		return &wdEntityHeader{}, nil
	}
	hdr := &wdEntityHeader{}
	if l, ok := e.Labels["en"]; ok {
		hdr.Label = l.Value
	}
	if d, ok := e.Descriptions["en"]; ok {
		hdr.Description = d.Value
	}
	if a, ok := e.Aliases["en"]; ok {
		for _, ai := range a {
			hdr.Aliases = append(hdr.Aliases, ai.Value)
		}
	}
	return hdr, nil
}

// wdRunSPARQL executes a SPARQL query and returns bindings.
func wdRunSPARQL(ctx context.Context, query string) ([]map[string]any, []string, error) {
	cli := &http.Client{Timeout: 60 * time.Second}
	form := url.Values{}
	form.Set("query", query)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://query.wikidata.org/sparql", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/sparql-results+json")
	req.Header.Set("User-Agent", "osint-agent/1.0 (contact: github.com/jroell/osint-agent)")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("sparql: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("sparql HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 400))
	}
	var raw struct {
		Head struct {
			Vars []string `json:"vars"`
		} `json:"head"`
		Results struct {
			Bindings []map[string]any `json:"bindings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, fmt.Errorf("sparql decode: %w", err)
	}
	return raw.Results.Bindings, raw.Head.Vars, nil
}

func getStringFromBinding(b map[string]any, key string) string {
	v, ok := b[key]
	if !ok {
		return ""
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	val, _ := m["value"].(string)
	return val
}

func buildWDHighlights(o *WikidataLookupOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d candidates for '%s'", len(o.SearchResults), o.Query))
		for i, r := range o.SearchResults {
			if i >= 5 {
				break
			}
			desc := r.Description
			if len(desc) > 100 {
				desc = desc[:100] + "…"
			}
			hi = append(hi, fmt.Sprintf("  • %s — %s — %s", r.QID, r.Label, desc))
		}
	case "entity":
		if o.Entity == nil {
			break
		}
		e := o.Entity
		hi = append(hi, fmt.Sprintf("✓ %s — %s", e.QID, e.Label))
		if e.Description != "" {
			hi = append(hi, "  "+hfTruncate(e.Description, 200))
		}
		// Surface key identity facts inline
		if dob, ok := e.Identity["date of birth"]; ok && len(dob) > 0 {
			hi = append(hi, "  born: "+dob[0].Text)
		}
		if pob, ok := e.Identity["place of birth"]; ok && len(pob) > 0 {
			hi = append(hi, "  birthplace: "+pob[0].Label)
		}
		if dod, ok := e.Identity["date of death"]; ok && len(dod) > 0 {
			hi = append(hi, "  died: "+dod[0].Text)
		}
		// Spouses
		if sp, ok := e.Family["spouse"]; ok && len(sp) > 0 {
			parts := []string{}
			for _, s := range sp {
				p := s.Label
				if s.StartDate != "" || s.EndDate != "" {
					p += fmt.Sprintf(" (%s – %s)", abbrevDate(s.StartDate), abbrevDate(s.EndDate))
				}
				parts = append(parts, p)
			}
			hi = append(hi, "  spouse(s): "+strings.Join(parts, "; "))
		}
		// Positions held
		if ph, ok := e.Career["position held"]; ok && len(ph) > 0 {
			hi = append(hi, fmt.Sprintf("  positions held: %d", len(ph)))
			for i, p := range ph {
				if i >= 4 {
					break
				}
				dr := ""
				if p.StartDate != "" || p.EndDate != "" {
					dr = fmt.Sprintf(" (%s – %s)", abbrevDate(p.StartDate), abbrevDate(p.EndDate))
				}
				hi = append(hi, "    • "+p.Label+dr)
			}
		}
		// Employers
		if em, ok := e.Career["employer"]; ok && len(em) > 0 {
			hi = append(hi, fmt.Sprintf("  employers: %d", len(em)))
			for i, p := range em {
				if i >= 3 {
					break
				}
				dr := ""
				if p.StartDate != "" || p.EndDate != "" {
					dr = fmt.Sprintf(" (%s – %s)", abbrevDate(p.StartDate), abbrevDate(p.EndDate))
				}
				hi = append(hi, "    • "+p.Label+dr)
			}
		}
		// Cross-platform identifiers — gold for ER pivoting
		if len(e.Identifiers) > 0 {
			hi = append(hi, "  cross-platform identifiers:")
			keys := make([]string, 0, len(e.Identifiers))
			for k := range e.Identifiers {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				hi = append(hi, fmt.Sprintf("    %s: %s", k, e.Identifiers[k]))
			}
		}
	case "sparql":
		hi = append(hi, fmt.Sprintf("✓ SPARQL returned %d rows × %d cols", len(o.SPARQLBindings), len(o.SPARQLVars)))
		if len(o.SPARQLBindings) > 0 {
			hi = append(hi, "  cols: "+strings.Join(o.SPARQLVars, ", "))
			// Show 3 sample rows
			for i, b := range o.SPARQLBindings {
				if i >= 3 {
					break
				}
				parts := []string{}
				for _, v := range o.SPARQLVars {
					parts = append(parts, fmt.Sprintf("%s=%s", v, hfTruncate(getStringFromBinding(b, v), 60)))
				}
				hi = append(hi, "  ["+fmt.Sprintf("%d", i+1)+"] "+strings.Join(parts, " | "))
			}
		}
	}
	return hi
}

func abbrevDate(s string) string {
	if s == "" {
		return "?"
	}
	// Strip "T00:00:00Z"
	if idx := strings.Index(s, "T"); idx > 0 {
		s = s[:idx]
	}
	return s
}
