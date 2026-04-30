package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type HNHit struct {
	ObjectID    string  `json:"object_id"`
	Type        string  `json:"type"` // story | comment | poll | job
	Title       string  `json:"title,omitempty"`
	URL         string  `json:"url,omitempty"`
	StoryURL    string  `json:"story_url,omitempty"` // for comments, points to parent story
	StoryTitle  string  `json:"story_title,omitempty"`
	Author      string  `json:"author"`
	Points      int     `json:"points"`
	NumComments int     `json:"num_comments,omitempty"`
	CreatedAt   string  `json:"created_at"`
	StoryID     int     `json:"story_id,omitempty"`
	Snippet     string  `json:"text_snippet,omitempty"`
	HNLink      string  `json:"hn_link"`
}

type HNAuthorAgg struct {
	Author      string `json:"author"`
	PostCount   int    `json:"post_count"`
	TotalPoints int    `json:"total_points"`
}

type HackerNewsSearchOutput struct {
	Query           string        `json:"query"`
	TotalHits       int           `json:"total_hits"`
	ProcessingTimeMs int          `json:"processing_time_ms"`
	Returned        int           `json:"returned"`
	TopByPoints     []HNHit       `json:"top_by_points"`
	MostRecent      []HNHit       `json:"most_recent"`
	TopAuthors      []HNAuthorAgg `json:"top_authors"`
	UniqueAuthors   int           `json:"unique_authors"`
	StoriesCount    int           `json:"story_count"`
	CommentsCount   int           `json:"comment_count"`
	HighlightFindings []string    `json:"highlight_findings"`
	Source          string        `json:"source"`
	TookMs          int64         `json:"tookMs"`
	Note            string        `json:"note,omitempty"`
}

