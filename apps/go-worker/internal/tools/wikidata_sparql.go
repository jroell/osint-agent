package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WikidataSPARQL runs an arbitrary SPARQL query against the Wikidata Query
// Service (https://query.wikidata.org/sparql). Free, no key, but rate-limited.
//
// This is the most powerful single tool in the catalog: arbitrary
// structured queries against Wikidata's ~110M-entity graph let you ask
// questions like "all behavioral ecologists who died in 2023" or "all
// US colleges founded between 1925 and 1930" or "all monarchs deposed
// in the 19th century whose descendants attended a 13th-century
// university."
//
// Modes:
//   - "sparql"            : run a raw SPARQL query (default; expert)
//   - "find_humans_by_attr": helper template that builds a SPARQL
//                            query for "humans matching [profession,
//                            year_of_death, nationality] etc." without
//                            requiring SPARQL syntax knowledge.
//
// Knowledge-graph: query results emit typed entities (kind:
// "wikidata_entity") with QID stable IDs. Pairs natively with
// `wikidata_entity_lookup` for follow-up enrichment.

type WDBinding struct {
	Vars   []string                 `json:"vars"`
	Result []map[string]string      `json:"result"`        // var → value (literal/iri/etc.)
	Raw    []map[string]interface{} `json:"raw,omitempty"` // full binding objects with type info
}

type WDEntity struct {
	Kind        string         `json:"kind"`
	QID         string         `json:"qid,omitempty"`
	Label       string         `json:"label,omitempty"`
	Description string         `json:"description,omitempty"`
	URL         string         `json:"url,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type WikidataSPARQLOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query"`
	BindingsCount     int        `json:"bindings_count"`
	Bindings          WDBinding  `json:"bindings"`
	Entities          []WDEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func WikidataSPARQL(ctx context.Context, input map[string]any) (*WikidataSPARQLOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if input["template"] != nil || input["occupation"] != nil || input["died_year"] != nil {
			mode = "find_humans_by_attr"
		} else {
			mode = "sparql"
		}
	}

	out := &WikidataSPARQLOutput{Mode: mode, Source: "query.wikidata.org/sparql"}
	start := time.Now()

	var sparql string
	switch mode {
	case "sparql":
		q, _ := input["query"].(string)
		if strings.TrimSpace(q) == "" {
			return nil, fmt.Errorf("input.query (SPARQL) required")
		}
		sparql = q
	case "find_humans_by_attr":
		sparql = buildHumansByAttrQuery(input)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use sparql or find_humans_by_attr", mode)
	}
	out.Query = sparql

	cli := &http.Client{Timeout: 60 * time.Second}
	form := url.Values{}
	form.Set("query", sparql)
	form.Set("format", "json")
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://query.wikidata.org/sparql",
		strings.NewReader(form.Encode()))
	req.Header.Set("Accept", "application/sparql-results+json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "osint-agent/1.0 (https://github.com/jroell/osint-agent)")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikidata sparql: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("wikidata sparql: rate limited (429)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wikidata sparql HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 400))
	}
	var srj struct {
		Head struct {
			Vars []string `json:"vars"`
		} `json:"head"`
		Results struct {
			Bindings []map[string]map[string]interface{} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &srj); err != nil {
		return nil, fmt.Errorf("wikidata sparql decode: %w", err)
	}
	out.Bindings.Vars = srj.Head.Vars
	for _, b := range srj.Results.Bindings {
		row := map[string]string{}
		raw := map[string]interface{}{}
		for v, obj := range b {
			val, _ := obj["value"].(string)
			row[v] = val
			raw[v] = obj
		}
		out.Bindings.Result = append(out.Bindings.Result, row)
		out.Bindings.Raw = append(out.Bindings.Raw, raw)
	}
	out.BindingsCount = len(out.Bindings.Result)
	out.Entities = wdSparqlBuildEntities(out)
	out.HighlightFindings = wdSparqlBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// buildHumansByAttrQuery generates a templated SPARQL query for the most
// common BrowseComp-style multi-attribute human search.
func buildHumansByAttrQuery(input map[string]any) string {
	occupation, _ := input["occupation"].(string) // e.g. "Q3072039" for behavioral ecologist
	occupationLabel, _ := input["occupation_label"].(string)
	diedYear, _ := input["died_year"].(string)      // YYYY
	bornYear, _ := input["born_year"].(string)      // YYYY
	nationality, _ := input["nationality"].(string) // QID
	limit := 50
	if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
		limit = int(l)
	}

	var b strings.Builder
	b.WriteString("SELECT DISTINCT ?person ?personLabel ?dob ?dod ?occupationLabel WHERE {\n")
	b.WriteString("  ?person wdt:P31 wd:Q5 .\n") // human
	if occupation != "" {
		fmt.Fprintf(&b, "  ?person wdt:P106 wd:%s .\n", occupation)
	} else if occupationLabel != "" {
		fmt.Fprintf(&b, "  ?person wdt:P106 ?occ . ?occ rdfs:label \"%s\"@en .\n", strings.ReplaceAll(occupationLabel, "\"", ""))
	}
	if diedYear != "" {
		fmt.Fprintf(&b, "  ?person wdt:P570 ?dod . FILTER(YEAR(?dod) = %s)\n", diedYear)
	}
	if bornYear != "" {
		fmt.Fprintf(&b, "  ?person wdt:P569 ?dob . FILTER(YEAR(?dob) = %s)\n", bornYear)
	}
	if nationality != "" {
		fmt.Fprintf(&b, "  ?person wdt:P27 wd:%s .\n", nationality)
	}
	// always pull DOB/DOD optional so they appear in result
	b.WriteString("  OPTIONAL { ?person wdt:P569 ?dob }\n")
	b.WriteString("  OPTIONAL { ?person wdt:P570 ?dod }\n")
	b.WriteString("  OPTIONAL { ?person wdt:P106 ?occupation }\n")
	b.WriteString("  SERVICE wikibase:label { bd:serviceParam wikibase:language \"en\" }\n")
	fmt.Fprintf(&b, "} LIMIT %d", limit)
	return b.String()
}

func wdSparqlBuildEntities(o *WikidataSPARQLOutput) []WDEntity {
	ents := []WDEntity{}
	for _, row := range o.Bindings.Result {
		// the first var is typically the entity URI; pull QID from any *URI.
		qid := ""
		label := ""
		uri := ""
		attrs := map[string]any{}
		for k, v := range row {
			if strings.Contains(v, "wikidata.org/entity/Q") {
				if qid == "" {
					qid = wdExtractQID(v)
					uri = v
				}
			}
			lk := strings.ToLower(k)
			if strings.HasSuffix(lk, "label") && label == "" {
				label = v
			}
			attrs[k] = v
		}
		ents = append(ents, WDEntity{
			Kind: "wikidata_entity", QID: qid, Label: label, URL: uri, Attributes: attrs,
		})
	}
	return ents
}

func wdExtractQID(uri string) string {
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

func wdSparqlBuildHighlights(o *WikidataSPARQLOutput) []string {
	hi := []string{fmt.Sprintf("✓ wikidata sparql: %d bindings (vars=%v)", o.BindingsCount, o.Bindings.Vars)}
	for i, row := range o.Bindings.Result {
		if i >= 8 {
			break
		}
		var parts []string
		for _, v := range o.Bindings.Vars {
			if val := row[v]; val != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", v, hfTruncate(val, 80)))
			}
		}
		hi = append(hi, "  • "+strings.Join(parts, " | "))
	}
	return hi
}
