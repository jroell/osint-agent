package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SubredditAbout is the subreddit metadata.
type SubredditAbout struct {
	DisplayName       string  `json:"display_name"`
	DisplayNamePrefix string  `json:"display_name_prefixed"`
	Title             string  `json:"title,omitempty"`
	PublicDescription string  `json:"public_description,omitempty"`
	Subscribers       int     `json:"subscribers,omitempty"`
	ActiveUsers       int     `json:"active_users,omitempty"`
	CreatedISO        string  `json:"created_iso,omitempty"`
	AgeYears          float64 `json:"age_years,omitempty"`
	SubredditType     string  `json:"subreddit_type,omitempty"` // public | private | restricted | gold_only | archived | etc.
	NSFW              bool    `json:"over_18,omitempty"`
	Quarantine        bool    `json:"quarantine,omitempty"`
	RestrictPosting   bool    `json:"restrict_posting,omitempty"`
	WikiEnabled       bool    `json:"wiki_enabled,omitempty"`
	Lang              string  `json:"language,omitempty"`
	URL               string  `json:"url,omitempty"`
}

// SubredditTopPoster aggregates a user's footprint in this sub.
type SubredditTopPoster struct {
	Author       string `json:"author"`
	PostCount    int    `json:"post_count"`
	TotalScore   int    `json:"total_score"`
	TotalCommentCount int `json:"total_comments_received"`
	TopPostTitle string `json:"top_post_title,omitempty"`
}

// SubredditDomainAggregate counts external linked domains.
type SubredditDomainAggregate struct {
	Domain     string `json:"domain"`
	LinkCount  int    `json:"link_count"`
	TotalScore int    `json:"total_score"`
}

// SubredditPost is a post entry in the response.
type SubredditPost struct {
	Title        string `json:"title"`
	Author       string `json:"author"`
	Score        int    `json:"score"`
	NumComments  int    `json:"num_comments"`
	URL          string `json:"url,omitempty"`
	Domain       string `json:"domain,omitempty"`
	IsSelf       bool   `json:"is_self,omitempty"`
	OverEighteen bool   `json:"over_18,omitempty"`
	Spoiler      bool   `json:"spoiler,omitempty"`
	Stickied     bool   `json:"stickied,omitempty"`
	CreatedISO   string `json:"created_iso,omitempty"`
	Permalink    string `json:"permalink,omitempty"`
	UpvoteRatio  float64 `json:"upvote_ratio,omitempty"`
	Flair        string `json:"flair,omitempty"`
}

// SubredditIntelOutput is the response.
type SubredditIntelOutput struct {
	Name              string                       `json:"subreddit"`
	About             *SubredditAbout              `json:"about,omitempty"`
	TopPosters        []SubredditTopPoster         `json:"top_posters,omitempty"`
	TopDomains        []SubredditDomainAggregate   `json:"top_domains,omitempty"`
	TopPosts          []SubredditPost              `json:"top_posts,omitempty"`
	HotPosts          []SubredditPost              `json:"hot_posts,omitempty"`
	SelfPostRatio     float64                      `json:"self_post_ratio,omitempty"`
	AvgScore          float64                      `json:"avg_score,omitempty"`
	AvgComments       float64                      `json:"avg_comments,omitempty"`
	AvgUpvoteRatio    float64                      `json:"avg_upvote_ratio,omitempty"`
	UniqueAuthors     int                          `json:"unique_authors_in_sample,omitempty"`
	UniqueDomains     int                          `json:"unique_external_domains,omitempty"`
	HighlightFindings []string                     `json:"highlight_findings"`
	Source            string                       `json:"source"`
	TookMs            int64                        `json:"tookMs"`
	Note              string                       `json:"note,omitempty"`
}

// raw structs
type rsAboutRaw struct {
	Data struct {
		DisplayName       string  `json:"display_name"`
		DisplayNamePrefix string  `json:"display_name_prefixed"`
		Title             string  `json:"title"`
		PublicDescription string  `json:"public_description"`
		Subscribers       int     `json:"subscribers"`
		ActiveUserCount   int     `json:"active_user_count"`
		AccountsActive    int     `json:"accounts_active"`
		CreatedUTC        float64 `json:"created_utc"`
		SubredditType     string  `json:"subreddit_type"`
		Over18            bool    `json:"over18"`
		Quarantine        bool    `json:"quarantine"`
		RestrictPosting   bool    `json:"restrict_posting"`
		WikiEnabled       bool    `json:"wiki_enabled"`
		Lang              string  `json:"lang"`
		URL               string  `json:"url"`
	} `json:"data"`
}