// HackerNewsSearch queries HN's Algolia-powered public search for posts +
// comments matching a query. Returns:
//   - Top stories by points (peak engagement)
//   - Most recent posts (current chatter)
//   - Top authors mentioning the term (potential SMEs, advocates, critics)
//   - Story vs comment counts (depth of discussion)
//
// HN is uniquely valuable for tech-OSINT because:
//   - Tech-heavy audience (engineers, founders, investors)
//   - Comments often reveal insider information not in the post
//   - Long-tail discussions surface regularly when topics resurface
//
// Free Algolia API, no auth, very fast. Returns up to 1000 hits per page.
func HackerNewsSearch(ctx context.Context, input map[string]any) (*HackerNewsSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 1000 {
		limit = int(v)
	}
	tags, _ := input["tags"].(string)
	if tags == "" {
		tags = "(story,comment)"
	}
	mode, _ := input["mode"].(string)
	if mode == "" {
		mode = "search" // search = relevance; search_by_date = recency
	}

	start := time.Now()
	endpoint := fmt.Sprintf("https://hn.algolia.com/api/v1/%s?query=%s&hitsPerPage=%d&tags=%s",
		mode, url.QueryEscape(q), limit, url.QueryEscape(tags))

	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/hn-search")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("algolia fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("algolia status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Hits []struct {
			ObjectID     string   `json:"objectID"`
			Title        string   `json:"title"`
			URL          string   `json:"url"`
			StoryURL     string   `json:"story_url"`
			StoryTitle   string   `json:"story_title"`
			Author       string   `json:"author"`
			Points       int      `json:"points"`
			NumComments  int      `json:"num_comments"`
			CreatedAt    string   `json:"created_at"`
			StoryID      int      `json:"story_id"`
			Tags         []string `json:"_tags"`
			StoryText    string   `json:"story_text"`
			CommentText  string   `json:"comment_text"`
		} `json:"hits"`
		NbHits           int `json:"nbHits"`
		Page             int `json:"page"`
		ProcessingTimeMs int `json:"processingTimeMS"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("algolia parse: %w", err)
	}

	out := &HackerNewsSearchOutput{
		Query: q, TotalHits: parsed.NbHits,
		ProcessingTimeMs: parsed.ProcessingTimeMs,
		Source: "hn.algolia.com",
	}
	authorAgg := map[string]*HNAuthorAgg{}
	allHits := []HNHit{}

	for _, h := range parsed.Hits {
		hitType := "story"
		for _, t := range h.Tags {
			if t == "comment" {
				hitType = "comment"
				out.CommentsCount++
			} else if t == "story" {
				out.StoriesCount++
			} else if t == "poll" {
				hitType = "poll"
			} else if t == "job" {
				hitType = "job"
			}
		}
		snippet := ""
		if hitType == "comment" && h.CommentText != "" {
			snippet = truncate(stripHTMLBare(h.CommentText), 200)
		} else if h.StoryText != "" {
			snippet = truncate(stripHTMLBare(h.StoryText), 200)
		}
		hnLink := "https://news.ycombinator.com/item?id=" + h.ObjectID
		hit := HNHit{
			ObjectID: h.ObjectID, Type: hitType,
			Title: h.Title, URL: h.URL,
			StoryURL: h.StoryURL, StoryTitle: h.StoryTitle,
			Author: h.Author, Points: h.Points,
			NumComments: h.NumComments, CreatedAt: h.CreatedAt,
			StoryID: h.StoryID, Snippet: snippet, HNLink: hnLink,
		}
		allHits = append(allHits, hit)

		if h.Author != "" {
			a, ok := authorAgg[h.Author]
			if !ok {
				a = &HNAuthorAgg{Author: h.Author}
				authorAgg[h.Author] = a
			}
			a.PostCount++
			a.TotalPoints += h.Points
		}
	}
	out.Returned = len(allHits)
	out.UniqueAuthors = len(authorAgg)

	// Top by points
	sorted := append([]HNHit{}, allHits...)
	sort.Slice(sorted, func(i, j int) bool {
		ei := sorted[i].Points + sorted[i].NumComments*2
		ej := sorted[j].Points + sorted[j].NumComments*2
		return ei > ej
	})
	if len(sorted) > 15 {
		out.TopByPoints = sorted[:15]
	} else {
		out.TopByPoints = sorted
	}

	// Most recent
	byTime := append([]HNHit{}, allHits...)
	sort.Slice(byTime, func(i, j int) bool {
		return byTime[i].CreatedAt > byTime[j].CreatedAt
	})
	if len(byTime) > 10 {
		out.MostRecent = byTime[:10]
	} else {
		out.MostRecent = byTime
	}

	// Top authors
	for _, a := range authorAgg {
		out.TopAuthors = append(out.TopAuthors, *a)
	}
	sort.Slice(out.TopAuthors, func(i, j int) bool {
		if out.TopAuthors[i].PostCount != out.TopAuthors[j].PostCount {
			return out.TopAuthors[i].PostCount > out.TopAuthors[j].PostCount
		}
		return out.TopAuthors[i].TotalPoints > out.TopAuthors[j].TotalPoints
	})
	if len(out.TopAuthors) > 25 {
		out.TopAuthors = out.TopAuthors[:25]
	}

	// Highlights
	highlights := []string{}
	if out.TotalHits > 0 {
		highlights = append(highlights,
			fmt.Sprintf("%d total hits across HN (%d returned this page) — %d stories, %d comments, %d unique authors",
				out.TotalHits, out.Returned, out.StoriesCount, out.CommentsCount, out.UniqueAuthors))
	}
	if len(out.TopByPoints) > 0 {
		top := out.TopByPoints[0]
		highlights = append(highlights,
			fmt.Sprintf("top story: '%s' (%d pts, %d comments) by %s",
				truncate(top.Title, 80), top.Points, top.NumComments, top.Author))
	}
	if len(out.TopAuthors) > 0 {
		highlights = append(highlights,
			fmt.Sprintf("most-active author: %s (%d posts, %d total points)",
				out.TopAuthors[0].Author, out.TopAuthors[0].PostCount, out.TopAuthors[0].TotalPoints))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
