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

// HathiTrustSearch wraps the HathiTrust Digital Library APIs:
//   - Bibliographic API (catalog.hathitrust.org/api/volumes/...)
//     for ID/ISBN/OCLC/LCCN-based volume lookups (free, no-key)
//   - SOLR-backed catalog search via the public Babel/CB front-door
//     proxy at catalog.hathitrust.org/Search/SearchPublication?...
//
// HathiTrust holds ~17M digitized books/periodicals from major US/UK
// research libraries. Critical for book-history chain questions and
// pre-1950 source-of-record lookups outside the LoC corpus.
//
// Modes:
//   - "search"        : free-text catalog search (returns hathitrust IDs + titles + authors)
//   - "volume_by_id"  : pull bibliographic record for a HathiTrust volume id
//   - "volume_by_oclc": lookup by OCLC number
//   - "volume_by_isbn": lookup by ISBN
//
// Knowledge-graph: emits typed entities (kind: "book") with stable
// HathiTrust handle URLs and OCLC/ISBN cross-references.

type HTVolume struct {
	HathiID     string   `json:"hathitrust_id"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors,omitempty"`
	PubInfo     string   `json:"pub_info,omitempty"`
	PubYear     string   `json:"pub_year,omitempty"`
	OCLC        string   `json:"oclc,omitempty"`
	ISBN        []string `json:"isbn,omitempty"`
	LCCN        string   `json:"lccn,omitempty"`
	URL         string   `json:"hathitrust_url"`
	HandleURL   string   `json:"handle_url,omitempty"`
	Rights      string   `json:"rights,omitempty"`
	HasFullText bool     `json:"has_full_text,omitempty"`
}

