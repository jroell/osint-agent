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

// WikipediaSearch wraps Wikipedia's REST + MediaWiki APIs for article-
// level access. Distinct from `wikidata_lookup` (structured data) and
// `wikipedia_user_intel` (user accounts) — this surface is article text.
//
// Four modes:
//
//   - "summary"         : by article title → REST summary with extract,
//                          description, thumbnail, last-edit timestamp,
//                          content URL. Lead paragraph only (~500 chars).
//   - "search"          : keyword query → matching articles with HTML-marked
//                          snippets, word count, byte size, last-edit
//                          timestamp. Article creation around current events
//                          is a unique ER signal — a brand-new article on
//                          a controversy is a high-leverage indicator.
//   - "article_meta"    : by title → categories (taxonomic placement),
//                          recent revisions (who's editing + when), article
//                          length, last-rev id.
//   - "article_content" : by title → FULL plain-text article body
//                          (typically 30k–300k chars). Pageable via
//                          start_offset / max_chars. Critical for multi-hop
//                          fact extraction where the answer is in a body
//                          section (Career, Filmography, Awards, etc.).
//
// Free, no auth. Configurable language via `lang` param (default "en").

type WikiSummary struct {
	Title            string `json:"title"`
	Description      string `json:"description,omitempty"`
	Extract          string `json:"extract,omitempty"`
	Thumbnail        string `json:"thumbnail_url,omitempty"`
	Lang             string `json:"lang,omitempty"`
	Timestamp        string `json:"last_edit_timestamp,omitempty"`
	Type             string `json:"type,omitempty"`
	ContentURL       string `json:"content_url,omitempty"`
	WikidataID       string `json:"wikidata_id,omitempty"`
}

type WikiSearchHit struct {
	Title       string `json:"title"`
	PageID      int    `json:"page_id"`
	Size        int    `json:"size_bytes"`
	WordCount   int    `json:"word_count"`
	Snippet     string `json:"snippet,omitempty"`
	Timestamp   string `json:"last_edit_timestamp,omitempty"`
	URL         string `json:"url,omitempty"`
}

type WikiRevision struct {
	User      string `json:"user"`
	Timestamp string `json:"timestamp"`
}

type WikiArticleMeta struct {
	Title         string         `json:"title"`
	PageID        int            `json:"page_id,omitempty"`
	LastRevID     int64          `json:"last_rev_id,omitempty"`
	LengthBytes   int            `json:"length_bytes,omitempty"`
	Categories    []string       `json:"categories,omitempty"`
	RecentRevs    []WikiRevision `json:"recent_revisions,omitempty"`
	URL           string         `json:"url,omitempty"`
}

