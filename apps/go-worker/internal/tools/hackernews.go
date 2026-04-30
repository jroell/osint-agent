package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type HNUserOutput struct {
	ID        string   `json:"id"`
	Created   int64    `json:"created"`
	CreatedAt string   `json:"created_at_iso"`
	Karma     int      `json:"karma"`
	About     string   `json:"about,omitempty"`
	Submitted int      `json:"submitted_count"`
	Recent    []HNItem `json:"recent_items,omitempty"`
	URL       string   `json:"profile_url"`
	TookMs    int64    `json:"tookMs"`
	Source    string   `json:"source"`
}

type HNItem struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url,omitempty"`
	Score    int    `json:"score,omitempty"`
	Time     int64  `json:"time,omitempty"`
	Comments int    `json:"descendants,omitempty"`
}

// HackerNewsUser pulls the public profile for a Hacker News username.
// Free, no key. The API is Firebase-backed and extremely stable.
func HackerNewsUser(ctx context.Context, input map[string]any) (*HNUserOutput, error) {
	id, _ := input["username"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("input.username required (HN username)")
	}
	includeRecent := true
	if v, ok := input["include_recent"].(bool); ok {
		includeRecent = v
	}
	recentN := 5
	if v, ok := input["recent_n"].(float64); ok && v > 0 {
		recentN = int(v)
	}

	start := time.Now()
	body, err := httpGetJSON(ctx, "https://hacker-news.firebaseio.com/v0/user/"+id+".json", 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("hn user fetch: %w", err)
	}
	if string(body) == "null" {
		return nil, fmt.Errorf("HN user %q not found", id)
	}
	var u struct {
		ID        string `json:"id"`
		Created   int64  `json:"created"`
		Karma     int    `json:"karma"`
		About     string `json:"about"`
		Submitted []int  `json:"submitted"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("hn user parse: %w", err)
	}
	out := &HNUserOutput{
		ID:        u.ID,
		Created:   u.Created,
		CreatedAt: time.Unix(u.Created, 0).UTC().Format(time.RFC3339),
		Karma:     u.Karma,
		About:     stripHTML(u.About, 600),
		Submitted: len(u.Submitted),
		URL:       "https://news.ycombinator.com/user?id=" + u.ID,
		Source:    "hacker-news.firebaseio.com",
	}

	if includeRecent && len(u.Submitted) > 0 {
		// Fetch first N submitted items in parallel.
		ids := u.Submitted
		if len(ids) > recentN {
			ids = ids[:recentN]
		}
		ch := make(chan HNItem, len(ids))
		for _, itemID := range ids {
			go func(itemID int) {
				ch <- fetchHNItem(ctx, itemID)
			}(itemID)
		}
		for range ids {
			it := <-ch
			if it.ID != 0 {
				out.Recent = append(out.Recent, it)
			}
		}
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func fetchHNItem(ctx context.Context, id int) HNItem {
	body, err := httpGetJSON(ctx, fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id), 5*time.Second)
	if err != nil {
		return HNItem{}
	}
	var it HNItem
	_ = json.Unmarshal(body, &it)
	return it
}

func stripHTML(s string, max int) string {
	// Very minimal — just strip tags. HN's "about" uses <p>/<a>/<i>/<b>.
	out := make([]rune, 0, len(s))
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			out = append(out, r)
		}
	}
	s = strings.TrimSpace(string(out))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
