package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// TwitterRapidAPILookup wraps the twitter154 RapidAPI endpoints. REQUIRES
// `RAPID_API_KEY` env var. Ported from vurvey-api `twitter154-api.ts`.
//
// twitter154 is a comprehensive third-party Twitter API (paid via
// RapidAPI subscription). Cheaper than X API v2 Premium ($100+/mo) for
// many OSINT use cases.
//
// Modes:
//   - "user_details" : profile by username or user_id
//   - "user_tweets"  : recent tweets for a user
//   - "search"       : keyword/hashtag search with optional filters
//   - "tweet_details": single tweet by id (with replies/retweets if requested)
//   - "hashtag"      : tweets for a hashtag
//   - "geo_search"   : tweets near lat/lng radius
//
// Knowledge-graph: emits typed entities (kind: "social_account" |
// "social_post") with platform="twitter".

const twitterAPIHost = "twitter154.p.rapidapi.com"

type TwUser struct {
	UserID        string `json:"user_id,omitempty"`
	Username      string `json:"username"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Verified      bool   `json:"verified,omitempty"`
	BlueVerified  bool   `json:"blue_verified,omitempty"`
	Followers     int    `json:"followers_count,omitempty"`
	Following     int    `json:"following_count,omitempty"`
	StatusesCount int    `json:"statuses_count,omitempty"`
	Location      string `json:"location,omitempty"`
	URL           string `json:"twitter_url"`
	JoinDate      string `json:"join_date,omitempty"`
	ProfilePicURL string `json:"profile_pic_url,omitempty"`
}

type TwTweet struct {
	TweetID       string `json:"tweet_id"`
	Text          string `json:"text"`
	CreatedAt     string `json:"created_at,omitempty"`
	Username      string `json:"author_username,omitempty"`
	UserID        string `json:"author_user_id,omitempty"`
	FavoriteCount int    `json:"favorite_count,omitempty"`
	RetweetCount  int    `json:"retweet_count,omitempty"`
	ReplyCount    int    `json:"reply_count,omitempty"`
	Views         int    `json:"views,omitempty"`
	URL           string `json:"twitter_url"`
}

type TwEntity struct {
	Kind        string         `json:"kind"`
	Platform    string         `json:"platform"`
	ID          string         `json:"platform_id"`
	Name        string         `json:"name,omitempty"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type TwitterRapidAPIOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	User              *TwUser        `json:"user,omitempty"`
	Tweets            []TwTweet      `json:"tweets,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []TwEntity     `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func TwitterRapidAPILookup(ctx context.Context, input map[string]any) (*TwitterRapidAPIOutput, error) {
	apiKey := os.Getenv("RAPID_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("RAPID_API_KEY not set; required (subscribe to twitter154 on RapidAPI)")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["tweet_id"] != nil:
			mode = "tweet_details"
		case input["hashtag"] != nil:
			mode = "hashtag"
		case input["latitude"] != nil:
			mode = "geo_search"
		case input["query"] != nil:
			mode = "search"
		case input["username"] != nil && input["tweets"] == true:
			mode = "user_tweets"
		case input["username"] != nil:
			mode = "user_details"
		default:
			return nil, fmt.Errorf("required: username, tweet_id, hashtag, query, or lat/lng")
		}
	}
	out := &TwitterRapidAPIOutput{Mode: mode, Source: twitterAPIHost}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	rapid := func(path string, params url.Values) ([]byte, error) {
		u := fmt.Sprintf("https://%s%s", twitterAPIHost, path)
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("x-rapidapi-key", apiKey)
		req.Header.Set("x-rapidapi-host", twitterAPIHost)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("twitter154: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("twitter154 HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "user_details":
		uname, _ := input["username"].(string)
		uname = strings.TrimPrefix(uname, "@")
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		body, err := rapid("/user/details", url.Values{"username": []string{uname}})
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("twitter154 decode: %w", err)
		}
		out.Detail = m
		out.User = parseTwUser(m, uname)
	case "user_tweets":
		uname, _ := input["username"].(string)
		uname = strings.TrimPrefix(uname, "@")
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		params := url.Values{"username": []string{uname}}
		if l, ok := input["limit"].(float64); ok && l > 0 {
			params.Set("limit", fmt.Sprintf("%d", int(l)))
		} else {
			params.Set("limit", "20")
		}
		body, err := rapid("/user/tweets", params)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("twitter154 decode: %w", err)
		}
		out.Detail = m
		out.Tweets = parseTwTweets(m, uname)
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		params := url.Values{"query": []string{q}}
		if section, ok := input["section"].(string); ok && section != "" {
			params.Set("section", section)
		} else {
			params.Set("section", "top")
		}
		if l, ok := input["limit"].(float64); ok && l > 0 {
			params.Set("limit", fmt.Sprintf("%d", int(l)))
		} else {
			params.Set("limit", "20")
		}
		if lang, ok := input["language"].(string); ok && lang != "" {
			params.Set("language", lang)
		}
		body, err := rapid("/search/search", params)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("twitter154 decode: %w", err)
		}
		out.Detail = m
		out.Tweets = parseTwTweets(m, "")
	case "tweet_details":
		tid, _ := input["tweet_id"].(string)
		if tid == "" {
			return nil, fmt.Errorf("input.tweet_id required")
		}
		out.Query = tid
		body, err := rapid("/tweet/details", url.Values{"tweet_id": []string{tid}})
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("twitter154 decode: %w", err)
		}
		out.Detail = m
		t := parseTwTweet(m)
		if t.TweetID != "" {
			out.Tweets = []TwTweet{t}
		}
	case "hashtag":
		tag, _ := input["hashtag"].(string)
		if tag == "" {
			return nil, fmt.Errorf("input.hashtag required")
		}
		tag = strings.TrimPrefix(tag, "#")
		out.Query = tag
		body, err := rapid("/hashtag/hashtag", url.Values{"hashtag": []string{tag}})
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("twitter154 decode: %w", err)
		}
		out.Detail = m
		out.Tweets = parseTwTweets(m, "")
	case "geo_search":
		lat, _ := input["latitude"].(float64)
		lng, _ := input["longitude"].(float64)
		params := url.Values{
			"latitude":  []string{fmt.Sprintf("%f", lat)},
			"longitude": []string{fmt.Sprintf("%f", lng)},
		}
		if r, ok := input["radius_km"].(float64); ok && r > 0 {
			params.Set("radius", fmt.Sprintf("%.1f", r))
		}
		body, err := rapid("/search/geo", params)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("twitter154 decode: %w", err)
		}
		out.Detail = m
		out.Tweets = parseTwTweets(m, "")
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Tweets)
	if out.User != nil {
		out.Returned++
	}
	out.Entities = twitterRapidBuildEntities(out)
	out.HighlightFindings = twitterRapidBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseTwUser(m map[string]any, fallbackUsername string) *TwUser {
	u := &TwUser{
		UserID:        gtString(m, "user_id"),
		Username:      gtString(m, "username"),
		Name:          gtString(m, "name"),
		Description:   gtString(m, "description"),
		Followers:     int(gtFloat(m, "follower_count")),
		Following:     int(gtFloat(m, "following_count")),
		StatusesCount: int(gtFloat(m, "number_of_tweets")),
		Location:      gtString(m, "location"),
		JoinDate:      gtString(m, "creation_date"),
		ProfilePicURL: gtString(m, "profile_pic_url"),
	}
	if u.Username == "" {
		u.Username = fallbackUsername
	}
	if v, ok := m["is_verified"].(bool); ok {
		u.Verified = v
	}
	if v, ok := m["is_blue_verified"].(bool); ok {
		u.BlueVerified = v
	}
	u.URL = "https://twitter.com/" + u.Username
	return u
}

