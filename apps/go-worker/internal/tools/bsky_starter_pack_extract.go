package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type BskyMember struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	Banner      string `json:"banner,omitempty"`
	Followers   int    `json:"followers_count,omitempty"`
	Following   int    `json:"follows_count,omitempty"`
	Posts       int    `json:"posts_count,omitempty"`
	IndexedAt   string `json:"indexed_at,omitempty"`
}

type BskyStarterPackOutput struct {
	StarterPackURI    string       `json:"starter_pack_uri"`
	URL               string       `json:"bsky_url,omitempty"`
	Name              string       `json:"name,omitempty"`
	Description       string       `json:"description,omitempty"`
	CreatedAt         string       `json:"created_at,omitempty"`
	CreatorDID        string       `json:"creator_did,omitempty"`
	CreatorHandle     string       `json:"creator_handle,omitempty"`
	JoinedAllTime     int          `json:"joined_all_time"`
	JoinedThisWeek    int          `json:"joined_this_week"`
	MemberCount       int          `json:"member_count"`
	Members           []BskyMember `json:"members"`
	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
	Note              string       `json:"note,omitempty"`
}

// BskyStarterPackExtract resolves a Bluesky starter pack to its complete
// member list. Starter packs are public curated lists of Bluesky accounts
// — often used to onboard new users to communities (e.g. "Follow these 30
// security researchers" or "Follow these 50 climate scientists"). They're
// genuine community-mapping artifacts.
//
// Accepts:
//   - Full URL: https://bsky.app/starter-pack/<handle-or-did>/<rkey>
//   - AT-URI: at://did:plc:.../app.bsky.graph.starterpack/<rkey>
//
// Returns starter pack metadata + ALL member profiles (handle, display name,
// bio, avatar, follower/following counts).
//
// Use case: community mapping. When you find an account in an attack
// campaign / threat actor cluster, finding their starter packs reveals
// who they're connected to.
//
// Free, no auth (public.api.bsky.app).
func BskyStarterPackExtract(ctx context.Context, input map[string]any) (*BskyStarterPackOutput, error) {
	rawInput, _ := input["url"].(string)
	rawInput = strings.TrimSpace(rawInput)
	if rawInput == "" {
		rawInput, _ = input["uri"].(string)
	}
	if rawInput == "" {
		return nil, errors.New("input.url required (bsky.app starter-pack URL or AT-URI)")
	}

	// Parse → AT-URI form
	atURI, err := bskyParseStarterPackURI(ctx, rawInput)
	if err != nil {
		return nil, fmt.Errorf("could not parse starter pack URI: %w", err)
	}

	start := time.Now()
	out := &BskyStarterPackOutput{
		StarterPackURI: atURI,
		Source:         "public.api.bsky.app",
	}

	// Fetch starter pack
	endpoint := "https://public.api.bsky.app/xrpc/app.bsky.graph.getStarterPack?starterPack=" + url.QueryEscape(atURI)
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/bsky-starter-pack")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bsky fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bsky status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		StarterPack struct {
			URI    string `json:"uri"`
			Record struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				CreatedAt   string `json:"createdAt"`
				List        string `json:"list"`
			} `json:"record"`
			Creator struct {
				DID         string `json:"did"`
				Handle      string `json:"handle"`
				DisplayName string `json:"displayName"`
			} `json:"creator"`
			JoinedAllTimeCount int `json:"joinedAllTimeCount"`
			JoinedWeekCount    int `json:"joinedWeekCount"`
			ListItemsSample    []struct {
				Subject struct {
					DID         string `json:"did"`
					Handle      string `json:"handle"`
					DisplayName string `json:"displayName"`
					Description string `json:"description"`
					Avatar      string `json:"avatar"`
				} `json:"subject"`
			} `json:"listItemsSample"`
		} `json:"starterPack"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("bsky parse: %w", err)
	}

	sp := parsed.StarterPack
	out.Name = sp.Record.Name
	out.Description = sp.Record.Description
	out.CreatedAt = sp.Record.CreatedAt
	out.CreatorDID = sp.Creator.DID
	out.CreatorHandle = sp.Creator.Handle
	out.JoinedAllTime = sp.JoinedAllTimeCount
	out.JoinedThisWeek = sp.JoinedWeekCount

	// Build URL
	if sp.Creator.Handle != "" {
		uriParts := strings.Split(atURI, "/")
		if len(uriParts) > 0 {
			rkey := uriParts[len(uriParts)-1]
			out.URL = fmt.Sprintf("https://bsky.app/starter-pack/%s/%s", sp.Creator.Handle, rkey)
		}
	}

	// Fetch full member list via the linked list
	if sp.Record.List != "" {
		members, _ := bskyFetchListMembers(ctx, sp.Record.List)
		if len(members) > 0 {
			out.Members = members
		}
	}
	// Fall back to sample if full list unavailable
	if len(out.Members) == 0 {
		for _, item := range sp.ListItemsSample {
			out.Members = append(out.Members, BskyMember{
				DID:         item.Subject.DID,
				Handle:      item.Subject.Handle,
				DisplayName: item.Subject.DisplayName,
				Description: truncate(item.Subject.Description, 200),
				Avatar:      item.Subject.Avatar,
			})
		}
	}
	out.MemberCount = len(out.Members)

	// Highlights
	highlights := []string{
		fmt.Sprintf("'%s' by @%s — %d members, joined-all-time=%d, joined-this-week=%d",
			out.Name, out.CreatorHandle, out.MemberCount, out.JoinedAllTime, out.JoinedThisWeek),
	}
	if out.Description != "" {
		highlights = append(highlights, "description: "+truncate(out.Description, 150))
	}
	if out.MemberCount > 0 {
		topHandles := []string{}
		for i, m := range out.Members {
			if i >= 5 {
				break
			}
			topHandles = append(topHandles, "@"+m.Handle)
		}
		highlights = append(highlights, "first 5 members: "+strings.Join(topHandles, ", "))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// bskyParseStarterPackURI normalizes various input forms to AT-URI format.
func bskyParseStarterPackURI(ctx context.Context, raw string) (string, error) {
	// Already AT-URI
	if strings.HasPrefix(raw, "at://") {
		return raw, nil
	}
	// Full URL: https://bsky.app/starter-pack/<handle>/<rkey>
	prefixes := []string{
		"https://bsky.app/starter-pack/",
		"http://bsky.app/starter-pack/",
		"bsky.app/starter-pack/",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(raw, p) {
			rest := raw[len(p):]
			parts := strings.Split(rest, "/")
			if len(parts) < 2 {
				return "", errors.New("URL missing handle or rkey")
			}
			handle := parts[0]
			rkey := parts[1]
			if i := strings.IndexAny(rkey, "?#"); i >= 0 {
				rkey = rkey[:i]
			}
			// Resolve handle → DID
			did := handle
			if !strings.HasPrefix(handle, "did:") {
				resolved, err := bskyResolveHandle(ctx, handle)
				if err != nil {
					return "", fmt.Errorf("resolve handle %s: %w", handle, err)
				}
				did = resolved
			}
			return fmt.Sprintf("at://%s/app.bsky.graph.starterpack/%s", did, rkey), nil
		}
	}
	return "", fmt.Errorf("unsupported URI format: %s", raw)
}

func bskyResolveHandle(ctx context.Context, handle string) (string, error) {
	endpoint := "https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=" + url.QueryEscape(handle)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("resolveHandle status %d", resp.StatusCode)
	}
	var parsed struct {
		DID string `json:"did"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.DID == "" {
		return "", errors.New("empty DID returned")
	}
	return parsed.DID, nil
}

