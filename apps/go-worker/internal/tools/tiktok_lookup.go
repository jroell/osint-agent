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

// TikTokLookup wraps the tiktok-scraper7 RapidAPI endpoints. REQUIRES
// `RAPID_API_KEY` env var. Ported from vurvey-api `tiktok-tools.ts`
// (which uses the same RapidAPI host).
//
// Modes:
//   - "user_profile"   : profile by username (without @)
//   - "user_videos"    : last N videos for a username
//   - "video_info"     : video details by URL
//   - "challenge_info" : hashtag/challenge info by name
//   - "challenge_posts": top posts in a challenge
//
// Knowledge-graph: emits typed entities (kind: "social_account" |
// "social_post") with platform="tiktok" and stable IDs.

const tiktokAPIHost = "tiktok-scraper7.p.rapidapi.com"

type TikTokAccount struct {
	UserID    string `json:"user_id,omitempty"`
	UniqueID  string `json:"unique_id"`
	Nickname  string `json:"nickname,omitempty"`
	Followers int    `json:"followers,omitempty"`
	Following int    `json:"following,omitempty"`
	Hearts    int    `json:"hearts,omitempty"`
	Videos    int    `json:"video_count,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	URL       string `json:"tiktok_url"`
}

type TikTokPost struct {
	VideoID   string `json:"video_id"`
	Author    string `json:"author,omitempty"`
	Caption   string `json:"caption,omitempty"`
	CreateAt  string `json:"create_at,omitempty"`
	PlayCount int    `json:"play_count,omitempty"`
	LikeCount int    `json:"like_count,omitempty"`
	URL       string `json:"tiktok_url"`
}

type TKEntity struct {
	Kind        string         `json:"kind"`
	Platform    string         `json:"platform"`
	ID          string         `json:"platform_id"`
	Name        string         `json:"name,omitempty"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type TikTokLookupOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Account           *TikTokAccount `json:"account,omitempty"`
	Posts             []TikTokPost   `json:"posts,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []TKEntity     `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func TikTokLookup(ctx context.Context, input map[string]any) (*TikTokLookupOutput, error) {
	apiKey := os.Getenv("RAPID_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("RAPID_API_KEY not set; required for TikTok (subscribe to tiktok-scraper7 on RapidAPI)")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["video_url"] != nil:
			mode = "video_info"
		case input["challenge_id"] != nil || input["challenge_name"] != nil:
			mode = "challenge_posts"
		case input["username"] != nil && input["videos"] == true:
			mode = "user_videos"
		case input["username"] != nil:
			mode = "user_profile"
		default:
			return nil, fmt.Errorf("required: username, video_url, or challenge_name")
		}
	}
	out := &TikTokLookupOutput{Mode: mode, Source: tiktokAPIHost}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	rapid := func(path string, params url.Values) (map[string]any, error) {
		u := fmt.Sprintf("https://%s%s", tiktokAPIHost, path)
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("X-RapidAPI-Key", apiKey)
		req.Header.Set("X-RapidAPI-Host", tiktokAPIHost)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("tiktok: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("tiktok HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("tiktok decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "user_profile":
		uname, _ := input["username"].(string)
		uname = strings.TrimPrefix(uname, "@")
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		m, err := rapid("/user/info", url.Values{"unique_id": []string{uname}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Account = parseTikTokAccount(m, uname)
	case "user_videos":
		uname, _ := input["username"].(string)
		uname = strings.TrimPrefix(uname, "@")
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		// First fetch profile to get user_id
		prof, err := rapid("/user/info", url.Values{"unique_id": []string{uname}})
		if err != nil {
			return nil, err
		}
		out.Account = parseTikTokAccount(prof, uname)
		userID := ""
		if data, ok := prof["data"].(map[string]any); ok {
			userID = gtString(data, "id")
			if userID == "" {
				if user, ok := data["user"].(map[string]any); ok {
					userID = gtString(user, "id")
				}
			}
		}
		if userID == "" {
			return nil, fmt.Errorf("could not resolve user_id from profile")
		}
		count := "20"
		if c, ok := input["limit"].(float64); ok && c > 0 {
			count = fmt.Sprintf("%d", int(c))
		}
		m, err := rapid("/user/posts", url.Values{"user_id": []string{userID}, "count": []string{count}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Posts = parseTikTokPosts(m, uname)
	case "video_info":
		urlStr, _ := input["video_url"].(string)
		if urlStr == "" {
			return nil, fmt.Errorf("input.video_url required")
		}
		out.Query = urlStr
		m, err := rapid("/", url.Values{"url": []string{urlStr}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		if d, ok := m["data"].(map[string]any); ok {
			post := TikTokPost{
				VideoID:   gtString(d, "id"),
				Caption:   gtString(d, "title"),
				PlayCount: int(gtFloat(d, "play_count")),
				LikeCount: int(gtFloat(d, "digg_count")),
				URL:       urlStr,
			}
			if author, ok := d["author"].(map[string]any); ok {
				post.Author = gtString(author, "unique_id")
			}
			out.Posts = []TikTokPost{post}
		}
	case "challenge_info":
		name, _ := input["challenge_name"].(string)
		if name == "" {
			return nil, fmt.Errorf("input.challenge_name required")
		}
		out.Query = name
		m, err := rapid("/challenge/info", url.Values{"challenge_name": []string{name}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "challenge_posts":
		challengeID, _ := input["challenge_id"].(string)
		if challengeID == "" {
			return nil, fmt.Errorf("input.challenge_id required (use challenge_info first to get the id)")
		}
		out.Query = challengeID
		count := "20"
		if c, ok := input["count"].(float64); ok && c > 0 {
			count = fmt.Sprintf("%d", int(c))
		}
		m, err := rapid("/challenge/posts", url.Values{"challenge_id": []string{challengeID}, "count": []string{count}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Posts = parseTikTokPosts(m, "")
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Posts)
	if out.Account != nil {
		out.Returned++
	}
	out.Entities = tiktokBuildEntities(out)
	out.HighlightFindings = tiktokBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseTikTokAccount(m map[string]any, uname string) *TikTokAccount {
	a := &TikTokAccount{
		UniqueID: uname,
		URL:      "https://www.tiktok.com/@" + uname,
	}
	if d, ok := m["data"].(map[string]any); ok {
		a.UserID = gtString(d, "id")
		if user, ok := d["user"].(map[string]any); ok {
			if a.UserID == "" {
				a.UserID = gtString(user, "id")
			}
			a.Nickname = gtString(user, "nickname")
			a.Avatar = gtString(user, "avatarMedium")
		}
		if stats, ok := d["stats"].(map[string]any); ok {
			a.Followers = int(gtFloat(stats, "followerCount"))
			a.Following = int(gtFloat(stats, "followingCount"))
			a.Hearts = int(gtFloat(stats, "heartCount"))
			a.Videos = int(gtFloat(stats, "videoCount"))
		}
	}
	return a
}

func parseTikTokPosts(m map[string]any, defaultAuthor string) []TikTokPost {
	posts := []TikTokPost{}
	d, _ := m["data"].(map[string]any)
	if d == nil {
		return posts
	}
	videos, _ := d["videos"].([]any)
	if videos == nil {
		videos, _ = d["data"].([]any)
	}
	for _, v := range videos {
		rec, _ := v.(map[string]any)
		if rec == nil {
			continue
		}
		author := defaultAuthor
		if a, ok := rec["author"].(map[string]any); ok {
			author = gtString(a, "unique_id")
		}
		id := gtString(rec, "video_id")
		if id == "" {
			id = gtString(rec, "id")
		}
		p := TikTokPost{
			VideoID:   id,
			Author:    author,
			Caption:   gtString(rec, "title"),
			CreateAt:  gtString(rec, "create_time"),
			PlayCount: int(gtFloat(rec, "play_count")),
			LikeCount: int(gtFloat(rec, "digg_count")),
			URL:       fmt.Sprintf("https://www.tiktok.com/@%s/video/%s", author, id),
		}
		posts = append(posts, p)
	}
	return posts
}

func tiktokBuildEntities(o *TikTokLookupOutput) []TKEntity {
	ents := []TKEntity{}
	if a := o.Account; a != nil {
		ents = append(ents, TKEntity{
			Kind: "social_account", Platform: "tiktok", ID: a.UserID, Name: a.UniqueID,
			URL:         a.URL,
			Description: a.Nickname,
			Attributes: map[string]any{
				"followers": a.Followers, "following": a.Following,
				"hearts": a.Hearts, "videos": a.Videos, "avatar": a.Avatar,
			},
		})
	}
	for _, p := range o.Posts {
		ents = append(ents, TKEntity{
			Kind: "social_post", Platform: "tiktok", ID: p.VideoID, Name: p.Author,
			URL: p.URL, Date: p.CreateAt, Description: p.Caption,
			Attributes: map[string]any{
				"author": p.Author, "play_count": p.PlayCount, "like_count": p.LikeCount,
			},
		})
	}
	return ents
}

func tiktokBuildHighlights(o *TikTokLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ tiktok %s: %d records", o.Mode, o.Returned)}
	if a := o.Account; a != nil {
		hi = append(hi, fmt.Sprintf("  • @%s (%s) — %d followers, %d videos, %d hearts",
			a.UniqueID, a.Nickname, a.Followers, a.Videos, a.Hearts))
	}
	for i, p := range o.Posts {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • [%s] %s — plays %d likes %d (%s)",
			p.VideoID, hfTruncate(p.Caption, 60), p.PlayCount, p.LikeCount, p.URL))
	}
	return hi
}
