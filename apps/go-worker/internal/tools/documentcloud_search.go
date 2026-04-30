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

// DocumentCloudSearch queries DocumentCloud — the investigative-journalism
// document repository run by MuckRock + UC Berkeley Investigative Reporting
// Program. Free, no auth, ~3M documents uploaded by professional newsrooms
// (NYT, WaPo, ProPublica, Reuters, Bloomberg, regional papers, etc.).
//
// Document types: legal filings, court documents, FOIA responses, leaked
// internal memos, regulatory submissions, public comments, government
// reports, scholarly papers, and primary-source artifacts that journalists
// have OCR'd and made publicly searchable.
//
// **Why this is high-leverage for ER**: journalists do the gruntwork of
// uploading + OCR'ing primary-source documents that are otherwise locked
// behind PACER, regulatory portals, or buried in PDFs nobody reads. A
// single search like `"Anthropic"` returns 467 docs — Copyright Office
// public comments signed by their Deputy General Counsel, regulatory
// filings, court complaints, etc. That's ER pivot gold (executive name,
// title, employer, position, dated to a specific document).
//
// Two modes:
//
//   - "search"   : full-text query → list of matching documents with
//                  metadata (title, page_count, created_at, language,
//                  contributor + contributor_organization, canonical URL,
//                  full_text_url for direct OCR fetch). Optional filters:
//                  organization, source, language, date range, ordering.
//
//   - "document" : by document ID → full metadata + OCR text excerpt
//                  (first N characters, default 8000). For deep-reading a
//                  specific surfaced document.
//
// API endpoints:
//   - api.www.documentcloud.org/api/documents/search/?q=...
//   - api.www.documentcloud.org/api/documents/{id}/
//   - s3.documentcloud.org/documents/{id}/{slug}.txt  (raw OCR)

type DocCloudDocument struct {
	ID                      string `json:"id"`
	Title                   string `json:"title"`
	Slug                    string `json:"slug,omitempty"`
	Description             string `json:"description,omitempty"`
	Source                  string `json:"source,omitempty"`
	Language                string `json:"language,omitempty"`
	PageCount               int    `json:"page_count,omitempty"`
	CreatedAt               string `json:"created_at,omitempty"`
	UpdatedAt               string `json:"updated_at,omitempty"`
	Contributor             string `json:"contributor,omitempty"`
	ContributorOrganization string `json:"contributor_organization,omitempty"`
	CanonicalURL            string `json:"canonical_url,omitempty"`
	FullTextURL             string `json:"full_text_url,omitempty"`
	OCRText                 string `json:"ocr_text,omitempty"`
}

type DocumentCloudSearchOutput struct {
	Mode              string             `json:"mode"`
	Query             string             `json:"query,omitempty"`
	TotalCount        int                `json:"total_count,omitempty"`
	Returned          int                `json:"returned"`

	Documents         []DocCloudDocument `json:"documents,omitempty"`
	Document          *DocCloudDocument  `json:"document,omitempty"`

	// Aggregations (only when results have data)
	UniqueOrganizations []string         `json:"unique_organizations,omitempty"`
	UniqueLanguages     []string         `json:"unique_languages,omitempty"`

	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
	Note              string             `json:"note,omitempty"`
}