func parseTwTweet(m map[string]any) TwTweet {
	t := TwTweet{
		TweetID:       gtString(m, "tweet_id"),
		Text:          gtString(m, "text"),
		CreatedAt:     gtString(m, "creation_date"),
		FavoriteCount: int(gtFloat(m, "favorite_count")),
		RetweetCount:  int(gtFloat(m, "retweet_count")),
		ReplyCount:    int(gtFloat(m, "reply_count")),
		Views:         int(gtFloat(m, "views")),
	}
	if user, ok := m["user"].(map[string]any); ok {
		t.Username = gtString(user, "username")
		t.UserID = gtString(user, "user_id")
	}
	if t.TweetID != "" {
		t.URL = "https://x.com/i/web/status/" + t.TweetID
	}
	return t
}

func parseTwTweets(m map[string]any, defaultUsername string) []TwTweet {
	out := []TwTweet{}
	results, _ := m["results"].([]any)
	if results == nil {
		results, _ = m["tweets"].([]any)
	}
	if results == nil {
		results, _ = m["data"].([]any)
	}
	for _, r := range results {
		rec, _ := r.(map[string]any)
		if rec == nil {
			continue
		}
		t := parseTwTweet(rec)
		if t.Username == "" {
			t.Username = defaultUsername
		}
		out = append(out, t)
	}
	return out
}

func twitterRapidBuildEntities(o *TwitterRapidAPIOutput) []TwEntity {
	ents := []TwEntity{}
	if u := o.User; u != nil {
		ents = append(ents, TwEntity{
			Kind: "social_account", Platform: "twitter", ID: u.UserID, Name: u.Username,
			URL: u.URL, Date: u.JoinDate, Description: u.Description,
			Attributes: map[string]any{
				"followers": u.Followers, "following": u.Following,
				"verified": u.Verified, "blue_verified": u.BlueVerified,
				"location": u.Location, "tweets_count": u.StatusesCount,
			},
		})
	}
	for _, t := range o.Tweets {
		ents = append(ents, TwEntity{
			Kind: "social_post", Platform: "twitter", ID: t.TweetID, Name: t.Username,
			URL: t.URL, Date: t.CreatedAt, Description: t.Text,
			Attributes: map[string]any{
				"author":         t.Username,
				"author_user_id": t.UserID,
				"likes":          t.FavoriteCount,
				"retweets":       t.RetweetCount,
				"replies":        t.ReplyCount,
				"views":          t.Views,
			},
		})
	}
	return ents
}

func twitterRapidBuildHighlights(o *TwitterRapidAPIOutput) []string {
	hi := []string{fmt.Sprintf("✓ twitter154 %s: %d records", o.Mode, o.Returned)}
	if u := o.User; u != nil {
		hi = append(hi, fmt.Sprintf("  • @%s (%s) — %d followers, %d tweets, verified=%v",
			u.Username, u.Name, u.Followers, u.StatusesCount, u.Verified))
	}
	for i, t := range o.Tweets {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • @%s [%s] %s — %d♥ %d↻",
			t.Username, t.TweetID, hfTruncate(t.Text, 80), t.FavoriteCount, t.RetweetCount))
	}
	return hi
}
