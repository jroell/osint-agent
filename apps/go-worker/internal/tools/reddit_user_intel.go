package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RedditUserProfile is the about.json profile.
type RedditUserProfile struct {
	Name              string `json:"name"`
	ID                string `json:"reddit_id,omitempty"`
	CreatedUTC        int64  `json:"created_utc,omitempty"`
	CreatedISO        string `json:"created_iso,omitempty"`
	AccountAgeDays    int    `json:"account_age_days,omitempty"`
	AccountAgeYears   float64 `json:"account_age_years,omitempty"`
	TotalKarma        int    `json:"total_karma,omitempty"`
	LinkKarma         int    `json:"link_karma,omitempty"`
	CommentKarma      int    `json:"comment_karma,omitempty"`
	IsEmployee        bool   `json:"is_employee,omitempty"`
	IsMod             bool   `json:"is_mod,omitempty"`
	IsGold            bool   `json:"is_gold,omitempty"`
	Verified          bool   `json:"verified,omitempty"`
	HasVerifiedEmail  bool   `json:"has_verified_email,omitempty"`
	IconImage         string `json:"icon_image,omitempty"`
	SubredditTitle    string `json:"subreddit_title,omitempty"`
	PublicDescription string `json:"public_description,omitempty"`
	OverEighteen      bool   `json:"over_eighteen,omitempty"`
}

// RedditUserPost is a top-level submitted post.
type RedditUserPost struct {
	Subreddit  string `json:"subreddit"`
	Title      string `json:"title"`
	Score      int    `json:"score"`
	NumComments int   `json:"num_comments"`
	CreatedUTC int64  `json:"created_utc"`
	CreatedISO string `json:"created_iso"`
	Permalink  string `json:"permalink,omitempty"`
	URL        string `json:"url,omitempty"`
	IsSelf     bool   `json:"is_self,omitempty"`
}

// RedditComment is a comment.
type RedditComment struct {
	Subreddit  string `json:"subreddit"`
	Body       string `json:"body"`
	Score      int    `json:"score"`
	CreatedUTC int64  `json:"created_utc"`
	CreatedISO string `json:"created_iso"`
	Permalink  string `json:"permalink,omitempty"`
	LinkTitle  string `json:"link_title,omitempty"`
}

// SubredditAggregate counts user activity per sub.
type SubredditAggregate struct {
	Subreddit string `json:"subreddit"`
	Posts     int    `json:"posts"`
	Comments  int    `json:"comments"`
	Total     int    `json:"total_activity"`
	TotalScore int   `json:"total_score"`
}

// HourBucket aggregates posting activity by UTC hour.
type HourBucket struct {
	HourUTC int `json:"hour_utc"`
	Count   int `json:"count"`
}

// RedditUserIntelOutput is the response.
type RedditUserIntelOutput struct {
	Username           string                `json:"username"`
	Profile            *RedditUserProfile    `json:"profile,omitempty"`
	RecentPosts        []RedditUserPost          `json:"recent_posts,omitempty"`
	RecentComments     []RedditComment       `json:"recent_comments,omitempty"`
	TopSubreddits      []SubredditAggregate  `json:"top_subreddits,omitempty"`
	HourDistributionUTC []HourBucket         `json:"hour_distribution_utc,omitempty"`
	InferredTimezone   string                `json:"inferred_timezone,omitempty"`
	MentionedEmails    []string              `json:"mentioned_emails,omitempty"`
	MentionedURLs      []string              `json:"mentioned_urls,omitempty"`
	LocationKeywords   []string              `json:"location_keywords,omitempty"`
	EmploymentKeywords []string              `json:"employment_keywords,omitempty"`
	HighlightFindings  []string              `json:"highlight_findings"`
	Source             string                `json:"source"`
	TookMs             int64                 `json:"tookMs"`
	Note               string                `json:"note,omitempty"`
}

// raw structs
type rrUserAbout struct {
	Data struct {
		Name              string  `json:"name"`
		ID                string  `json:"id"`
		CreatedUTC        float64 `json:"created_utc"`
		TotalKarma        int     `json:"total_karma"`
		LinkKarma         int     `json:"link_karma"`
		CommentKarma      int     `json:"comment_karma"`
		IsEmployee        bool    `json:"is_employee"`
		IsMod             bool    `json:"is_mod"`
		IsGold            bool    `json:"is_gold"`
		Verified          bool    `json:"verified"`
		HasVerifiedEmail  bool    `json:"has_verified_email"`
		IconImg           string  `json:"icon_img"`
		Subreddit struct {
			Title             string `json:"title"`
			PublicDescription string `json:"public_description"`
			Over18            bool   `json:"over_18"`
		} `json:"subreddit"`
	} `json:"data"`
}

