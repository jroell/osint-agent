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

type RedditPost struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Author        string  `json:"author"`
	Subreddit     string  `json:"subreddit"`
	Score         int     `json:"score"`
	UpvoteRatio   float64 `json:"upvote_ratio,omitempty"`
	NumComments   int     `json:"num_comments"`
	URL           string  `json:"url"`
	PermaLink     string  `json:"permalink"`
	CreatedUTC    float64 `json:"created_utc"`
	CreatedISO    string  `json:"created_iso,omitempty"`
	IsSelf        bool    `json:"is_self"`
	SelfTextLen   int     `json:"self_text_length,omitempty"`
	SelfTextSnip  string  `json:"self_text_snippet,omitempty"`
	OverEighteen  bool    `json:"over_18,omitempty"`
	Stickied      bool    `json:"stickied,omitempty"`
	IsVideo       bool    `json:"is_video,omitempty"`
	Domain        string  `json:"domain,omitempty"`
}

type RedditSubredditAgg struct {
	Subreddit       string `json:"subreddit"`
	MentionCount    int    `json:"mention_count"`
	TotalScore      int    `json:"total_score"`
	TotalComments   int    `json:"total_comments"`
	UniqueAuthors   int    `json:"unique_authors_in_sample"`
}

type RedditAuthorAgg struct {
	Author          string   `json:"author"`
	PostCount       int      `json:"post_count"`
	TotalScore      int      `json:"total_score"`
	TotalComments   int      `json:"total_comments"`
	Subreddits      []string `json:"subreddits_posted_to"`
}

type RedditOrgIntelOutput struct {
	Query             string               `json:"query"`
	TimeRange         string               `json:"time_range"`
	TotalPosts        int                  `json:"total_posts_returned"`
	TopPosts          []RedditPost         `json:"top_posts_by_engagement"`
	RecentPosts       []RedditPost         `json:"recent_posts"`
	TopSubreddits     []RedditSubredditAgg `json:"top_subreddits_discussing"`
	TopAuthors        []RedditAuthorAgg    `json:"top_authors_mentioning"`
	UniqueSubredditCount int               `json:"unique_subreddits"`
	UniqueAuthorCount    int               `json:"unique_authors"`
	TotalEngagement   int                  `json:"total_engagement_score"`
	HighlightFindings []string             `json:"highlight_findings"`
	Source            string               `json:"source"`
	TookMs            int64                `json:"tookMs"`
	Note              string               `json:"note,omitempty"`
}