type rsListingRaw struct {
	Data struct {
		Children []struct {
			Data struct {
				Title       string  `json:"title"`
				Author      string  `json:"author"`
				Score       int     `json:"score"`
				NumComments int     `json:"num_comments"`
				URL         string  `json:"url"`
				Domain      string  `json:"domain"`
				IsSelf      bool    `json:"is_self"`
				Over18      bool    `json:"over_18"`
				Spoiler     bool    `json:"spoiler"`
				Stickied    bool    `json:"stickied"`
				CreatedUTC  float64 `json:"created_utc"`
				Permalink   string  `json:"permalink"`
				UpvoteRatio float64 `json:"upvote_ratio"`
				Flair       string  `json:"link_flair_text"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

// RedditSubredditIntel fetches subreddit metadata + post listings and
// aggregates them into community-level intelligence. Free, no auth.
//
// NOTE: The /about/moderators.json endpoint requires authenticated access
// since Reddit's 2023 API changes — we surface that limitation in the note
// rather than failing silently. Top posters from recent activity often
// overlap heavily with mod lists in practice (long-term active members
// frequently end up modding).
//
// Why this matters for ER:
//   - Subreddit "top posters" reveals community leaders / topic experts.
//   - Top external domains reveals what content the community gravitates
//     toward (industry-specific blogs, news outlets, GitHub orgs).
//   - Self-post ratio reveals discussion-heavy vs link-heavy culture.
//   - Avg upvote_ratio reveals controversy level (low ratio = polarized).
//   - NSFW + quarantine + restrict_posting flags reveal moderation state.
//   - Subreddit age + subscriber count reveals community maturity.
//   - Pairs with reddit_user_intel (per-user) and reddit_org_intel (search).
func RedditSubredditIntel(ctx context.Context, input map[string]any) (*SubredditIntelOutput, error) {
	name, _ := input["subreddit"].(string)
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/r/")
	name = strings.TrimPrefix(name, "r/")
	if name == "" {
		return nil, fmt.Errorf("input.subreddit required (e.g. 'MachineLearning' or 'r/MachineLearning')")
	}

	topTime, _ := input["top_time_window"].(string)
	topTime = strings.ToLower(strings.TrimSpace(topTime))
	if topTime == "" {
		topTime = "month"
	}
	switch topTime {
	case "hour", "day", "week", "month", "year", "all":
	default:
		return nil, fmt.Errorf("top_time_window must be one of: hour, day, week, month, year, all")
	}

	postLimit := 50
	if v, ok := input["post_limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		postLimit = int(v)
	}

	out := &SubredditIntelOutput{
		Name:   name,
		Source: "reddit.com (public JSON, unauthenticated)",
	}
	start := time.Now()
	client := &http.Client{Timeout: 25 * time.Second}
	ua := "osint-agent/0.1 (subreddit recon)"

	// 1. About metadata
	if about, err := fetchSubredditAbout(ctx, client, ua, name); err == nil && about != nil {
		out.About = about
	} else if err != nil {
		out.Note = fmt.Sprintf("about fetch failed: %v", err)
	}

	// 2. Top posts
	topPosts, _ := fetchSubredditPosts(ctx, client, ua, name, "top", topTime, postLimit)
	out.TopPosts = topPosts

	// 3. Hot posts (current activity)
	hotPosts, _ := fetchSubredditPosts(ctx, client, ua, name, "hot", topTime, postLimit/2)
	out.HotPosts = hotPosts

	// If no data at all, mark as not found
	if out.About == nil && len(topPosts) == 0 && len(hotPosts) == 0 {
		if out.Note == "" {
			out.Note = fmt.Sprintf("no data for r/%s — possibly private, banned, or non-existent", name)
		}
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// 4. Aggregations from combined post pool
	allPosts := append([]SubredditPost{}, topPosts...)
	allPosts = append(allPosts, hotPosts...)

	posterAgg := map[string]*SubredditTopPoster{}
	domainAgg := map[string]*SubredditDomainAggregate{}
	authorSet := map[string]struct{}{}
	domainSet := map[string]struct{}{}
	selfPosts := 0
	totalScore := 0
	totalComments := 0
	totalUpvoteRatio := 0.0
	totalCount := 0

	for _, p := range allPosts {
		// posters
		if p.Author != "" && p.Author != "[deleted]" && p.Author != "AutoModerator" {
			ag, ok := posterAgg[p.Author]
			if !ok {
				ag = &SubredditTopPoster{Author: p.Author}
				posterAgg[p.Author] = ag
			}
			ag.PostCount++
			ag.TotalScore += p.Score
			ag.TotalCommentCount += p.NumComments
			if p.Score > 0 && (ag.TopPostTitle == "" || p.Score > 0) {
				ag.TopPostTitle = p.Title
			}
			authorSet[p.Author] = struct{}{}
		}
		// domains
		if !p.IsSelf && p.Domain != "" && !strings.HasPrefix(p.Domain, "self.") && !strings.HasPrefix(p.Domain, "i.redd.it") {
			ag, ok := domainAgg[p.Domain]
			if !ok {
				ag = &SubredditDomainAggregate{Domain: p.Domain}
				domainAgg[p.Domain] = ag
			}
			ag.LinkCount++
			ag.TotalScore += p.Score
			domainSet[p.Domain] = struct{}{}
		}
		if p.IsSelf {
			selfPosts++
		}
		totalScore += p.Score
		totalComments += p.NumComments
		totalUpvoteRatio += p.UpvoteRatio
		totalCount++
	}

	// Materialize top posters
	for _, ag := range posterAgg {
		out.TopPosters = append(out.TopPosters, *ag)
	}
	sort.SliceStable(out.TopPosters, func(i, j int) bool {
		if out.TopPosters[i].PostCount != out.TopPosters[j].PostCount {
			return out.TopPosters[i].PostCount > out.TopPosters[j].PostCount
		}
		return out.TopPosters[i].TotalScore > out.TopPosters[j].TotalScore
	})
	if len(out.TopPosters) > 15 {
		out.TopPosters = out.TopPosters[:15]
	}

	// Materialize top domains
	for _, ag := range domainAgg {
		out.TopDomains = append(out.TopDomains, *ag)
	}
	sort.SliceStable(out.TopDomains, func(i, j int) bool {
		return out.TopDomains[i].LinkCount > out.TopDomains[j].LinkCount
	})
	if len(out.TopDomains) > 15 {
		out.TopDomains = out.TopDomains[:15]
	}

	out.UniqueAuthors = len(authorSet)
	out.UniqueDomains = len(domainSet)

	if totalCount > 0 {
		out.SelfPostRatio = float64(selfPosts) / float64(totalCount)
		out.AvgScore = float64(totalScore) / float64(totalCount)
		out.AvgComments = float64(totalComments) / float64(totalCount)
		out.AvgUpvoteRatio = totalUpvoteRatio / float64(totalCount)
	}

	out.HighlightFindings = buildSubredditIntelHighlights(out, topTime)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func fetchSubredditAbout(ctx context.Context, client *http.Client, ua, name string) (*SubredditAbout, error) {
	endpoint := fmt.Sprintf("https://www.reddit.com/r/%s/about.json", url.PathEscape(name))
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 403 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("about %d", resp.StatusCode)
	}
	var raw rsAboutRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.Data.DisplayName == "" {
		return nil, nil
	}
	d := raw.Data
	about := &SubredditAbout{
		DisplayName:       d.DisplayName,
		DisplayNamePrefix: d.DisplayNamePrefix,
		Title:             d.Title,
		PublicDescription: d.PublicDescription,
		Subscribers:       d.Subscribers,
		ActiveUsers:       d.ActiveUserCount,
		SubredditType:     d.SubredditType,
		NSFW:              d.Over18,
		Quarantine:        d.Quarantine,
		RestrictPosting:   d.RestrictPosting,
		WikiEnabled:       d.WikiEnabled,
		Lang:              d.Lang,
		URL:               d.URL,
	}
	if d.AccountsActive > 0 && about.ActiveUsers == 0 {
		about.ActiveUsers = d.AccountsActive
	}
	if d.CreatedUTC > 0 {
		t := time.Unix(int64(d.CreatedUTC), 0).UTC()
		about.CreatedISO = t.Format(time.RFC3339)
		about.AgeYears = time.Since(t).Hours() / (24 * 365.25)
	}
	return about, nil
}

func fetchSubredditPosts(ctx context.Context, client *http.Client, ua, name, sort, timeWindow string, limit int) ([]SubredditPost, error) {
	if limit <= 0 {
		return nil, nil
	}
	endpoint := fmt.Sprintf("https://www.reddit.com/r/%s/%s.json?limit=%d", url.PathEscape(name), sort, limit)
	if sort == "top" && timeWindow != "" {
		endpoint += "&t=" + timeWindow
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil
	}
	var raw rsListingRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, nil
	}
	posts := make([]SubredditPost, 0, len(raw.Data.Children))
	for _, ch := range raw.Data.Children {
		d := ch.Data
		p := SubredditPost{
			Title:        d.Title,
			Author:       d.Author,
			Score:        d.Score,
			NumComments:  d.NumComments,
			URL:          d.URL,
			Domain:       d.Domain,
			IsSelf:       d.IsSelf,
			OverEighteen: d.Over18,
			Spoiler:      d.Spoiler,
			Stickied:     d.Stickied,
			Permalink:    "https://reddit.com" + d.Permalink,
			UpvoteRatio:  d.UpvoteRatio,
			Flair:        d.Flair,
		}
		if d.CreatedUTC > 0 {
			ts := time.Unix(int64(d.CreatedUTC), 0).UTC()
			p.CreatedISO = ts.Format(time.RFC3339)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func buildSubredditIntelHighlights(o *SubredditIntelOutput, topTime string) []string {
	hi := []string{}
	if o.About != nil {
		a := o.About
		hi = append(hi, fmt.Sprintf("✓ r/%s — '%s' (%d subscribers, age %.1fy, type=%s)",
			a.DisplayName, a.Title, a.Subscribers, a.AgeYears, a.SubredditType))
		if a.NSFW {
			hi = append(hi, "🔞 NSFW subreddit")
		}
		if a.Quarantine {
			hi = append(hi, "🚫 quarantined — Reddit-flagged content concern")
		}
		if a.SubredditType == "private" {
			hi = append(hi, "🔒 private subreddit — invite-only")
		}
		if a.SubredditType == "restricted" {
			hi = append(hi, "🔐 restricted — only approved users can post")
		}
		if a.PublicDescription != "" {
			hi = append(hi, "description: "+a.PublicDescription)
		}
		if a.ActiveUsers > 0 {
			engagement := float64(a.ActiveUsers) / float64(a.Subscribers) * 100
			hi = append(hi, fmt.Sprintf("📊 %d currently active (%.2f%% of subscribers)", a.ActiveUsers, engagement))
		}
	}
	if len(o.TopPosters) > 0 {
		topPostersStr := []string{}
		for _, p := range o.TopPosters[:min2(5, len(o.TopPosters))] {
			topPostersStr = append(topPostersStr, fmt.Sprintf("u/%s (%dx, %d total karma)", p.Author, p.PostCount, p.TotalScore))
		}
		hi = append(hi, "👥 top posters in returned set: "+strings.Join(topPostersStr, ", "))
	}
	if len(o.TopDomains) > 0 {
		topDomainsStr := []string{}
		for _, d := range o.TopDomains[:min2(5, len(o.TopDomains))] {
			topDomainsStr = append(topDomainsStr, fmt.Sprintf("%s (%d)", d.Domain, d.LinkCount))
		}
		hi = append(hi, "🔗 top external domains: "+strings.Join(topDomainsStr, ", "))
	}
	if o.SelfPostRatio > 0 {
		culture := "discussion-heavy"
		if o.SelfPostRatio < 0.3 {
			culture = "link-heavy (curation-focused)"
		} else if o.SelfPostRatio > 0.7 {
			culture = "discussion-dominated"
		}
		hi = append(hi, fmt.Sprintf("📝 %.0f%% self-posts vs %.0f%% link posts — %s culture",
			o.SelfPostRatio*100, (1-o.SelfPostRatio)*100, culture))
	}
	if o.AvgUpvoteRatio > 0 {
		controversy := ""
		if o.AvgUpvoteRatio < 0.7 {
			controversy = " (low → polarized/controversial)"
		} else if o.AvgUpvoteRatio > 0.93 {
			controversy = " (high → echo-chamber-like)"
		}
		hi = append(hi, fmt.Sprintf("📈 avg upvote ratio %.2f%s", o.AvgUpvoteRatio, controversy))
	}
	hi = append(hi, fmt.Sprintf("⚠️  mod list unavailable without auth (Reddit API change 2023) — top_posters approximates community leaders"))
	return hi
}

