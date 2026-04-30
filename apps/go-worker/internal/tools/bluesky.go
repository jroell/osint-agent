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

type BlueskyPost struct {
	URI         string `json:"uri"`
	CID         string `json:"cid,omitempty"`
	Text        string `json:"text"`
	IndexedAt   string `json:"indexed_at,omitempty"`
	LikeCount   int    `json:"like_count,omitempty"`
	RepostCount int    `json:"repost_count,omitempty"`
	ReplyCount  int    `json:"reply_count,omitempty"`
}

type BlueskyOutput struct {
	Handle         string        `json:"handle"`
	DID            string        `json:"did"`
	DisplayName    string        `json:"display_name,omitempty"`
	Description    string        `json:"description,omitempty"`
	FollowersCount int           `json:"followers_count"`
	FollowsCount   int           `json:"follows_count"`
	PostsCount     int           `json:"posts_count"`
	IndexedAt      string        `json:"indexed_at,omitempty"`
	AvatarURL      string        `json:"avatar_url,omitempty"`
	BannerURL      string        `json:"banner_url,omitempty"`
	Pinned         []BlueskyPost `json:"recent_posts,omitempty"`
	ProfileURL     string        `json:"profile_url"`
	Source         string        `json:"source"`
	TookMs         int64         `json:"tookMs"`
}

// BlueskyUser fetches a Bluesky profile via the public AT-Protocol XRPC API.
// Free, no key, very stable. Bluesky exposes its entire firehose publicly,
// which makes it the most OSINT-friendly social network currently active.
func BlueskyUser(ctx context.Context, input map[string]any) (*BlueskyOutput, error) {
	handle, _ := input["handle"].(string)
	handle = strings.TrimSpace(strings.ToLower(handle))
	handle = strings.TrimPrefix(handle, "@")
	if handle == "" {
		return nil, errors.New("input.handle required (e.g. \"alice.bsky.social\" or just \"alice\")")
	}
	// If user passes a bare username, default to .bsky.social.
	if !strings.Contains(handle, ".") {
		handle = handle + ".bsky.social"
	}
	includeRecent := true
	if v, ok := input["include_recent"].(bool); ok {
		includeRecent = v
	}

	start := time.Now()
	profURL := "https://public.api.bsky.app/xrpc/app.bsky.actor.getProfile?actor=" + url.QueryEscape(handle)
	body, err := httpGetJSON(ctx, profURL, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("bluesky profile: %w", err)
	}
	var p struct {
		DID            string `json:"did"`
		Handle         string `json:"handle"`
		DisplayName    string `json:"displayName"`
		Description    string `json:"description"`
		FollowersCount int    `json:"followersCount"`
		FollowsCount   int    `json:"followsCount"`
		PostsCount     int    `json:"postsCount"`
		IndexedAt      string `json:"indexedAt"`
		Avatar         string `json:"avatar"`
		Banner         string `json:"banner"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("bluesky parse: %w", err)
	}
	if p.DID == "" {
		return nil, fmt.Errorf("bluesky user %q not found", handle)
	}

	out := &BlueskyOutput{
		Handle: p.Handle, DID: p.DID, DisplayName: p.DisplayName,
		Description: p.Description, FollowersCount: p.FollowersCount,
		FollowsCount: p.FollowsCount, PostsCount: p.PostsCount,
		IndexedAt: p.IndexedAt, AvatarURL: p.Avatar, BannerURL: p.Banner,
		ProfileURL: "https://bsky.app/profile/" + p.Handle,
		Source:     "public.api.bsky.app",
	}

	if includeRecent {
		feedURL := "https://public.api.bsky.app/xrpc/app.bsky.feed.getAuthorFeed?actor=" + url.QueryEscape(p.DID) + "&limit=10"
		feedBody, ferr := httpGetJSON(ctx, feedURL, 10*time.Second)
		if ferr == nil {
			var fr struct {
				Feed []struct {
					Post struct {
						URI       string `json:"uri"`
						CID       string `json:"cid"`
						IndexedAt string `json:"indexedAt"`
						Record    struct {
							Text string `json:"text"`
						} `json:"record"`
						LikeCount   int `json:"likeCount"`
						RepostCount int `json:"repostCount"`
						ReplyCount  int `json:"replyCount"`
					} `json:"post"`
				} `json:"feed"`
			}
			if err := json.Unmarshal(feedBody, &fr); err == nil {
				for _, f := range fr.Feed {
					out.Pinned = append(out.Pinned, BlueskyPost{
						URI: f.Post.URI, CID: f.Post.CID, Text: f.Post.Record.Text,
						IndexedAt: f.Post.IndexedAt, LikeCount: f.Post.LikeCount,
						RepostCount: f.Post.RepostCount, ReplyCount: f.Post.ReplyCount,
					})
				}
			}
		}
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
