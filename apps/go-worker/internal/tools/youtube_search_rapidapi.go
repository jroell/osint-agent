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

// YouTubeSearchRapidAPI wraps yt-api.p.rapidapi.com for video search,
// video details, channel info, comments, and trending. REQUIRES
// `RAPID_API_KEY` env var. Ported from vurvey-api `youtube-service.ts`.
//
// Complements the free `youtube_transcript` tool: this gives video
// discovery + metadata (title, channel, view count, publish date,
// length), while transcript fetches captions.
//
// Modes:
//   - "search"        : keyword search (video / channel / playlist)
//   - "video_details" : metadata for one video id
//   - "video_comments": top-level comments
//   - "channel_info"  : channel metadata by id
//   - "channel_videos": videos uploaded by a channel
//   - "trending"      : country trending list
//
// Knowledge-graph: typed entities (kind: "video" | "channel" | "comment").

const ytAPIHost = "yt-api.p.rapidapi.com"

type YTVideo struct {
	VideoID      string `json:"video_id"`
	Title        string `json:"title"`
	ChannelID    string `json:"channel_id,omitempty"`
	ChannelTitle string `json:"channel_title,omitempty"`
	ViewCount    string `json:"view_count,omitempty"`
	PublishedAt  string `json:"published_at,omitempty"`
	Length       string `json:"length,omitempty"`
	Description  string `json:"description,omitempty"`
	URL          string `json:"youtube_url"`
}

type YTEntity struct {
	Kind        string         `json:"kind"`
	Platform    string         `json:"platform"`
	ID          string         `json:"platform_id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type YouTubeSearchRapidAPIOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Returned          int            `json:"returned"`
	Videos            []YTVideo      `json:"videos,omitempty"`
	Detail            map[string]any `json:"detail,omitempty"`
	Entities          []YTEntity     `json:"entities"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
}

func YouTubeSearchRapidAPILookup(ctx context.Context, input map[string]any) (*YouTubeSearchRapidAPIOutput, error) {
	apiKey := os.Getenv("RAPID_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("RAPID_API_KEY not set; required (subscribe to yt-api on RapidAPI)")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["video_id"] != nil && input["comments"] == true:
			mode = "video_comments"
		case input["video_id"] != nil:
			mode = "video_details"
		case input["channel_id"] != nil && input["videos"] == true:
			mode = "channel_videos"
		case input["channel_id"] != nil:
			mode = "channel_info"
		case input["geo"] != nil:
			mode = "trending"
		default:
			mode = "search"
		}
	}
	out := &YouTubeSearchRapidAPIOutput{Mode: mode, Source: ytAPIHost}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	rapid := func(path string, params url.Values) (map[string]any, error) {
		u := fmt.Sprintf("https://%s%s", ytAPIHost, path)
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("x-rapidapi-key", apiKey)
		req.Header.Set("x-rapidapi-host", ytAPIHost)
		req.Header.Set("Accept", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("yt-api: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("yt-api HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("yt-api decode: %w", err)
		}
		return m, nil
	}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		params := url.Values{"query": []string{q}}
		if t, ok := input["type"].(string); ok && t != "" {
			params.Set("type", t)
		}
		m, err := rapid("/search", params)
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Videos = parseYTVideos(m)
	case "video_details":
		id, _ := input["video_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.video_id required")
		}
		out.Query = id
		m, err := rapid("/video/info", url.Values{"id": []string{id}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		v := YTVideo{
			VideoID:      gtString(m, "id"),
			Title:        gtString(m, "title"),
			ChannelID:    gtString(m, "channelId"),
			ChannelTitle: gtString(m, "channelTitle"),
			Description:  gtString(m, "description"),
			URL:          "https://www.youtube.com/watch?v=" + id,
		}
		if vc, ok := m["viewCount"].(float64); ok {
			v.ViewCount = fmt.Sprintf("%d", int(vc))
		}
		out.Videos = []YTVideo{v}
	case "video_comments":
		id, _ := input["video_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.video_id required")
		}
		out.Query = id
		m, err := rapid("/comments", url.Values{"id": []string{id}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "channel_info":
		id, _ := input["channel_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.channel_id required")
		}
		out.Query = id
		m, err := rapid("/channel/about", url.Values{"id": []string{id}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
	case "channel_videos":
		id, _ := input["channel_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.channel_id required")
		}
		out.Query = id
		m, err := rapid("/channel/videos", url.Values{"id": []string{id}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Videos = parseYTVideos(m)
	case "trending":
		geo, _ := input["geo"].(string)
		if geo == "" {
			geo = "US"
		}
		out.Query = geo
		m, err := rapid("/trending", url.Values{"geo": []string{geo}})
		if err != nil {
			return nil, err
		}
		out.Detail = m
		out.Videos = parseYTVideos(m)
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Videos)
	out.Entities = ytBuildEntities(out)
	out.HighlightFindings = ytBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseYTVideos(m map[string]any) []YTVideo {
	out := []YTVideo{}
	data, _ := m["data"].([]any)
	for _, x := range data {
		rec, _ := x.(map[string]any)
		if rec == nil {
			continue
		}
		// Skip non-video items in mixed search results (channels, playlists)
		t := gtString(rec, "type")
		if t != "" && t != "video" && t != "shorts" {
			continue
		}
		v := YTVideo{
			VideoID:      gtString(rec, "videoId"),
			Title:        gtString(rec, "title"),
			ChannelID:    gtString(rec, "channelId"),
			ChannelTitle: gtString(rec, "channelTitle"),
			ViewCount:    gtString(rec, "viewCount"),
			PublishedAt:  gtString(rec, "publishedText"),
			Length:       gtString(rec, "lengthText"),
			Description:  gtString(rec, "description"),
		}
		if v.VideoID != "" {
			v.URL = "https://www.youtube.com/watch?v=" + v.VideoID
		}
		out = append(out, v)
	}
	return out
}

func ytBuildEntities(o *YouTubeSearchRapidAPIOutput) []YTEntity {
	ents := []YTEntity{}
	for _, v := range o.Videos {
		ents = append(ents, YTEntity{
			Kind: "video", Platform: "youtube", ID: v.VideoID,
			Name: v.Title, URL: v.URL, Date: v.PublishedAt,
			Description: v.Description,
			Attributes: map[string]any{
				"channel_id":    v.ChannelID,
				"channel_title": v.ChannelTitle,
				"view_count":    v.ViewCount,
				"length":        v.Length,
			},
		})
	}
	return ents
}

func ytBuildHighlights(o *YouTubeSearchRapidAPIOutput) []string {
	hi := []string{fmt.Sprintf("✓ youtube-rapidapi %s: %d videos", o.Mode, o.Returned)}
	for i, v := range o.Videos {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • [%s] %s — %s (%s views, %s)",
			v.VideoID, hfTruncate(v.Title, 80), v.ChannelTitle, v.ViewCount, v.PublishedAt))
	}
	return hi
}