// RedditOrgIntel queries Reddit's free public JSON API for posts matching a
// keyword/brand across all of Reddit. Aggregates by subreddit and author to
// reveal:
//   - Top subreddits where the brand is discussed (community pulse)
//   - Top authors mentioning it (potential employees, advocates, critics)
//   - Sentiment indicators (upvote ratio, score, comment density)
//   - Recent vs. all-time engagement patterns
//
// Use cases:
//   - Brand monitoring: where is X being discussed and how does sentiment trend?
//   - Threat actor research: subreddits where exploits/leaks are shared
//   - Recruiting intel: identify SMEs in niche topics
//   - Competitive intel: comparison threads, feature requests, pain points
//
// Free, no key. Reddit requires a meaningful User-Agent header — we set one.
// Falls back gracefully on rate limits.
func RedditOrgIntel(ctx context.Context, input map[string]any) (*RedditOrgIntelOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (brand name, keyword, or phrase)")
	}
	timeRange, _ := input["time_range"].(string)
	if timeRange == "" {
		timeRange = "year" // hour | day | week | month | year | all
	}
	limit := 100
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 500 {
		limit = int(v)
	}

	start := time.Now()
	out := &RedditOrgIntelOutput{Query: q, TimeRange: timeRange, Source: "reddit.com"}

	// Fetch up to 500 results across multiple sort orders to capture both
	// recent buzz AND historically-popular content. We do this in 2 calls:
	// sort=new (recency) + sort=top (engagement).
	endpoints := []string{
		fmt.Sprintf("https://www.reddit.com/search.json?q=%s&sort=top&t=%s&limit=%d",
			url.QueryEscape(q), timeRange, minInt(limit, 100)),
		fmt.Sprintf("https://www.reddit.com/search.json?q=%s&sort=new&t=%s&limit=%d",
			url.QueryEscape(q), timeRange, minInt(limit, 100)),
	}

	allPosts := map[string]RedditPost{} // dedupe by post ID

	for _, ep := range endpoints {
		posts, err := redditFetchSearch(ctx, ep)
		if err != nil {
			out.Note = fmt.Sprintf("partial — one fetch failed: %v", err)
			continue
		}
		for _, p := range posts {
			if _, exists := allPosts[p.ID]; !exists {
				allPosts[p.ID] = p
			}
		}
	}

	// Materialize unique posts
	posts := make([]RedditPost, 0, len(allPosts))
	for _, p := range allPosts {
		posts = append(posts, p)
	}

	// Aggregate by subreddit
	bySubreddit := map[string]*RedditSubredditAgg{}
	authorsBySub := map[string]map[string]bool{}
	for _, p := range posts {
		agg, ok := bySubreddit[p.Subreddit]
		if !ok {
			agg = &RedditSubredditAgg{Subreddit: p.Subreddit}
			bySubreddit[p.Subreddit] = agg
			authorsBySub[p.Subreddit] = map[string]bool{}
		}
		agg.MentionCount++
		agg.TotalScore += p.Score
		agg.TotalComments += p.NumComments
		authorsBySub[p.Subreddit][p.Author] = true
	}
	for sub, agg := range bySubreddit {
		agg.UniqueAuthors = len(authorsBySub[sub])
	}
	for _, agg := range bySubreddit {
		out.TopSubreddits = append(out.TopSubreddits, *agg)
	}
	sort.Slice(out.TopSubreddits, func(i, j int) bool {
		return out.TopSubreddits[i].MentionCount > out.TopSubreddits[j].MentionCount
	})
	if len(out.TopSubreddits) > 20 {
		out.TopSubreddits = out.TopSubreddits[:20]
	}

	// Aggregate by author
	byAuthor := map[string]*RedditAuthorAgg{}
	subsByAuthor := map[string]map[string]bool{}
	for _, p := range posts {
		agg, ok := byAuthor[p.Author]
		if !ok {
			agg = &RedditAuthorAgg{Author: p.Author}
			byAuthor[p.Author] = agg
			subsByAuthor[p.Author] = map[string]bool{}
		}
		agg.PostCount++
		agg.TotalScore += p.Score
		agg.TotalComments += p.NumComments
		subsByAuthor[p.Author][p.Subreddit] = true
	}
	for a, agg := range byAuthor {
		for sub := range subsByAuthor[a] {
			agg.Subreddits = append(agg.Subreddits, sub)
		}
		sort.Strings(agg.Subreddits)
		out.TopAuthors = append(out.TopAuthors, *agg)
	}
	sort.Slice(out.TopAuthors, func(i, j int) bool {
		// Sort by post count primarily, then by total score
		if out.TopAuthors[i].PostCount != out.TopAuthors[j].PostCount {
			return out.TopAuthors[i].PostCount > out.TopAuthors[j].PostCount
		}
		return out.TopAuthors[i].TotalScore > out.TopAuthors[j].TotalScore
	})
	// Filter out [deleted]/AutoModerator
	filtered := []RedditAuthorAgg{}
	for _, a := range out.TopAuthors {
		if a.Author != "[deleted]" && a.Author != "AutoModerator" {
			filtered = append(filtered, a)
		}
	}
	out.TopAuthors = filtered
	if len(out.TopAuthors) > 25 {
		out.TopAuthors = out.TopAuthors[:25]
	}

	// Top 15 posts by engagement (score + comments*2)
	scored := append([]RedditPost{}, posts...)
	sort.Slice(scored, func(i, j int) bool {
		ei := scored[i].Score + scored[i].NumComments*2
		ej := scored[j].Score + scored[j].NumComments*2
		return ei > ej
	})
	if len(scored) > 15 {
		out.TopPosts = scored[:15]
	} else {
		out.TopPosts = scored
	}

	// Most recent 10 posts
	byTime := append([]RedditPost{}, posts...)
	sort.Slice(byTime, func(i, j int) bool {
		return byTime[i].CreatedUTC > byTime[j].CreatedUTC
	})
	if len(byTime) > 10 {
		out.RecentPosts = byTime[:10]
	} else {
		out.RecentPosts = byTime
	}

	out.TotalPosts = len(posts)
	out.UniqueSubredditCount = len(bySubreddit)
	out.UniqueAuthorCount = len(byAuthor)
	for _, p := range posts {
		out.TotalEngagement += p.Score + p.NumComments*2
	}

	// Highlights
	highlights := []string{}
	if out.TotalPosts > 0 {
		highlights = append(highlights, fmt.Sprintf("%d posts across %d subreddits by %d unique authors (%dh window)",
			out.TotalPosts, out.UniqueSubredditCount, out.UniqueAuthorCount, redditTimeRangeToHours(timeRange)))
	}
	if len(out.TopSubreddits) > 0 {
		topSubs := []string{}
		for _, s := range out.TopSubreddits[:minInt(3, len(out.TopSubreddits))] {
			topSubs = append(topSubs, fmt.Sprintf("r/%s(%d)", s.Subreddit, s.MentionCount))
		}
		highlights = append(highlights, "top discussing communities: "+strings.Join(topSubs, ", "))
	}
	if len(out.TopAuthors) > 0 {
		highlights = append(highlights, fmt.Sprintf("most-active author: u/%s (%d posts)", out.TopAuthors[0].Author, out.TopAuthors[0].PostCount))
	}
	if len(out.TopPosts) > 0 {
		top := out.TopPosts[0]
		highlights = append(highlights, fmt.Sprintf("top engagement: %d⬆ %d💬 — '%s' on r/%s",
			top.Score, top.NumComments, truncate(top.Title, 80), top.Subreddit))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func redditTimeRangeToHours(t string) int {
	switch t {
	case "hour":
		return 1
	case "day":
		return 24
	case "week":
		return 168
	case "month":
		return 720
	case "year":
		return 8760
	default:
		return 0
	}
}

func redditFetchSearch(ctx context.Context, endpoint string) ([]RedditPost, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	// Reddit requires meaningful User-Agent
	req.Header.Set("User-Agent", "osint-agent/1.0 (by /u/osint-agent)")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("reddit rate-limited (429)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("reddit status %d", resp.StatusCode)
	}
	var parsed struct {
		Data struct {
			Children []struct {
				Data struct {
					ID            string  `json:"id"`
					Title         string  `json:"title"`
					Author        string  `json:"author"`
					Subreddit     string  `json:"subreddit"`
					Score         int     `json:"score"`
					UpvoteRatio   float64 `json:"upvote_ratio"`
					NumComments   int     `json:"num_comments"`
					URL           string  `json:"url"`
					Permalink     string  `json:"permalink"`
					CreatedUTC    float64 `json:"created_utc"`
					IsSelf        bool    `json:"is_self"`
					SelfText      string  `json:"selftext"`
					Over18        bool    `json:"over_18"`
					Stickied      bool    `json:"stickied"`
					IsVideo       bool    `json:"is_video"`
					Domain        string  `json:"domain"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("reddit parse: %w", err)
	}
	out := []RedditPost{}
	for _, c := range parsed.Data.Children {
		d := c.Data
		snip := ""
		if d.SelfText != "" && len(d.SelfText) > 0 {
			snip = truncate(d.SelfText, 200)
		}
		out = append(out, RedditPost{
			ID: d.ID, Title: d.Title, Author: d.Author, Subreddit: d.Subreddit,
			Score: d.Score, UpvoteRatio: d.UpvoteRatio, NumComments: d.NumComments,
			URL: d.URL, PermaLink: "https://www.reddit.com" + d.Permalink,
			CreatedUTC: d.CreatedUTC, CreatedISO: time.Unix(int64(d.CreatedUTC), 0).UTC().Format(time.RFC3339),
			IsSelf: d.IsSelf, SelfTextLen: len(d.SelfText), SelfTextSnip: snip,
			OverEighteen: d.Over18, Stickied: d.Stickied, IsVideo: d.IsVideo, Domain: d.Domain,
		})
	}
	return out, nil
}
