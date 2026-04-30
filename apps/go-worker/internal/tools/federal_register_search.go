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

// FederalRegisterSearch queries the US Federal Register API — the official
// daily journal of the federal government. Every federal action since 1994
// is here: rules, proposed rules, notices, presidential documents
// (including executive orders), agency RFIs, comment periods, etc. Free,
// no auth.
//
// Why this matters for ER:
//
//   - Authoritative citation: every doc has a Federal Register citation
//     (e.g. "91 FR 698") that's the legally-binding reference. Pairs with
//     courtlistener_search (court rulings), sec_edgar_search (corporate
//     filings), documentcloud_search (journalism) for full regulatory ER.
//   - Comment-period tracking: regulatory RFIs surface every public
//     comment by company / industry group / individual. The list of
//     commenters on a controversial rule IS an ER pivot ("who fought
//     this regulation?").
//   - Executive orders: presidential_document_type filter surfaces every
//     EO with signing date and action. Useful for executive-action timing
//     analysis.
//
// Three modes:
//
//   - "search"      : full-text query with optional agency, doc type
//                      (RULE / PRORULE / NOTICE / PRESDOCU / OTHER), date
//                      range, agency slug filters
//   - "document"    : by document_number (e.g. "2026-00206") → full
//                      metadata + body_html_url + raw_text_url for
//                      direct text retrieval
//   - "recent_eos"  : presidential executive orders feed, optional from
//                      date — operational tracking of new EOs

type FRDocument struct {
	DocumentNumber          string   `json:"document_number"`
	Title                   string   `json:"title"`
	Type                    string   `json:"type,omitempty"`
	Subtype                 string   `json:"subtype,omitempty"`
	Citation                string   `json:"citation,omitempty"`
	Action                  string   `json:"action,omitempty"`
	Abstract                string   `json:"abstract,omitempty"`
	Agencies                []string `json:"agencies,omitempty"`
	PublicationDate         string   `json:"publication_date,omitempty"`
	EffectiveOn             string   `json:"effective_on,omitempty"`
	CommentsCloseOn         string   `json:"comments_close_on,omitempty"`
	SigningDate             string   `json:"signing_date,omitempty"`
	PresidentialDocType     string   `json:"presidential_document_type,omitempty"`
	ExecutiveOrderNumber    string   `json:"executive_order_number,omitempty"`
	HTMLURL                 string   `json:"html_url,omitempty"`
	PDFURL                  string   `json:"pdf_url,omitempty"`
	BodyHTMLURL             string   `json:"body_html_url,omitempty"`
	RawTextURL              string   `json:"raw_text_url,omitempty"`
	FullTextXMLURL          string   `json:"full_text_xml_url,omitempty"`
}

type FederalRegisterSearchOutput struct {
	Mode              string       `json:"mode"`
	Query             string       `json:"query,omitempty"`
	TotalCount        int          `json:"total_count,omitempty"`
	Returned          int          `json:"returned"`
	Documents         []FRDocument `json:"documents,omitempty"`
	Document          *FRDocument  `json:"document,omitempty"`

	// Aggregations
	UniqueAgencies    []string     `json:"unique_agencies,omitempty"`
	UniqueTypes       []string     `json:"unique_types,omitempty"`

	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
	Note              string       `json:"note,omitempty"`
}

