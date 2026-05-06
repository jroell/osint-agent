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

// NDLJapanSearch wraps the National Diet Library (Japan) Digital
// Collections OpenSearch API. Free, no key.
//
// NDL is the Japanese national library. Its digital collections cover
// ~7.4M items including Meiji-era books, woodblock prints, postwar
// periodicals, manuscripts, and items only digitized within Japan.
// Critical for Japan-specific historical chains, including questions
// where the source-of-record is in Japanese.
//
// Modes:
//   - "search" : OpenSearch query against Digital Collections (any/title/creator)
//
// Knowledge-graph: emits typed entities (kind: "book" | "image" |
// "audio" | "library_item") with stable NDL handle URLs.

type NDLItem struct {
	NDLID   string `json:"ndl_id"`
	Title   string `json:"title"`
	Creator string `json:"creator,omitempty"`
	Date    string `json:"date,omitempty"`
	Type    string `json:"type,omitempty"`
	Subject string `json:"subject,omitempty"`
	URL     string `json:"ndl_url"`
}

type NDLEntity struct {
	Kind        string         `json:"kind"`
	NDLID       string         `json:"ndl_id"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type NDLJapanSearchOutput struct {
	Mode              string      `json:"mode"`
	Query             string      `json:"query"`
	Returned          int         `json:"returned"`
	Total             int         `json:"total,omitempty"`
	Items             []NDLItem   `json:"items,omitempty"`
	Entities          []NDLEntity `json:"entities"`
	HighlightFindings []string    `json:"highlight_findings"`
	Source            string      `json:"source"`
	TookMs            int64       `json:"tookMs"`
}

type ndlOpensearchRSS struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		TotalResults int `xml:"http://a9.com/-/spec/opensearch/1.1/ totalResults"`
		Items        []struct {
			Title       string   `xml:"title"`
			Link        string   `xml:"link"`
			Description string   `xml:"description"`
			Creator     string   `xml:"http://purl.org/dc/elements/1.1/ creator"`
			Date        string   `xml:"http://purl.org/dc/elements/1.1/ date"`
			Type        string   `xml:"http://purl.org/dc/elements/1.1/ type"`
			Subject     []string `xml:"http://purl.org/dc/elements/1.1/ subject"`
			GUID        string   `xml:"guid"`
		} `xml:"item"`
	} `xml:"channel"`
}

func NDLJapanSearch(ctx context.Context, input map[string]any) (*NDLJapanSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "search"
	}
	out := &NDLJapanSearchOutput{Mode: mode, Source: "ndlsearch.ndl.go.jp"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		params := url.Values{}
		params.Set("any", q)
		params.Set("cnt", "20")
		params.Set("format", "rss")
		// Public NDL OpenSearch endpoint
		u := "https://ndlsearch.ndl.go.jp/api/opensearch?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/rss+xml")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ndl: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("ndl HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var rss ndlOpensearchRSS
		if err := xml.Unmarshal(body, &rss); err != nil {
			return nil, fmt.Errorf("ndl XML decode: %w", err)
		}
		out.Total = rss.Channel.TotalResults
		for _, it := range rss.Channel.Items {
			subj := ""
			if len(it.Subject) > 0 {
				subj = strings.Join(it.Subject, "; ")
			}
			ndlID := it.GUID
			if ndlID == "" {
				ndlID = it.Link
			}
			out.Items = append(out.Items, NDLItem{
				NDLID:   ndlID,
				Title:   strings.TrimSpace(it.Title),
				Creator: strings.TrimSpace(it.Creator),
				Date:    strings.TrimSpace(it.Date),
				Type:    strings.TrimSpace(it.Type),
				Subject: subj,
				URL:     it.Link,
			})
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Items)
	out.Entities = ndlBuildEntities(out)
	out.HighlightFindings = ndlBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func ndlBuildEntities(o *NDLJapanSearchOutput) []NDLEntity {
	ents := []NDLEntity{}
	for _, it := range o.Items {
		kind := "library_item"
		t := strings.ToLower(it.Type)
		switch {
		case strings.Contains(t, "book"), strings.Contains(t, "図書"):
			kind = "book"
		case strings.Contains(t, "image"), strings.Contains(t, "画像"):
			kind = "image"
		case strings.Contains(t, "audio"), strings.Contains(t, "録音"):
			kind = "audio"
		case strings.Contains(t, "newspaper"), strings.Contains(t, "新聞"):
			kind = "newspaper"
		}
		ents = append(ents, NDLEntity{
			Kind: kind, NDLID: it.NDLID, Title: it.Title, URL: it.URL, Date: it.Date,
			Description: it.Subject,
			Attributes:  map[string]any{"creator": it.Creator, "type": it.Type, "subject": it.Subject},
		})
	}
	return ents
}

func ndlBuildHighlights(o *NDLJapanSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ ndl %s: %d items (total %d)", o.Mode, o.Returned, o.Total)}
	for i, it := range o.Items {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s — %s (%s) %s", it.Title, it.Creator, it.Date, it.URL))
	}
	return hi
}