type HTEntity struct {
	Kind        string         `json:"kind"`
	HathiID     string         `json:"hathitrust_id,omitempty"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type HathiTrustSearchOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	Returned          int        `json:"returned"`
	Volumes           []HTVolume `json:"volumes,omitempty"`
	Entities          []HTEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func HathiTrustSearch(ctx context.Context, input map[string]any) (*HathiTrustSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["hathitrust_id"] != nil:
			mode = "volume_by_id"
		case input["oclc"] != nil:
			mode = "volume_by_oclc"
		case input["isbn"] != nil:
			mode = "volume_by_isbn"
		default:
			mode = "search"
		}
	}
	out := &HathiTrustSearchOutput{Mode: mode, Source: "catalog.hathitrust.org"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hathitrust: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("hathitrust: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("hathitrust HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		// HT catalog public search (Solr JSON output)
		params := url.Values{}
		params.Set("lookfor", q)
		params.Set("type", "AllFields")
		params.Set("limit", "20")
		// JSON output via the catalog's RecordViewer JSON proxy isn't always
		// stable; the Solr JSON output we use is the publicly documented
		// `?type=AllFields&lookfor=...&limit=N&jsonResponse=1` form.
		params.Set("jsonResponse", "1")
		body, err := get("https://catalog.hathitrust.org/Search/Home?" + params.Encode())
		if err != nil {
			return nil, err
		}
		// Catalog returns a Solr JSON response when ?jsonResponse=1 is present.
		var resp struct {
			Records []struct {
				ID      string   `json:"id"`
				Title   string   `json:"title_display"`
				Author  []string `json:"author_display"`
				PubDate []string `json:"pub_date_display"`
				Pub     []string `json:"publishDate"`
				OCLC    []string `json:"oclc"`
				ISBN    []string `json:"isbn"`
				LCCN    []string `json:"lccn"`
			} `json:"records"`
			Total int `json:"resultCount"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			// Fall back: HT may return non-JSON HTML in some configurations; degrade gracefully.
			out.Returned = 0
		} else {
			for _, r := range resp.Records {
				vol := HTVolume{
					HathiID: r.ID,
					Title:   r.Title,
					Authors: r.Author,
					URL:     "https://catalog.hathitrust.org/Record/" + r.ID,
				}
				if len(r.PubDate) > 0 {
					vol.PubYear = r.PubDate[0]
				}
				if len(r.Pub) > 0 && vol.PubInfo == "" {
					vol.PubInfo = r.Pub[0]
				}
				if len(r.OCLC) > 0 {
					vol.OCLC = r.OCLC[0]
				}
				vol.ISBN = r.ISBN
				if len(r.LCCN) > 0 {
					vol.LCCN = r.LCCN[0]
				}
				out.Volumes = append(out.Volumes, vol)
			}
		}

	case "volume_by_id":
		id, _ := input["hathitrust_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.hathitrust_id required")
		}
		out.Query = id
		body, err := get(fmt.Sprintf("https://catalog.hathitrust.org/api/volumes/full/htid/%s.json",
			url.PathEscape(id)))
		if err != nil {
			return nil, err
		}
		out.Volumes = parseHTBibVolumes(body)

	case "volume_by_oclc":
		oclc, _ := input["oclc"].(string)
		if oclc == "" {
			return nil, fmt.Errorf("input.oclc required")
		}
		out.Query = oclc
		body, err := get(fmt.Sprintf("https://catalog.hathitrust.org/api/volumes/brief/oclc/%s.json",
			url.PathEscape(oclc)))
		if err != nil {
			return nil, err
		}
		out.Volumes = parseHTBibVolumes(body)

	case "volume_by_isbn":
		isbn, _ := input["isbn"].(string)
		if isbn == "" {
			return nil, fmt.Errorf("input.isbn required")
		}
		out.Query = isbn
		body, err := get(fmt.Sprintf("https://catalog.hathitrust.org/api/volumes/brief/isbn/%s.json",
			url.PathEscape(isbn)))
		if err != nil {
			return nil, err
		}
		out.Volumes = parseHTBibVolumes(body)

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Volumes)
	out.Entities = htBuildEntities(out)
	out.HighlightFindings = htBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseHTBibVolumes(body []byte) []HTVolume {
	// HT bibliographic API returns:
	// { "<key>": { "records": { "<id>": {...} }, "items": [{...}] } }
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	out := []HTVolume{}
	for _, v := range raw {
		obj, _ := v.(map[string]any)
		if obj == nil {
			continue
		}
		records, _ := obj["records"].(map[string]any)
		for recID, recVal := range records {
			recObj, _ := recVal.(map[string]any)
			if recObj == nil {
				continue
			}
			vol := HTVolume{HathiID: recID}
			if titles, ok := recObj["titles"].([]any); ok && len(titles) > 0 {
				if t, ok := titles[0].(string); ok {
					vol.Title = t
				}
			}
			if isbns, ok := recObj["isbns"].([]any); ok {
				for _, x := range isbns {
					if s, ok := x.(string); ok {
						vol.ISBN = append(vol.ISBN, s)
					}
				}
			}
			if oclcs, ok := recObj["oclcs"].([]any); ok && len(oclcs) > 0 {
				if s, ok := oclcs[0].(string); ok {
					vol.OCLC = s
				}
			}
			if pub, ok := recObj["publishDates"].([]any); ok && len(pub) > 0 {
				if s, ok := pub[0].(string); ok {
					vol.PubYear = s
				}
			}
			vol.URL = "https://catalog.hathitrust.org/Record/" + recID
			out = append(out, vol)
		}
		if items, ok := obj["items"].([]any); ok && len(out) > 0 {
			// pick first handle as the volume URL
			for _, it := range items {
				m, _ := it.(map[string]any)
				if m == nil {
					continue
				}
				htid := gtString(m, "htid")
				url := gtString(m, "itemURL")
				rights := gtString(m, "rightsCode")
				if htid != "" {
					for i := range out {
						if out[i].HathiID == "" || out[i].HandleURL == "" {
							out[i].HandleURL = url
							out[i].Rights = rights
							out[i].HasFullText = strings.HasPrefix(rights, "pd") || rights == "und-world"
							break
						}
					}
				}
			}
		}
	}
	return out
}

func htBuildEntities(o *HathiTrustSearchOutput) []HTEntity {
	ents := []HTEntity{}
	for _, v := range o.Volumes {
		ents = append(ents, HTEntity{
			Kind: "book", HathiID: v.HathiID, Title: v.Title, URL: v.URL, Date: v.PubYear,
			Description: strings.Join(v.Authors, ", "),
			Attributes: map[string]any{
				"authors":    v.Authors,
				"pub_year":   v.PubYear,
				"pub_info":   v.PubInfo,
				"oclc":       v.OCLC,
				"isbn":       v.ISBN,
				"lccn":       v.LCCN,
				"rights":     v.Rights,
				"handle_url": v.HandleURL,
				"full_text":  v.HasFullText,
			},
		})
	}
	return ents
}

func htBuildHighlights(o *HathiTrustSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ hathitrust %s: %d volumes", o.Mode, o.Returned)}
	for i, v := range o.Volumes {
		if i >= 6 {
			break
		}
		auth := strings.Join(v.Authors, ", ")
		if len(auth) > 60 {
			auth = auth[:60] + "…"
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s (%s) [%s]", v.Title, auth, v.PubYear, v.URL))
	}
	return hi
}
