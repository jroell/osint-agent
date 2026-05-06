package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// InstagramRapidAPI wraps instagram120.p.rapidapi.com. REQUIRES
// `RAPID_API_KEY`. Ported from vurvey-api `instagram120-api.ts`.
//
// Modes:
//   - "user_profile" : profile by username (POST /api/instagram/profile)
//   - "user_info"    : extended user info incl. Real ID
//   - "user_posts"   : recent posts grid
//   - "user_reels"   : recent reels
//   - "user_stories" : currently-live stories
//   - "highlights"   : story highlights
//   - "post_by_url"  : metadata for a post URL
//   - "post_by_shortcode" : metadata for a /p/<shortcode>/
//
// Knowledge-graph: emits typed entities (kind: social_account |
// social_post, platform: instagram).

const igAPIHost = "instagram120.p.rapidapi.com"

type IGAccount struct {
	UserID    string `json:"user_id,omitempty"`
	Username  string `json:"username"`
	FullName  string `json:"full_name,omitempty"`
	Biography string `json:"biography,omitempty"`
	Followers int    `json:"follower_count,omitempty"`
	Following int    `json:"following_count,omitempty"`
	Posts     int    `json:"media_count,omitempty"`
	Verified  bool   `json:"is_verified,omitempty"`
	Private   bool   `json:"is_private,omitempty"`
	Business  bool   `json:"is_business_account,omitempty"`
	Email     string `json:"public_email,omitempty"`
	Phone     string `json:"public_phone_number,omitempty"`
	Website   string `json:"external_url,omitempty"`
	URL       string `json:"instagram_url"`
}

type IGPost struct {
	ID           string `json:"id"`
	Shortcode    string `json:"shortcode,omitempty"`
	Caption      string `json:"caption,omitempty"`
	LikeCount    int    `json:"like_count,omitempty"`
	CommentCount int    `json:"comment_count,omitempty"`
	TakenAt      string `json:"taken_at,omitempty"`
	Type         string `json:"type,omitempty"`
	URL          string `json:"instagram_url"`
}