type rrListing struct {
	Data struct {
		Children []struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

type rrPostData struct {
	Subreddit   string  `json:"subreddit"`
	Title       string  `json:"title"`
	Score       int     `json:"score"`
	NumComments int     `json:"num_comments"`
	CreatedUTC  float64 `json:"created_utc"`
	Permalink   string  `json:"permalink"`
	URL         string  `json:"url"`
	IsSelf      bool    `json:"is_self"`
}

type rrCommentData struct {
	Subreddit  string  `json:"subreddit"`
	Body       string  `json:"body"`
	Score      int     `json:"score"`
	CreatedUTC float64 `json:"created_utc"`
	Permalink  string  `json:"permalink"`
	LinkTitle  string  `json:"link_title"`
}

var emailRegex = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
var urlRegex = regexp.MustCompile(`https?://[^\s)]+`)

// Common location/employment self-disclosure keywords (lowercased anchors)
// We extract sentences containing "I work at X" / "I live in Y" patterns.
var locationPatterns = regexp.MustCompile(`(?i)\b(?:i live in|i'm from|i am from|located in|moved to|currently in|here in|in my (?:hometown|city|state|country))\s+([A-Z][\w\s,\.]{3,40})`)
var employmentPatterns = regexp.MustCompile(`(?i)\b(?:i work (?:at|for)|i'm employed by|my (?:employer|company|job at)|engineer at|developer at|director at|founder of|ceo of)\s+([A-Z][\w\s\.]{2,40})`)

// RedditUserIntel performs a deep dive on a single Reddit user via
// Reddit's public JSON API. No auth required (subject to fair-use; respect
// robots.txt). Pulls:
//   - Profile (account age, karma, verification flags, NSFW flag)
//   - Recent posts (top + new)
//   - Recent comments
//   - Aggregates: top subreddits by activity, posting hour distribution
//   - Inferred timezone from posting hours
//   - Self-disclosure extraction: emails, URLs, location phrases,
//     employer phrases from comment bodies
//
// Why this matters for ER:
//   - Reddit users overshare under handles thinking they're anonymous —
//     this surfaces those signals.
//   - Account creation timestamps are a strong "is this a new throwaway?"
//     signal (suspicious accounts < 30 days old).
//   - Top subreddit affinity = interest graph (e.g. r/cscareerquestions
//     suggests CS career, r/personalfinance suggests financial concerns).
//   - Posting hour distribution narrows to a 4-6 hour timezone window —
//     "active 18:00-04:00 UTC" → likely Pacific Time (West Coast US).
func RedditUserIntel(ctx context.Context, input map[string]any) (*RedditUserIntelOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	username = strings.TrimPrefix(username, "u/")
	username = strings.TrimPrefix(username, "/u/")
	if username == "" {
		return nil, fmt.Errorf("input.username required")
	}

	postLimit := 25
	if v, ok := input["post_limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		postLimit = int(v)
	}
	commentLimit := 50
	if v, ok := input["comment_limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		commentLimit = int(v)
	}

	start := time.Now()
	out := &RedditUserIntelOutput{
		Username: username,
		Source:   "reddit.com/user/* (public JSON)",
	}

	client := &http.Client{Timeout: 25 * time.Second}
	ua := "osint-agent/0.1 (deep-dive recon; respects fair-use)"

	// 1. Profile
	prof, err := fetchRedditProfile(ctx, client, ua, username)
	if err != nil {
		out.Note = fmt.Sprintf("profile fetch failed: %v", err)
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	out.Profile = prof

	// 2. Posts (top + new combined)
	postsTop, _ := fetchRedditUserPosts(ctx, client, ua, username, "top", postLimit/2)
	postsNew, _ := fetchRedditUserPosts(ctx, client, ua, username, "new", postLimit/2)
	posts := append([]RedditUserPost{}, postsTop...)
	posts = append(posts, postsNew...)
	posts = dedupePosts(posts)
	if len(posts) > postLimit {
		posts = posts[:postLimit]
	}
	out.RecentPosts = posts

	// 3. Comments
	comments, _ := fetchRedditComments(ctx, client, ua, username, "new", commentLimit)
	out.RecentComments = comments

	// 4. Aggregations
	subAgg := map[string]*SubredditAggregate{}
	hourCount := [24]int{}
	emails := map[string]bool{}
	urls := map[string]bool{}
	locKeywords := map[string]bool{}
	empKeywords := map[string]bool{}
	for _, p := range posts {
		ag, ok := subAgg[p.Subreddit]
		if !ok {
			ag = &SubredditAggregate{Subreddit: p.Subreddit}
			subAgg[p.Subreddit] = ag
		}
		ag.Posts++
		ag.Total++
		ag.TotalScore += p.Score
		if p.CreatedUTC > 0 {
			h := time.Unix(p.CreatedUTC, 0).UTC().Hour()
			hourCount[h]++
		}
	}
	for _, c := range comments {
		ag, ok := subAgg[c.Subreddit]
		if !ok {
			ag = &SubredditAggregate{Subreddit: c.Subreddit}
			subAgg[c.Subreddit] = ag
		}
		ag.Comments++
		ag.Total++
		ag.TotalScore += c.Score
		if c.CreatedUTC > 0 {
			h := time.Unix(c.CreatedUTC, 0).UTC().Hour()
			hourCount[h]++
		}
		// extract emails/URLs/keywords from body
		for _, m := range emailRegex.FindAllString(c.Body, -1) {
			emails[strings.ToLower(m)] = true
		}
		for _, m := range urlRegex.FindAllString(c.Body, -1) {
			// strip trailing punct
			m = strings.TrimRight(m, ".,);]'\"!?")
			urls[m] = true
		}
		for _, m := range locationPatterns.FindAllStringSubmatch(c.Body, -1) {
			if len(m) > 1 {
				locKeywords[strings.TrimSpace(strings.Split(m[1], ".")[0])] = true
			}
		}
		for _, m := range employmentPatterns.FindAllStringSubmatch(c.Body, -1) {
			if len(m) > 1 {
				empKeywords[strings.TrimSpace(strings.Split(m[1], ".")[0])] = true
			}
		}
	}

	for _, ag := range subAgg {
		out.TopSubreddits = append(out.TopSubreddits, *ag)
	}
	sort.SliceStable(out.TopSubreddits, func(i, j int) bool {
		if out.TopSubreddits[i].Total != out.TopSubreddits[j].Total {
			return out.TopSubreddits[i].Total > out.TopSubreddits[j].Total
		}
		return out.TopSubreddits[i].TotalScore > out.TopSubreddits[j].TotalScore
	})
	if len(out.TopSubreddits) > 15 {
		out.TopSubreddits = out.TopSubreddits[:15]
	}

	for h, c := range hourCount {
		if c > 0 {
			out.HourDistributionUTC = append(out.HourDistributionUTC, HourBucket{HourUTC: h, Count: c})
		}
	}
	sort.SliceStable(out.HourDistributionUTC, func(i, j int) bool {
		return out.HourDistributionUTC[i].HourUTC < out.HourDistributionUTC[j].HourUTC
	})

	// Infer timezone from peak posting window
	out.InferredTimezone = inferTimezoneFromHours(hourCount[:])

	for k := range emails {
		out.MentionedEmails = append(out.MentionedEmails, k)
	}
	sort.Strings(out.MentionedEmails)
	for k := range urls {
		out.MentionedURLs = append(out.MentionedURLs, k)
	}
	sort.Strings(out.MentionedURLs)
	if len(out.MentionedURLs) > 30 {
		out.MentionedURLs = out.MentionedURLs[:30]
	}
	for k := range locKeywords {
		out.LocationKeywords = append(out.LocationKeywords, k)
	}
	sort.Strings(out.LocationKeywords)
	for k := range empKeywords {
		out.EmploymentKeywords = append(out.EmploymentKeywords, k)
	}
	sort.Strings(out.EmploymentKeywords)

	out.HighlightFindings = buildRedditHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func fetchRedditProfile(ctx context.Context, client *http.Client, ua, username string) (*RedditUserProfile, error) {
	endpoint := fmt.Sprintf("https://www.reddit.com/user/%s/about.json", username)
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("user not found (404)")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("reddit %d: %s", resp.StatusCode, string(body))
	}
	var raw rrUserAbout
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	d := raw.Data
	createdUnix := int64(d.CreatedUTC)
	prof := &RedditUserProfile{
		Name:              d.Name,
		ID:                d.ID,
		CreatedUTC:        createdUnix,
		TotalKarma:        d.TotalKarma,
		LinkKarma:         d.LinkKarma,
		CommentKarma:      d.CommentKarma,
		IsEmployee:        d.IsEmployee,
		IsMod:             d.IsMod,
		IsGold:            d.IsGold,
		Verified:          d.Verified,
		HasVerifiedEmail:  d.HasVerifiedEmail,
		IconImage:         d.IconImg,
		SubredditTitle:    d.Subreddit.Title,
		PublicDescription: d.Subreddit.PublicDescription,
		OverEighteen:      d.Subreddit.Over18,
	}
	if createdUnix > 0 {
		t := time.Unix(createdUnix, 0).UTC()
		prof.CreatedISO = t.Format(time.RFC3339)
		ageDays := int(time.Since(t).Hours() / 24)
		prof.AccountAgeDays = ageDays
		prof.AccountAgeYears = float64(ageDays) / 365.25
	}
	return prof, nil
}

func fetchRedditUserPosts(ctx context.Context, client *http.Client, ua, username, sort string, limit int) ([]RedditUserPost, error) {
	if limit <= 0 {
		return nil, nil
	}
	endpoint := fmt.Sprintf("https://www.reddit.com/user/%s/submitted.json?sort=%s&limit=%d", username, sort, limit)
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
	var raw rrListing
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, nil
	}
	var posts []RedditUserPost
	for _, ch := range raw.Data.Children {
		var d rrPostData
		if err := json.Unmarshal(ch.Data, &d); err != nil {
			continue
		}
		ut := int64(d.CreatedUTC)
		p := RedditUserPost{
			Subreddit:   d.Subreddit,
			Title:       d.Title,
			Score:       d.Score,
			NumComments: d.NumComments,
			CreatedUTC:  ut,
			Permalink:   "https://reddit.com" + d.Permalink,
			URL:         d.URL,
			IsSelf:      d.IsSelf,
		}
		if ut > 0 {
			p.CreatedISO = time.Unix(ut, 0).UTC().Format(time.RFC3339)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func fetchRedditComments(ctx context.Context, client *http.Client, ua, username, sort string, limit int) ([]RedditComment, error) {
	if limit <= 0 {
		return nil, nil
	}
	endpoint := fmt.Sprintf("https://www.reddit.com/user/%s/comments.json?sort=%s&limit=%d", username, sort, limit)
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
	var raw rrListing
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, nil
	}
	var comments []RedditComment
	for _, ch := range raw.Data.Children {
		var d rrCommentData
		if err := json.Unmarshal(ch.Data, &d); err != nil {
			continue
		}
		ut := int64(d.CreatedUTC)
		body := d.Body
		if len(body) > 600 {
			body = body[:600] + "..."
		}
		c := RedditComment{
			Subreddit:  d.Subreddit,
			Body:       body,
			Score:      d.Score,
			CreatedUTC: ut,
			Permalink:  "https://reddit.com" + d.Permalink,
			LinkTitle:  d.LinkTitle,
		}
		if ut > 0 {
			c.CreatedISO = time.Unix(ut, 0).UTC().Format(time.RFC3339)
		}
		comments = append(comments, c)
	}
	return comments, nil
}

func dedupePosts(posts []RedditUserPost) []RedditUserPost {
	seen := map[string]bool{}
	var out []RedditUserPost
	for _, p := range posts {
		k := p.Permalink
		if k == "" {
			k = p.Title + "|" + p.Subreddit
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

func inferTimezoneFromHours(hourCount []int) string {
	total := 0
	for _, c := range hourCount {
		total += c
	}
	if total < 5 {
		return "" // not enough signal
	}
	// Use single-hour peak detection (more accurate for narrow-window users
	// like working professionals who post mostly in the late-afternoon /
	// early-evening local time).
	peakHour, peakCount := 0, 0
	for h, c := range hourCount {
		if c > peakCount {
			peakCount = c
			peakHour = h
		}
	}
	// Also compute the 6-hour window for reporting context
	bestStart, bestSum := 0, 0
	for s := 0; s < 24; s++ {
		sum := 0
		for k := 0; k < 6; k++ {
			sum += hourCount[(s+k)%24]
		}
		if sum > bestSum {
			bestSum = sum
			bestStart = s
		}
	}
	// Assume peak local hour = 16:00 (4pm, late-workday / commute / evening start).
	// offset = local_hour - utc_hour
	offset := 16 - peakHour
	if offset > 12 {
		offset -= 24
	}
	if offset < -11 {
		offset += 24
	}
	tz := ""
	switch {
	case offset >= -8 && offset <= -7:
		tz = "Pacific Time (UTC-7/-8)"
	case offset == -6 || offset == -5:
		tz = "Central / Eastern US (UTC-5/-6)"
	case offset == -4 || offset == -3:
		tz = "Atlantic / South America (UTC-3/-4)"
	case offset == 0 || offset == 1:
		tz = "UK / Western Europe (UTC+0/+1)"
	case offset >= 2 && offset <= 3:
		tz = "Eastern Europe / Israel (UTC+2/+3)"
	case offset == 4 || offset == 5:
		tz = "Russia (Moscow) / South Asia (UTC+3/+5)"
	case offset >= 7 && offset <= 9:
		tz = "China / SE Asia / Japan (UTC+7/+9)"
	case offset == 10 || offset == 11:
		tz = "Australia East / Western Pacific (UTC+10/+11)"
	default:
		tz = fmt.Sprintf("approx UTC%+d", offset)
	}
	return fmt.Sprintf("%s (peak posting window UTC %02d:00-%02d:00, %d posts)", tz, bestStart, (bestStart+6)%24, bestSum)
}

func buildRedditHighlights(o *RedditUserIntelOutput) []string {
	hi := []string{}
	if o.Profile != nil {
		p := o.Profile
		yrs := p.AccountAgeYears
		hi = append(hi, fmt.Sprintf("u/%s — %.1f years old (created %s) — %d total karma (%d link / %d comment)",
			p.Name, yrs, p.CreatedISO[:10], p.TotalKarma, p.LinkKarma, p.CommentKarma))
		flags := []string{}
		if p.IsEmployee {
			flags = append(flags, "Reddit employee")
		}
		if p.IsMod {
			flags = append(flags, "moderator")
		}
		if p.IsGold {
			flags = append(flags, "gold")
		}
		if p.Verified {
			flags = append(flags, "verified")
		}
		if p.HasVerifiedEmail {
			flags = append(flags, "verified email")
		}
		if p.OverEighteen {
			flags = append(flags, "NSFW profile flag")
		}
		if len(flags) > 0 {
			hi = append(hi, "flags: "+strings.Join(flags, ", "))
		}
		if yrs < 0.1 {
			hi = append(hi, "⚠️  account created within last ~30 days — possible throwaway / sock puppet")
		} else if yrs > 10 {
			hi = append(hi, fmt.Sprintf("✓ established account (>%d yrs) — likely not throwaway", int(yrs)))
		}
	}
	if len(o.TopSubreddits) > 0 {
		topSubs := []string{}
		for _, s := range o.TopSubreddits[:min2(5, len(o.TopSubreddits))] {
			topSubs = append(topSubs, fmt.Sprintf("r/%s (%dx)", s.Subreddit, s.Total))
		}
		hi = append(hi, "interest graph (top subs): "+strings.Join(topSubs, ", "))
	}
	if o.InferredTimezone != "" {
		hi = append(hi, "📍 timezone inference: "+o.InferredTimezone)
	}
	if len(o.MentionedEmails) > 0 {
		hi = append(hi, fmt.Sprintf("⚡ %d email(s) self-disclosed in comments: %s", len(o.MentionedEmails), strings.Join(o.MentionedEmails, ", ")))
	}
	if len(o.LocationKeywords) > 0 {
		hi = append(hi, fmt.Sprintf("📌 location keyword(s) from self-disclosure: %s", strings.Join(o.LocationKeywords, " | ")))
	}
	if len(o.EmploymentKeywords) > 0 {
		hi = append(hi, fmt.Sprintf("💼 employer/role keyword(s): %s", strings.Join(o.EmploymentKeywords, " | ")))
	}
	if len(o.MentionedURLs) > 0 {
		hi = append(hi, fmt.Sprintf("%d URL(s) shared (potential cross-platform handle leakage)", len(o.MentionedURLs)))
	}
	hi = append(hi, fmt.Sprintf("recovered: %d posts, %d comments", len(o.RecentPosts), len(o.RecentComments)))
	return hi
}
