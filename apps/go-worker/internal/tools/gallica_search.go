package tools

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GallicaSearch wraps the Bibliothèque nationale de France (BnF) Gallica
// digital library SRU search API. Free, no key.
//
// Gallica holds ~10M+ digitized French/European newspapers, manuscripts,
// books, maps, photographs, and audio. Critical for any French-language
// historical-source question and pre-1950 Continental European cultural
// references.
//
// The Gallica SRU endpoint returns Dublin Core XML (oai_dc). We
// flatten it into a typed-entity envelope.
//
// Modes:
//   - "search" : SRU CQL query (gallica.bnf.fr/SRU)
//
// Knowledge-graph: emits typed entities (kind: "book" | "newspaper" |
// "manuscript" | "map" | "image") with stable Gallica ARK URLs.

type GallicaItem struct {
	ARK         string `json:"ark_id"`
	Title       string `json:"title"`
	Creator     string `json:"creator,omitempty"`
	Date        string `json:"date,omitempty"`
	Type        string `json:"type,omitempty"`
	Language    string `json:"language,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"gallica_url"`
}

type GallicaEntity struct {
	Kind        string         `json:"kind"`
	ARK         string         `json:"ark_id"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type GallicaSearchOutput struct {
	Mode              string          `json:"mode"`
	Query             string          `json:"query"`
	Returned          int             `json:"returned"`
	Total             int             `json:"total,omitempty"`
	Items             []GallicaItem   `json:"items,omitempty"`
	Entities          []GallicaEntity `json:"entities"`
	HighlightFindings []string        `json:"highlight_findings"`
	Source            string          `json:"source"`
	TookMs            int64           `json:"tookMs"`
}

func GallicaSearch(ctx context.Context, input map[string]any) (*GallicaSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "search"
	}
	out := &GallicaSearchOutput{Mode: mode, Source: "gallica.bnf.fr/SRU"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required (CQL or simple keyword)")
		}
		out.Query = q
		// Default to dc.title or full-text any
		cql := q
		if !strings.Contains(q, "=") && !strings.Contains(q, " all ") {
			cql = "(gallica adj \"" + strings.ReplaceAll(q, "\"", "") + "\")"
		}
		params := url.Values{}
		params.Set("operation", "searchRetrieve")
		params.Set("version", "1.2")
		params.Set("collapsing", "disabled")
		params.Set("startRecord", "1")
		params.Set("maximumRecords", "20")
		params.Set("query", cql)
		u := "https://gallica.bnf.fr/SRU?" + params.Encode()

		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/xml")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gallica: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("gallica HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		items, total, err := parseGallicaSRU(body)
		if err != nil {
			return nil, err
		}
		out.Items = items
		out.Total = total

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Items)
	out.Entities = gallicaBuildEntities(out)
	out.HighlightFindings = gallicaBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// SRU XML response structures
type sruSearchResponse struct {
	XMLName         xml.Name `xml:"searchRetrieveResponse"`
	NumberOfRecords int      `xml:"numberOfRecords"`
	Records         struct {
		Record []sruRecord `xml:"record"`
	} `xml:"records"`
}

type sruRecord struct {
	RecordData struct {
		DC struct {
			Titles       []string `xml:"http://purl.org/dc/elements/1.1/ title"`
			Creators     []string `xml:"http://purl.org/dc/elements/1.1/ creator"`
			Dates        []string `xml:"http://purl.org/dc/elements/1.1/ date"`
			Types        []string `xml:"http://purl.org/dc/elements/1.1/ type"`
			Languages    []string `xml:"http://purl.org/dc/elements/1.1/ language"`
			Publishers   []string `xml:"http://purl.org/dc/elements/1.1/ publisher"`
			Descriptions []string `xml:"http://purl.org/dc/elements/1.1/ description"`
			Identifiers  []string `xml:"http://purl.org/dc/elements/1.1/ identifier"`
		} `xml:"oai_dc>dc"`
	} `xml:"recordData"`
}

func parseGallicaSRU(body []byte) ([]GallicaItem, int, error) {
	var resp sruSearchResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("gallica SRU decode: %w", err)
	}
	out := []GallicaItem{}
	for _, r := range resp.Records.Record {
		dc := r.RecordData.DC
		var ark string
		var url string
		for _, id := range dc.Identifiers {
			if strings.HasPrefix(id, "https://gallica.bnf.fr/") || strings.HasPrefix(id, "http://gallica.bnf.fr/") {
				url = id
				if i := strings.Index(id, "ark:/"); i >= 0 {
					ark = id[i:]
				}
			}
		}
		title := ""
		if len(dc.Titles) > 0 {
			title = dc.Titles[0]
		}
		creator := ""
		if len(dc.Creators) > 0 {
			creator = dc.Creators[0]
		}
		date := ""
		if len(dc.Dates) > 0 {
			date = dc.Dates[0]
		}
		typeStr := ""
		if len(dc.Types) > 0 {
			typeStr = dc.Types[0]
		}
		language := ""
		if len(dc.Languages) > 0 {
			language = dc.Languages[0]
		}
		publisher := ""
		if len(dc.Publishers) > 0 {
			publisher = dc.Publishers[0]
		}
		description := ""
		if len(dc.Descriptions) > 0 {
			description = dc.Descriptions[0]
			if len(description) > 600 {
				description = description[:600] + "…"
			}
		}
		out = append(out, GallicaItem{
			ARK: ark, Title: title, Creator: creator, Date: date, Type: typeStr,
			Language: language, Publisher: publisher, Description: description, URL: url,
		})
	}
	return out, resp.NumberOfRecords, nil
}

func gallicaBuildEntities(o *GallicaSearchOutput) []GallicaEntity {
	ents := []GallicaEntity{}
	for _, it := range o.Items {
		// Map Dublin Core type → entity kind
		kind := "library_item"
		t := strings.ToLower(it.Type)
		switch {
		case strings.Contains(t, "monographie"), strings.Contains(t, "livre"), strings.Contains(t, "book"):
			kind = "book"
		case strings.Contains(t, "fascicule"), strings.Contains(t, "publication en série"), strings.Contains(t, "newspaper"):
			kind = "newspaper"
		case strings.Contains(t, "manuscrit"), strings.Contains(t, "manuscript"):
			kind = "manuscript"
		case strings.Contains(t, "carte"), strings.Contains(t, "map"):
			kind = "map"
		case strings.Contains(t, "image"), strings.Contains(t, "estampe"), strings.Contains(t, "photographie"):
			kind = "image"
		}
		ents = append(ents, GallicaEntity{
			Kind: kind, ARK: it.ARK, Title: it.Title, URL: it.URL, Date: it.Date,
			Description: it.Description,
			Attributes:  map[string]any{"creator": it.Creator, "language": it.Language, "publisher": it.Publisher, "type": it.Type},
		})
	}
	return ents
}

func gallicaBuildHighlights(o *GallicaSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ gallica %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, it := range o.Items {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s (%s) %s", it.Title, it.Creator, it.Date, it.URL))
	}
	return hi
}