func DocumentCloudSearch(ctx context.Context, input map[string]any) (*DocumentCloudSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if _, ok := input["document_id"]; ok {
			mode = "document"
		} else {
			mode = "search"
		}
	}

	out := &DocumentCloudSearchOutput{
		Mode:   mode,
		Source: "documentcloud.org",
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
		params.Set("q", q)
		// Page size 1-25 (DocumentCloud caps)
		perPage := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 25 {
			perPage = int(l)
		}
		params.Set("per_page", fmt.Sprintf("%d", perPage))
		// Optional filters: organization, source, language, date range
		if v, ok := input["organization"].(string); ok && v != "" {
			params.Set("organization", v)
		}
		if v, ok := input["source"].(string); ok && v != "" {
			params.Set("source", v)
		}
		if v, ok := input["language"].(string); ok && v != "" {
			params.Set("language", v)
		}
		if v, ok := input["start_date"].(string); ok && v != "" {
			params.Set("created_at__gt", v)
		}
		if v, ok := input["end_date"].(string); ok && v != "" {
			params.Set("created_at__lt", v)
		}
		if v, ok := input["order"].(string); ok && v != "" {
			params.Set("ordering", v) // "-created_at" for newest first, etc.
		}
		body, err := dcGet(ctx, cli, "https://api.www.documentcloud.org/api/documents/search/?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Count   int `json:"count"`
			Results []struct {
				ID                      any    `json:"id"` // numeric or string
				Title                   string `json:"title"`
				Slug                    string `json:"slug"`
				Description             string `json:"description"`
				Source                  string `json:"source"`
				Language                string `json:"language"`
				PageCount               int    `json:"page_count"`
				CreatedAt               string `json:"created_at"`
				UpdatedAt               string `json:"updated_at"`
				Contributor             string `json:"contributor"`
				ContributorOrganization string `json:"contributor_organization"`
				CanonicalURL            string `json:"canonical_url"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("dc decode: %w", err)
		}
		out.TotalCount = raw.Count
		orgSet := map[string]struct{}{}
		langSet := map[string]struct{}{}
		for _, r := range raw.Results {
			id := dcStringID(r.ID)
			d := DocCloudDocument{
				ID:                      id,
				Title:                   r.Title,
				Slug:                    r.Slug,
				Description:             r.Description,
				Source:                  r.Source,
				Language:                r.Language,
				PageCount:               r.PageCount,
				CreatedAt:               r.CreatedAt,
				UpdatedAt:               r.UpdatedAt,
				Contributor:             r.Contributor,
				ContributorOrganization: r.ContributorOrganization,
				CanonicalURL:            r.CanonicalURL,
			}
			if id != "" && r.Slug != "" {
				d.FullTextURL = fmt.Sprintf("https://s3.documentcloud.org/documents/%s/%s.txt", id, r.Slug)
			}
			out.Documents = append(out.Documents, d)
			if r.ContributorOrganization != "" {
				orgSet[r.ContributorOrganization] = struct{}{}
			}
			if r.Language != "" {
				langSet[r.Language] = struct{}{}
			}
		}
		out.Returned = len(out.Documents)
		for o := range orgSet {
			out.UniqueOrganizations = append(out.UniqueOrganizations, o)
		}
		for l := range langSet {
			out.UniqueLanguages = append(out.UniqueLanguages, l)
		}

	case "document":
		idAny, ok := input["document_id"]
		if !ok {
			return nil, fmt.Errorf("input.document_id required for document mode")
		}
		docID := dcStringID(idAny)
		if docID == "" {
			return nil, fmt.Errorf("invalid document_id")
		}
		out.Query = docID
		body, err := dcGet(ctx, cli, "https://api.www.documentcloud.org/api/documents/"+docID+"/")
		if err != nil {
			return nil, err
		}
		var r struct {
			ID                      any    `json:"id"`
			Title                   string `json:"title"`
			Slug                    string `json:"slug"`
			Description             string `json:"description"`
			Source                  string `json:"source"`
			Language                string `json:"language"`
			PageCount               int    `json:"page_count"`
			CreatedAt               string `json:"created_at"`
			UpdatedAt               string `json:"updated_at"`
			Contributor             string `json:"contributor"`
			ContributorOrganization string `json:"contributor_organization"`
			CanonicalURL            string `json:"canonical_url"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("dc doc decode: %w", err)
		}
		d := &DocCloudDocument{
			ID:                      dcStringID(r.ID),
			Title:                   r.Title,
			Slug:                    r.Slug,
			Description:             r.Description,
			Source:                  r.Source,
			Language:                r.Language,
			PageCount:               r.PageCount,
			CreatedAt:               r.CreatedAt,
			UpdatedAt:               r.UpdatedAt,
			Contributor:             r.Contributor,
			ContributorOrganization: r.ContributorOrganization,
			CanonicalURL:            r.CanonicalURL,
		}
		if d.ID != "" && d.Slug != "" {
			d.FullTextURL = fmt.Sprintf("https://s3.documentcloud.org/documents/%s/%s.txt", d.ID, d.Slug)
			// Fetch OCR text excerpt
			fetchText, _ := input["fetch_text"].(bool)
			if !fetchText {
				// Default: fetch text unless explicitly disabled
				if v, ok := input["fetch_text"].(bool); ok {
					fetchText = v
				} else {
					fetchText = true
				}
			}
			if fetchText {
				maxChars := 8000
				if mc, ok := input["max_text_chars"].(float64); ok && mc > 0 && mc <= 100000 {
					maxChars = int(mc)
				}
				txt, err := dcFetchText(ctx, cli, d.FullTextURL, maxChars)
				if err == nil {
					d.OCRText = txt
				} else {
					out.Note = "OCR text fetch failed: " + err.Error()
				}
			}
		}
		out.Document = d
		out.Returned = 1

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, document", mode)
	}

	out.HighlightFindings = buildDocCloudHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func dcGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("documentcloud: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("documentcloud HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func dcFetchText(ctx context.Context, cli *http.Client, urlStr string, maxChars int) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	s := string(body)
	if len(s) > maxChars {
		s = s[:maxChars] + "\n\n…[truncated]"
	}
	return s, nil
}

func dcStringID(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return fmt.Sprintf("%.0f", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	default:
		return ""
	}
}

func buildDocCloudHighlights(o *DocumentCloudSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d documents match '%s' (returning %d)", o.TotalCount, o.Query, o.Returned))
		if len(o.UniqueOrganizations) > 0 {
			orgs := o.UniqueOrganizations
			suffix := ""
			if len(orgs) > 6 {
				orgs = orgs[:6]
				suffix = fmt.Sprintf(" … +%d more", len(o.UniqueOrganizations)-6)
			}
			hi = append(hi, fmt.Sprintf("  unique uploader orgs (%d): %s%s", len(o.UniqueOrganizations), strings.Join(orgs, ", "), suffix))
		}
		for i, d := range o.Documents {
			if i >= 8 {
				break
			}
			date := d.CreatedAt
			if len(date) > 10 {
				date = date[:10]
			}
			meta := []string{fmt.Sprintf("%dpg", d.PageCount)}
			if d.ContributorOrganization != "" {
				meta = append(meta, "uploader: "+d.ContributorOrganization)
			}
			if d.Source != "" {
				meta = append(meta, "source: "+d.Source)
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s (%s)", date, hfTruncate(d.Title, 80), strings.Join(meta, " · ")))
		}

	case "document":
		if o.Document == nil {
			hi = append(hi, "✗ document not found")
			break
		}
		d := o.Document
		hi = append(hi, fmt.Sprintf("✓ doc %s — %s", d.ID, d.Title))
		date := d.CreatedAt
		if len(date) > 10 {
			date = date[:10]
		}
		hi = append(hi, fmt.Sprintf("  uploaded: %s · %d pages · lang: %s", date, d.PageCount, d.Language))
		if d.ContributorOrganization != "" {
			hi = append(hi, "  uploader org: "+d.ContributorOrganization)
		}
		if d.Source != "" {
			hi = append(hi, "  source: "+d.Source)
		}
		if d.Description != "" {
			hi = append(hi, "  description: "+hfTruncate(d.Description, 200))
		}
		if d.CanonicalURL != "" {
			hi = append(hi, "  url: "+d.CanonicalURL)
		}
		if d.OCRText != "" {
			textLen := len(d.OCRText)
			hi = append(hi, fmt.Sprintf("  📄 OCR text excerpt (%d chars):", textLen))
			// Show first 4 lines of the text
			lines := strings.Split(d.OCRText, "\n")
			shown := 0
			for _, ln := range lines {
				ln = strings.TrimSpace(ln)
				if ln == "" {
					continue
				}
				hi = append(hi, "    "+hfTruncate(ln, 100))
				shown++
				if shown >= 4 {
					break
				}
			}
		}
	}
	return hi
}
