package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type RedditItem struct {
	Kind        string `json:"kind"`              // "t3" post, "t1" comment, "t2" user, "t5" sub
	ID          string `json:"id,omitempty"`
	Title       string `json:"title,omitempty"`   // posts only
	Author      string `json:"author,omitempty"`
	Subreddit   string `json:"subreddit,omitempty"`
	Score       int    `json:"score"`
	NumComments int    `json:"num_comments,omitempty"` // posts only
	Created     int64  `json:"created_utc,omitempty"`
	Permalink   string `json:"permalink,omitempty"`
	Body        string `json:"body,omitempty"`        // comments
	URL         string `json:"url,omitempty"`         // posts (link target)
	Selftext    string `json:"selftext,omitempty"`
}

type RedditOutput struct {
	Mode    string       `json:"mode"`
	Query   string       `json:"query"`
	Items   []RedditItem `json:"items"`
	Count   int          `json:"count"`
	TookMs  int64        `json:"tookMs"`
	Source  string       `json:"source"`
}

// RedditQuery hits Reddit's public JSON API. Free, no API key needed (reads
// only). Three modes:
//   user:<u>      — recent posts + comments by user
//   subreddit:<r> — recent hot posts in r/<sub>
//   search:<q>    — site-wide search for query string
// Limit caps results; default 25, max 100.
func RedditQuery(ctx context.Context, input map[string]any) (*RedditOutput, error) {
	mode, _ := input["mode"].(string)
	query, _ := input["query"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("input.query required")
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
		if limit > 100 {
			limit = 100
		}
	}

	var endpoint string
	switch mode {
	case "user":
		endpoint = fmt.Sprintf("https://www.reddit.com/user/%s/.json?limit=%d", url.PathEscape(query), limit)
	case "subreddit", "sub":
		mode = "subreddit"
		endpoint = fmt.Sprintf("https://www.reddit.com/r/%s/hot.json?limit=%d", url.PathEscape(query), limit)
	case "search", "":
		mode = "search"
		endpoint = fmt.Sprintf("https://www.reddit.com/search.json?q=%s&limit=%d", url.QueryEscape(query), limit)
	default:
		return nil, fmt.Errorf("input.mode must be one of: user | subreddit | search (got %q)", mode)
	}

	start := time.Now()
	body, err := httpGetJSON(ctx, endpoint, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("reddit fetch: %w", err)
	}
	var listing struct {
		Data struct {
			Children []struct {
				Kind string                 `json:"kind"`
				Data map[string]interface{} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("reddit parse: %w", err)
	}

	out := &RedditOutput{
		Mode:   mode,
		Query:  query,
		Source: "reddit.com",
		TookMs: time.Since(start).Milliseconds(),
	}
	for _, c := range listing.Data.Children {
		it := RedditItem{Kind: c.Kind}
		if v, ok := c.Data["id"].(string); ok {
			it.ID = v
		}
		if v, ok := c.Data["title"].(string); ok {
			it.Title = v
		}
		if v, ok := c.Data["author"].(string); ok {
			it.Author = v
		}
		if v, ok := c.Data["subreddit"].(string); ok {
			it.Subreddit = v
		}
		if v, ok := c.Data["score"].(float64); ok {
			it.Score = int(v)
		}
		if v, ok := c.Data["num_comments"].(float64); ok {
			it.NumComments = int(v)
		}
		if v, ok := c.Data["created_utc"].(float64); ok {
			it.Created = int64(v)
		}
		if v, ok := c.Data["permalink"].(string); ok {
			it.Permalink = "https://reddit.com" + v
		}
		if v, ok := c.Data["body"].(string); ok {
			it.Body = truncate(v, 600)
		}
		if v, ok := c.Data["url"].(string); ok {
			it.URL = v
		}
		if v, ok := c.Data["selftext"].(string); ok {
			it.Selftext = truncate(v, 800)
		}
		out.Items = append(out.Items, it)
	}
	out.Count = len(out.Items)
	return out, nil
}
