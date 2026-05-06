package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GovInfoSearch wraps the GPO GovInfo API (api.govinfo.gov). Free with
// API key (api.data.gov / sign up at GPO). Covers Federal Register,
// Congressional Record, Public Laws, U.S. Code, GAO Reports, and many
// other US federal publications with full text.
//
// Modes:
//   - "search"           : full-text search across collections
//   - "package"          : fetch a package summary by package_id
//   - "collections_list" : list available collections
//
// Knowledge-graph: emits typed entities (kind: "publication") with stable
// GovInfo package URLs.

type GIPackage struct {
	PackageID  string `json:"package_id"`
	Title      string `json:"title"`
	DocClass   string `json:"doc_class,omitempty"`
	Date       string `json:"date_issued,omitempty"`
	Collection string `json:"collection_code,omitempty"`
	URL        string `json:"govinfo_url"`
}

type GIEntity struct {
	Kind        string         `json:"kind"`
	PackageID   string         `json:"package_id"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type GovInfoSearchOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Total             int            `json:"total,omitempty"`
	Packages          []GIPackage    `json:"packages,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []GIEntity     `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func GovInfoSearch(ctx context.Context, input map[string]any) (*GovInfoSearchOutput, error) {
	apiKey := os.Getenv("GOVINFO_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("DATA_GOV_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GOVINFO_API_KEY (or DATA_GOV_API_KEY) not set; register at api.data.gov")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["package_id"] != nil:
			mode = "package"
		case input["query"] != nil:
			mode = "search"
		default:
			mode = "collections_list"
		}
	}
	out := &GovInfoSearchOutput{Mode: mode, Source: "api.govinfo.gov"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(path string, params url.Values) ([]byte, error) {
		params.Set("api_key", apiKey)
		u := "https://api.govinfo.gov" + path + "?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("govinfo: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("govinfo: unauthorized — check API key")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("govinfo HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
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
		params := url.Values{}
		params.Set("query", q)
		params.Set("pageSize", "20")
		params.Set("offsetMark", "*")
		body, err := get("/search", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Count   int              `json:"count"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("govinfo decode: %w", err)
		}
		out.Total = resp.Count
		for _, r := range resp.Results {
			id := gtString(r, "packageId")
			out.Packages = append(out.Packages, GIPackage{
				PackageID:  id,
				Title:      gtString(r, "title"),
				DocClass:   gtString(r, "docClass"),
				Date:       gtString(r, "dateIssued"),
				Collection: gtString(r, "collectionCode"),
				URL:        "https://www.govinfo.gov/app/details/" + id,
			})
		}
	case "package":
		id, _ := input["package_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.package_id required")
		}
		out.Query = id
		body, err := get("/packages/"+url.PathEscape(id)+"/summary", url.Values{})
		if err != nil {
			return nil, err
		}
		var rec map[string]any
		if err := json.Unmarshal(body, &rec); err != nil {
			return nil, fmt.Errorf("govinfo decode: %w", err)
		}
		out.Detail = rec
		out.Packages = []GIPackage{{
			PackageID:  id,
			Title:      gtString(rec, "title"),
			DocClass:   gtString(rec, "docClass"),
			Date:       gtString(rec, "dateIssued"),
			Collection: gtString(rec, "collectionCode"),
			URL:        "https://www.govinfo.gov/app/details/" + id,
		}}
	case "collections_list":
		body, err := get("/collections", url.Values{})
		if err != nil {
			return nil, err
		}
		var resp map[string]any
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("govinfo decode: %w", err)
		}
		out.Detail = resp
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Packages)
	out.Entities = govinfoBuildEntities(out)
	out.HighlightFindings = govinfoBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func govinfoBuildEntities(o *GovInfoSearchOutput) []GIEntity {
	ents := []GIEntity{}
	for _, p := range o.Packages {
		ents = append(ents, GIEntity{
			Kind: "publication", PackageID: p.PackageID, Title: p.Title,
			URL: p.URL, Date: p.Date,
			Description: p.DocClass,
			Attributes: map[string]any{
				"collection_code": p.Collection,
				"doc_class":       p.DocClass,
			},
		})
	}
	return ents
}

func govinfoBuildHighlights(o *GovInfoSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ govinfo %s: %d packages (total %d)", o.Mode, o.Returned, o.Total)}
	for i, p := range o.Packages {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • [%s/%s] %s (%s) — %s", p.Collection, p.PackageID, hfTruncate(p.Title, 80), p.Date, p.URL))
	}
	return hi
}