type WikiArticleContent struct {
	Title       string `json:"title"`
	TotalChars  int    `json:"total_chars"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	NextOffset  *int   `json:"next_offset,omitempty"`
	Content     string `json:"content"`
	URL         string `json:"url,omitempty"`
}

type WikipediaSearchOutput struct {
	Mode              string              `json:"mode"`
	Lang              string              `json:"lang,omitempty"`
	Query             string              `json:"query,omitempty"`
	TotalCount        int                 `json:"total_count,omitempty"`
	Returned          int                 `json:"returned"`
	Summary           *WikiSummary        `json:"summary,omitempty"`
	Hits              []WikiSearchHit     `json:"hits,omitempty"`
	ArticleMeta       *WikiArticleMeta    `json:"article_meta,omitempty"`
	ArticleContent    *WikiArticleContent `json:"article_content,omitempty"`

	HighlightFindings []string `json:"highlight_findings"`
	Source            string   `json:"source"`
	TookMs            int64    `json:"tookMs"`
	Note              string   `json:"note,omitempty"`
}

func WikipediaSearch(ctx context.Context, input map[string]any) (*WikipediaSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["title"]; ok {
			mode = "summary"
		} else if _, ok := input["query"]; ok {
			mode = "search"
		} else {
			return nil, fmt.Errorf("input.title (summary/article_meta) or input.query (search) required")
		}
	}

	lang, _ := input["lang"].(string)
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		lang = "en"
	}

	out := &WikipediaSearchOutput{
		Mode:   mode,
		Lang:   lang,
		Source: lang + ".wikipedia.org",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "summary":
		title, _ := input["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, fmt.Errorf("input.title required for summary mode")
		}
		out.Query = title
		// Wikipedia REST expects URL-encoded title with spaces as underscores
		titleEnc := strings.ReplaceAll(title, " ", "_")
		urlStr := fmt.Sprintf("https://%s.wikipedia.org/api/rest_v1/page/summary/%s", lang, url.PathEscape(titleEnc))
		body, err := wikiGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Extract     string `json:"extract"`
			Thumbnail   struct {
				Source string `json:"source"`
			} `json:"thumbnail"`
			Lang        string `json:"lang"`
			Timestamp   string `json:"timestamp"`
			Type        string `json:"type"`
			ContentURLs struct {
				Desktop struct {
					Page string `json:"page"`
				} `json:"desktop"`
			} `json:"content_urls"`
			WikibaseItem string `json:"wikibase_item"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("wiki summary decode: %w", err)
		}
		s := &WikiSummary{
			Title:       raw.Title,
			Description: raw.Description,
			Extract:     raw.Extract,
			Thumbnail:   raw.Thumbnail.Source,
			Lang:        raw.Lang,
			Timestamp:   raw.Timestamp,
			Type:        raw.Type,
			ContentURL:  raw.ContentURLs.Desktop.Page,
			WikidataID:  raw.WikibaseItem,
		}
		out.Summary = s
		out.Returned = 1

	case "search":
		query, _ := input["query"].(string)
		query = strings.TrimSpace(query)
		if query == "" {
			return nil, fmt.Errorf("input.query required for search mode")
		}
		out.Query = query
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		params := url.Values{}
		params.Set("action", "query")
		params.Set("list", "search")
		params.Set("srsearch", query)
		params.Set("srlimit", fmt.Sprintf("%d", limit))
		params.Set("format", "json")
		urlStr := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?%s", lang, params.Encode())
		body, err := wikiGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Query struct {
				SearchInfo struct {
					TotalHits int `json:"totalhits"`
				} `json:"searchinfo"`
				Search []struct {
					Title     string `json:"title"`
					PageID    int    `json:"pageid"`
					Size      int    `json:"size"`
					WordCount int    `json:"wordcount"`
					Snippet   string `json:"snippet"`
					Timestamp string `json:"timestamp"`
				} `json:"search"`
			} `json:"query"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("wiki search decode: %w", err)
		}
		out.TotalCount = raw.Query.SearchInfo.TotalHits
		for _, h := range raw.Query.Search {
			titleEnc := strings.ReplaceAll(h.Title, " ", "_")
			out.Hits = append(out.Hits, WikiSearchHit{
				Title:     h.Title,
				PageID:    h.PageID,
				Size:      h.Size,
				WordCount: h.WordCount,
				Snippet:   wikiStripHTML(h.Snippet),
				Timestamp: h.Timestamp,
				URL:       fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, url.PathEscape(titleEnc)),
			})
		}
		out.Returned = len(out.Hits)

	case "article_meta":
		title, _ := input["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, fmt.Errorf("input.title required for article_meta mode")
		}
		out.Query = title
		params := url.Values{}
		params.Set("action", "query")
		params.Set("titles", title)
		params.Set("prop", "categories|info|revisions")
		params.Set("rvprop", "timestamp|user|comment|size")
		params.Set("rvlimit", "10")
		params.Set("clshow", "!hidden")
		params.Set("cllimit", "20")
		params.Set("format", "json")
		urlStr := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?%s", lang, params.Encode())
		body, err := wikiGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Query struct {
				Pages map[string]struct {
					Title       string `json:"title"`
					PageID      int    `json:"pageid"`
					LastRevID   int64  `json:"lastrevid"`
					Length      int    `json:"length"`
					Categories  []struct{ Title string `json:"title"` } `json:"categories"`
					Revisions   []struct {
						User      string `json:"user"`
						Timestamp string `json:"timestamp"`
						Comment   string `json:"comment"`
					} `json:"revisions"`
				} `json:"pages"`
			} `json:"query"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("wiki meta decode: %w", err)
		}
		for _, p := range raw.Query.Pages {
			titleEnc := strings.ReplaceAll(p.Title, " ", "_")
			meta := &WikiArticleMeta{
				Title:       p.Title,
				PageID:      p.PageID,
				LastRevID:   p.LastRevID,
				LengthBytes: p.Length,
				URL:         fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, url.PathEscape(titleEnc)),
			}
			for _, c := range p.Categories {
				meta.Categories = append(meta.Categories, c.Title)
			}
			for _, r := range p.Revisions {
				meta.RecentRevs = append(meta.RecentRevs, WikiRevision{
					User:      r.User,
					Timestamp: r.Timestamp,
				})
			}
			out.ArticleMeta = meta
			out.Returned = 1
			break
		}

	case "article_content":
		title, _ := input["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, fmt.Errorf("input.title required for article_content mode")
		}
		out.Query = title
		startOffset := 0
		if v, ok := input["start_offset"].(float64); ok && v > 0 {
			startOffset = int(v)
		}
		maxChars := 12000
		if v, ok := input["max_chars"].(float64); ok && v > 0 {
			maxChars = int(v)
		}
		if maxChars > 30000 {
			maxChars = 30000
		}
		if maxChars < 1000 {
			maxChars = 1000
		}
		params := url.Values{}
		params.Set("action", "query")
		params.Set("prop", "extracts")
		params.Set("explaintext", "true")
		params.Set("exsectionformat", "plain")
		params.Set("titles", title)
		params.Set("format", "json")
		urlStr := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?%s", lang, params.Encode())
		body, err := wikiGet(ctx, cli, urlStr)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Query struct {
				Pages map[string]struct {
					Title   string `json:"title"`
					Extract string `json:"extract"`
					Missing *string `json:"missing,omitempty"`
				} `json:"pages"`
			} `json:"query"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("wiki content decode: %w", err)
		}
		for _, p := range raw.Query.Pages {
			if p.Missing != nil {
				return nil, fmt.Errorf("wikipedia: page not found: %s", title)
			}
			full := p.Extract
			total := len(full)
			if startOffset > total {
				startOffset = total
			}
			end := startOffset + maxChars
			if end > total {
				end = total
			}
			chunk := full[startOffset:end]
			titleEnc := strings.ReplaceAll(p.Title, " ", "_")
			ac := &WikiArticleContent{
				Title:       p.Title,
				TotalChars:  total,
				StartOffset: startOffset,
				EndOffset:   end,
				Content:     chunk,
				URL:         fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, url.PathEscape(titleEnc)),
			}
			if end < total {
				next := end
				ac.NextOffset = &next
			}
			out.ArticleContent = ac
			out.Returned = 1
			break
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: summary, search, article_meta, article_content", mode)
	}

	out.HighlightFindings = buildWikiArticleHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// wikiStripHTML strips minimal HTML tags from search snippets
func wikiStripHTML(s string) string {
	s = strings.ReplaceAll(s, `<span class="searchmatch">`, "**")
	s = strings.ReplaceAll(s, `</span>`, "**")
	return s
}

func wikiGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0 (https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("wikipedia: page not found (404)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wikipedia HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildWikiArticleHighlights(o *WikipediaSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "summary":
		if o.Summary == nil {
			break
		}
		s := o.Summary
		hi = append(hi, fmt.Sprintf("✓ %s — %s", s.Title, s.Description))
		if s.Extract != "" {
			hi = append(hi, "  extract: "+hfTruncate(s.Extract, 300))
		}
		if s.WikidataID != "" {
			hi = append(hi, "  wikidata: "+s.WikidataID)
		}
		if s.Timestamp != "" {
			hi = append(hi, "  last edit: "+s.Timestamp)
		}
		if s.ContentURL != "" {
			hi = append(hi, "  url: "+s.ContentURL)
		}

	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d articles match '%s' (returning %d)", o.TotalCount, o.Query, o.Returned))
		for i, h := range o.Hits {
			if i >= 6 {
				break
			}
			ts := h.Timestamp
			if len(ts) > 10 {
				ts = ts[:10]
			}
			hi = append(hi, fmt.Sprintf("  • %s [%d words, edited %s]", h.Title, h.WordCount, ts))
			if h.Snippet != "" {
				hi = append(hi, "    "+hfTruncate(h.Snippet, 150))
			}
		}

	case "article_meta":
		if o.ArticleMeta == nil {
			hi = append(hi, "✗ no article")
			break
		}
		m := o.ArticleMeta
		hi = append(hi, fmt.Sprintf("✓ %s — %d bytes · revision %d", m.Title, m.LengthBytes, m.LastRevID))
		if len(m.Categories) > 0 {
			cats := m.Categories
			if len(cats) > 6 {
				cats = cats[:6]
			}
			hi = append(hi, "  categories: "+strings.Join(cats, " · "))
		}
		if len(m.RecentRevs) > 0 {
			hi = append(hi, fmt.Sprintf("  recent edits (top %d):", len(m.RecentRevs)))
			for i, r := range m.RecentRevs {
				if i >= 5 {
					break
				}
				ts := r.Timestamp
				if len(ts) > 16 {
					ts = ts[:16]
				}
				hi = append(hi, fmt.Sprintf("    [%s] %s", ts, r.User))
			}
		}

	case "article_content":
		if o.ArticleContent == nil {
			hi = append(hi, "✗ no article content")
			break
		}
		ac := o.ArticleContent
		hi = append(hi, fmt.Sprintf("✓ %s — %d total chars, returning [%d:%d]", ac.Title, ac.TotalChars, ac.StartOffset, ac.EndOffset))
		if ac.NextOffset != nil {
			hi = append(hi, fmt.Sprintf("  more available — call again with start_offset=%d", *ac.NextOffset))
		}
		if ac.Content != "" {
			hi = append(hi, "  preview: "+hfTruncate(ac.Content, 200))
		}
	}
	return hi
}
