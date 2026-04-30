package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type WikidataSearchHit struct {
	QID         string `json:"qid"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
}

type WikidataClaim struct {
	Property string `json:"property"`
	Label    string `json:"property_label,omitempty"`
	Value    any    `json:"value,omitempty"`
	ValueQID string `json:"value_qid,omitempty"`
	ValueLabel string `json:"value_label,omitempty"`
}

type WikidataEntity struct {
	QID            string                  `json:"qid"`
	Label          string                  `json:"label"`
	Description    string                  `json:"description"`
	Aliases        []string                `json:"aliases,omitempty"`
	URL            string                  `json:"url"`
	OfficialWebsite string                 `json:"official_website,omitempty"`
	InstanceOf     []string                `json:"instance_of,omitempty"`           // e.g. "business", "person"
	Founders       []string                `json:"founders,omitempty"`              // P112
	CEO            []string                `json:"chief_executive,omitempty"`       // P169
	Inception      string                  `json:"inception_date,omitempty"`        // P571
	Headquarters   []string                `json:"headquarters,omitempty"`          // P159
	ParentOrgs     []string                `json:"parent_organizations,omitempty"`  // P749
	Subsidiaries   []string                `json:"subsidiaries,omitempty"`          // P355
	Owners         []string                `json:"owners,omitempty"`                // P127
	Industry       []string                `json:"industries,omitempty"`            // P452
	NumEmployees   string                  `json:"num_employees,omitempty"`         // P1128
	StockExchange  []string                `json:"stock_exchange,omitempty"`        // P414
	IsinTickers    []string                `json:"isin_tickers,omitempty"`          // P946 / P249
	GitHubURL      string                  `json:"github,omitempty"`                // P1324
	Twitter        string                  `json:"twitter,omitempty"`               // P2002
	LinkedIn       string                  `json:"linkedin,omitempty"`              // P6634
	OpenCorpsID    string                  `json:"opencorporates_id,omitempty"`     // P1320
	CountryCode    string                  `json:"country,omitempty"`               // P17
	OtherClaims    map[string]any          `json:"other_claims,omitempty"`
}

type WikidataEntityLookupOutput struct {
	Query           string             `json:"query"`
	SearchHits      []WikidataSearchHit `json:"search_hits"`
	TopEntity       *WikidataEntity    `json:"top_entity,omitempty"`
	Source          string             `json:"source"`
	TookMs          int64              `json:"tookMs"`
	Note            string             `json:"note,omitempty"`
}

// Property IDs we extract by default — covers the most common ER signals.
var wikidataPropertiesOfInterest = map[string]string{
	"P31":   "instance_of",
	"P112":  "founder",
	"P169":  "chief_executive_officer",
	"P571":  "inception",
	"P159":  "headquarters_location",
	"P749":  "parent_organization",
	"P355":  "subsidiary",
	"P127":  "owned_by",
	"P452":  "industry",
	"P1128": "employees",
	"P414":  "stock_exchange",
	"P946":  "isin",
	"P249":  "ticker_symbol",
	"P1324": "source_code_repository",
	"P2002": "twitter_username",
	"P6634": "linkedin_personal_id",
	"P1320": "opencorporates_id",
	"P17":   "country",
	"P856":  "official_website",
}

func WikidataEntityLookup(ctx context.Context, input map[string]any) (*WikidataEntityLookupOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (entity name or QID like 'Q116758847')")
	}
	maxHits := 5
	if v, ok := input["max_hits"].(float64); ok && int(v) > 0 && int(v) <= 20 {
		maxHits = int(v)
	}
	expandTop := true
	if v, ok := input["expand_top"].(bool); ok {
		expandTop = v
	}
	start := time.Now()
	out := &WikidataEntityLookupOutput{Query: q, Source: "wikidata"}

	// If query is a QID directly (Q\d+), skip search and fetch directly.
	if strings.HasPrefix(q, "Q") && len(q) > 1 {
		isQID := true
		for _, c := range q[1:] {
			if c < '0' || c > '9' {
				isQID = false
				break
			}
		}
		if isQID {
			ent, err := wikidataFetchEntity(ctx, q)
			if err != nil {
				return nil, err
			}
			out.TopEntity = ent
			out.SearchHits = []WikidataSearchHit{{QID: q, Label: ent.Label, Description: ent.Description, URL: ent.URL}}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	// Step 1: search
	hits, err := wikidataSearch(ctx, q, maxHits)
	if err != nil {
		return nil, err
	}
	out.SearchHits = hits
	if len(hits) == 0 {
		out.Note = "No matches in Wikidata. Try alternate spelling or known QID."
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// Step 2: expand top hit
	if expandTop {
		ent, err := wikidataFetchEntity(ctx, hits[0].QID)
		if err == nil {
			out.TopEntity = ent
		}
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func wikidataSearch(ctx context.Context, q string, limit int) ([]WikidataSearchHit, error) {
	endpoint := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbsearchentities&search=%s&language=en&format=json&limit=%d&type=item",
		url.QueryEscape(q), limit)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/wikidata-lookup")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wikidata search status %d", resp.StatusCode)
	}
	var parsed struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
			ConceptURI  string `json:"concepturi"`
		} `json:"search"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	hits := []WikidataSearchHit{}
	for _, s := range parsed.Search {
		hits = append(hits, WikidataSearchHit{
			QID: s.ID, Label: s.Label, Description: s.Description,
			URL: "https://www.wikidata.org/wiki/" + s.ID,
		})
	}
	return hits, nil
}

