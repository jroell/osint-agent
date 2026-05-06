package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ADBSearch wraps the Australian Dictionary of Biography
// (adb.anu.edu.au). Free, scrape-only — no public API. Authoritative
// biographical reference for ~13,000 Australians.
//
// Modes:
//   - "search"  : keyword search → list of candidate biographies
//   - "biography" : fetch one biography by ADB slug
//
// Knowledge-graph: emits typed entities (kind: "person") with stable
// ADB URLs.

type ADBHit struct {
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type ADBBiography struct {
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Birth      string `json:"birth,omitempty"`
	Death      string `json:"death,omitempty"`
	Profession string `json:"profession,omitempty"`
	Excerpt    string `json:"excerpt,omitempty"`
	URL        string `json:"url"`
}

type ADBEntity struct {
	Kind        string         `json:"kind"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type ADBSearchOutput struct {
	Mode              string        `json:"mode"`
	Query             string        `json:"query"`
	Returned          int           `json:"returned"`
	Hits              []ADBHit      `json:"hits,omitempty"`
	Biography         *ADBBiography `json:"biography,omitempty"`
	Entities          []ADBEntity   `json:"entities"`
	HighlightFindings []string      `json:"highlight_findings"`
	Source            string        `json:"source"`
	TookMs            int64         `json:"tookMs"`
}

func ADBSearch(ctx context.Context, input map[string]any) (*ADBSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if input["slug"] != nil {
			mode = "biography"
		} else {
			mode = "search"
		}
	}
	out := &ADBSearchOutput{Mode: mode, Source: "adb.anu.edu.au"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) (string, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return "", fmt.Errorf("adb: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("adb HTTP %d", resp.StatusCode)
		}
		return string(body), nil
	}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		html, err := get("https://adb.anu.edu.au/search/?fulltext=" + url.QueryEscape(q))
		if err != nil {
			return nil, err
		}
		out.Hits = parseADBSearchResults(html)

	case "biography":
		slug, _ := input["slug"].(string)
		if slug == "" {
			return nil, fmt.Errorf("input.slug required")
		}
		out.Query = slug
		bioURL := "https://adb.anu.edu.au/biography/" + slug
		html, err := get(bioURL)
		if err != nil {
			return nil, err
		}
		out.Biography = parseADBBiography(html, slug, bioURL)

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Hits)
	if out.Biography != nil {
		out.Returned = 1
	}
	out.Entities = adbBuildEntities(out)
	out.HighlightFindings = adbBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

var (
	adbHitRe = regexp.MustCompile(`(?is)<a[^>]+href="(/biography/[^"]+)"[^>]*>([^<]+)</a>`)
	// h2 holds the actual bio person name; h1 is the site banner
	adbBioNameRe = regexp.MustCompile(`(?is)<h2[^>]*>\s*([^<(]+?)\s*\(\s*(\d{4}|\??)\s*[–\-—]\s*(\d{4}|\??)\s*\)\s*</h2>`)
	// "(1867-1922)" or "(1 January 1860 – 5 March 1925)" patterns
	adbBioBirthRe     = regexp.MustCompile(`(?is)<h2[^>]*>[^<]*\(\s*(\d{4})\s*[–\-—]\s*(\d{4})\s*\)`)
	adbBioFirstParaRe = regexp.MustCompile(`(?is)<p[^>]*>([^<]{40,})</p>`)
)

func parseADBSearchResults(html string) []ADBHit {
	hits := []ADBHit{}
	seen := map[string]bool{}
	for _, m := range adbHitRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 3 {
			path := m[1]
			slug := strings.TrimPrefix(path, "/biography/")
			if seen[slug] {
				continue
			}
			seen[slug] = true
			hits = append(hits, ADBHit{
				Title: strings.TrimSpace(stripHTMLBare(m[2])),
				Slug:  slug,
				URL:   "https://adb.anu.edu.au" + path,
			})
		}
		if len(hits) >= 30 {
			break
		}
	}
	return hits
}

func parseADBBiography(html, slug, url string) *ADBBiography {
	b := &ADBBiography{Slug: slug, URL: url}
	if m := adbBioNameRe.FindStringSubmatch(html); len(m) >= 4 {
		b.Name = strings.TrimSpace(stripHTMLBare(m[1]))
		b.Birth = m[2]
		b.Death = m[3]
	}
	if b.Birth == "" {
		if m := adbBioBirthRe.FindStringSubmatch(html); len(m) >= 3 {
			b.Birth = m[1]
			b.Death = m[2]
		}
	}
	if m := adbBioFirstParaRe.FindStringSubmatch(html); len(m) >= 2 {
		excerpt := strings.TrimSpace(stripHTMLBare(m[1]))
		if len(excerpt) > 800 {
			excerpt = excerpt[:800] + "…"
		}
		b.Excerpt = excerpt
	}
	return b
}

func adbBuildEntities(o *ADBSearchOutput) []ADBEntity {
	ents := []ADBEntity{}
	for _, h := range o.Hits {
		ents = append(ents, ADBEntity{
			Kind: "person", Slug: h.Slug, Name: h.Title, URL: h.URL,
			Description: h.Snippet,
			Attributes:  map[string]any{"role": "search_candidate"},
		})
	}
	if b := o.Biography; b != nil {
		ents = append(ents, ADBEntity{
			Kind: "person", Slug: b.Slug, Name: b.Name, URL: b.URL, Date: b.Birth,
			Description: b.Excerpt,
			Attributes: map[string]any{
				"birth": b.Birth, "death": b.Death, "profession": b.Profession,
			},
		})
	}
	return ents
}

func adbBuildHighlights(o *ADBSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ adb %s: %d records", o.Mode, o.Returned)}
	for i, h := range o.Hits {
		if i >= 8 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s", h.Title, h.URL))
	}
	if b := o.Biography; b != nil {
		hi = append(hi, fmt.Sprintf("  • %s — %s", b.Name, b.URL))
		if b.Excerpt != "" {
			hi = append(hi, "    "+hfTruncate(b.Excerpt, 200))
		}
	}
	return hi
}