func FederalRegisterSearch(ctx context.Context, input map[string]any) (*FederalRegisterSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["document_number"]; ok {
			mode = "document"
		} else if _, ok := input["query"]; ok {
			mode = "search"
		} else {
			mode = "recent_eos"
		}
	}

	out := &FederalRegisterSearchOutput{
		Mode:   mode,
		Source: "federalregister.gov/api/v1",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.query required for search mode")
		}
		out.Query = q
		params := url.Values{}
		params.Set("conditions[term]", q)
		// Optional filters
		if v, ok := input["agency"].(string); ok && v != "" {
			params.Set("conditions[agencies][]", v)
		}
		if v, ok := input["doc_type"].(string); ok && v != "" {
			// Allow multiple comma-separated
			for _, t := range strings.Split(v, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					params.Add("conditions[type][]", t)
				}
			}
		}
		if v, ok := input["start_date"].(string); ok && v != "" {
			params.Set("conditions[publication_date][gte]", v)
		}
		if v, ok := input["end_date"].(string); ok && v != "" {
			params.Set("conditions[publication_date][lte]", v)
		}
		if v, ok := input["effective_after"].(string); ok && v != "" {
			params.Set("conditions[effective_date][gte]", v)
		}
		perPage := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			perPage = int(l)
		}
		params.Set("per_page", fmt.Sprintf("%d", perPage))
		// Newest first by default
		order := "newest"
		if v, ok := input["order"].(string); ok && v != "" {
			order = v
		}
		params.Set("order", order)

		body, err := frGet(ctx, cli, "https://www.federalregister.gov/api/v1/documents.json?"+params.Encode())
		if err != nil {
			return nil, err
		}
		if err := decodeFRSearch(body, out); err != nil {
			return nil, err
		}

	case "document":
		docNum, _ := input["document_number"].(string)
		docNum = strings.TrimSpace(docNum)
		if docNum == "" {
			return nil, fmt.Errorf("input.document_number required (e.g. '2026-00206')")
		}
		out.Query = docNum
		body, err := frGet(ctx, cli, "https://www.federalregister.gov/api/v1/documents/"+docNum+".json")
		if err != nil {
			return nil, err
		}
		var raw frRawDoc
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("FR doc decode: %w", err)
		}
		d := convertFRDoc(raw)
		out.Document = &d
		out.Returned = 1
		// Optional raw-text fetch
		fetchText, _ := input["fetch_text"].(bool)
		if fetchText && d.RawTextURL != "" {
			req, _ := http.NewRequestWithContext(ctx, "GET", d.RawTextURL, nil)
			req.Header.Set("User-Agent", "osint-agent/1.0")
			resp, err := cli.Do(req)
			if err == nil && resp.StatusCode == 200 {
				maxChars := 8000
				if mc, ok := input["max_text_chars"].(float64); ok && mc > 0 && mc <= 100000 {
					maxChars = int(mc)
				}
				txtBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
				resp.Body.Close()
				txt := string(txtBody)
				if len(txt) > maxChars {
					txt = txt[:maxChars] + "\n\n…[truncated]"
				}
				// Stash text in Abstract since we don't have a dedicated field
				d.Abstract = txt
				out.Document = &d
			} else if resp != nil {
				resp.Body.Close()
			}
		}

	case "recent_eos":
		params := url.Values{}
		params.Set("conditions[type][]", "PRESDOCU")
		params.Set("conditions[presidential_document_type][]", "executive_order")
		if v, ok := input["start_date"].(string); ok && v != "" {
			params.Set("conditions[publication_date][gte]", v)
		} else {
			// Default last 365 days
			oneYearAgo := time.Now().AddDate(-1, 0, 0).Format("2006-01-02")
			params.Set("conditions[publication_date][gte]", oneYearAgo)
		}
		perPage := 20
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
			perPage = int(l)
		}
		params.Set("per_page", fmt.Sprintf("%d", perPage))
		params.Set("order", "newest")
		out.Query = "presidential executive orders feed"
		body, err := frGet(ctx, cli, "https://www.federalregister.gov/api/v1/documents.json?"+params.Encode())
		if err != nil {
			return nil, err
		}
		if err := decodeFRSearch(body, out); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, document, recent_eos", mode)
	}

	// Aggregations
	agencySet := map[string]struct{}{}
	typeSet := map[string]struct{}{}
	for _, d := range out.Documents {
		for _, a := range d.Agencies {
			agencySet[a] = struct{}{}
		}
		if d.Type != "" {
			typeSet[d.Type] = struct{}{}
		}
	}
	for a := range agencySet {
		out.UniqueAgencies = append(out.UniqueAgencies, a)
	}
	sort.Strings(out.UniqueAgencies)
	for t := range typeSet {
		out.UniqueTypes = append(out.UniqueTypes, t)
	}
	sort.Strings(out.UniqueTypes)

	out.HighlightFindings = buildFRHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// raw shapes
type frRawAgency struct {
	Name string `json:"name"`
	RawName string `json:"raw_name"`
}
type frRawDoc struct {
	DocumentNumber       string        `json:"document_number"`
	Title                string        `json:"title"`
	Type                 string        `json:"type"`
	Subtype              string        `json:"subtype"`
	Citation             string        `json:"citation"`
	Action               string        `json:"action"`
	Abstract             string        `json:"abstract"`
	Agencies             []frRawAgency `json:"agencies"`
	PublicationDate      string        `json:"publication_date"`
	EffectiveOn          string        `json:"effective_on"`
	CommentsCloseOn      string        `json:"comments_close_on"`
	SigningDate          string        `json:"signing_date"`
	PresidentialDocType  string        `json:"presidential_document_type"`
	ExecutiveOrderNumber any           `json:"executive_order_number"`
	HTMLURL              string        `json:"html_url"`
	PDFURL               string        `json:"pdf_url"`
	BodyHTMLURL          string        `json:"body_html_url"`
	RawTextURL           string        `json:"raw_text_url"`
	FullTextXMLURL       string        `json:"full_text_xml_url"`
}