func wikidataFetchEntity(ctx context.Context, qid string) (*WikidataEntity, error) {
	endpoint := fmt.Sprintf("https://www.wikidata.org/wiki/Special:EntityData/%s.json", qid)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/wikidata-lookup")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wikidata fetch status %d", resp.StatusCode)
	}

	var parsed struct {
		Entities map[string]struct {
			Labels       map[string]struct{ Value string `json:"value"` } `json:"labels"`
			Descriptions map[string]struct{ Value string `json:"value"` } `json:"descriptions"`
			Aliases      map[string][]struct{ Value string `json:"value"` } `json:"aliases"`
			Sitelinks    map[string]struct{ Title string `json:"title"`; URL string `json:"url"` } `json:"sitelinks"`
			Claims       map[string][]struct {
				Mainsnak struct {
					Datavalue struct {
						Value any    `json:"value"`
						Type  string `json:"type"`
					} `json:"datavalue"`
					DataType string `json:"datatype"`
				} `json:"mainsnak"`
			} `json:"claims"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("wikidata response parse: %w", err)
	}
	entRaw, ok := parsed.Entities[qid]
	if !ok {
		return nil, fmt.Errorf("entity %s not found", qid)
	}

	ent := &WikidataEntity{QID: qid, URL: "https://www.wikidata.org/wiki/" + qid}
	if l, ok := entRaw.Labels["en"]; ok {
		ent.Label = l.Value
	}
	if d, ok := entRaw.Descriptions["en"]; ok {
		ent.Description = d.Value
	}
	if al, ok := entRaw.Aliases["en"]; ok {
		for _, a := range al {
			ent.Aliases = append(ent.Aliases, a.Value)
		}
	}

	// Collect all referenced QIDs to bulk-resolve labels.
	refQIDs := map[string]bool{}
	for prop, claims := range entRaw.Claims {
		if _, want := wikidataPropertiesOfInterest[prop]; !want {
			continue
		}
		for _, c := range claims {
			if v, ok := c.Mainsnak.Datavalue.Value.(map[string]any); ok {
				if id, ok := v["id"].(string); ok {
					refQIDs[id] = true
				}
			}
		}
	}
	// Resolve labels for referenced QIDs (best-effort, batched).
	labelMap := map[string]string{}
	if len(refQIDs) > 0 {
		ids := make([]string, 0, len(refQIDs))
		for id := range refQIDs {
			ids = append(ids, id)
		}
		// Wikidata wbgetentities limit = 50 per call
		for i := 0; i < len(ids); i += 50 {
			end := i + 50
			if end > len(ids) {
				end = len(ids)
			}
			batch := ids[i:end]
			labelsForBatch := wikidataResolveLabels(ctx, batch)
			for k, v := range labelsForBatch {
				labelMap[k] = v
			}
		}
	}

	// Helper to extract claim values.
	getStringValues := func(prop string) []string {
		out := []string{}
		for _, c := range entRaw.Claims[prop] {
			v := c.Mainsnak.Datavalue.Value
			if vm, ok := v.(map[string]any); ok {
				if id, ok := vm["id"].(string); ok {
					if lbl := labelMap[id]; lbl != "" {
						out = append(out, fmt.Sprintf("%s (%s)", lbl, id))
					} else {
						out = append(out, id)
					}
				} else if t, ok := vm["time"].(string); ok {
					out = append(out, t)
				}
			} else if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	getFirstString := func(prop string) string {
		vs := getStringValues(prop)
		if len(vs) > 0 {
			return vs[0]
		}
		return ""
	}

	ent.InstanceOf = getStringValues("P31")
	ent.Founders = getStringValues("P112")
	ent.CEO = getStringValues("P169")
	ent.Inception = getFirstString("P571")
	ent.Headquarters = getStringValues("P159")
	ent.ParentOrgs = getStringValues("P749")
	ent.Subsidiaries = getStringValues("P355")
	ent.Owners = getStringValues("P127")
	ent.Industry = getStringValues("P452")
	ent.NumEmployees = getFirstString("P1128")
	ent.StockExchange = getStringValues("P414")
	ent.OfficialWebsite = getFirstString("P856")
	ent.GitHubURL = getFirstString("P1324")
	ent.Twitter = getFirstString("P2002")
	ent.LinkedIn = getFirstString("P6634")
	ent.OpenCorpsID = getFirstString("P1320")
	ent.CountryCode = getFirstString("P17")
	ent.IsinTickers = append(ent.IsinTickers, getStringValues("P946")...)
	ent.IsinTickers = append(ent.IsinTickers, getStringValues("P249")...)

	return ent, nil
}

func wikidataResolveLabels(ctx context.Context, qids []string) map[string]string {
	endpoint := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbgetentities&ids=%s&format=json&props=labels&languages=en",
		url.QueryEscape(strings.Join(qids, "|")))
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/wikidata-labels")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]string{}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var parsed struct {
		Entities map[string]struct {
			Labels map[string]struct{ Value string `json:"value"` } `json:"labels"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for id, e := range parsed.Entities {
		if l, ok := e.Labels["en"]; ok {
			out[id] = l.Value
		}
	}
	return out
}

// _ keeps "sync" used if needed in future
var _ = sync.WaitGroup{}