type IGEntity struct {
	Kind        string         `json:"kind"`
	Platform    string         `json:"platform"`
	ID          string         `json:"platform_id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type InstagramRapidAPIOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Account           *IGAccount     `json:"account,omitempty"`
	Posts             []IGPost       `json:"posts,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []IGEntity     `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func InstagramRapidAPILookup(ctx context.Context, input map[string]any) (*InstagramRapidAPIOutput, error) {
	apiKey := os.Getenv("RAPID_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("RAPID_API_KEY not set; required (subscribe to instagram120 on RapidAPI)")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["shortcode"] != nil:
			mode = "post_by_shortcode"
		case input["post_url"] != nil:
			mode = "post_by_url"
		case input["username"] != nil && input["stories"] == true:
			mode = "user_stories"
		case input["username"] != nil && input["highlights"] == true:
			mode = "highlights"
		case input["username"] != nil && input["reels"] == true:
			mode = "user_reels"
		case input["username"] != nil && input["posts"] == true:
			mode = "user_posts"
		case input["username"] != nil:
			mode = "user_profile"
		default:
			return nil, fmt.Errorf("required: username, post_url, or shortcode")
		}
	}
	out := &InstagramRapidAPIOutput{Mode: mode, Source: igAPIHost}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	rapidPost := func(path string, payload map[string]any) (map[string]any, error) {
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(ctx, "POST",
			"https://"+igAPIHost+path, bytes.NewReader(body))
		req.Header.Set("x-rapidapi-key", apiKey)
		req.Header.Set("x-rapidapi-host", igAPIHost)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("instagram120: %w", err)
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("instagram120 HTTP %d: %s", resp.StatusCode, hfTruncate(string(respBody), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(respBody, &m); err != nil {
			return nil, fmt.Errorf("instagram120 decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "user_profile":
		uname, _ := input["username"].(string)
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		m, err := rapidPost("/api/instagram/profile", map[string]any{"username": uname})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Account = parseIGAccount(m, uname)
	case "user_info":
		uname, _ := input["username"].(string)
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		m, err := rapidPost("/api/instagram/userinfo", map[string]any{"username": uname})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Account = parseIGAccount(m, uname)
	case "user_posts":
		uname, _ := input["username"].(string)
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		m, err := rapidPost("/api/instagram/posts", map[string]any{"username": uname})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Posts = parseIGPosts(m, uname)
	case "user_reels":
		uname, _ := input["username"].(string)
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		m, err := rapidPost("/api/instagram/reels", map[string]any{"username": uname})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Posts = parseIGPosts(m, uname)
	case "user_stories":
		uname, _ := input["username"].(string)
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		m, err := rapidPost("/api/instagram/stories", map[string]any{"username": uname})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "highlights":
		uname, _ := input["username"].(string)
		if uname == "" {
			return nil, fmt.Errorf("input.username required")
		}
		out.Query = uname
		m, err := rapidPost("/api/instagram/highlights", map[string]any{"username": uname})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "post_by_url":
		u, _ := input["post_url"].(string)
		if u == "" {
			return nil, fmt.Errorf("input.post_url required")
		}
		out.Query = u
		m, err := rapidPost("/api/instagram/links", map[string]any{"url": u})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "post_by_shortcode":
		sc, _ := input["shortcode"].(string)
		if sc == "" {
			return nil, fmt.Errorf("input.shortcode required")
		}
		out.Query = sc
		m, err := rapidPost("/api/instagram/mediabbyshortcode", map[string]any{"shortcode": sc})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Posts)
	if out.Account != nil {
		out.Returned++
	}
	out.Entities = igBuildEntities(out)
	out.HighlightFindings = igBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseIGAccount(m map[string]any, uname string) *IGAccount {
	a := &IGAccount{Username: uname, URL: "https://www.instagram.com/" + uname + "/"}
	src := m
	if data, ok := m["data"].(map[string]any); ok {
		src = data
	}
	if user, ok := src["user"].(map[string]any); ok {
		src = user
	}
	a.UserID = gtString(src, "id")
	a.FullName = gtString(src, "full_name")
	a.Biography = gtString(src, "biography")
	a.Followers = int(gtFloat(src, "follower_count"))
	if a.Followers == 0 {
		// alternate naming
		if edge, ok := src["edge_followed_by"].(map[string]any); ok {
			a.Followers = int(gtFloat(edge, "count"))
		}
	}
	a.Following = int(gtFloat(src, "following_count"))
	if a.Following == 0 {
		if edge, ok := src["edge_follow"].(map[string]any); ok {
			a.Following = int(gtFloat(edge, "count"))
		}
	}
	a.Posts = int(gtFloat(src, "media_count"))
	if v, ok := src["is_verified"].(bool); ok {
		a.Verified = v
	}
	if v, ok := src["is_private"].(bool); ok {
		a.Private = v
	}
	if v, ok := src["is_business_account"].(bool); ok {
		a.Business = v
	}
	a.Email = gtString(src, "public_email")
	a.Phone = gtString(src, "public_phone_number")
	a.Website = gtString(src, "external_url")
	return a
}

func parseIGPosts(m map[string]any, defaultAuthor string) []IGPost {
	out := []IGPost{}
	src, _ := m["data"].([]any)
	if src == nil {
		if d, ok := m["data"].(map[string]any); ok {
			if items, ok := d["items"].([]any); ok {
				src = items
			}
		}
	}
	for _, x := range src {
		rec, _ := x.(map[string]any)
		if rec == nil {
			continue
		}
		p := IGPost{
			ID:           gtString(rec, "id"),
			Shortcode:    gtString(rec, "shortcode"),
			Caption:      gtString(rec, "caption_text"),
			LikeCount:    int(gtFloat(rec, "like_count")),
			CommentCount: int(gtFloat(rec, "comment_count")),
			TakenAt:      gtString(rec, "taken_at"),
			Type:         gtString(rec, "media_type_str"),
		}
		if p.Caption == "" {
			if cap, ok := rec["caption"].(map[string]any); ok {
				p.Caption = gtString(cap, "text")
			}
		}
		if p.Shortcode != "" {
			p.URL = "https://www.instagram.com/p/" + p.Shortcode + "/"
		}
		out = append(out, p)
	}
	return out
}

func igBuildEntities(o *InstagramRapidAPIOutput) []IGEntity {
	ents := []IGEntity{}
	if a := o.Account; a != nil {
		ents = append(ents, IGEntity{
			Kind: "social_account", Platform: "instagram", ID: a.UserID, Name: a.Username,
			URL: a.URL, Description: a.Biography,
			Attributes: map[string]any{
				"full_name": a.FullName,
				"followers": a.Followers, "following": a.Following, "posts": a.Posts,
				"verified": a.Verified, "private": a.Private, "business": a.Business,
				"email": a.Email, "phone": a.Phone, "website": a.Website,
			},
		})
	}
	for _, p := range o.Posts {
		ents = append(ents, IGEntity{
			Kind: "social_post", Platform: "instagram", ID: p.ID,
			URL: p.URL, Date: p.TakenAt, Description: p.Caption,
			Attributes: map[string]any{
				"shortcode":     p.Shortcode,
				"like_count":    p.LikeCount,
				"comment_count": p.CommentCount,
				"type":          p.Type,
			},
		})
	}
	return ents
}

func igBuildHighlights(o *InstagramRapidAPIOutput) []string {
	hi := []string{fmt.Sprintf("✓ instagram120 %s: %d records", o.Mode, o.Returned)}
	if a := o.Account; a != nil {
		hi = append(hi, fmt.Sprintf("  • @%s (%s) — %d followers, %d following, %d posts (verified=%v)",
			a.Username, a.FullName, a.Followers, a.Following, a.Posts, a.Verified))
		if a.Email != "" {
			hi = append(hi, "    contact: "+a.Email)
		}
	}
	for i, p := range o.Posts {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • [%s] %s ♥%d 💬%d (%s)", p.Shortcode, hfTruncate(p.Caption, 60), p.LikeCount, p.CommentCount, p.URL))
	}
	return hi
}