func convertFRDoc(r frRawDoc) FRDocument {
	d := FRDocument{
		DocumentNumber:      r.DocumentNumber,
		Title:               r.Title,
		Type:                r.Type,
		Subtype:             r.Subtype,
		Citation:            r.Citation,
		Action:              r.Action,
		Abstract:            r.Abstract,
		PublicationDate:     r.PublicationDate,
		EffectiveOn:         r.EffectiveOn,
		CommentsCloseOn:     r.CommentsCloseOn,
		SigningDate:         r.SigningDate,
		PresidentialDocType: r.PresidentialDocType,
		HTMLURL:             r.HTMLURL,
		PDFURL:              r.PDFURL,
		BodyHTMLURL:         r.BodyHTMLURL,
		RawTextURL:          r.RawTextURL,
		FullTextXMLURL:      r.FullTextXMLURL,
	}
	for _, a := range r.Agencies {
		name := a.Name
		if name == "" {
			name = a.RawName
		}
		if name != "" {
			d.Agencies = append(d.Agencies, name)
		}
	}
	if r.ExecutiveOrderNumber != nil {
		d.ExecutiveOrderNumber = fmt.Sprintf("%v", r.ExecutiveOrderNumber)
	}
	return d
}

func decodeFRSearch(body []byte, out *FederalRegisterSearchOutput) error {
	var raw struct {
		Count   int        `json:"count"`
		Results []frRawDoc `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("FR decode: %w", err)
	}
	out.TotalCount = raw.Count
	for _, r := range raw.Results {
		out.Documents = append(out.Documents, convertFRDoc(r))
	}
	out.Returned = len(out.Documents)
	return nil
}

func frGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FR: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("FR HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildFRHighlights(o *FederalRegisterSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d documents match '%s' (returning %d, sorted newest first)", o.TotalCount, o.Query, o.Returned))
		if len(o.UniqueAgencies) > 0 {
			ag := o.UniqueAgencies
			suffix := ""
			if len(ag) > 4 {
				ag = ag[:4]
				suffix = fmt.Sprintf(" … +%d", len(o.UniqueAgencies)-4)
			}
			hi = append(hi, fmt.Sprintf("  agencies: %s%s", strings.Join(ag, ", "), suffix))
		}
		if len(o.UniqueTypes) > 0 {
			hi = append(hi, fmt.Sprintf("  types: %s", strings.Join(o.UniqueTypes, ", ")))
		}
		for i, d := range o.Documents {
			if i >= 6 {
				break
			}
			cite := ""
			if d.Citation != "" {
				cite = " · " + d.Citation
			}
			eff := ""
			if d.EffectiveOn != "" {
				eff = " · effective " + d.EffectiveOn
			}
			com := ""
			if d.CommentsCloseOn != "" {
				com = " · comments close " + d.CommentsCloseOn
			}
			ag := strings.Join(d.Agencies, "/")
			hi = append(hi, fmt.Sprintf("  • [%s] %s [%s]%s%s%s — %s", d.PublicationDate, hfTruncate(d.Title, 80), d.Type, cite, eff, com, ag))
		}

	case "document":
		if o.Document == nil {
			hi = append(hi, fmt.Sprintf("✗ no document for '%s'", o.Query))
			break
		}
		d := o.Document
		hi = append(hi, fmt.Sprintf("✓ %s — %s [%s]", d.DocumentNumber, d.Title, d.Type))
		if d.Citation != "" {
			hi = append(hi, "  citation: "+d.Citation)
		}
		if len(d.Agencies) > 0 {
			hi = append(hi, "  agencies: "+strings.Join(d.Agencies, ", "))
		}
		hi = append(hi, "  published: "+d.PublicationDate)
		if d.Action != "" {
			hi = append(hi, "  action: "+d.Action)
		}
		if d.EffectiveOn != "" {
			hi = append(hi, "  effective on: "+d.EffectiveOn)
		}
		if d.CommentsCloseOn != "" {
			hi = append(hi, "  comments close: "+d.CommentsCloseOn)
		}
		if d.SigningDate != "" {
			hi = append(hi, "  signed: "+d.SigningDate)
		}
		if d.ExecutiveOrderNumber != "" && d.ExecutiveOrderNumber != "<nil>" {
			hi = append(hi, "  EO number: "+d.ExecutiveOrderNumber)
		}
		if d.HTMLURL != "" {
			hi = append(hi, "  url: "+d.HTMLURL)
		}
		if d.RawTextURL != "" {
			hi = append(hi, "  raw text: "+d.RawTextURL)
		}
		if d.Abstract != "" && len(d.Abstract) > 0 {
			hi = append(hi, "  abstract: "+hfTruncate(d.Abstract, 220))
		}

	case "recent_eos":
		hi = append(hi, fmt.Sprintf("✓ %d recent presidential executive orders (returning %d)", o.TotalCount, o.Returned))
		for i, d := range o.Documents {
			if i >= 10 {
				break
			}
			eo := ""
			if d.ExecutiveOrderNumber != "" && d.ExecutiveOrderNumber != "<nil>" {
				eo = " EO " + d.ExecutiveOrderNumber
			}
			signed := ""
			if d.SigningDate != "" {
				signed = " · signed " + d.SigningDate
			}
			hi = append(hi, fmt.Sprintf("  • [%s]%s — %s%s", d.PublicationDate, eo, hfTruncate(d.Title, 80), signed))
		}
	}
	return hi
}
