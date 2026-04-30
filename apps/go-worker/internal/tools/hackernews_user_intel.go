package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// HNStoryHit is one story by the user.
type HNStoryHit struct {
	ObjectID    string `json:"object_id"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Domain      string `json:"domain,omitempty"`
	Points      int    `json:"points"`
	NumComments int    `json:"num_comments"`
	CreatedISO  string `json:"created_iso"`
	CreatedTs   int64  `json:"created_ts,omitempty"`
	StoryURL    string `json:"hn_story_url"`
}

// HNCommentHit is one comment by the user.
type HNCommentHit struct {
	ObjectID    string `json:"object_id"`
	Body        string `json:"body"`
	StoryID     string `json:"story_id,omitempty"`
	StoryTitle  string `json:"story_title,omitempty"`
	CreatedISO  string `json:"created_iso"`
	CreatedTs   int64  `json:"created_ts,omitempty"`
	HNURL       string `json:"hn_url"`
}

// HNDomainAggregate counts the user's submitted-link domains.
type HNDomainAggregate struct {
	Domain     string `json:"domain"`
	Submitted  int    `json:"submitted"`
	TotalPoints int   `json:"total_points"`
}

// HNHourBucket counts activity per UTC hour.
type HNHourBucket struct {
	HourUTC int `json:"hour_utc"`
	Count   int `json:"count"`
}

// HNUserIntelOutput is the response.
type HNUserIntelOutput struct {
	Username           string             `json:"username"`
	CreatedISO         string             `json:"created_iso,omitempty"`
	AccountAgeYears    float64            `json:"account_age_years,omitempty"`
	Karma              int                `json:"karma,omitempty"`
	About              string             `json:"about,omitempty"`
	SubmittedTotal     int                `json:"submitted_total_count,omitempty"`
	StoriesIndexedTotal int               `json:"stories_indexed_total,omitempty"`
	CommentsIndexedTotal int              `json:"comments_indexed_total,omitempty"`
	TopStoriesByPoints []HNStoryHit       `json:"top_stories_by_points,omitempty"`
	RecentStories      []HNStoryHit       `json:"recent_stories,omitempty"`
	RecentComments     []HNCommentHit     `json:"recent_comments,omitempty"`
	TopDomains         []HNDomainAggregate `json:"top_submitted_domains,omitempty"`
	HourDistribution   []HNHourBucket     `json:"hour_distribution_utc,omitempty"`
	InferredTimezone   string             `json:"inferred_timezone,omitempty"`
	StoryToCommentRatio float64           `json:"story_to_comment_ratio,omitempty"`
	OldestActivityISO  string             `json:"oldest_activity_iso,omitempty"`
	NewestActivityISO  string             `json:"newest_activity_iso,omitempty"`
	BioEmails          []string           `json:"bio_emails,omitempty"`
	BioURLs            []string           `json:"bio_urls,omitempty"`
	ProfileURL         string             `json:"profile_url"`
	HighlightFindings  []string           `json:"highlight_findings"`
	Source             string             `json:"source"`
	TookMs             int64              `json:"tookMs"`
	Note               string             `json:"note,omitempty"`
}

type hnFirebaseUser struct {
	ID        string `json:"id"`
	Created   int64  `json:"created"`
	Karma     int    `json:"karma"`
	About     string `json:"about"`
	Submitted []int  `json:"submitted"`
}

type hnAlgoliaResp struct {
	NbHits int                   `json:"nbHits"`
	Hits   []hnAlgoliaHit        `json:"hits"`
}

type hnAlgoliaHit struct {
	ObjectID     string   `json:"objectID"`
	Title        string   `json:"title"`
	URL          string   `json:"url"`
	StoryURL     string   `json:"story_url"`
	StoryID      int      `json:"story_id"`
	StoryTitle   string   `json:"story_title"`
	Author       string   `json:"author"`
	Points       int      `json:"points"`
	NumComments  int      `json:"num_comments"`
	CreatedAt    string   `json:"created_at"`
	CreatedTs    int64    `json:"created_at_i"`
	CommentText  string   `json:"comment_text"`
	Tags         []string `json:"_tags"`
}

// HackerNewsUserIntel performs a deep dive on a HN username via Firebase
// (profile) + Algolia (full-text indexed stories + comments). Free, no auth.
//
// Mirrors reddit_user_intel for HN. Aggregations:
//   - Top stories by points (their highest-engagement contributions)
//   - Recent stories + comments
//   - Top submitted domains (interest graph — what blogs/news sites they share)
//   - Posting hour distribution → inferred timezone
//   - Story-to-comment ratio (curator vs commenter style)
//   - Account age in years
//   - Email + URL extraction from `about` field
//
// Why this matters for ER:
//   - HN is THE central tech-identity hub for senior engineers, founders,
//     VCs, AI researchers, security pros. Per-user deep dive is missing
//     from the catalog (only basic profile lookup exists).
//   - Top-submitted domains reveal interest graph: someone submitting heavily
//     from `arxiv.org` is academic; from `news.ycombinator.com/item` is a
//     long-time community member; from blog domains is curating opinion.
//   - About fields commonly contain employer + email + GitHub link + Twitter
//     handle (high-value cross-platform pivot).
func HackerNewsUserIntel(ctx context.Context, input map[string]any) (*HNUserIntelOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	if username == "" {
		return nil, fmt.Errorf("input.username required")
	}

	storyLimit := 30
	if v, ok := input["story_limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		storyLimit = int(v)
	}
	commentLimit := 30
	if v, ok := input["comment_limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		commentLimit = int(v)
	}

	out := &HNUserIntelOutput{
		Username:   username,
		Source:     "hacker-news.firebaseio.com (profile) + hn.algolia.com (search)",
		ProfileURL: "https://news.ycombinator.com/user?id=" + username,
	}
	start := time.Now()
	client := &http.Client{Timeout: 25 * time.Second}

	// 1. Profile via Firebase
	prof, err := hnFetchProfile(ctx, client, username)
	if err != nil {
		return nil, err
	}
	if prof == nil {
		out.Note = fmt.Sprintf("HN user '%s' not found", username)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	out.Karma = prof.Karma
	out.About = stripBasicHTML(prof.About)
	out.SubmittedTotal = len(prof.Submitted)
	if prof.Created > 0 {
		t := time.Unix(prof.Created, 0).UTC()
		out.CreatedISO = t.Format(time.RFC3339)
		out.AccountAgeYears = time.Since(t).Hours() / (24 * 365.25)
	}

	// Mine emails + URLs from about
	out.BioEmails = uniqueStrings(emailRegex.FindAllString(prof.About, -1))
	for i, e := range out.BioEmails {
		out.BioEmails[i] = strings.ToLower(e)
	}
	out.BioURLs = uniqueStrings(lichessExtractURLs(prof.About))

	// 2. Top stories by points (Algolia)
	topStories, totalStories, err := hnAlgoliaSearch(ctx, client, username, "story", "popularity", storyLimit)
	if err == nil {
		out.TopStoriesByPoints = materializeHNStories(topStories)
		out.StoriesIndexedTotal = totalStories
	}
	// 3. Recent stories (date-sorted)
	recentStories, _, err := hnAlgoliaSearch(ctx, client, username, "story", "date", storyLimit/2)
	if err == nil {
		out.RecentStories = materializeHNStories(recentStories)
	}
	// 4. Recent comments
	recentComments, totalComments, err := hnAlgoliaSearch(ctx, client, username, "comment", "date", commentLimit)
	if err == nil {
		out.RecentComments = materializeHNComments(recentComments)
		out.CommentsIndexedTotal = totalComments
	}

	// Aggregations from combined story+comment pool
	domainAgg := map[string]*HNDomainAggregate{}
	hourCount := [24]int{}
	var oldestTs, newestTs int64
	for _, s := range out.TopStoriesByPoints {
		if s.Domain != "" {
			ag, ok := domainAgg[s.Domain]
			if !ok {
				ag = &HNDomainAggregate{Domain: s.Domain}
				domainAgg[s.Domain] = ag
			}
			ag.Submitted++
			ag.TotalPoints += s.Points
		}
		if s.CreatedTs > 0 {
			h := time.Unix(s.CreatedTs, 0).UTC().Hour()
			hourCount[h]++
			if oldestTs == 0 || s.CreatedTs < oldestTs {
				oldestTs = s.CreatedTs
			}
			if s.CreatedTs > newestTs {
				newestTs = s.CreatedTs
			}
		}
	}
	for _, s := range out.RecentStories {
		if s.Domain != "" {
			ag, ok := domainAgg[s.Domain]
			if !ok {
				ag = &HNDomainAggregate{Domain: s.Domain}
				domainAgg[s.Domain] = ag
			}
			ag.Submitted++
			ag.TotalPoints += s.Points
		}
		if s.CreatedTs > 0 {
			h := time.Unix(s.CreatedTs, 0).UTC().Hour()
			hourCount[h]++
		}
	}
	for _, c := range out.RecentComments {
		if c.CreatedTs > 0 {
			h := time.Unix(c.CreatedTs, 0).UTC().Hour()
			hourCount[h]++
			if oldestTs == 0 || c.CreatedTs < oldestTs {
				oldestTs = c.CreatedTs
			}
			if c.CreatedTs > newestTs {
				newestTs = c.CreatedTs
			}
		}
	}

	for _, ag := range domainAgg {
		out.TopDomains = append(out.TopDomains, *ag)
	}
	sort.SliceStable(out.TopDomains, func(i, j int) bool {
		if out.TopDomains[i].Submitted != out.TopDomains[j].Submitted {
			return out.TopDomains[i].Submitted > out.TopDomains[j].Submitted
		}
		return out.TopDomains[i].TotalPoints > out.TopDomains[j].TotalPoints
	})
	if len(out.TopDomains) > 15 {
		out.TopDomains = out.TopDomains[:15]
	}

	for h, c := range hourCount {
		if c > 0 {
			out.HourDistribution = append(out.HourDistribution, HNHourBucket{HourUTC: h, Count: c})
		}
	}
	sort.SliceStable(out.HourDistribution, func(i, j int) bool { return out.HourDistribution[i].HourUTC < out.HourDistribution[j].HourUTC })
	out.InferredTimezone = inferTimezoneFromHours(hourCount[:])

	if oldestTs > 0 {
		out.OldestActivityISO = time.Unix(oldestTs, 0).UTC().Format(time.RFC3339)
	}
	if newestTs > 0 {
		out.NewestActivityISO = time.Unix(newestTs, 0).UTC().Format(time.RFC3339)
	}

	if out.CommentsIndexedTotal > 0 {
		out.StoryToCommentRatio = float64(out.StoriesIndexedTotal) / float64(out.CommentsIndexedTotal)
	}

	out.HighlightFindings = buildHNHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func hnFetchProfile(ctx context.Context, client *http.Client, username string) (*hnFirebaseUser, error) {
	endpoint := "https://hacker-news.firebaseio.com/v0/user/" + url.PathEscape(username) + ".json"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hn firebase: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("hn %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4_000_000))
	if len(body) == 0 || string(body) == "null" {
		return nil, nil
	}
	var raw hnFirebaseUser
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if raw.ID == "" {
		return nil, nil
	}
	return &raw, nil
}

func hnAlgoliaSearch(ctx context.Context, client *http.Client, username, kind, sortMode string, limit int) ([]hnAlgoliaHit, int, error) {
	endpoint := "https://hn.algolia.com/api/v1/search"
	if sortMode == "date" {
		endpoint = "https://hn.algolia.com/api/v1/search_by_date"
	}
	params := url.Values{}
	params.Set("tags", fmt.Sprintf("author_%s,%s", username, kind))
	params.Set("hitsPerPage", fmt.Sprintf("%d", limit))
	endpoint += "?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("hn algolia: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("hn algolia %d", resp.StatusCode)
	}
	var raw hnAlgoliaResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	return raw.Hits, raw.NbHits, nil
}

func materializeHNStories(hits []hnAlgoliaHit) []HNStoryHit {
	out := []HNStoryHit{}
	for _, h := range hits {
		s := HNStoryHit{
			ObjectID:    h.ObjectID,
			Title:       h.Title,
			URL:         h.URL,
			Points:      h.Points,
			NumComments: h.NumComments,
			CreatedISO:  h.CreatedAt,
			CreatedTs:   h.CreatedTs,
			StoryURL:    "https://news.ycombinator.com/item?id=" + h.ObjectID,
		}
		if h.URL != "" {
			if u, err := url.Parse(h.URL); err == nil {
				s.Domain = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
			}
		}
		out = append(out, s)
	}
	return out
}

func materializeHNComments(hits []hnAlgoliaHit) []HNCommentHit {
	out := []HNCommentHit{}
	for _, h := range hits {
		body := stripBasicHTML(h.CommentText)
		if len(body) > 600 {
			body = body[:600] + "..."
		}
		c := HNCommentHit{
			ObjectID:   h.ObjectID,
			Body:       body,
			StoryTitle: h.StoryTitle,
			CreatedISO: h.CreatedAt,
			CreatedTs:  h.CreatedTs,
			HNURL:      "https://news.ycombinator.com/item?id=" + h.ObjectID,
		}
		if h.StoryID > 0 {
			c.StoryID = fmt.Sprintf("%d", h.StoryID)
		}
		out = append(out, c)
	}
	return out
}

func buildHNHighlights(o *HNUserIntelOutput) []string {
	hi := []string{}
	createdDate := ""
	if len(o.CreatedISO) >= 10 {
		createdDate = o.CreatedISO[:10]
	}
	hi = append(hi, fmt.Sprintf("✓ HN user u/%s — created %s (%.1fy), karma=%d, total submitted=%d",
		o.Username, createdDate, o.AccountAgeYears, o.Karma, o.SubmittedTotal))
	if o.About != "" {
		about := o.About
		if len(about) > 200 {
			about = about[:200] + "..."
		}
		hi = append(hi, "📝 about: "+about)
	}
	if o.StoriesIndexedTotal > 0 || o.CommentsIndexedTotal > 0 {
		hi = append(hi, fmt.Sprintf("📊 Algolia indexed: %d stories, %d comments — ratio %.2f (>1 = curator-style, <1 = commenter-style)",
			o.StoriesIndexedTotal, o.CommentsIndexedTotal, o.StoryToCommentRatio))
	}
	if len(o.TopStoriesByPoints) > 0 {
		topS := o.TopStoriesByPoints[0]
		hi = append(hi, fmt.Sprintf("🏆 top story: '%s' — %d points, %d comments (%s)", truncateSiteSnippet(topS.Title, 80), topS.Points, topS.NumComments, topS.StoryURL))
	}
	if len(o.TopDomains) > 0 {
		topDomains := []string{}
		for _, d := range o.TopDomains[:min2(6, len(o.TopDomains))] {
			topDomains = append(topDomains, fmt.Sprintf("%s (%dx, %d pts)", d.Domain, d.Submitted, d.TotalPoints))
		}
		hi = append(hi, "🔗 top submitted domains (interest graph): "+strings.Join(topDomains, ", "))
	}
	if o.InferredTimezone != "" {
		hi = append(hi, "📍 timezone inference: "+o.InferredTimezone)
	}
	if len(o.BioEmails) > 0 {
		hi = append(hi, fmt.Sprintf("📧 emails in about field: %s", strings.Join(o.BioEmails, ", ")))
	}
	if len(o.BioURLs) > 0 {
		hi = append(hi, fmt.Sprintf("🔗 URLs in about field: %s", strings.Join(o.BioURLs, " | ")))
	}
	if o.OldestActivityISO != "" && o.NewestActivityISO != "" {
		hi = append(hi, fmt.Sprintf("activity range: %s → %s", o.OldestActivityISO[:10], o.NewestActivityISO[:10]))
	}
	if o.AccountAgeYears > 0 && o.AccountAgeYears < 0.1 && o.SubmittedTotal > 30 {
		hi = append(hi, "⚠️  young account (<30 days) with high submitted count — possible throwaway/sockpuppet")
	}
	if o.Karma > 100000 {
		hi = append(hi, "✓ extremely high karma — established HN power user (founders, VCs, frequent commenters)")
	}
	return hi
}