func bskyFetchListMembers(ctx context.Context, listURI string) ([]BskyMember, error) {
	out := []BskyMember{}
	cursor := ""
	for {
		endpoint := "https://public.api.bsky.app/xrpc/app.bsky.graph.getList?list=" + url.QueryEscape(listURI) + "&limit=100"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/bsky-list")
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			return out, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, fmt.Errorf("getList status %d", resp.StatusCode)
		}
		var parsed struct {
			Items []struct {
				Subject struct {
					DID            string `json:"did"`
					Handle         string `json:"handle"`
					DisplayName    string `json:"displayName"`
					Description    string `json:"description"`
					Avatar         string `json:"avatar"`
					Banner         string `json:"banner"`
					FollowersCount int    `json:"followersCount"`
					FollowsCount   int    `json:"followsCount"`
					PostsCount     int    `json:"postsCount"`
					IndexedAt      string `json:"indexedAt"`
				} `json:"subject"`
			} `json:"items"`
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return out, err
		}
		for _, it := range parsed.Items {
			out = append(out, BskyMember{
				DID:         it.Subject.DID,
				Handle:      it.Subject.Handle,
				DisplayName: it.Subject.DisplayName,
				Description: truncate(it.Subject.Description, 200),
				Avatar:      it.Subject.Avatar,
				Banner:      it.Subject.Banner,
				Followers:   it.Subject.FollowersCount,
				Following:   it.Subject.FollowsCount,
				Posts:       it.Subject.PostsCount,
				IndexedAt:   it.Subject.IndexedAt,
			})
		}
		// Cap at 500 members for response size
		if len(out) >= 500 {
			break
		}
		if parsed.Cursor == "" {
			break
		}
		cursor = parsed.Cursor
	}
	return out, nil
}
